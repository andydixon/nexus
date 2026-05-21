// Package localshell provides a local PTY-based terminal session.
// It spawns a shell process and bridges I/O for the frontend terminal.
package localshell

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/creack/pty"
)

// Client manages a local shell PTY session.
type Client struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	ptmx     *os.File
	shell    string
	onData   func([]byte)
	onError  func(error)
	onClose  func()
	closed   bool
	cols     uint16
	rows     uint16
}

// Config holds configuration for a local shell session.
type Config struct {
	Shell   string // Path to the shell binary (e.g. /bin/zsh)
	Cols    uint16
	Rows    uint16
	OnData  func([]byte)
	OnError func(error)
	OnClose func()
}

// DefaultShell returns the system default shell.
func DefaultShell() string {
	if runtime.GOOS == "windows" {
		// Prefer PowerShell if available, else cmd.exe.
		if ps, err := exec.LookPath("powershell.exe"); err == nil {
			return ps
		}
		return "cmd.exe"
	}
	// Unix: use SHELL env var, fallback to /bin/sh.
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// NewClient creates a new local shell client.
func NewClient(cfg Config) *Client {
	shell := cfg.Shell
	if shell == "" {
		shell = DefaultShell()
	}
	cols := cfg.Cols
	if cols == 0 {
		cols = 80
	}
	rows := cfg.Rows
	if rows == 0 {
		rows = 24
	}
	return &Client{
		shell:  shell,
		cols:   cols,
		rows:   rows,
		onData: cfg.OnData,
		onError: cfg.OnError,
		onClose: cfg.OnClose,
	}
}

// Start spawns the shell process and begins reading output.
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ptmx != nil {
		return fmt.Errorf("shell already running")
	}

	cmd := exec.Command(c.shell)
	cmd.Env = os.Environ()

	// Start in the user's home directory.
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}

	// Set TERM for proper terminal behaviour.
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: c.cols,
		Rows: c.rows,
	})
	if err != nil {
		return fmt.Errorf("failed to start shell %q: %w", c.shell, err)
	}

	c.cmd = cmd
	c.ptmx = ptmx

	// Read loop — forward PTY output to the frontend.
	go c.readLoop()

	// Wait for process exit.
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		wasClosed := c.closed
		c.closed = true
		c.mu.Unlock()

		if !wasClosed && c.onClose != nil {
			c.onClose()
		}
	}()

	return nil
}

// Write sends data to the shell's stdin (PTY).
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	ptmx := c.ptmx
	c.mu.Unlock()

	if ptmx == nil {
		return fmt.Errorf("shell not running")
	}
	_, err := ptmx.Write(data)
	return err
}

// Resize updates the PTY window size.
func (c *Client) Resize(cols, rows uint16) error {
	c.mu.Lock()
	ptmx := c.ptmx
	c.mu.Unlock()

	if ptmx == nil {
		return nil
	}
	return pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close terminates the shell process and PTY.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}
	c.closed = true

	if c.ptmx != nil {
		c.ptmx.Close()
		c.ptmx = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}

func (c *Client) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := c.ptmx.Read(buf)
		if n > 0 && c.onData != nil {
			c.onData(buf[:n])
		}
		if err != nil {
			c.mu.Lock()
			wasClosed := c.closed
			c.mu.Unlock()
			if !wasClosed && c.onError != nil {
				c.onError(err)
			}
			return
		}
	}
}
