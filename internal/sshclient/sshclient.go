package sshclient

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/andydixon/nexus/internal/models"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

// Client manages a single SSH session lifecycle.
// It handles connection establishment, authentication, PTY allocation,
// terminal resize propagation, port forwarding, and clean shutdown.
type Client struct {
	mu        sync.Mutex
	config    *models.Profile
	sshClient *ssh.Client
	session   *ssh.Session
	stdin     io.WriteCloser
	stdout    io.Reader
	stderr    io.Reader
	connected bool
	onData    func(data []byte)
	onError   func(err error)
	onClose   func()
	ctx       context.Context
	cancel    context.CancelFunc
	forwards  *ForwardManager // port forwarding tunnels
	agentCleanup func()       // closes agent socket connection
}

// NewClient creates a new SSH client bound to the given profile.
func NewClient(profile *models.Profile) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		config: profile,
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetCallbacks sets the data, error, and close callbacks. Must be called
// before Connect.
func (c *Client) SetCallbacks(onData func([]byte), onError func(error), onClose func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onData = onData
	c.onError = onError
	c.onClose = onClose
}

// sshConf returns the SSH-specific configuration, or a zero value if nil.
func (c *Client) sshConf() models.SSHConfig {
	if c.config.SSH != nil {
		return *c.config.SSH
	}
	return models.SSHConfig{}
}

// Connect establishes the SSH connection using the default (insecure)
// host-key callback. Prefer ConnectWithHostKeyCallback for production use.
func (c *Client) Connect(getPassword func() (string, error), getPassphrase func() (string, error)) error {
	return c.connectInternal(ssh.InsecureIgnoreHostKey(), getPassword, getPassphrase)
}

// ConnectWithHostKeyCallback applies a custom HostKeyCallback (e.g. from
// HostKeyVerifier) and then establishes the connection.
func (c *Client) ConnectWithHostKeyCallback(
	hkCallback ssh.HostKeyCallback,
	getPassword func() (string, error),
	getPassphrase func() (string, error),
) error {
	return c.connectInternal(hkCallback, getPassword, getPassphrase)
}

// connectInternal is the shared implementation for both Connect variants.
func (c *Client) connectInternal(
	hkCallback ssh.HostKeyCallback,
	getPassword func() (string, error),
	getPassphrase func() (string, error),
) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	c.mu.Unlock()

	sc := c.sshConf()

	authMethods, err := c.buildAuthMethods(sc, getPassword, getPassphrase)
	if err != nil {
		return fmt.Errorf("failed to build auth methods: %w", err)
	}

	termType := sc.TerminalType
	if termType == "" {
		termType = "xterm-256color"
	}

	timeout := time.Duration(c.config.ConnectTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	sshConfig := &ssh.ClientConfig{
		User:            c.config.Username,
		Auth:            authMethods,
		HostKeyCallback: hkCallback,
		Timeout:         timeout,
	}

	var sshConn *ssh.Client

	if len(sc.JumpHosts) > 0 {
		sshConn, err = c.connectViaJumpHosts(sc, sshConfig, getPassphrase)
	} else {
		port := c.config.Port
		if port == 0 {
			port = 22
		}
		addr := net.JoinHostPort(c.config.Host, fmt.Sprintf("%d", port))

		// Dial through proxy if configured, otherwise use direct TCP.
		conn, dialErr := c.dialTarget(sc, addr, timeout)
		if dialErr != nil {
			return fmt.Errorf("SSH connection failed: %w", dialErr)
		}
		ncc, chans, reqs, sshErr := ssh.NewClientConn(conn, addr, sshConfig)
		if sshErr != nil {
			conn.Close()
			return fmt.Errorf("SSH handshake failed: %w", sshErr)
		}
		sshConn = ssh.NewClient(ncc, chans, reqs)
	}
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}

	session, err := sshConn.NewSession()
	if err != nil {
		sshConn.Close()
		return fmt.Errorf("failed to create SSH session: %w", err)
	}

	// Allocate a PTY with sensible defaults and terminal modes.
	cols := 80
	rows := 24
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
		ssh.IUTF8:         1,
	}

	if err := session.RequestPty(termType, rows, cols, modes); err != nil {
		session.Close()
		sshConn.Close()
		return fmt.Errorf("PTY request failed: %w", err)
	}

	// Set up SSH agent forwarding if requested (must happen before shell start).
	if sc.AgentForwarding {
		if fwdErr := requestAgentForwarding(sshConn, session); fwdErr != nil {
			// Non-fatal — log but don't abort the connection.
			if c.onError != nil {
				c.onError(fmt.Errorf("agent forwarding: %w", fwdErr))
			}
		}
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		sshConn.Close()
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		sshConn.Close()
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stderrPipe, err := session.StderrPipe()
	if err != nil {
		session.Close()
		sshConn.Close()
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Start shell or explicit startup command.
	if sc.StartupCommand != "" {
		if err := session.Start(sc.StartupCommand); err != nil {
			session.Close()
			sshConn.Close()
			return fmt.Errorf("failed to start command: %w", err)
		}
	} else {
		if err := session.Shell(); err != nil {
			session.Close()
			sshConn.Close()
			return fmt.Errorf("failed to start shell: %w", err)
		}
	}

	c.mu.Lock()
	c.sshClient = sshConn
	c.session = session
	c.stdin = stdinPipe
	c.stdout = stdoutPipe
	c.stderr = stderrPipe
	c.connected = true
	c.forwards = NewForwardManager(sshConn)
	c.mu.Unlock()

	go c.readLoop()

	if sc.KeepaliveInterval > 0 {
		go c.startKeepalive(sc.KeepaliveInterval)
	}

	return nil
}

// Write sends data to the SSH session's stdin.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	if !c.connected || c.stdin == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	stdin := c.stdin
	c.mu.Unlock()

	_, err := stdin.Write(data)
	return err
}

// Resize sends a terminal resize request to the remote PTY.
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	if !c.connected || c.session == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	session := c.session
	c.mu.Unlock()

	return session.WindowChange(rows, cols)
}

// Ping sends a keepalive request and returns the round-trip time in milliseconds.
func (c *Client) Ping() (int64, error) {
	c.mu.Lock()
	if !c.connected || c.sshClient == nil {
		c.mu.Unlock()
		return 0, fmt.Errorf("not connected")
	}
	client := c.sshClient
	c.mu.Unlock()

	start := time.Now()
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	if err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

// RunCommand executes a single command using a new SSH session.
func (c *Client) RunCommand(command string) (string, error) {
	c.mu.Lock()
	if !c.connected || c.sshClient == nil {
		c.mu.Unlock()
		return "", fmt.Errorf("not connected")
	}
	client := c.sshClient
	c.mu.Unlock()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to open SSH session: %w", err)
	}
	defer session.Close()

	out, err := session.Output(command)
	if err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Close cleanly shuts down the SSH session and connection, cancels the
// background context, and invokes the onClose callback.
func (c *Client) Close() error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil
	}
	c.connected = false
	session := c.session
	sshClient := c.sshClient
	fwdMgr := c.forwards
	agCleanup := c.agentCleanup
	onClose := c.onClose
	c.mu.Unlock()

	c.cancel()

	// Shut down any active port forwards first.
	if fwdMgr != nil {
		fwdMgr.StopAll()
	}

	// Close the SSH agent socket connection.
	if agCleanup != nil {
		agCleanup()
	}

	var firstErr error
	if session != nil {
		if err := session.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if sshClient != nil {
		if err := sshClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if onClose != nil {
		onClose()
	}
	return firstErr
}

// IsConnected returns the current connection state.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Forwards returns the ForwardManager for this client, or nil if not connected.
func (c *Client) Forwards() *ForwardManager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forwards
}

// SSHConn returns the underlying ssh.Client for use by subsystems such as
// SFTP. Returns nil if not connected.
func (c *Client) SSHConn() *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sshClient
}

// buildAuthMethods constructs the SSH authentication method list from the
// profile's SSHConfig and the provided credential callbacks.
func (c *Client) buildAuthMethods(sc models.SSHConfig, getPassword func() (string, error), getPassphrase func() (string, error)) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// If agent auth is enabled, try to use the local SSH agent first.
	if sc.AgentAuth {
		am, cleanup, err := agentAuthMethod()
		if err == nil {
			methods = append(methods, am)
			c.mu.Lock()
			c.agentCleanup = cleanup
			c.mu.Unlock()
		}
		// Non-fatal — fall through to other auth methods if agent unavailable.
	}

	authList := sc.AuthMethods
	if len(authList) == 0 {
		authList = []string{"publickey", "password", "keyboard-interactive"}
	}

	for _, m := range authList {
		switch m {
		case "password":
			if getPassword != nil {
				methods = append(methods, ssh.PasswordCallback(func() (string, error) {
					return getPassword()
				}))
			}

		case "publickey":
			if sc.PrivateKeyPath != "" {
				signer, err := c.parsePrivateKey(sc.PrivateKeyPath, getPassphrase)
				if err != nil {
					return nil, fmt.Errorf("failed to parse private key %s: %w", sc.PrivateKeyPath, err)
				}
				methods = append(methods, ssh.PublicKeys(signer))
			}

		case "keyboard-interactive":
			if getPassword != nil {
				pwdCallback := getPassword
				totpSecret := ""
				if sc.TOTPSecret != "" {
					totpSecret = sc.TOTPSecret
				}
				methods = append(methods, ssh.KeyboardInteractive(
					func(name, instruction string, questions []string, echos []bool) ([]string, error) {
						answers := make([]string, len(questions))
						for i, question := range questions {
							q := strings.ToLower(question)
							// Detect TOTP/2FA prompts and auto-fill when secret is configured.
							if totpSecret != "" && isTOTPPrompt(q) {
								code, err := totp.GenerateCode(totpSecret, time.Now())
								if err != nil {
									return nil, fmt.Errorf("failed to generate TOTP code: %w", err)
								}
								answers[i] = code
							} else {
								ans, err := pwdCallback()
								if err != nil {
									return nil, err
								}
								answers[i] = ans
							}
						}
						return answers, nil
					},
				))
			}
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no authentication methods available")
	}
	return methods, nil
}

// isTOTPPrompt checks whether a keyboard-interactive question looks like a
// TOTP/2FA prompt. Common patterns include "verification code", "otp",
// "two-factor", "2fa", "authenticator", etc.
func isTOTPPrompt(question string) bool {
	totpKeywords := []string{
		"verification code",
		"otp",
		"one-time",
		"one time",
		"two-factor",
		"two factor",
		"2fa",
		"totp",
		"authenticator",
		"security code",
		"6-digit",
		"6 digit",
	}
	for _, kw := range totpKeywords {
		if strings.Contains(question, kw) {
			return true
		}
	}
	return false
}

// dialTarget dials the target address, optionally through a SOCKS5 or HTTP
// CONNECT proxy as configured in the profile's SSH settings.
func (c *Client) dialTarget(sc models.SSHConfig, addr string, timeout time.Duration) (net.Conn, error) {
	switch strings.ToLower(sc.ProxyType) {
	case "socks5":
		return c.dialSOCKS5(sc, addr, timeout)
	case "http":
		return c.dialHTTPProxy(sc, addr, timeout)
	default:
		// No proxy — direct TCP dial.
		return net.DialTimeout("tcp", addr, timeout)
	}
}

// dialSOCKS5 connects to the target via a SOCKS5 proxy.
func (c *Client) dialSOCKS5(sc models.SSHConfig, addr string, timeout time.Duration) (net.Conn, error) {
	proxyPort := sc.ProxyPort
	if proxyPort == 0 {
		proxyPort = 1080
	}
	proxyAddr := net.JoinHostPort(sc.ProxyHost, fmt.Sprintf("%d", proxyPort))

	var auth *proxy.Auth
	if sc.ProxyUsername != "" {
		auth = &proxy.Auth{
			User:     sc.ProxyUsername,
			Password: sc.ProxyPassword,
		}
	}

	dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, &net.Dialer{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 proxy setup failed: %w", err)
	}

	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 proxy connection to %s failed: %w", addr, err)
	}
	return conn, nil
}

// dialHTTPProxy connects to the target via an HTTP CONNECT proxy.
func (c *Client) dialHTTPProxy(sc models.SSHConfig, addr string, timeout time.Duration) (net.Conn, error) {
	proxyPort := sc.ProxyPort
	if proxyPort == 0 {
		proxyPort = 8080
	}
	proxyAddr := net.JoinHostPort(sc.ProxyHost, fmt.Sprintf("%d", proxyPort))

	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, fmt.Errorf("HTTP proxy connection failed: %w", err)
	}

	// Send CONNECT request.
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if sc.ProxyUsername != "" {
		cred := base64.StdEncoding.EncodeToString([]byte(sc.ProxyUsername + ":" + sc.ProxyPassword))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", cred)
	}
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("HTTP CONNECT write failed: %w", err)
	}

	// Read response.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("HTTP CONNECT response read failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("HTTP CONNECT proxy returned %d %s", resp.StatusCode, resp.Status)
	}

	return conn, nil
}

// connectViaJumpHosts establishes the SSH connection through a chain of
// jump hosts. Each hop opens a new net.Conn through the previous hop's
// SSH client.
func (c *Client) connectViaJumpHosts(sc models.SSHConfig, finalConfig *ssh.ClientConfig, getPassphrase func() (string, error)) (*ssh.Client, error) {
	var currentClient *ssh.Client

	for i, jump := range sc.JumpHosts {
		jumpPort := jump.Port
		if jumpPort == 0 {
			jumpPort = 22
		}
		jumpAddr := net.JoinHostPort(jump.Host, fmt.Sprintf("%d", jumpPort))

		var jumpAuth []ssh.AuthMethod
		if jump.PrivateKeyPath != "" {
			signer, err := c.parsePrivateKey(jump.PrivateKeyPath, getPassphrase)
			if err != nil {
				if currentClient != nil {
					currentClient.Close()
				}
				return nil, fmt.Errorf("jump host %d private key error: %w", i, err)
			}
			jumpAuth = append(jumpAuth, ssh.PublicKeys(signer))
		}

		jumpConfig := &ssh.ClientConfig{
			User:            jump.Username,
			Auth:            jumpAuth,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         finalConfig.Timeout,
		}

		if currentClient == nil {
			client, err := ssh.Dial("tcp", jumpAddr, jumpConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to connect to jump host %d (%s): %w", i, jumpAddr, err)
			}
			currentClient = client
		} else {
			conn, err := currentClient.Dial("tcp", jumpAddr)
			if err != nil {
				currentClient.Close()
				return nil, fmt.Errorf("failed to dial through jump host %d (%s): %w", i, jumpAddr, err)
			}
			ncc, chans, reqs, err := ssh.NewClientConn(conn, jumpAddr, jumpConfig)
			if err != nil {
				conn.Close()
				currentClient.Close()
				return nil, fmt.Errorf("failed to establish SSH through jump host %d: %w", i, err)
			}
			currentClient = ssh.NewClient(ncc, chans, reqs)
		}
	}

	// Final hop to the target host.
	targetPort := c.config.Port
	if targetPort == 0 {
		targetPort = 22
	}
	targetAddr := net.JoinHostPort(c.config.Host, fmt.Sprintf("%d", targetPort))

	conn, err := currentClient.Dial("tcp", targetAddr)
	if err != nil {
		currentClient.Close()
		return nil, fmt.Errorf("failed to dial target %s through jump hosts: %w", targetAddr, err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, finalConfig)
	if err != nil {
		conn.Close()
		currentClient.Close()
		return nil, fmt.Errorf("failed to establish SSH to target %s: %w", targetAddr, err)
	}

	return ssh.NewClient(ncc, chans, reqs), nil
}

// parsePrivateKey loads and parses an SSH private key file. If the key is
// encrypted and getPassphrase is provided, it will attempt decryption.
func (c *Client) parsePrivateKey(path string, getPassphrase func() (string, error)) (ssh.Signer, error) {
	keyData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read key file: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err == nil {
		return signer, nil
	}

	if getPassphrase == nil {
		return nil, fmt.Errorf("key is encrypted and no passphrase callback provided: %w", err)
	}

	passphrase, err := getPassphrase()
	if err != nil {
		return nil, fmt.Errorf("failed to get passphrase: %w", err)
	}

	signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("failed to parse encrypted key: %w", err)
	}
	return signer, nil
}

// startKeepalive periodically sends keepalive requests to the server.
// It exits when the context is cancelled or the connection closes.
func (c *Client) startKeepalive(intervalSecs int) {
	interval := time.Duration(intervalSecs) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			client := c.sshClient
			connected := c.connected
			c.mu.Unlock()

			if !connected || client == nil {
				return
			}

			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				c.mu.Lock()
				onError := c.onError
				c.mu.Unlock()
				if onError != nil {
					onError(fmt.Errorf("keepalive failed: %w", err))
				}
				return
			}
		}
	}
}

// readLoop reads from stdout and stderr, dispatching received data to the
// onData callback. It runs until the context is cancelled or the streams close.
func (c *Client) readLoop() {
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.mu.Lock()
			stdout := c.stdout
			c.mu.Unlock()
			if stdout == nil {
				return
			}

			n, err := stdout.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				c.mu.Lock()
				cb := c.onData
				c.mu.Unlock()
				if cb != nil {
					cb(data)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.mu.Lock()
			stderr := c.stderr
			c.mu.Unlock()
			if stderr == nil {
				return
			}

			n, err := stderr.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				c.mu.Lock()
				cb := c.onData
				c.mu.Unlock()
				if cb != nil {
					cb(data)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	<-done
	<-done
	c.Close()
}
