package rawclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/andydixon/nexus/internal/models"
)

// Client manages a raw TCP connection.
// It provides binary-safe data handling with support for text and hex views,
// timestamps, direction markers, and payload presets.
type Client struct {
	mu        sync.Mutex
	config    *models.Profile
	conn      net.Conn
	connected bool
	onData    func(data []byte, direction string, timestamp time.Time)
	onError   func(err error)
	onClose   func()
	ctx       context.Context
	cancel    context.CancelFunc
	history   []HistoryEntry
	historyMu sync.RWMutex
}

// HistoryEntry records a single sent or received data frame with its timestamp.
type HistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Direction string    `json:"direction"` // "send" or "recv"
	Data      []byte    `json:"data"`
}

// NewClient creates a new raw TCP client bound to the given profile.
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
func (c *Client) SetCallbacks(onData func([]byte, string, time.Time), onError func(error), onClose func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onData = onData
	c.onError = onError
	c.onClose = onClose
}

// rawConf returns the RAW-specific configuration, or a zero value if nil.
func (c *Client) rawConf() models.RAWConfig {
	if c.config.RAW != nil {
		return *c.config.RAW
	}
	return models.RAWConfig{}
}

// Connect establishes the raw TCP connection and starts the read loop.
func (c *Client) Connect() error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	c.mu.Unlock()

	timeout := time.Duration(c.config.ConnectTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	port := c.config.Port
	if port == 0 {
		return fmt.Errorf("port must be specified for raw TCP connections")
	}
	addr := net.JoinHostPort(c.config.Host, fmt.Sprintf("%d", port))

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("raw TCP connection to %s failed: %w", addr, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	go c.readLoop()
	return nil
}

// Write sends raw bytes to the connection and records the frame in history.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	_, err := conn.Write(data)
	if err != nil {
		return err
	}

	now := time.Now()
	c.appendHistory(HistoryEntry{
		Timestamp: now,
		Direction: "send",
		Data:      copyBytes(data),
	})

	c.mu.Lock()
	cb := c.onData
	c.mu.Unlock()
	if cb != nil {
		cb(copyBytes(data), "send", now)
	}

	return nil
}

// WriteText sends text with the configured line ending appended.
func (c *Client) WriteText(text string) error {
	data := c.applyLineEnding(text)
	return c.Write(data)
}

// WriteHex sends hex-encoded bytes (e.g. "48656c6c6f" or "48 65 6c 6c 6f").
func (c *Client) WriteHex(hexStr string) error {
	data, err := ParseHexString(hexStr)
	if err != nil {
		return fmt.Errorf("invalid hex input: %w", err)
	}
	return c.Write(data)
}

// WriteEscaped sends text with C-style escape sequences processed
// (\r, \n, \t, \x00, etc.).
func (c *Client) WriteEscaped(text string) error {
	data, err := ParseEscaped(text)
	if err != nil {
		return fmt.Errorf("escape processing failed: %w", err)
	}
	return c.Write(data)
}

// Close shuts down the raw TCP connection and invokes the onClose callback.
func (c *Client) Close() error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil
	}
	c.connected = false
	conn := c.conn
	onClose := c.onClose
	c.mu.Unlock()

	c.cancel()

	var err error
	if conn != nil {
		err = conn.Close()
	}
	if onClose != nil {
		onClose()
	}
	return err
}

// IsConnected returns the current connection state.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// GetHistory returns a copy of the session data history.
func (c *Client) GetHistory() []HistoryEntry {
	c.historyMu.RLock()
	defer c.historyMu.RUnlock()

	out := make([]HistoryEntry, len(c.history))
	copy(out, c.history)
	return out
}

// ClearHistory clears the session history buffer.
func (c *Client) ClearHistory() {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	c.history = nil
}

// SaveCapture writes the session history to a file in JSON format.
func (c *Client) SaveCapture(path string) error {
	entries := c.GetHistory()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal capture data: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// readLoop reads from the connection and dispatches received data to the
// onData callback.
func (c *Client) readLoop() {
	buf := make([]byte, 4096)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.Lock()
		conn := c.conn
		connected := c.connected
		c.mu.Unlock()

		if !connected || conn == nil {
			return
		}

		n, err := conn.Read(buf)
		if n > 0 {
			now := time.Now()
			data := copyBytes(buf[:n])

			c.appendHistory(HistoryEntry{
				Timestamp: now,
				Direction: "recv",
				Data:      data,
			})

			c.mu.Lock()
			cb := c.onData
			c.mu.Unlock()
			if cb != nil {
				cb(data, "recv", now)
			}
		}

		if err != nil {
			c.mu.Lock()
			cb := c.onError
			c.mu.Unlock()
			if cb != nil {
				cb(fmt.Errorf("read error: %w", err))
			}
			c.Close()
			return
		}
	}
}

// appendHistory records a history entry in a thread-safe manner.
func (c *Client) appendHistory(entry HistoryEntry) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	c.history = append(c.history, entry)
}

// applyLineEnding appends the configured line ending to text.
func (c *Client) applyLineEnding(text string) []byte {
	rc := c.rawConf()
	le := rc.LineEnding
	if le == "" {
		le = models.LineEndingCRLF
	}

	var suffix string
	switch le {
	case models.LineEndingCR:
		suffix = "\r"
	case models.LineEndingLF:
		suffix = "\n"
	case models.LineEndingCRLF:
		suffix = "\r\n"
	case models.LineEndingNone:
		// No suffix.
	}

	if suffix != "" && !strings.HasSuffix(text, suffix) {
		text += suffix
	}
	return []byte(text)
}

// copyBytes returns an independent copy of a byte slice.
func copyBytes(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
