package telnetclient

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/andydixon/nexus/internal/models"
	"golang.org/x/text/encoding/charmap"
)

// Telnet command bytes (RFC 854/855).
const (
	IAC  byte = 255 // Interpret As Command
	DONT byte = 254
	DO   byte = 253
	WONT byte = 252
	WILL byte = 251
	SB   byte = 250 // Sub-negotiation Begin
	SE   byte = 240 // Sub-negotiation End

	// Common options.
	OptEcho     byte = 1
	OptSGA      byte = 3  // Suppress Go Ahead
	OptTermType byte = 24 // Terminal Type
	OptNAWS     byte = 31 // Negotiate About Window Size
	OptLineMode byte = 34
)

// Sub-negotiation constants.
const (
	subIS   byte = 0 // IS (used in terminal-type sub-negotiation)
	subSEND byte = 1 // SEND (server requests terminal type)
)

// Client manages a single Telnet session.
// It handles Telnet option negotiation (RFC 854/855), terminal-type
// sub-negotiation, window-size negotiation (NAWS), and data framing.
type Client struct {
	mu        sync.Mutex
	config    *models.Profile
	conn      net.Conn
	connected bool
	onData    func(data []byte)
	onError   func(err error)
	onClose   func()
	onDebug   func(msg string)
	ctx       context.Context
	cancel    context.CancelFunc
	localEcho bool
	encoding  string
	cols      int
	rows      int

	// Track which options we have agreed to support.
	willOptions map[byte]bool
	doOptions   map[byte]bool
}

// NewClient creates a new Telnet client bound to the given profile.
func NewClient(profile *models.Profile) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	localEcho := false
	enc := "utf-8"
	if profile.Telnet != nil {
		localEcho = profile.Telnet.LocalEcho
		if profile.Telnet.Encoding != "" {
			enc = profile.Telnet.Encoding
		}
	}

	return &Client{
		config:      profile,
		ctx:         ctx,
		cancel:      cancel,
		localEcho:   localEcho,
		encoding:    enc,
		cols:        80,
		rows:        24,
		willOptions: make(map[byte]bool),
		doOptions:   make(map[byte]bool),
	}
}

// SetCallbacks sets the data, error, close, and debug callbacks.
// Must be called before Connect.
func (c *Client) SetCallbacks(onData func([]byte), onError func(error), onClose func(), onDebug func(string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onData = onData
	c.onError = onError
	c.onClose = onClose
	c.onDebug = onDebug
}

// telnetConf returns the Telnet-specific configuration, or a zero value
// if nil.
func (c *Client) telnetConf() models.TelnetConfig {
	if c.config.Telnet != nil {
		return *c.config.Telnet
	}
	return models.TelnetConfig{}
}

// Connect establishes the Telnet connection and starts the read loop.
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
		port = 23
	}
	addr := net.JoinHostPort(c.config.Host, fmt.Sprintf("%d", port))

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("Telnet connection to %s failed: %w", addr, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	go c.readLoop()
	return nil
}

// Write sends data to the Telnet session, applying line-ending conversion
// and escaping any literal IAC bytes in the payload.
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	converted := c.convertLineEnding(data)
	escaped := c.escapeIAC(converted)
	encoded := c.encodeOutgoing(escaped)

	_, err := conn.Write(encoded)
	return err
}

// Resize sends a NAWS (Negotiate About Window Size) update to the remote host.
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	c.cols = cols
	c.rows = rows
	conn := c.conn
	c.mu.Unlock()

	return c.sendNAWS(conn, cols, rows)
}

// Close shuts down the Telnet connection and invokes the onClose callback.
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

// IsConnected returns the connection state.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// SendBreak transmits a Telnet break signal (IAC BRK).
func (c *Client) SendBreak() error {
	c.mu.Lock()
	if !c.connected || c.conn == nil {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	_, err := conn.Write([]byte{IAC, 243}) // 243 = BRK
	return err
}

// readLoop reads from the connection, processes IAC sequences, and dispatches
// plain data to the onData callback.
func (c *Client) readLoop() {
	buf := make([]byte, 4096)
	var dataBuf []byte

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
			raw := buf[:n]
			dataBuf = dataBuf[:0]

			i := 0
			for i < len(raw) {
				if raw[i] == IAC {
					if len(dataBuf) > 0 {
						c.dispatchData(dataBuf)
						dataBuf = dataBuf[:0]
					}
					consumed := c.handleIAC(raw[i:])
					if consumed == 0 {
						break
					}
					i += consumed
				} else {
					dataBuf = append(dataBuf, raw[i])
					i++
				}
			}

			if len(dataBuf) > 0 {
				c.dispatchData(dataBuf)
			}
		}

		if err != nil {
			c.mu.Lock()
			onError := c.onError
			c.mu.Unlock()
			if onError != nil {
				onError(fmt.Errorf("read error: %w", err))
			}
			c.Close()
			return
		}
	}
}

// handleIAC processes a Telnet IAC sequence starting at data[0].
// Returns the number of bytes consumed.
func (c *Client) handleIAC(data []byte) int {
	if len(data) < 2 {
		return 0
	}

	// IAC IAC = escaped 0xFF byte.
	if data[1] == IAC {
		c.dispatchData([]byte{IAC})
		return 2
	}

	switch data[1] {
	case DO:
		if len(data) < 3 {
			return 0
		}
		c.handleDO(data[2])
		return 3

	case DONT:
		if len(data) < 3 {
			return 0
		}
		c.handleDONT(data[2])
		return 3

	case WILL:
		if len(data) < 3 {
			return 0
		}
		c.handleWILL(data[2])
		return 3

	case WONT:
		if len(data) < 3 {
			return 0
		}
		c.handleWONT(data[2])
		return 3

	case SB:
		return c.handleSubnegotiation(data)

	default:
		c.debug("received IAC command: %d", data[1])
		return 2
	}
}

// handleDO processes a DO request from the server.
func (c *Client) handleDO(option byte) {
	c.debug("received DO %d", option)

	switch option {
	case OptTermType:
		c.negotiate(WILL, option)
		c.mu.Lock()
		c.willOptions[option] = true
		c.mu.Unlock()

	case OptNAWS:
		c.negotiate(WILL, option)
		c.mu.Lock()
		c.willOptions[option] = true
		cols := c.cols
		rows := c.rows
		conn := c.conn
		c.mu.Unlock()
		if conn != nil {
			_ = c.sendNAWS(conn, cols, rows)
		}

	case OptSGA:
		c.negotiate(WILL, option)
		c.mu.Lock()
		c.willOptions[option] = true
		c.mu.Unlock()

	default:
		c.negotiate(WONT, option)
	}
}

// handleDONT processes a DONT request from the server.
func (c *Client) handleDONT(option byte) {
	c.debug("received DONT %d", option)
	c.mu.Lock()
	delete(c.willOptions, option)
	c.mu.Unlock()
	c.negotiate(WONT, option)
}

// handleWILL processes a WILL offer from the server.
func (c *Client) handleWILL(option byte) {
	c.debug("received WILL %d", option)

	switch option {
	case OptEcho:
		c.negotiate(DO, option)
		c.mu.Lock()
		c.doOptions[option] = true
		c.localEcho = false
		c.mu.Unlock()

	case OptSGA:
		c.negotiate(DO, option)
		c.mu.Lock()
		c.doOptions[option] = true
		c.mu.Unlock()

	default:
		c.negotiate(DONT, option)
	}
}

// handleWONT processes a WONT refusal from the server.
func (c *Client) handleWONT(option byte) {
	c.debug("received WONT %d", option)

	if option == OptEcho {
		c.mu.Lock()
		c.localEcho = true
		c.mu.Unlock()
	}

	c.mu.Lock()
	delete(c.doOptions, option)
	c.mu.Unlock()
	c.negotiate(DONT, option)
}

// handleSubnegotiation processes an SB...SE sub-negotiation block.
// Returns the total number of bytes consumed (including IAC SB and IAC SE).
func (c *Client) handleSubnegotiation(data []byte) int {
	for i := 2; i < len(data)-1; i++ {
		if data[i] == IAC && data[i+1] == SE {
			payload := data[2:i]
			c.processSubnegotiation(payload)
			return i + 2
		}
	}
	return 0
}

// processSubnegotiation handles the payload between SB and SE.
func (c *Client) processSubnegotiation(payload []byte) {
	if len(payload) < 1 {
		return
	}

	option := payload[0]
	c.debug("sub-negotiation for option %d", option)

	switch option {
	case OptTermType:
		if len(payload) >= 2 && payload[1] == subSEND {
			c.sendTermType()
		}
	}
}

// sendTermType responds to a terminal-type SEND request with an IS response.
func (c *Client) sendTermType() {
	tc := c.telnetConf()
	termType := tc.TerminalType
	if termType == "" {
		termType = "xterm-256color"
	}

	c.debug("sending terminal type: %s", termType)

	resp := []byte{IAC, SB, OptTermType, subIS}
	resp = append(resp, []byte(strings.ToUpper(termType))...)
	resp = append(resp, IAC, SE)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_, _ = conn.Write(resp)
	}
}

// negotiate sends a WILL/WONT/DO/DONT response for the given option.
func (c *Client) negotiate(command, option byte) {
	c.debug("sending %s %d", commandName(command), option)

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_, _ = conn.Write([]byte{IAC, command, option})
	}
}

// sendNAWS sends the current window size via NAWS sub-negotiation.
// Columns and rows are each sent as big-endian uint16 values.
func (c *Client) sendNAWS(conn net.Conn, cols, rows int) error {
	c.debug("sending NAWS: %dx%d", cols, rows)

	var buf [9]byte
	buf[0] = IAC
	buf[1] = SB
	buf[2] = OptNAWS
	binary.BigEndian.PutUint16(buf[3:5], uint16(cols))
	binary.BigEndian.PutUint16(buf[5:7], uint16(rows))
	buf[7] = IAC
	buf[8] = SE

	_, err := conn.Write(buf[:])
	return err
}

// convertLineEnding applies the configured line-ending conversion on
// outgoing data.
func (c *Client) convertLineEnding(data []byte) []byte {
	tc := c.telnetConf()
	le := tc.LineEnding
	if le == "" {
		le = models.LineEndingCRLF
	}
	if le == models.LineEndingNone {
		return data
	}

	s := string(data)

	// Normalise all line endings to LF first.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	switch le {
	case models.LineEndingCR:
		s = strings.ReplaceAll(s, "\n", "\r")
	case models.LineEndingCRLF:
		s = strings.ReplaceAll(s, "\n", "\r\n")
	case models.LineEndingLF:
		// Already LF.
	}
	return []byte(s)
}

// escapeIAC doubles any literal 0xFF bytes in the data so they are not
// misinterpreted as IAC commands.
func (c *Client) escapeIAC(data []byte) []byte {
	count := 0
	for _, b := range data {
		if b == IAC {
			count++
		}
	}
	if count == 0 {
		return data
	}
	escaped := make([]byte, 0, len(data)+count)
	for _, b := range data {
		escaped = append(escaped, b)
		if b == IAC {
			escaped = append(escaped, IAC)
		}
	}
	return escaped
}

// encodeOutgoing converts outgoing text from UTF-8 to the configured encoding.
func (c *Client) encodeOutgoing(data []byte) []byte {
	switch strings.ToLower(c.encoding) {
	case "iso-8859-1", "latin1", "latin-1":
		encoded, err := charmap.ISO8859_1.NewEncoder().Bytes(data)
		if err != nil {
			return data
		}
		return encoded
	default:
		return data
	}
}

// dispatchData sends received data to the onData callback.
func (c *Client) dispatchData(data []byte) {
	out := make([]byte, len(data))
	copy(out, data)

	c.mu.Lock()
	cb := c.onData
	c.mu.Unlock()
	if cb != nil {
		cb(out)
	}
}

// debug emits a formatted debug message if a debug callback is registered.
func (c *Client) debug(format string, args ...interface{}) {
	c.mu.Lock()
	cb := c.onDebug
	c.mu.Unlock()
	if cb != nil {
		cb(fmt.Sprintf(format, args...))
	}
}

// commandName returns a human-readable name for a Telnet negotiation command.
func commandName(cmd byte) string {
	switch cmd {
	case WILL:
		return "WILL"
	case WONT:
		return "WONT"
	case DO:
		return "DO"
	case DONT:
		return "DONT"
	default:
		return fmt.Sprintf("CMD(%d)", cmd)
	}
}
