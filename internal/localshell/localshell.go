// Package localshell provides a local PTY-based terminal session.
// It spawns a shell process and bridges I/O for the frontend terminal.
package localshell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andydixon/nexus/internal/terminalenv"
	"github.com/creack/pty"
)

// Client manages a local shell PTY session.
type Client struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	ptmx     *os.File
	shell    string
	startDir string
	onData   func([]byte)
	onError  func(error)
	onClose  func()
	closed   bool
	cols     uint16
	rows     uint16
}

// Config holds configuration for a local shell session.
type Config struct {
	Shell    string // Path to the shell binary (e.g. /bin/zsh)
	Cols     uint16
	Rows     uint16
	StartDir string // Initial working directory; defaults to the user's home directory.
	OnData   func([]byte)
	OnError  func(error)
	OnClose  func()
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
	startDir := cfg.StartDir
	if startDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			startDir = home
		}
	}
	return &Client{
		shell:    shell,
		startDir: startDir,
		cols:     cols,
		rows:     rows,
		onData:   cfg.OnData,
		onError:  cfg.OnError,
		onClose:  cfg.OnClose,
	}
}

// Start spawns the shell process and begins reading output.
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("shell already closed")
	}
	if c.ptmx != nil {
		return fmt.Errorf("shell already running")
	}

	cmd := exec.Command(c.shell)
	cmd.Env = terminalenv.LocalShellEnv(os.Environ())
	cmd.Dir = c.startDir

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
	go c.readLoop(ptmx)

	// Wait for process exit.
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		wasClosed := c.closed
		c.closed = true
		if c.ptmx == ptmx {
			c.ptmx = nil
		}
		c.mu.Unlock()

		if !wasClosed && c.onClose != nil {
			c.onClose()
		}
	}()

	return nil
}

// Write sends data to the shell's stdin (PTY).
func (c *Client) Write(data []byte) error {
	// Capture the shell's current directory before it handles a command or
	// EOF. This lets a replacement shell resume in the same directory after
	// an ordinary `exit`, without injecting shell-specific prompt hooks.
	if bytesContainCommandBoundary(data) {
		c.captureWorkingDirectory()
	}

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
	if cols > 0 {
		c.cols = cols
	}
	if rows > 0 {
		c.rows = rows
	}
	ptmx := c.ptmx
	cols = c.cols
	rows = c.rows
	c.mu.Unlock()

	if ptmx == nil {
		return nil
	}
	return pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close terminates the shell process and PTY.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	ptmx := c.ptmx
	cmd := c.cmd
	c.ptmx = nil
	c.mu.Unlock()

	if ptmx != nil {
		_ = ptmx.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// WorkingDirectory returns the last directory observed for the shell.
func (c *Client) WorkingDirectory() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startDir
}

func (c *Client) readLoop(ptmx *os.File) {
	defer ptmx.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 && c.onData != nil {
			c.onData(buf[:n])
		}
		if err != nil {
			c.mu.Lock()
			wasClosed := c.closed
			c.mu.Unlock()
			if !wasClosed && !isNormalPTYClosure(err) && c.onError != nil {
				c.onError(err)
			}
			return
		}
	}
}

func bytesContainCommandBoundary(data []byte) bool {
	for _, b := range data {
		if b == '\r' || b == '\n' || b == 0x04 {
			return true
		}
	}
	return false
}

func isNormalPTYClosure(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EIO)
}

func (c *Client) captureWorkingDirectory() {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}

	dir := processWorkingDirectory(cmd.Process.Pid)
	if dir == "" || !isDirectory(dir) {
		return
	}

	c.mu.Lock()
	if c.cmd == cmd {
		c.startDir = dir
	}
	c.mu.Unlock()
}

func processWorkingDirectory(pid int) string {
	switch runtime.GOOS {
	case "linux":
		dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		if err == nil {
			return dir
		}
	case "darwin":
		// macOS does not expose /proc. lsof is part of the base system and its
		// field output gives us the cwd without parsing a human-readable table.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
		if err != nil {
			return ""
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "n") {
				return strings.TrimPrefix(line, "n")
			}
		}
	}
	return ""
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
