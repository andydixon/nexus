// Package vncclient provides a WebSocket-to-TCP proxy for VNC connections,
// with optional SSH tunnel support.
package vncclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Client manages a VNC session, optionally tunnelled through SSH.
type Client struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	listener  net.Listener
	sshClient *ssh.Client
	wsURL     string
	connected bool
	onError   func(error)
}

// Config holds the configuration for a VNC connection.
type Config struct {
	// VNC server address
	VNCHost string
	VNCPort int

	// SSH tunnel settings
	SSHTunnel          bool
	SSHHost            string
	SSHPort            int
	SSHUsername        string
	SSHAuthMethod      string // "password", "publickey"
	SSHPassword        string
	SSHPrivateKey      []byte
	SSHPassphrase      string
	SSHAgentAuth       bool
	SSHAgentConn       net.Conn
	SSHHostKeyCallback ssh.HostKeyCallback

	// Callbacks
	OnError func(error)
}

var upgrader = websocket.Upgrader{
	CheckOrigin:     allowProxyOrigin,
	ReadBufferSize:  65536,
	WriteBufferSize: 65536,
}

const maxWebSocketMessageBytes = 8 << 20

// NewClient creates a new VNC proxy client.
func NewClient(cfg Config) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		ctx:     ctx,
		cancel:  cancel,
		onError: cfg.OnError,
	}
}

// Connect establishes the VNC connection (with optional SSH tunnel) and starts
// a local WebSocket proxy. Returns the WebSocket URL for the frontend.
func (c *Client) Connect(cfg Config) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return c.wsURL, nil
	}

	var vncConn net.Conn
	var err error

	vncAddr := net.JoinHostPort(cfg.VNCHost, strconv.Itoa(cfg.VNCPort))

	if cfg.SSHTunnel {
		// Establish SSH connection first
		sshAddr := net.JoinHostPort(cfg.SSHHost, strconv.Itoa(cfg.SSHPort))
		if cfg.SSHHostKeyCallback == nil {
			return "", fmt.Errorf("SSH host key callback is required")
		}
		sshConfig := &ssh.ClientConfig{
			User:            cfg.SSHUsername,
			HostKeyCallback: cfg.SSHHostKeyCallback,
			Timeout:         15 * time.Second,
		}

		// Configure auth methods
		var authMethods []ssh.AuthMethod
		if cfg.SSHAgentAuth && cfg.SSHAgentConn != nil {
			agent := newSSHAgent(cfg.SSHAgentConn)
			if agent != nil {
				authMethods = append(authMethods, agent)
			}
		}
		if len(cfg.SSHPrivateKey) > 0 {
			var signer ssh.Signer
			if cfg.SSHPassphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(cfg.SSHPrivateKey, []byte(cfg.SSHPassphrase))
			} else {
				signer, err = ssh.ParsePrivateKey(cfg.SSHPrivateKey)
			}
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
		if cfg.SSHPassword != "" {
			authMethods = append(authMethods, ssh.Password(cfg.SSHPassword))
		}
		sshConfig.Auth = authMethods

		c.sshClient, err = ssh.Dial("tcp", sshAddr, sshConfig)
		if err != nil {
			return "", fmt.Errorf("SSH connection failed: %w", err)
		}

		// Dial VNC through the SSH tunnel
		// When tunnelling, VNC address is relative to the SSH server
		tunnelAddr := vncAddr
		if cfg.VNCHost == "" || cfg.VNCHost == cfg.SSHHost {
			tunnelAddr = fmt.Sprintf("127.0.0.1:%d", cfg.VNCPort)
		}
		vncConn, err = c.sshClient.Dial("tcp", tunnelAddr)
		if err != nil {
			c.sshClient.Close()
			c.sshClient = nil
			return "", fmt.Errorf("VNC tunnel dial failed: %w", err)
		}
	} else {
		// Direct TCP connection to VNC server
		vncConn, err = net.DialTimeout("tcp", vncAddr, 15*time.Second)
		if err != nil {
			return "", fmt.Errorf("VNC connection failed: %w", err)
		}
	}

	// Start local WebSocket proxy
	wsURL, err := c.startProxy(vncConn)
	if err != nil {
		vncConn.Close()
		if c.sshClient != nil {
			c.sshClient.Close()
			c.sshClient = nil
		}
		return "", fmt.Errorf("failed to start WebSocket proxy: %w", err)
	}

	c.wsURL = wsURL
	c.connected = true
	return wsURL, nil
}

// startProxy creates a local HTTP server that upgrades to WebSocket and bridges
// traffic to the VNC TCP connection.
func (c *Client) startProxy(vncConn net.Conn) (string, error) {
	// Listen on a random available port on localhost
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	token, err := newProxyToken()
	if err != nil {
		listener.Close()
		return "", err
	}
	c.listener = listener

	port := listener.Addr().(*net.TCPAddr).Port
	proxyPath := "/" + token
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d%s", port, proxyPath)

	mux := http.NewServeMux()
	mux.HandleFunc(proxyPath, func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.SetReadLimit(maxWebSocketMessageBytes)

		// Bridge WebSocket ↔ VNC TCP in both directions
		done := make(chan struct{}, 2)

		// VNC → WebSocket
		go func() {
			defer func() { done <- struct{}{} }()
			buf := make([]byte, 65536)
			for {
				n, err := vncConn.Read(buf)
				if n > 0 {
					if writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		// WebSocket → VNC
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				_, msg, err := ws.ReadMessage()
				if err != nil {
					return
				}
				if _, err := vncConn.Write(msg); err != nil {
					return
				}
			}
		}()

		// Wait for either direction to close
		select {
		case <-done:
		case <-c.ctx.Done():
		}
	})

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  0, // VNC is long-lived
		WriteTimeout: 0,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			if c.onError != nil {
				c.onError(fmt.Errorf("VNC proxy error: %w", err))
			}
		}
	}()

	// Close server when context is cancelled
	go func() {
		<-c.ctx.Done()
		server.Close()
		vncConn.Close()
	}()

	return wsURL, nil
}

func allowProxyOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "wails":
		return true
	case "http", "https":
		return isLoopbackHost(u.Hostname())
	default:
		return false
	}
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func newProxyToken() (string, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate proxy token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

// Close shuts down the VNC proxy and any SSH tunnel.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cancel()
	c.connected = false

	if c.listener != nil {
		c.listener.Close()
		c.listener = nil
	}
	if c.sshClient != nil {
		c.sshClient.Close()
		c.sshClient = nil
	}
}

// IsConnected returns whether the VNC session is active.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// WSURL returns the WebSocket URL for the frontend to connect to.
func (c *Client) WSURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wsURL
}

// newSSHAgent creates an SSH agent auth method from the given connection.
func newSSHAgent(conn net.Conn) ssh.AuthMethod {
	if conn == nil {
		return nil
	}
	ag := agent.NewClient(conn)
	signers, err := ag.Signers()
	if err != nil || len(signers) == 0 {
		return nil
	}
	return ssh.PublicKeys(signers...)
}
