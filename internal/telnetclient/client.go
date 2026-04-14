// Package telnetclient provides a Telnet connection with basic option
// negotiation, break signal support, and callback-driven output.
package telnetclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Telnet command bytes.
const (
	cmdIAC  byte = 255
	cmdDONT byte = 254
	cmdDO   byte = 253
	cmdWONT byte = 252
	cmdWILL byte = 251
	cmdSB   byte = 250
	cmdSE   byte = 240
	cmdBRK  byte = 243

	optEcho          byte = 1
	optSuppressGoAh  byte = 3
	optTerminalType  byte = 24
	optNAWS          byte = 31
)

// Options configures a new Telnet connection.
type Options struct {
	Host           string
	Port           int
	TerminalType   string
	Encoding       string
	ConnectTimeout time.Duration

	// Callbacks invoked from a background goroutine.
	OnData  func(data []byte)
	OnError func(err error)
	OnClose func()

	// DebugLog, when non-nil, receives diagnostic messages.
	DebugLog func(msg string)
}

// Client manages a single Telnet connection with basic option negotiation.
type Client struct {
	opts   Options
	conn   net.Conn
	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc

	// Negotiated capabilities.
	nawsEnabled bool
	cols, rows  int
}

// New creates a Client with the given options.
func New(opts Options) *Client {
	if opts.TerminalType == "" {
		opts.TerminalType = "xterm-256color"
	}
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 30 * time.Second
	}
	return &Client{
		opts: opts,
		cols: 80,
		rows: 24,
	}
}

// Connect establishes the Telnet connection and begins reading data.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.debug("dialling %s:%d", c.opts.Host, c.opts.Port)

	addr := net.JoinHostPort(c.opts.Host, fmt.Sprintf("%d", c.opts.Port))
	dialer := &net.Dialer{Timeout: c.opts.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("telnetclient: dial failed: %w", err)
	}
	c.conn = conn

	go c.readLoop(ctx)
	return nil
}

// Write sends data to the remote host, escaping any IAC bytes.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil {
		return fmt.Errorf("telnetclient: connection is closed")
	}

	escaped := escapeIAC(data)
	_, err := c.conn.Write(escaped)
	return err
}

// Resize informs the remote host of a terminal size change via NAWS if
// negotiated.
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cols = cols
	c.rows = rows

	if !c.nawsEnabled || c.closed || c.conn == nil {
		return nil
	}
	return c.sendNAWS()
}

// SendBreak transmits a Telnet BRK command.
func (c *Client) SendBreak() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil {
		return fmt.Errorf("telnetclient: connection is closed")
	}

	c.debug("sending break signal")
	_, err := c.conn.Write([]byte{cmdIAC, cmdBRK})
	return err
}

// Close tears down the Telnet connection.
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
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// readLoop reads from the connection, processes Telnet commands, and delivers
// plain data via the OnData callback.
func (c *Client) readLoop(ctx context.Context) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := c.conn.Read(buf)
		if n > 0 {
			c.processBytes(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				c.debug("read error: %v", err)
				if c.opts.OnError != nil {
					c.opts.OnError(err)
				}
			}
			c.handleClose()
			return
		}
	}
}

// processBytes separates Telnet commands from application data.
func (c *Client) processBytes(data []byte) {
	var appData []byte

	for i := 0; i < len(data); i++ {
		if data[i] == cmdIAC && i+1 < len(data) {
			switch data[i+1] {
			case cmdIAC:
				// Escaped IAC → literal 0xFF.
				appData = append(appData, cmdIAC)
				i++
			case cmdDO:
				if i+2 < len(data) {
					c.handleDO(data[i+2])
					i += 2
				}
			case cmdDONT:
				if i+2 < len(data) {
					c.handleDONT(data[i+2])
					i += 2
				}
			case cmdWILL:
				if i+2 < len(data) {
					c.handleWILL(data[i+2])
					i += 2
				}
			case cmdWONT:
				if i+2 < len(data) {
					// Acknowledge WONT with DONT.
					c.sendCommand(cmdDONT, data[i+2])
					i += 2
				}
			case cmdSB:
				// Consume the sub-negotiation until IAC SE.
				end := c.findSubEnd(data[i+2:])
				i += 2 + end
			default:
				i++
			}
		} else {
			appData = append(appData, data[i])
		}
	}

	if len(appData) > 0 && c.opts.OnData != nil {
		c.opts.OnData(appData)
	}
}

// handleDO responds to a DO request from the server.
func (c *Client) handleDO(opt byte) {
	switch opt {
	case optTerminalType:
		c.debug("negotiated terminal type")
		c.sendCommand(cmdWILL, optTerminalType)
	case optNAWS:
		c.debug("negotiated NAWS")
		c.sendCommand(cmdWILL, optNAWS)
		c.mu.Lock()
		c.nawsEnabled = true
		_ = c.sendNAWS()
		c.mu.Unlock()
	case optSuppressGoAh:
		c.sendCommand(cmdWILL, optSuppressGoAh)
	default:
		c.sendCommand(cmdWONT, opt)
	}
}

// handleDONT responds to a DONT request from the server.
func (c *Client) handleDONT(opt byte) {
	if opt == optNAWS {
		c.mu.Lock()
		c.nawsEnabled = false
		c.mu.Unlock()
	}
	c.sendCommand(cmdWONT, opt)
}

// handleWILL responds to a WILL request from the server.
func (c *Client) handleWILL(opt byte) {
	switch opt {
	case optEcho, optSuppressGoAh:
		c.sendCommand(cmdDO, opt)
	default:
		c.sendCommand(cmdDONT, opt)
	}
}

// sendCommand writes a three-byte Telnet command.
func (c *Client) sendCommand(cmd, opt byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil && !c.closed {
		c.conn.Write([]byte{cmdIAC, cmd, opt})
	}
}

// sendNAWS transmits the current window size via NAWS sub-negotiation.
// Caller must hold c.mu.
func (c *Client) sendNAWS() error {
	if c.conn == nil {
		return nil
	}
	msg := []byte{
		cmdIAC, cmdSB, optNAWS,
		byte(c.cols >> 8), byte(c.cols & 0xFF),
		byte(c.rows >> 8), byte(c.rows & 0xFF),
		cmdIAC, cmdSE,
	}
	_, err := c.conn.Write(msg)
	return err
}

// findSubEnd returns the offset past the IAC SE that terminates a
// sub-negotiation block.
func (c *Client) findSubEnd(data []byte) int {
	for i := 0; i < len(data)-1; i++ {
		if data[i] == cmdIAC && data[i+1] == cmdSE {
			return i + 2
		}
	}
	return len(data)
}

// handleClose is called when the connection ends.
func (c *Client) handleClose() {
	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	c.mu.Unlock()

	if !wasClosed && c.opts.OnClose != nil {
		c.opts.OnClose()
	}
}

// escapeIAC doubles any 0xFF bytes in outbound application data so they are
// not mistaken for Telnet commands.
func escapeIAC(data []byte) []byte {
	count := 0
	for _, b := range data {
		if b == cmdIAC {
			count++
		}
	}
	if count == 0 {
		return data
	}
	out := make([]byte, 0, len(data)+count)
	for _, b := range data {
		out = append(out, b)
		if b == cmdIAC {
			out = append(out, cmdIAC)
		}
	}
	return out
}

func (c *Client) debug(format string, args ...interface{}) {
	if c.opts.DebugLog != nil {
		c.opts.DebugLog(fmt.Sprintf(format, args...))
	}
}
