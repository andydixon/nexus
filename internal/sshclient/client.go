// Package sshclient provides a managed SSH session with PTY allocation,
// keepalive support, and callback-driven output.
package sshclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Options configures a new SSH connection.
type Options struct {
	Host              string
	Port              int
	Username          string
	AuthMethods       []ssh.AuthMethod
	TerminalType      string
	Cols              int
	Rows              int
	KeepaliveInterval time.Duration
	KeepaliveMax      int
	HostKeyCallback   ssh.HostKeyCallback
	ConnectTimeout    time.Duration
	EnvVars           map[string]string
	Command           string // if empty, a login shell is started

	// Callbacks invoked from a background goroutine.
	OnData  func(data []byte)
	OnError func(err error)
	OnClose func()

	// DebugLog, when non-nil, receives diagnostic messages.
	DebugLog func(msg string)
}

// Client manages a single SSH connection with PTY, keepalive, and
// callback-driven output delivery.
type Client struct {
	opts    Options
	conn    *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	mu      sync.Mutex
	closed  bool
	cancel  context.CancelFunc
}

// New creates a Client with the given options. Call Connect to establish
// the session.
func New(opts Options) *Client {
	if opts.TerminalType == "" {
		opts.TerminalType = "xterm-256color"
	}
	if opts.Cols == 0 {
		opts.Cols = 80
	}
	if opts.Rows == 0 {
		opts.Rows = 24
	}
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 30 * time.Second
	}
	return &Client{opts: opts}
}

// Connect establishes the SSH connection, requests a PTY, and starts the
// session. Output data is delivered via opts.OnData. The connection is torn
// down when ctx is cancelled or when Close is called.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.debug("dialling %s:%d", c.opts.Host, c.opts.Port)

	addr := net.JoinHostPort(c.opts.Host, fmt.Sprintf("%d", c.opts.Port))
	dialer := &net.Dialer{Timeout: c.opts.ConnectTimeout}
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("sshclient: dial failed: %w", err)
	}

	hostKeyCallback := c.opts.HostKeyCallback
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, &ssh.ClientConfig{
		User:            c.opts.Username,
		Auth:            c.opts.AuthMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         c.opts.ConnectTimeout,
	})
	if err != nil {
		tcpConn.Close()
		return fmt.Errorf("sshclient: handshake failed: %w", err)
	}

	c.conn = ssh.NewClient(sshConn, chans, reqs)

	sess, err := c.conn.NewSession()
	if err != nil {
		c.conn.Close()
		return fmt.Errorf("sshclient: session creation failed: %w", err)
	}
	c.session = sess

	// Set environment variables.
	for k, v := range c.opts.EnvVars {
		_ = sess.Setenv(k, v)
	}

	// Request a pseudo-terminal.
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(c.opts.TerminalType, c.opts.Rows, c.opts.Cols, modes); err != nil {
		sess.Close()
		c.conn.Close()
		return fmt.Errorf("sshclient: PTY request failed: %w", err)
	}

	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		c.conn.Close()
		return fmt.Errorf("sshclient: stdout pipe failed: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		sess.Close()
		c.conn.Close()
		return fmt.Errorf("sshclient: stderr pipe failed: %w", err)
	}

	c.stdin, err = sess.StdinPipe()
	if err != nil {
		sess.Close()
		c.conn.Close()
		return fmt.Errorf("sshclient: stdin pipe failed: %w", err)
	}

	// Start the shell or command.
	if c.opts.Command != "" {
		c.debug("starting command: %s", c.opts.Command)
		if err := sess.Start(c.opts.Command); err != nil {
			sess.Close()
			c.conn.Close()
			return fmt.Errorf("sshclient: command start failed: %w", err)
		}
	} else {
		c.debug("starting login shell")
		if err := sess.Shell(); err != nil {
			sess.Close()
			c.conn.Close()
			return fmt.Errorf("sshclient: shell start failed: %w", err)
		}
	}

	// Read stdout in a background goroutine.
	go c.readLoop(stdout, ctx)
	go c.readLoop(stderr, ctx)

	// Start keepalive if configured.
	if c.opts.KeepaliveInterval > 0 {
		go c.keepaliveLoop(ctx)
	}

	// Watch for session completion.
	go func() {
		_ = sess.Wait()
		c.handleClose()
	}()

	return nil
}

// Write sends data to the remote session's standard input.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("sshclient: session is closed")
	}
	if c.stdin == nil {
		return fmt.Errorf("sshclient: session not connected")
	}

	_, err := c.stdin.Write(data)
	return err
}

// Resize sends a window change request to the remote PTY.
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.session == nil {
		return fmt.Errorf("sshclient: session is closed")
	}
	return c.session.WindowChange(rows, cols)
}

// Close tears down the SSH session and connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	c.debug("closing connection")

	if c.cancel != nil {
		c.cancel()
	}
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.session != nil {
		c.session.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
	return nil
}

// readLoop continuously reads from the given reader and delivers data via the
// OnData callback.
func (c *Client) readLoop(r io.Reader, ctx context.Context) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := r.Read(buf)
		if n > 0 && c.opts.OnData != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			c.opts.OnData(chunk)
		}
		if err != nil {
			if err != io.EOF {
				c.debug("read error: %v", err)
			}
			return
		}
	}
}

// keepaliveLoop sends SSH keepalive requests at the configured interval.
func (c *Client) keepaliveLoop(ctx context.Context) {
	ticker := time.NewTicker(c.opts.KeepaliveInterval)
	defer ticker.Stop()
	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, err := c.conn.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				failures++
				c.debug("keepalive failure %d/%d", failures, c.opts.KeepaliveMax)
				if c.opts.KeepaliveMax > 0 && failures >= c.opts.KeepaliveMax {
					c.debug("keepalive limit reached, closing")
					c.handleClose()
					return
				}
			} else {
				failures = 0
			}
		}
	}
}

// handleClose is called when the session ends, either by the remote side
// or due to keepalive failure.
func (c *Client) handleClose() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	c.mu.Unlock()

	if !wasClosed && c.opts.OnClose != nil {
		c.opts.OnClose()
	}
}

func (c *Client) debug(format string, args ...interface{}) {
	if c.opts.DebugLog != nil {
		c.opts.DebugLog(fmt.Sprintf(format, args...))
	}
}
