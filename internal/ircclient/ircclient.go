// Package ircclient wraps a full IRC client with Link-specific event and
// command handling, including optional SSH tunnelling.
package ircclient

import (
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lrstanley/girc"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const ServerTarget = "server"

const maxRoomListEntries = 2000

// Config describes an IRC connection.
type Config struct {
	Server        string
	Port          int
	Nick          string
	User          string
	RealName      string
	UseTLS        bool
	ServerPass    string
	AutoJoinRooms []string

	NickServIdentify bool
	NickServAccount  string
	NickServPassword string

	SSHTunnel          bool
	SSHHost            string
	SSHPort            int
	SSHUsername        string
	SSHAuthMethod      string
	SSHPassword        string
	SSHPrivateKey      []byte
	SSHPassphrase      string
	SSHAgentAuth       bool
	SSHAgentConn       net.Conn
	SSHHostKeyCallback ssh.HostKeyCallback

	ConnectTimeout time.Duration
	OnEvent        func(Event)
	OnError        func(error)
	OnClose        func()
	OnRoomsChanged func([]string)
}

// Event is a typed IRC event suitable for JSON delivery to the frontend.
type Event struct {
	Type      string     `json:"type"`
	Target    string     `json:"target,omitempty"`
	From      string     `json:"from,omitempty"`
	Text      string     `json:"text,omitempty"`
	Command   string     `json:"command,omitempty"`
	Timestamp int64      `json:"timestamp"`
	Rooms     []RoomInfo `json:"rooms,omitempty"`
	Users     []string   `json:"users,omitempty"`
	Topic     string     `json:"topic,omitempty"`
}

// RoomInfo describes a joined or listed IRC room.
type RoomInfo struct {
	Name  string `json:"name"`
	Topic string `json:"topic,omitempty"`
	Users int    `json:"users,omitempty"`
}

// Client owns a girc client and optional SSH tunnel.
type Client struct {
	cfg       Config
	client    *girc.Client
	sshClient *ssh.Client
	agentConn net.Conn

	mu        sync.Mutex
	connected bool
	closing   bool
	listRooms []RoomInfo
	roomNames []string
}

// New creates an IRC client wrapper.
func New(cfg Config) *Client {
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	if cfg.User == "" {
		cfg.User = cfg.Nick
	}
	if cfg.RealName == "" {
		cfg.RealName = cfg.Nick
	}

	c := &Client{cfg: cfg}
	c.client = girc.New(girc.Config{
		Server:     cfg.Server,
		ServerPass: cfg.ServerPass,
		Port:       cfg.Port,
		Nick:       cfg.Nick,
		User:       cfg.User,
		Name:       cfg.RealName,
		SSL:        cfg.UseTLS && !cfg.SSHTunnel,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: cfg.Server,
		},
		AllowFlood: false,
	})
	c.registerHandlers()
	return c
}

// Connect starts the IRC connection and returns once registration succeeds.
func (c *Client) Connect() error {
	connectedCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var connectedOnce sync.Once

	c.client.Handlers.Add(girc.CONNECTED, func(gc *girc.Client, e girc.Event) {
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()

		c.emit(Event{
			Type:      "system",
			Target:    ServerTarget,
			Text:      fmt.Sprintf("Connected to %s", gc.Server()),
			Timestamp: eventTime(e),
		})

		c.identifyWithNickServ(gc)
		if len(c.cfg.AutoJoinRooms) > 0 {
			gc.Cmd.Join(c.cfg.AutoJoinRooms...)
		}
		c.emitRooms()

		connectedOnce.Do(func() {
			select {
			case connectedCh <- struct{}{}:
			default:
			}
		})
	})

	go func() {
		var err error
		if c.cfg.SSHTunnel {
			dialer, dialerErr := c.newSSHDialer()
			if dialerErr != nil {
				select {
				case errCh <- dialerErr:
				default:
				}
				return
			}
			err = c.client.DialerConnect(dialer)
		} else {
			err = c.client.Connect()
		}

		c.mu.Lock()
		wasClosing := c.closing
		wasConnected := c.connected
		c.connected = false
		c.mu.Unlock()

		if err != nil && !wasClosing {
			c.emit(Event{
				Type:      "error",
				Target:    ServerTarget,
				Text:      err.Error(),
				Timestamp: time.Now().UnixMilli(),
			})
			if c.cfg.OnError != nil {
				c.cfg.OnError(err)
			}
			select {
			case errCh <- err:
			default:
			}
		}
		if wasConnected && !wasClosing && c.cfg.OnClose != nil {
			c.cfg.OnClose()
		}
	}()

	select {
	case <-connectedCh:
		return nil
	case err := <-errCh:
		return err
	case <-time.After(c.cfg.ConnectTimeout):
		c.Close()
		return fmt.Errorf("IRC connection timed out after %s", c.cfg.ConnectTimeout)
	}
}

// Close disconnects from IRC and closes any SSH tunnel resources.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return
	}
	c.closing = true
	c.mu.Unlock()

	if c.client != nil {
		c.client.Quit("Alchementary Link disconnected")
		c.client.Close()
	}
	if c.sshClient != nil {
		_ = c.sshClient.Close()
		c.sshClient = nil
	}
	if c.agentConn != nil {
		_ = c.agentConn.Close()
		c.agentConn = nil
	}
}

// HandleInput parses a user-entered IRC composer line.
func (c *Client) HandleInput(target string, input string) error {
	text := strings.TrimSpace(input)
	if text == "" {
		return nil
	}
	if target == "" {
		target = ServerTarget
	}

	if !strings.HasPrefix(text, "/") || text == "/" {
		if target == ServerTarget {
			return fmt.Errorf("select a room or use /msg <nick> <message>")
		}
		c.client.Cmd.Message(target, text)
		c.emitLocalMessage("message", target, text)
		return nil
	}

	commandLine := strings.TrimSpace(strings.TrimPrefix(text, "/"))
	if commandLine == "" {
		return nil
	}
	fields := strings.Fields(commandLine)
	cmd := strings.ToLower(fields[0])
	args := strings.TrimSpace(commandLine[len(fields[0]):])

	switch cmd {
	case "me", "action":
		if target == ServerTarget {
			return fmt.Errorf("/me requires an active room or private tab")
		}
		if args == "" {
			return fmt.Errorf("/me requires an action")
		}
		c.client.Cmd.Action(target, args)
		c.emitLocalMessage("action", target, args)
	case "msg", "query":
		recipient, body, ok := splitFirstArg(args)
		if !ok || body == "" {
			return fmt.Errorf("/msg requires a target and message")
		}
		c.client.Cmd.Message(recipient, body)
		c.emitLocalMessage("message", recipient, body)
	case "notice":
		recipient, body, ok := splitFirstArg(args)
		if !ok || body == "" {
			return fmt.Errorf("/notice requires a target and message")
		}
		c.client.Cmd.Notice(recipient, body)
		c.emitLocalMessage("notice", recipient, body)
	case "join", "j":
		room, key, ok := splitFirstArg(args)
		if !ok {
			return fmt.Errorf("/join requires a room")
		}
		if key != "" {
			c.client.Cmd.JoinKey(room, key)
		} else {
			c.client.Cmd.Join(room)
		}
	case "part", "leave":
		room, msg, _ := splitFirstArg(args)
		if room == "" {
			room = target
		}
		if room == "" || room == ServerTarget {
			return fmt.Errorf("/part requires a room")
		}
		if msg != "" {
			c.client.Cmd.PartMessage(room, msg)
		} else {
			c.client.Cmd.Part(room)
		}
	case "quit":
		if args == "" {
			args = "Alchementary Link disconnected"
		}
		c.client.Quit(args)
		c.client.Close()
	case "nick":
		if args == "" {
			return fmt.Errorf("/nick requires a nickname")
		}
		c.client.Cmd.Nick(strings.Fields(args)[0])
	case "topic":
		room, topic, _ := splitFirstArg(args)
		if room == "" {
			room = target
		}
		if room == "" || room == ServerTarget {
			return fmt.Errorf("/topic requires a room")
		}
		c.client.Cmd.Topic(room, topic)
	case "list":
		if args == "" {
			c.client.Cmd.List()
		} else {
			c.client.Cmd.List(strings.Fields(args)...)
		}
	case "names":
		room := args
		if room == "" && target != ServerTarget {
			room = target
		}
		if room == "" {
			return c.client.Cmd.SendRaw("NAMES")
		}
		return c.client.Cmd.SendRaw("NAMES " + room)
	case "whois":
		if args == "" {
			return fmt.Errorf("/whois requires a nickname")
		}
		c.client.Cmd.Whois(strings.Fields(args)...)
	case "who":
		if args == "" {
			if target == ServerTarget {
				return fmt.Errorf("/who requires a target")
			}
			args = target
		}
		c.client.Cmd.Who(strings.Fields(args)...)
	case "raw", "quote":
		if args == "" {
			return fmt.Errorf("/raw requires an IRC command")
		}
		return c.client.Cmd.SendRawNoSplit(args)
	case "help":
		c.emit(Event{
			Type:      "system",
			Target:    target,
			Text:      "Commands: /join, /part, /msg, /notice, /me, /nick, /topic, /list, /names, /whois, /who, /raw, /quit",
			Timestamp: time.Now().UnixMilli(),
		})
	default:
		raw := strings.ToUpper(fields[0])
		if args != "" {
			raw += " " + args
		}
		return c.client.Cmd.SendRawNoSplit(raw)
	}

	return nil
}

// Join requests a room join.
func (c *Client) Join(room string) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("room is required")
	}
	c.client.Cmd.Join(room)
	return nil
}

// Part leaves a room.
func (c *Client) Part(room string) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("room is required")
	}
	c.client.Cmd.Part(room)
	return nil
}

// ListRooms requests a server-side room list. Results arrive as events.
func (c *Client) ListRooms() error {
	c.mu.Lock()
	c.listRooms = nil
	c.mu.Unlock()
	c.client.Cmd.List()
	return nil
}

// Rooms returns currently joined rooms.
func (c *Client) Rooms() []RoomInfo {
	if c.client == nil {
		return nil
	}
	c.mu.Lock()
	roomNames := append([]string{}, c.roomNames...)
	c.mu.Unlock()
	rooms := make([]RoomInfo, 0, len(roomNames))
	for _, roomName := range roomNames {
		ch := c.client.LookupChannel(roomName)
		if ch == nil {
			rooms = append(rooms, RoomInfo{Name: roomName})
			continue
		}
		rooms = append(rooms, RoomInfo{
			Name:  ch.Name,
			Topic: ch.Topic,
			Users: len(ch.UserList),
		})
	}
	sort.Slice(rooms, func(i, j int) bool {
		return strings.ToLower(rooms[i].Name) < strings.ToLower(rooms[j].Name)
	})
	return rooms
}

// Users returns known users in a room.
func (c *Client) Users(room string) []string {
	if c.client == nil {
		return nil
	}
	ch := c.client.LookupChannel(room)
	if ch == nil {
		return []string{}
	}
	users := append([]string{}, ch.UserList...)
	sort.Slice(users, func(i, j int) bool {
		return strings.TrimLeft(users[i], "~&@%+") < strings.TrimLeft(users[j], "~&@%+")
	})
	return users
}

func (c *Client) registerHandlers() {
	c.client.Handlers.Add(girc.PRIVMSG, c.handleMessage)
	c.client.Handlers.Add(girc.NOTICE, c.handleNotice)
	c.client.Handlers.Add(girc.JOIN, c.handleJoin)
	c.client.Handlers.Add(girc.PART, c.handlePart)
	c.client.Handlers.Add(girc.KICK, c.handleKick)
	c.client.Handlers.Add(girc.QUIT, c.handleQuit)
	c.client.Handlers.Add(girc.NICK, c.handleNick)
	c.client.Handlers.Add(girc.TOPIC, c.handleTopic)
	c.client.Handlers.Add(girc.RPL_TOPIC, c.handleTopic)
	c.client.Handlers.Add(girc.RPL_NAMREPLY, c.handleNames)
	c.client.Handlers.Add(girc.RPL_ENDOFNAMES, c.handleEndOfNames)
	c.client.Handlers.Add(girc.RPL_LIST, c.handleList)
	c.client.Handlers.Add(girc.RPL_LISTEND, c.handleListEnd)
	c.client.Handlers.Add(girc.ERROR, func(_ *girc.Client, e girc.Event) {
		c.emit(Event{Type: "error", Target: ServerTarget, Text: e.Last(), Timestamp: eventTime(e)})
	})
}

func (c *Client) handleMessage(gc *girc.Client, e girc.Event) {
	if len(e.Params) < 2 || e.Source == nil {
		return
	}
	target := e.Params[0]
	if strings.EqualFold(target, gc.GetNick()) {
		target = e.Source.Name
	}
	eventType := "message"
	text := e.Last()
	if e.IsAction() {
		eventType = "action"
		text = e.StripAction()
	}
	c.emit(Event{Type: eventType, Target: target, From: e.Source.Name, Text: text, Timestamp: eventTime(e)})
}

func (c *Client) handleNotice(gc *girc.Client, e girc.Event) {
	target := ServerTarget
	if len(e.Params) > 0 {
		target = e.Params[0]
		if target == "*" {
			target = ServerTarget
		}
		if strings.EqualFold(target, gc.GetNick()) && e.Source != nil {
			target = e.Source.Name
		}
	}
	from := ""
	if e.Source != nil {
		from = e.Source.Name
	}
	c.emit(Event{Type: "notice", Target: target, From: from, Text: e.Last(), Timestamp: eventTime(e)})
}

func (c *Client) handleJoin(gc *girc.Client, e girc.Event) {
	room := eventRoom(e)
	if room == "" {
		return
	}
	from := sourceName(e)
	if strings.EqualFold(from, gc.GetNick()) {
		c.rememberRoomMembership(room, true)
	}
	c.emit(Event{Type: "join", Target: room, From: from, Text: fmt.Sprintf("%s joined %s", from, room), Timestamp: eventTime(e)})
	c.emitRooms()
	c.emitUsers(room)
}

func (c *Client) handlePart(gc *girc.Client, e girc.Event) {
	room := eventRoom(e)
	if room == "" {
		return
	}
	from := sourceName(e)
	message := ""
	if len(e.Params) > 1 {
		message = strings.TrimSpace(e.Last())
	}
	if strings.EqualFold(from, gc.GetNick()) {
		c.rememberRoomMembership(room, false)
	}
	c.emit(Event{Type: "part", Target: room, From: from, Text: message, Timestamp: eventTime(e)})
	c.emitRooms()
	c.emitUsers(room)
}

func (c *Client) handleKick(gc *girc.Client, e girc.Event) {
	if len(e.Params) < 2 {
		return
	}
	room := e.Params[0]
	kicked := e.Params[1]
	if strings.EqualFold(kicked, gc.GetNick()) {
		c.rememberRoomMembership(room, false)
	}
	c.emit(Event{
		Type:      "kick",
		Target:    room,
		From:      sourceName(e),
		Command:   kicked,
		Text:      strings.TrimSpace(e.Last()),
		Timestamp: eventTime(e),
	})
	c.emitRooms()
	c.emitUsers(room)
}

func (c *Client) handleQuit(gc *girc.Client, e girc.Event) {
	from := sourceName(e)
	c.emit(Event{Type: "quit", Target: ServerTarget, From: from, Text: strings.TrimSpace(e.Last()), Timestamp: eventTime(e)})
	c.emitUsersForAll()
}

func (c *Client) handleNick(gc *girc.Client, e girc.Event) {
	from := sourceName(e)
	newNick := e.Last()
	c.emit(Event{Type: "nick", Target: ServerTarget, From: from, Text: newNick, Timestamp: eventTime(e)})
	c.emitUsersForAll()
}

func (c *Client) handleTopic(gc *girc.Client, e girc.Event) {
	room := ""
	topic := e.Last()
	if e.Command == girc.RPL_TOPIC && len(e.Params) >= 3 {
		room = e.Params[1]
		topic = e.Params[2]
	} else if len(e.Params) >= 2 {
		room = e.Params[0]
		topic = e.Params[1]
	}
	if room == "" {
		return
	}
	c.emit(Event{Type: "topic", Target: room, From: sourceName(e), Text: topic, Topic: topic, Timestamp: eventTime(e)})
	c.emitRooms()
}

func (c *Client) handleNames(gc *girc.Client, e girc.Event) {
	if len(e.Params) >= 4 {
		c.emit(Event{Type: "users", Target: e.Params[2], Users: splitNames(e.Params[3]), Timestamp: eventTime(e)})
	}
}

func (c *Client) handleEndOfNames(gc *girc.Client, e girc.Event) {
	if len(e.Params) >= 2 {
		c.emitUsers(e.Params[1])
	}
}

func (c *Client) handleList(gc *girc.Client, e girc.Event) {
	if len(e.Params) < 4 {
		return
	}
	users, _ := strconv.Atoi(e.Params[2])
	room := RoomInfo{Name: e.Params[1], Users: users, Topic: e.Params[3]}
	c.mu.Lock()
	if len(c.listRooms) < maxRoomListEntries {
		c.listRooms = append(c.listRooms, room)
	}
	c.mu.Unlock()
}

func (c *Client) handleListEnd(gc *girc.Client, e girc.Event) {
	c.mu.Lock()
	rooms := append([]RoomInfo{}, c.listRooms...)
	c.listRooms = nil
	c.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool {
		if rooms[i].Users == rooms[j].Users {
			return strings.ToLower(rooms[i].Name) < strings.ToLower(rooms[j].Name)
		}
		return rooms[i].Users > rooms[j].Users
	})
	c.emit(Event{Type: "room-list", Target: ServerTarget, Rooms: rooms, Timestamp: eventTime(e)})
}

func (c *Client) identifyWithNickServ(gc *girc.Client) {
	if !c.cfg.NickServIdentify || c.cfg.NickServPassword == "" {
		return
	}
	if c.cfg.NickServAccount != "" {
		gc.Cmd.Message("NickServ", fmt.Sprintf("IDENTIFY %s %s", c.cfg.NickServAccount, c.cfg.NickServPassword))
	} else {
		gc.Cmd.Message("NickServ", "IDENTIFY "+c.cfg.NickServPassword)
	}
	c.emit(Event{Type: "system", Target: ServerTarget, Text: "Sent NickServ identify request", Timestamp: time.Now().UnixMilli()})
}

func (c *Client) newSSHDialer() (*sshDialer, error) {
	if c.cfg.SSHHostKeyCallback == nil {
		return nil, fmt.Errorf("SSH host key callback is required")
	}
	authMethods, err := c.sshAuthMethods()
	if err != nil {
		return nil, err
	}
	sshAddr := net.JoinHostPort(c.cfg.SSHHost, strconv.Itoa(c.cfg.SSHPort))
	sshConfig := &ssh.ClientConfig{
		User:            c.cfg.SSHUsername,
		Auth:            authMethods,
		HostKeyCallback: c.cfg.SSHHostKeyCallback,
		Timeout:         c.cfg.ConnectTimeout,
	}
	sshClient, err := ssh.Dial("tcp", sshAddr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}
	c.sshClient = sshClient
	return &sshDialer{
		client:     sshClient,
		useTLS:     c.cfg.UseTLS,
		serverName: c.cfg.Server,
		timeout:    c.cfg.ConnectTimeout,
	}, nil
}

func (c *Client) sshAuthMethods() ([]ssh.AuthMethod, error) {
	var authMethods []ssh.AuthMethod
	if c.cfg.SSHAgentAuth && c.cfg.SSHAgentConn != nil {
		c.agentConn = c.cfg.SSHAgentConn
		ag := agent.NewClient(c.cfg.SSHAgentConn)
		signers, err := ag.Signers()
		if err == nil && len(signers) > 0 {
			authMethods = append(authMethods, ssh.PublicKeys(signers...))
		}
	}
	if len(c.cfg.SSHPrivateKey) > 0 {
		var signer ssh.Signer
		var err error
		if c.cfg.SSHPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(c.cfg.SSHPrivateKey, []byte(c.cfg.SSHPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(c.cfg.SSHPrivateKey)
		}
		if err != nil {
			return nil, fmt.Errorf("parsing SSH private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if c.cfg.SSHPassword != "" {
		authMethods = append(authMethods, ssh.Password(c.cfg.SSHPassword))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication method available")
	}
	return authMethods, nil
}

func (c *Client) emitRooms() {
	rooms := c.Rooms()
	c.emit(Event{Type: "rooms", Target: ServerTarget, Rooms: rooms, Timestamp: time.Now().UnixMilli()})
}

func (c *Client) rememberRoomMembership(room string, joined bool) {
	room = strings.TrimSpace(room)
	if room == "" {
		return
	}

	c.mu.Lock()
	position := -1
	changed := false
	for i, existing := range c.roomNames {
		if strings.EqualFold(existing, room) {
			position = i
			break
		}
	}
	if joined && position == -1 {
		c.roomNames = append(c.roomNames, room)
		changed = true
	}
	if !joined && position >= 0 {
		c.roomNames = append(c.roomNames[:position], c.roomNames[position+1:]...)
		changed = true
	}
	names := append([]string{}, c.roomNames...)
	c.mu.Unlock()

	if changed && c.cfg.OnRoomsChanged != nil {
		c.cfg.OnRoomsChanged(names)
	}
}

func (c *Client) emitUsers(room string) {
	if room == "" {
		return
	}
	c.emit(Event{Type: "users", Target: room, Users: c.Users(room), Timestamp: time.Now().UnixMilli()})
}

func (c *Client) emitUsersForAll() {
	for _, room := range c.Rooms() {
		c.emitUsers(room.Name)
	}
}

func (c *Client) emitLocalMessage(eventType string, target string, text string) {
	c.emit(Event{Type: eventType, Target: target, From: c.client.GetNick(), Text: text, Timestamp: time.Now().UnixMilli()})
}

func (c *Client) emit(event Event) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	if event.Target == "" {
		event.Target = ServerTarget
	}
	if c.cfg.OnEvent != nil {
		c.cfg.OnEvent(event)
	}
}

type sshDialer struct {
	client     *ssh.Client
	useTLS     bool
	serverName string
	timeout    time.Duration
}

func (d *sshDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := d.client.Dial(network, address)
	if err != nil {
		return nil, err
	}
	if !d.useTLS {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: d.serverName,
	})
	if d.timeout > 0 {
		_ = tlsConn.SetDeadline(time.Now().Add(d.timeout))
	}
	if err := tlsConn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("IRC TLS handshake failed: %w", err)
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return tlsConn, nil
}

func eventTime(e girc.Event) int64 {
	if e.Timestamp.IsZero() {
		return time.Now().UnixMilli()
	}
	return e.Timestamp.UnixMilli()
}

func eventRoom(e girc.Event) string {
	if len(e.Params) == 0 {
		return ""
	}
	return e.Params[0]
}

func sourceName(e girc.Event) string {
	if e.Source == nil {
		return ""
	}
	return e.Source.Name
}

func splitFirstArg(input string) (first string, rest string, ok bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", "", false
	}
	first = fields[0]
	rest = strings.TrimSpace(input[len(first):])
	return first, rest, true
}

func splitNames(input string) []string {
	fields := strings.Fields(input)
	names := make([]string, 0, len(fields))
	for _, name := range fields {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
