// Package rawclient provides a plain TCP connection with bidirectional data
// history, capture-to-file support, and callback-driven output.
package rawclient

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// HistoryEntry records a single sent or received data frame.
type HistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Direction string    `json:"direction"` // "sent" or "received"
	Data      []byte    `json:"data"`
}

// Options configures a new RAW TCP connection.
type Options struct {
	Host           string
	Port           int
	ConnectTimeout time.Duration

	// OnData is called for every received chunk. The direction parameter is
	// always "received" for incoming data.
	OnData func(data []byte, direction string, timestamp time.Time)

	// OnError is called on non-EOF read errors.
	OnError func(err error)

	// OnClose is called once when the connection terminates.
	OnClose func()

	// DebugLog, when non-nil, receives diagnostic messages.
	DebugLog func(msg string)
}

// Client manages a single RAW TCP connection with history tracking.
type Client struct {
	opts    Options
	conn    net.Conn
	mu      sync.Mutex
	closed  bool
	cancel  context.CancelFunc
	history []HistoryEntry
	histMu  sync.Mutex
}

// New creates a Client with the given options.
func New(opts Options) *Client {
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 30 * time.Second
	}
	return &Client{opts: opts}
}

// Connect establishes the TCP connection and begins reading data.
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
		return fmt.Errorf("rawclient: dial failed: %w", err)
	}
	c.conn = conn

	go c.readLoop(ctx)
	return nil
}

// Write sends data to the remote host and records it in the history.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil {
		return fmt.Errorf("rawclient: connection is closed")
	}

	_, err := c.conn.Write(data)
	if err != nil {
		return err
	}

	now := time.Now()
	c.recordHistory("sent", data, now)

	if c.opts.OnData != nil {
		echoCopy := make([]byte, len(data))
		copy(echoCopy, data)
		c.opts.OnData(echoCopy, "sent", now)
	}
	return nil
}

// Close tears down the TCP connection.
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

// GetHistory returns a copy of the recorded data history.
func (c *Client) GetHistory() []HistoryEntry {
	c.histMu.Lock()
	defer c.histMu.Unlock()

	result := make([]HistoryEntry, len(c.history))
	copy(result, c.history)
	return result
}

// ClearHistory removes all recorded history entries.
func (c *Client) ClearHistory() {
	c.histMu.Lock()
	defer c.histMu.Unlock()
	c.history = nil
}

// SaveCapture writes the session history to a file in a human-readable
// format with timestamps and direction markers.
func (c *Client) SaveCapture(path string) error {
	c.histMu.Lock()
	entries := make([]HistoryEntry, len(c.history))
	copy(entries, c.history)
	c.histMu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("rawclient: failed to create capture file: %w", err)
	}
	defer f.Close()

	for _, entry := range entries {
		ts := entry.Timestamp.Format("2006-01-02 15:04:05.000")
		dir := ">>>"
		if entry.Direction == "received" {
			dir = "<<<"
		}
		hexStr := hex.EncodeToString(entry.Data)
		if _, err := fmt.Fprintf(f, "[%s] %s %s\n", ts, dir, hexStr); err != nil {
			return fmt.Errorf("rawclient: failed to write capture: %w", err)
		}
	}
	return nil
}

// readLoop continuously reads from the connection and delivers data.
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
			now := time.Now()
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			c.recordHistory("received", chunk, now)

			if c.opts.OnData != nil {
				c.opts.OnData(chunk, "received", now)
			}
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

// recordHistory appends a data frame to the session history.
func (c *Client) recordHistory(direction string, data []byte, ts time.Time) {
	c.histMu.Lock()
	defer c.histMu.Unlock()

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	c.history = append(c.history, HistoryEntry{
		Timestamp: ts,
		Direction: direction,
		Data:      dataCopy,
	})
}

// handleClose is called when the connection terminates.
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
