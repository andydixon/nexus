// Package sessions orchestrates multiple simultaneous SSH, Telnet, RAW TCP,
// and GitHub sessions. It coordinates between protocol clients, the storage
// layer, the secret store, and the Wails runtime for event emission.
package sessions

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"

	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/githubclient"
	"github.com/andydixon/nexus/internal/ircclient"
	"github.com/andydixon/nexus/internal/localshell"
	"github.com/andydixon/nexus/internal/models"
	"github.com/andydixon/nexus/internal/rawclient"
	"github.com/andydixon/nexus/internal/recording"
	"github.com/andydixon/nexus/internal/secrets"
	"github.com/andydixon/nexus/internal/sftpclient"
	"github.com/andydixon/nexus/internal/sshclient"
	"github.com/andydixon/nexus/internal/storage"
	"github.com/andydixon/nexus/internal/telnetclient"
	"github.com/andydixon/nexus/internal/vncclient"
)

const maxTunnelPrivateKeyBytes = 1 << 20

// Session wraps a protocol client with metadata and state.
type Session struct {
	Info           models.SessionInfo
	closed         bool // explicitly removed from the manager
	sshClient      *sshclient.Client
	telnetClient   *telnetclient.Client
	rawClient      *rawclient.Client
	githubClient   *githubclient.Client
	githubCancel   context.CancelFunc // stops the auto-refresh goroutine
	vncClient      *vncclient.Client  // VNC proxy client, nil for non-VNC sessions
	ircClient      *ircclient.Client  // IRC client, nil for non-IRC sessions
	localClient    *localshell.Client // local shell PTY, nil for non-local sessions
	localShell     string             // configured shell path, retained for restart
	localCwd       string             // last known local working directory
	sftpClient     *sftpclient.Client // SFTP subsystem, nil until requested
	profile        *models.Profile
	recorder       *recording.Recorder // active recording, nil if not recording
	reconnectCount int                 // number of reconnection attempts so far
	cancelReconn   context.CancelFunc  // cancels an in-progress reconnect backoff

	// Latest terminal geometry received before a PTY-capable client is attached.
	// The frontend can know the correct size before SSH/Telnet/local setup finishes.
	pendingResizeCols int
	pendingResizeRows int

	// Login script support — temporary buffer for expect-style matching.
	loginBuf   []byte
	loginBufMu sync.Mutex
	loginDone  chan struct{} // closed when login script completes or is abandoned
}

// Manager handles the lifecycle of all active sessions.
// It coordinates between protocol clients, the storage layer,
// the secret store, and the Wails runtime for event emission.
type Manager struct {
	mu                        sync.RWMutex
	sessions                  map[string]*Session
	store                     *storage.Store
	secretStore               *secrets.SecretStore
	keychainStore             *secrets.KeychainStore
	auditLog                  *audit.Logger
	recordStore               *recording.Store
	ctx                       context.Context // Wails app context for event emission
	eventSink                 func(string, interface{})
	hostKeyMu                 sync.Mutex
	hostKeyChans              map[string]chan bool
	hostKeyListenerRegistered bool
	secretMu                  sync.Mutex
	secretChans               map[string]chan string
}

// NewManager creates a Manager wired to the given backing services.
func NewManager(store *storage.Store, secretStore *secrets.SecretStore, keychainStore *secrets.KeychainStore, auditLog *audit.Logger, recordStore *recording.Store) *Manager {
	return &Manager{
		sessions:      make(map[string]*Session),
		store:         store,
		secretStore:   secretStore,
		keychainStore: keychainStore,
		auditLog:      auditLog,
		recordStore:   recordStore,
	}
}

// SetContext stores the Wails runtime context for event emission.
func (m *Manager) SetContext(ctx context.Context) {
	m.ctx = ctx
	m.registerHostKeyResponseListener()
}

// SetEventSink registers an alternate UI event sink for non-Wails hosts.
// Wails remains supported through SetContext; this hook is used by the VS Code
// bridge so existing protocol/session code can be reused without a webview
// runtime context.
func (m *Manager) SetEventSink(sink func(string, interface{})) {
	m.mu.Lock()
	m.eventSink = sink
	m.mu.Unlock()
}

// Connect creates a new session from a profile and connects it.
// The getSecret callback is used to prompt the user for credentials when
// required. Returns the session ID on success.
func (m *Manager) Connect(profile *models.Profile, getSecret func(prompt string) (string, error)) (string, error) {
	sessionID := uuid.New().String()
	now := time.Now()

	info := models.SessionInfo{
		ID:          sessionID,
		ProfileID:   profile.ID,
		ProfileName: profile.DisplayName,
		Protocol:    profile.Protocol,
		Host:        profile.Host,
		Port:        profile.Port,
		State:       models.SessionConnecting,
		TabName:     m.createTabName(profile),
		ConnectedAt: now,
	}

	sess := &Session{
		Info:    info,
		profile: profile,
	}

	m.mu.Lock()
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionConnecting),
	})

	// Perform the actual protocol handshake in a goroutine so we do not
	// block the Wails event loop — secret prompts require the UI thread.
	go func() {
		var err error
		switch profile.Protocol {
		case models.ProtocolSSH:
			err = m.connectSSH(sess, getSecret)
		case models.ProtocolTelnet:
			err = m.connectTelnet(sess)
		case models.ProtocolRAW:
			err = m.connectRAW(sess)
		case models.ProtocolGitHub:
			err = m.connectGitHub(sess, getSecret)
		case models.ProtocolVNC:
			err = m.connectVNC(sess, getSecret)
		case models.ProtocolIRC:
			err = m.connectIRC(sess, getSecret)
		default:
			err = fmt.Errorf("unsupported protocol %q", profile.Protocol)
		}

		if err != nil {
			m.mu.Lock()
			if current, ok := m.sessions[sessionID]; !ok || current != sess || sess.closed {
				m.mu.Unlock()
				m.closeSession(sess)
				return
			}
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sessionID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
			return
		}

		m.mu.Lock()
		if current, ok := m.sessions[sessionID]; !ok || current != sess || sess.closed {
			m.mu.Unlock()
			m.closeSession(sess)
			return
		}
		sess.Info.State = models.SessionConnected
		sess.Info.ConnectedAt = time.Now()
		m.mu.Unlock()

		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionConnected),
		})

		if m.auditLog != nil {
			m.auditLog.LogConnect(profile.ID, sessionID, string(profile.Protocol), profile.Host)
		}
	}()

	return sessionID, nil
}

// ConnectLocal creates a local shell session without a saved profile.
// The shell path is resolved from the given parameter or falls back to the system default.
func (m *Manager) ConnectLocal(shell string) (string, error) {
	sessionID := uuid.New().String()
	now := time.Now()

	info := models.SessionInfo{
		ID:          sessionID,
		ProfileID:   "",
		ProfileName: "Local Machine",
		Protocol:    models.ProtocolLocal,
		Host:        "localhost",
		Port:        0,
		State:       models.SessionConnecting,
		TabName:     "Local",
		ConnectedAt: now,
	}

	sess := &Session{
		Info:       info,
		localShell: shell,
	}

	m.mu.Lock()
	m.sessions[sessionID] = sess
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionConnecting),
	})

	go m.startLocalSession(sess, 80, 24)

	return sessionID, nil
}

// RestartLocal starts a replacement shell inside an exited local session.
// Keeping the session ID lets the frontend retain the tab and terminal buffer.
func (m *Manager) RestartLocal(sessionID string, cols, rows int) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.Info.Protocol != models.ProtocolLocal {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q is not local", sessionID)
	}
	if sess.localClient != nil || sess.Info.State == models.SessionConnecting || sess.Info.State == models.SessionConnected {
		m.mu.Unlock()
		return fmt.Errorf("sessions: local session %q is still running", sessionID)
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	sess.Info.State = models.SessionConnecting
	sess.Info.Error = ""
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionConnecting),
	})
	go m.startLocalSession(sess, cols, rows)
	return nil
}

func (m *Manager) startLocalSession(sess *Session, cols, rows int) {
	m.mu.RLock()
	sessionID := sess.Info.ID
	shell := sess.localShell
	startDir := sess.localCwd
	m.mu.RUnlock()
	var client *localshell.Client
	client = localshell.NewClient(localshell.Config{
		Shell:    shell,
		StartDir: startDir,
		Cols:     terminalDimension(cols, 80),
		Rows:     terminalDimension(rows, 24),
		OnData: func(data []byte) {
			m.emitEvent("session:data", map[string]interface{}{
				"sessionID": sessionID,
				"data":      base64.StdEncoding.EncodeToString(data),
			})
			m.mu.RLock()
			rec := sess.recorder
			m.mu.RUnlock()
			if rec != nil {
				rec.WriteOutput(data)
			}
		},
		OnError: func(err error) {
			m.mu.Lock()
			if sess.localClient != client {
				m.mu.Unlock()
				return
			}
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sessionID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		},
		OnClose: func() {
			m.handleLocalSessionClosed(sessionID, client)
		},
	})

	// Publish the client before Start so a resize arriving during process
	// creation is retained by Client.Resize and used for the initial PTY size.
	m.mu.Lock()
	if sess.closed || sess.Info.State != models.SessionConnecting {
		m.mu.Unlock()
		return
	}
	sess.localClient = client
	pendingCols, pendingRows := sess.consumePendingResizeLocked()
	m.mu.Unlock()
	applyLocalResize(client, pendingCols, pendingRows)

	if err := client.Start(); err != nil {
		m.mu.Lock()
		emitError := false
		if sess.localClient == client {
			sess.localClient = nil
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			emitError = true
		}
		m.mu.Unlock()

		if emitError {
			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sessionID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		}
		return
	}

	m.mu.Lock()
	if sess.closed || sess.localClient != client || sess.Info.State != models.SessionConnecting {
		m.mu.Unlock()
		client.Close()
		return
	}
	sess.Info.State = models.SessionConnected
	sess.Info.ConnectedAt = time.Now()
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionConnected),
	})
	if m.auditLog != nil {
		m.auditLog.LogConnect("", sessionID, "local", "localhost")
	}
}

func (m *Manager) handleLocalSessionClosed(sessionID string, client *localshell.Client) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok || sess.localClient != client {
		m.mu.Unlock()
		return
	}
	sess.localClient = nil
	if cwd := client.WorkingDirectory(); cwd != "" {
		sess.localCwd = cwd
	}
	m.mu.Unlock()

	m.handleSessionClosed(sessionID)
}

func terminalDimension(value int, fallback uint16) uint16 {
	if value <= 0 {
		return fallback
	}
	if value > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(value)
}

// Disconnect closes a specific session.
func (m *Manager) Disconnect(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	// Explicit disconnect means the tab/session is gone, not merely offline.
	// Detach it before closing clients so their onClose callbacks cannot start
	// auto-reconnect work or retain the session in the manager indefinitely.
	delete(m.sessions, sessionID)
	sess.closed = true
	m.mu.Unlock()

	m.closeSession(sess)
	if m.auditLog != nil {
		m.auditLog.LogDisconnect(sessionID)
	}
	return nil
}

// Reconnect closes and re-establishes a session using its original profile.
func (m *Manager) Reconnect(sessionID string, getSecret func(prompt string) (string, error)) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	profile := sess.profile
	tabName := sess.Info.TabName
	pinned := sess.Info.Pinned
	m.mu.RUnlock()

	// Close the existing session.
	m.closeSession(sess)

	// Remove the old entry.
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	// Reconnect with the same profile.
	newID, err := m.Connect(profile, getSecret)
	if err != nil {
		return err
	}

	// Preserve tab name and pin state.
	m.mu.Lock()
	if newSess, ok := m.sessions[newID]; ok {
		newSess.Info.TabName = tabName
		newSess.Info.Pinned = pinned
	}
	m.mu.Unlock()

	return nil
}

// WriteToSession sends data to a session's input.
func (m *Manager) WriteToSession(sessionID string, data []byte) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	rec := sess.recorder
	sshClient := sess.sshClient
	telnetClient := sess.telnetClient
	rawClient := sess.rawClient
	localClient := sess.localClient
	m.mu.RUnlock()

	// Record input if recording is active.
	if rec != nil {
		rec.WriteInput(data)
	}

	switch {
	case sshClient != nil:
		return sshClient.Write(data)
	case telnetClient != nil:
		return telnetClient.Write(data)
	case rawClient != nil:
		return rawClient.Write(data)
	case localClient != nil:
		return localClient.Write(data)
	}
	return fmt.Errorf("sessions: no active client for session %q", sessionID)
}

// ResizeSession sends a terminal resize to a session.
func (m *Manager) ResizeSession(sessionID string, cols, rows int) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}

	sshClient := sess.sshClient
	telnetClient := sess.telnetClient
	localClient := sess.localClient
	isRaw := sess.rawClient != nil || (sess.profile != nil && sess.profile.Protocol == models.ProtocolRAW)
	if sshClient == nil && telnetClient == nil && localClient == nil && !isRaw {
		sess.pendingResizeCols = cols
		sess.pendingResizeRows = rows
	}
	m.mu.Unlock()

	switch {
	case sshClient != nil:
		return sshClient.Resize(cols, rows)
	case telnetClient != nil:
		return telnetClient.Resize(cols, rows)
	case localClient != nil:
		return localClient.Resize(terminalDimension(cols, 80), terminalDimension(rows, 24))
	}
	// RAW sessions have no concept of terminal size.
	return nil
}

// consumePendingResizeLocked returns and clears the latest pre-connect geometry.
// m.mu must be held by the caller.
func (sess *Session) consumePendingResizeLocked() (int, int) {
	cols := sess.pendingResizeCols
	rows := sess.pendingResizeRows
	sess.pendingResizeCols = 0
	sess.pendingResizeRows = 0
	return cols, rows
}

func applySSHResize(client *sshclient.Client, cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	_ = client.Resize(cols, rows)
}

func applyTelnetResize(client *telnetclient.Client, cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	_ = client.Resize(cols, rows)
}

func applyLocalResize(client *localshell.Client, cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	_ = client.Resize(terminalDimension(cols, 80), terminalDimension(rows, 24))
}

// GetSessionCwd returns a best-effort working directory for SSH sessions.
func (m *Manager) GetSessionCwd(sessionID string) (string, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		return "", fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	cwd, err := sess.sshClient.RunCommand("pwd")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(cwd), nil
}

// GetSession returns session info by ID.
func (m *Manager) GetSession(sessionID string) (*models.SessionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	info := sess.Info
	return &info, nil
}

// GetSessionInfo returns session metadata or nil if the session does not exist.
func (m *Manager) GetSessionInfo(sessionID string) *models.SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil
	}
	info := sess.Info
	return &info
}

// ListSessions returns all active session info.
func (m *Manager) ListSessions() []models.SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]models.SessionInfo, 0, len(m.sessions))
	for _, sess := range m.sessions {
		result = append(result, sess.Info)
	}
	return result
}

// RenameSession changes a session's tab name.
func (m *Manager) RenameSession(sessionID string, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	sess.Info.TabName = name
	return nil
}

// PinSession toggles pin status.
func (m *Manager) PinSession(sessionID string, pinned bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	sess.Info.Pinned = pinned
	return nil
}

// CloseAll closes every active session.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sess.closed = true
		sessions = append(sessions, sess)
	}
	// Detach all sessions before closing protocol clients. Close callbacks can
	// otherwise schedule reconnects while the application is shutting down.
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, sess := range sessions {
		m.closeSession(sess)
	}
}

// GetSessionHistory returns the RAW session history (RAW protocol only).
func (m *Manager) GetSessionHistory(sessionID string) ([]rawclient.HistoryEntry, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return nil, fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	return sess.rawClient.GetHistory(), nil
}

// ClearSessionHistory clears RAW session history.
func (m *Manager) ClearSessionHistory(sessionID string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	sess.rawClient.ClearHistory()
	return nil
}

// SaveSessionCapture saves RAW session capture to file.
func (m *Manager) SaveSessionCapture(sessionID string, path string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	return sess.rawClient.SaveCapture(path)
}

// SendBreak sends a Telnet break signal (Telnet only).
func (m *Manager) SendBreak(sessionID string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.telnetClient == nil {
		return fmt.Errorf("sessions: session %q is not a Telnet connection", sessionID)
	}
	return sess.telnetClient.SendBreak()
}

// IRCHandleInput sends a user-entered composer line to an IRC session.
func (m *Manager) IRCHandleInput(sessionID string, target string, input string) error {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return err
	}
	return client.HandleInput(target, input)
}

// IRCJoin joins an IRC room.
func (m *Manager) IRCJoin(sessionID string, room string) error {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return err
	}
	return client.Join(room)
}

// IRCPart leaves an IRC room.
func (m *Manager) IRCPart(sessionID string, room string) error {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return err
	}
	return client.Part(room)
}

// IRCListRooms requests a server-side IRC room list.
func (m *Manager) IRCListRooms(sessionID string) error {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return err
	}
	return client.ListRooms()
}

// IRCRooms returns currently joined IRC rooms.
func (m *Manager) IRCRooms(sessionID string) ([]ircclient.RoomInfo, error) {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return nil, err
	}
	return client.Rooms(), nil
}

// IRCUsers returns known users in an IRC room.
func (m *Manager) IRCUsers(sessionID string, room string) ([]string, error) {
	client, err := m.getIRC(sessionID)
	if err != nil {
		return nil, err
	}
	return client.Users(room), nil
}

func (m *Manager) getIRC(sessionID string) (*ircclient.Client, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.ircClient == nil {
		return nil, fmt.Errorf("sessions: session %q is not an IRC connection", sessionID)
	}
	return sess.ircClient, nil
}

// WriteSessionText sends text with line ending via the RAW client.
func (m *Manager) WriteSessionText(sessionID string, text string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	return sess.rawClient.WriteText(text)
}

// WriteSessionHex sends hex-encoded data via the RAW client.
func (m *Manager) WriteSessionHex(sessionID string, hexStr string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	return sess.rawClient.WriteHex(hexStr)
}

// WriteSessionEscaped sends text with C-style escapes via the RAW client.
func (m *Manager) WriteSessionEscaped(sessionID string, text string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.rawClient == nil {
		return fmt.Errorf("sessions: session %q is not a RAW connection", sessionID)
	}
	return sess.rawClient.WriteEscaped(text)
}

// ---------------------------------------------------------------------------
// Internal: protocol-specific connection logic
// ---------------------------------------------------------------------------

// connectSSH creates an SSH client, sets callbacks, and connects.
func (m *Manager) connectSSH(sess *Session, getSecret func(prompt string) (string, error)) error {
	profile := sess.profile

	client := sshclient.NewClient(profile)
	client.SetCallbacks(
		// onData — forward terminal output to the frontend.
		func(data []byte) {
			m.emitEvent("session:data", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"data":      base64.StdEncoding.EncodeToString(data),
			})
			// Write to active recording if present.
			m.mu.RLock()
			rec := sess.recorder
			m.mu.RUnlock()
			if rec != nil {
				rec.WriteOutput(data)
			}
			// Feed data to the login script buffer if active.
			sess.loginBufMu.Lock()
			if sess.loginDone != nil {
				select {
				case <-sess.loginDone:
					// Login script finished — stop buffering.
				default:
					sess.loginBuf = append(sess.loginBuf, data...)
					if len(sess.loginBuf) > 16384 {
						sess.loginBuf = sess.loginBuf[len(sess.loginBuf)-16384:]
					}
				}
			}
			sess.loginBufMu.Unlock()
		},
		// onError — report connection errors to the frontend.
		func(err error) {
			m.mu.Lock()
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		},
		// onClose — handle remote session closure.
		func() {
			m.handleSessionClosed(sess.Info.ID)
		},
	)

	// Build credential callbacks that respect the profile's secret mode.
	sshConf := profile.SSH
	getPassword := m.buildSecretCallback(profile.ID, "password", sshConf.SecretMode, getSecret)
	getPassphrase := m.buildSecretCallback(profile.ID, "passphrase", sshConf.KeySecretMode, getSecret)

	// Resolve TOTP secret from vault if not already inline.
	if sshConf.TOTPSecret == "" {
		totpKey := fmt.Sprintf("profile:%s:totp", profile.ID)
		if m.keychainStore != nil && m.keychainStore.Available() {
			if val, err := m.keychainStore.GetSecret(totpKey); err == nil && val != "" {
				sshConf.TOTPSecret = val
			}
		}
		if sshConf.TOTPSecret == "" && m.secretStore != nil && m.secretStore.IsUnlocked() {
			if val, err := m.secretStore.GetSecret(totpKey); err == nil && val != "" {
				sshConf.TOTPSecret = val
			}
		}
	}

	if err := client.ConnectWithHostKeyCallback(m.hostKeyCallback(sess.Info.ID, sshConf.HostKeyPolicy), getPassword, getPassphrase); err != nil {
		return err
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		_ = client.Close()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.sshClient = client
	cols, rows := sess.consumePendingResizeLocked()
	m.mu.Unlock()
	applySSHResize(client, cols, rows)

	// Start any port-forwarding rules configured in the profile.
	if sshConf != nil && len(sshConf.PortForwards) > 0 {
		fwdMgr := client.Forwards()
		if fwdMgr != nil {
			for _, pf := range sshConf.PortForwards {
				if !pf.Enabled {
					continue
				}
				if err := fwdMgr.StartForward(pf); err != nil {
					// Non-fatal — log and notify but don't abort the session.
					m.emitEvent("session:forward:error", map[string]interface{}{
						"sessionID": sess.Info.ID,
						"forwardID": pf.ID,
						"label":     pf.Label,
						"error":     err.Error(),
					})
				} else {
					m.emitEvent("session:forward:started", map[string]interface{}{
						"sessionID": sess.Info.ID,
						"forward":   pf,
					})
				}
			}
		}
	}

	// Execute login scripts if configured.
	if sshConf != nil && len(sshConf.LoginScripts) > 0 {
		go m.executeLoginScripts(sess, sshConf.LoginScripts)
	}

	return nil
}

// connectTelnet creates a Telnet client, sets callbacks, and connects.
func (m *Manager) connectTelnet(sess *Session) error {
	profile := sess.profile

	client := telnetclient.NewClient(profile)
	client.SetCallbacks(
		// onData
		func(data []byte) {
			m.emitEvent("session:data", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"data":      base64.StdEncoding.EncodeToString(data),
			})
			// Write to active recording if present.
			m.mu.RLock()
			rec := sess.recorder
			m.mu.RUnlock()
			if rec != nil {
				rec.WriteOutput(data)
			}
			// Feed data to the login script buffer if active.
			sess.loginBufMu.Lock()
			if sess.loginDone != nil {
				select {
				case <-sess.loginDone:
				default:
					sess.loginBuf = append(sess.loginBuf, data...)
					if len(sess.loginBuf) > 16384 {
						sess.loginBuf = sess.loginBuf[len(sess.loginBuf)-16384:]
					}
				}
			}
			sess.loginBufMu.Unlock()
		},
		// onError
		func(err error) {
			m.mu.Lock()
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		},
		// onClose
		func() {
			m.handleSessionClosed(sess.Info.ID)
		},
		// onDebug
		func(msg string) {
			m.emitEvent("session:debug", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"message":   msg,
			})
		},
	)

	if err := client.Connect(); err != nil {
		return err
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		_ = client.Close()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.telnetClient = client
	cols, rows := sess.consumePendingResizeLocked()
	m.mu.Unlock()
	applyTelnetResize(client, cols, rows)

	// Execute login scripts if configured.
	telConf := profile.Telnet
	if telConf != nil && len(telConf.LoginScripts) > 0 {
		go m.executeLoginScripts(sess, telConf.LoginScripts)
	}

	return nil
}

// connectRAW creates a RAW TCP client, sets callbacks, and connects.
func (m *Manager) connectRAW(sess *Session) error {
	profile := sess.profile

	client := rawclient.NewClient(profile)
	client.SetCallbacks(
		// onData — includes direction and timestamp for RAW protocol.
		func(data []byte, direction string, timestamp time.Time) {
			m.emitEvent("session:raw:data", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"data":      base64.StdEncoding.EncodeToString(data),
				"direction": direction,
				"timestamp": timestamp.UnixMilli(),
			})
		},
		// onError
		func(err error) {
			m.mu.Lock()
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		},
		// onClose
		func() {
			m.handleSessionClosed(sess.Info.ID)
		},
	)

	if err := client.Connect(); err != nil {
		return err
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		_ = client.Close()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.rawClient = client
	m.mu.Unlock()
	return nil
}

// connectGitHub validates the token and performs the initial repository fetch.
// It also starts a background goroutine for periodic refresh.
func (m *Manager) connectGitHub(sess *Session, getSecret func(prompt string) (string, error)) error {
	profile := sess.profile
	ghConf := profile.GitHub
	if ghConf == nil {
		return fmt.Errorf("GitHub configuration missing on profile %q", profile.ID)
	}

	// Resolve the access token using the standard secret flow.
	getToken := m.buildSecretCallback(profile.ID, "github-token", ghConf.SecretMode, getSecret)
	token, err := getToken()
	if err != nil {
		return fmt.Errorf("obtaining GitHub token: %w", err)
	}
	if token == "" {
		return fmt.Errorf("GitHub token cannot be empty")
	}

	client := githubclient.New(ghConf.BaseURL, token)

	// Validate the token.
	login, err := client.ValidateToken(context.Background())
	if err != nil {
		if m.isGitHubAuthError(err) && getSecret != nil {
			retryToken, retryErr := m.promptAndStoreSecret(
				profile.ID,
				"github-token",
				ghConf.SecretMode,
				getSecret,
				"GitHub token did not work. Please enter a valid token:",
			)
			if retryErr == nil && retryToken != "" {
				client = githubclient.New(ghConf.BaseURL, retryToken)
				login, err = client.ValidateToken(context.Background())
			}
		}
		if err != nil {
			return fmt.Errorf("GitHub authentication failed: %w", err)
		}
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.githubClient = client
	m.mu.Unlock()

	// Perform the initial fetch.
	m.emitEvent("session:github:status", map[string]interface{}{
		"sessionID": sess.Info.ID,
		"status":    "Loading repositories...",
	})
	repos, err := client.FetchReposWithProgress(context.Background(), func(done int, total int) {
		if total <= 0 {
			return
		}
		m.emitEvent("session:github:status", map[string]interface{}{
			"sessionID": sess.Info.ID,
			"status":    fmt.Sprintf("Loading commit info (%d/%d)", done, total),
		})
	})
	if err != nil {
		return fmt.Errorf("fetching repositories: %w", err)
	}

	// Emit the initial repo list.
	m.emitEvent("session:github:repos", map[string]interface{}{
		"sessionID": sess.Info.ID,
		"login":     login,
		"repos":     repos,
	})

	// Start the periodic refresh goroutine.
	interval := time.Duration(ghConf.RefreshIntervalSecs) * time.Second
	if interval < 60*time.Second {
		interval = 5 * time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.githubCancel = cancel
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.emitEvent("session:github:status", map[string]interface{}{
					"sessionID": sess.Info.ID,
					"status":    "Refreshing repositories...",
				})
				updated, fetchErr := client.FetchReposWithProgress(ctx, func(done int, total int) {
					if total <= 0 {
						return
					}
					m.emitEvent("session:github:status", map[string]interface{}{
						"sessionID": sess.Info.ID,
						"status":    fmt.Sprintf("Loading commit info (%d/%d)", done, total),
					})
				})
				if fetchErr != nil {
					m.emitEvent("session:github:error", map[string]interface{}{
						"sessionID": sess.Info.ID,
						"error":     fetchErr.Error(),
					})
					continue
				}
				m.emitEvent("session:github:repos", map[string]interface{}{
					"sessionID": sess.Info.ID,
					"login":     login,
					"repos":     updated,
				})
			}
		}
	}()

	return nil
}

// FetchGitHubRepos triggers an on-demand refresh of GitHub repos for a session.
func (m *Manager) FetchGitHubRepos(sessionID string) ([]models.GitHubRepo, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.githubClient == nil {
		return nil, fmt.Errorf("sessions: session %q is not a GitHub connection", sessionID)
	}

	repos, err := sess.githubClient.FetchRepos(context.Background())
	if err != nil {
		return nil, fmt.Errorf("fetching repos: %w", err)
	}

	m.emitEvent("session:github:repos", map[string]interface{}{
		"sessionID": sessionID,
		"repos":     repos,
	})

	return repos, nil
}

// DownloadGitHubArchive downloads a repository zip archive for a GitHub session.
func (m *Manager) DownloadGitHubArchive(sessionID string, fullName string, ref string, destPath string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.githubClient == nil {
		return fmt.Errorf("sessions: session %q is not a GitHub connection", sessionID)
	}

	if ref == "" {
		ref = "HEAD"
	}

	if err := sess.githubClient.DownloadRepoArchive(context.Background(), fullName, ref, destPath); err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}
	return nil
}

// connectVNC establishes a VNC connection (with optional SSH tunnel) and
// emits the local WebSocket proxy URL to the frontend.
func (m *Manager) connectVNC(sess *Session, getSecret func(prompt string) (string, error)) error {
	profile := sess.profile
	vncConf := profile.VNC
	if vncConf == nil {
		return fmt.Errorf("VNC configuration missing")
	}

	// Fall back to the profile-level host if VNC-specific host is empty.
	vncHost := vncConf.VNCHost
	if vncHost == "" {
		vncHost = profile.Host
	}

	cfg := vncclient.Config{
		VNCHost: vncHost,
		VNCPort: vncConf.VNCPort,
		OnError: func(err error) {
			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sess.Info.ID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})
		},
	}

	// SSH tunnel configuration
	if vncConf.SSHTunnel {
		cfg.SSHTunnel = true
		cfg.SSHHost = vncConf.SSHHost
		cfg.SSHPort = vncConf.SSHPort
		cfg.SSHUsername = vncConf.SSHUsername
		cfg.SSHAuthMethod = vncConf.SSHAuthMethod
		cfg.SSHAgentAuth = vncConf.SSHAgentAuth
		cfg.SSHHostKeyCallback = m.hostKeyCallback(sess.Info.ID, models.HostKeyAsk)

		// Resolve SSH credentials
		switch vncConf.SSHAuthMethod {
		case "password":
			getPass := m.buildSecretCallback(profile.ID, "vnc-ssh-password", vncConf.SSHSecretMode, getSecret)
			password, err := getPass()
			if err != nil {
				return fmt.Errorf("SSH password: %w", err)
			}
			cfg.SSHPassword = password
		case "publickey":
			if vncConf.SSHPrivateKey != "" {
				info, err := os.Stat(vncConf.SSHPrivateKey)
				if err != nil {
					return fmt.Errorf("reading SSH key metadata: %w", err)
				}
				if info.IsDir() {
					return fmt.Errorf("SSH key path is a directory")
				}
				if info.Size() > maxTunnelPrivateKeyBytes {
					return fmt.Errorf("SSH key is too large: %d bytes exceeds %d bytes", info.Size(), maxTunnelPrivateKeyBytes)
				}
				keyData, err := os.ReadFile(vncConf.SSHPrivateKey)
				if err != nil {
					return fmt.Errorf("reading SSH key: %w", err)
				}
				cfg.SSHPrivateKey = keyData
				// Get passphrase if needed
				getPassphrase := m.buildSecretCallback(profile.ID, "vnc-ssh-passphrase", vncConf.SSHSecretMode, getSecret)
				passphrase, _ := getPassphrase()
				cfg.SSHPassphrase = passphrase
			}
		}

		// SSH agent support
		if vncConf.SSHAgentAuth {
			socketPath := os.Getenv("SSH_AUTH_SOCK")
			if socketPath != "" {
				agentConn, err := net.Dial("unix", socketPath)
				if err == nil {
					cfg.SSHAgentConn = agentConn
				}
			}
		}
	} else {
		// Direct VNC — may need a VNC password
		getVNCPass := m.buildSecretCallback(profile.ID, "vnc-password", vncConf.SecretMode, getSecret)
		password, _ := getVNCPass()
		// VNC auth is handled by the RFB protocol in the frontend (noVNC),
		// but we store it in case we need server-side auth negotiation later.
		_ = password
	}

	client := vncclient.NewClient(cfg)
	wsURL, err := client.Connect(cfg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		client.Close()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.vncClient = client
	m.mu.Unlock()

	// Emit the WebSocket URL so the frontend can connect noVNC.
	m.emitEvent("session:vnc:ready", map[string]interface{}{
		"sessionID": sess.Info.ID,
		"wsURL":     wsURL,
	})

	return nil
}

// connectIRC establishes an IRC connection, optionally through an SSH tunnel,
// and forwards typed IRC events to the frontend.
func (m *Manager) connectIRC(sess *Session, getSecret func(prompt string) (string, error)) error {
	profile := sess.profile
	ircConf := profile.IRC
	if ircConf == nil {
		return fmt.Errorf("IRC configuration missing")
	}

	serverPass := ""
	if ircConf.ServerPasswordRequired {
		getPass := m.buildSecretCallback(profile.ID, "irc-server-password", ircConf.ServerPasswordMode, getSecret)
		password, err := getPass()
		if err != nil {
			return fmt.Errorf("IRC server password: %w", err)
		}
		serverPass = password
	}

	nickServPass := ""
	if ircConf.NickServIdentify {
		getPass := m.buildSecretCallback(profile.ID, "irc-nickserv-password", ircConf.NickServMode, getSecret)
		password, err := getPass()
		if err != nil {
			return fmt.Errorf("NickServ password: %w", err)
		}
		nickServPass = password
	}

	cfg := ircclient.Config{
		Server:             profile.Host,
		Port:               profile.Port,
		Nick:               ircConf.Nick,
		User:               profile.Username,
		RealName:           ircConf.RealName,
		UseTLS:             ircConf.UseTLS,
		ServerPass:         serverPass,
		AutoJoinRooms:      append([]string{}, ircConf.AutoJoinRooms...),
		NickServIdentify:   ircConf.NickServIdentify,
		NickServAccount:    ircConf.NickServAccount,
		NickServPassword:   nickServPass,
		ConnectTimeout:     time.Duration(profile.ConnectTimeout) * time.Second,
		OnEvent:            func(event ircclient.Event) { m.emitIRCEvent(sess.Info.ID, event) },
		OnError:            func(err error) { m.markSessionError(sess, err) },
		OnClose:            func() { m.handleSessionClosed(sess.Info.ID) },
		SSHTunnel:          ircConf.SSHTunnel,
		SSHHost:            ircConf.SSHHost,
		SSHPort:            ircConf.SSHPort,
		SSHUsername:        ircConf.SSHUsername,
		SSHAuthMethod:      ircConf.SSHAuthMethod,
		SSHAgentAuth:       ircConf.SSHAgentAuth,
		SSHHostKeyCallback: m.hostKeyCallback(sess.Info.ID, models.HostKeyAsk),
	}

	if cfg.User == "" {
		cfg.User = ircConf.Nick
	}

	if ircConf.SSHTunnel {
		switch ircConf.SSHAuthMethod {
		case "password":
			getPass := m.buildSecretCallback(profile.ID, "irc-ssh-password", ircConf.SSHSecretMode, getSecret)
			password, err := getPass()
			if err != nil {
				return fmt.Errorf("IRC SSH password: %w", err)
			}
			cfg.SSHPassword = password
		case "publickey":
			if ircConf.SSHPrivateKey != "" {
				info, err := os.Stat(ircConf.SSHPrivateKey)
				if err != nil {
					return fmt.Errorf("reading IRC SSH key metadata: %w", err)
				}
				if info.IsDir() {
					return fmt.Errorf("IRC SSH key path is a directory")
				}
				if info.Size() > maxTunnelPrivateKeyBytes {
					return fmt.Errorf("IRC SSH key is too large: %d bytes exceeds %d bytes", info.Size(), maxTunnelPrivateKeyBytes)
				}
				keyData, err := os.ReadFile(ircConf.SSHPrivateKey)
				if err != nil {
					return fmt.Errorf("reading IRC SSH key: %w", err)
				}
				cfg.SSHPrivateKey = keyData
				getPassphrase := m.buildSecretCallback(profile.ID, "irc-ssh-passphrase", ircConf.SSHSecretMode, getSecret)
				passphrase, _ := getPassphrase()
				cfg.SSHPassphrase = passphrase
			}
		}

		if ircConf.SSHAgentAuth {
			socketPath := os.Getenv("SSH_AUTH_SOCK")
			if socketPath != "" {
				agentConn, err := net.Dial("unix", socketPath)
				if err == nil {
					cfg.SSHAgentConn = agentConn
				}
			}
		}
	}

	client := ircclient.New(cfg)
	if err := client.Connect(); err != nil {
		return err
	}

	m.mu.Lock()
	if current, ok := m.sessions[sess.Info.ID]; !ok || current != sess || sess.closed {
		m.mu.Unlock()
		client.Close()
		return fmt.Errorf("session %q was closed while connecting", sess.Info.ID)
	}
	sess.ircClient = client
	m.mu.Unlock()
	return nil
}

func (m *Manager) emitIRCEvent(sessionID string, event ircclient.Event) {
	m.emitEvent("session:irc:event", map[string]interface{}{
		"sessionID": sessionID,
		"event":     event,
	})
}

func (m *Manager) markSessionError(sess *Session, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	sess.Info.State = models.SessionError
	sess.Info.Error = err.Error()
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sess.Info.ID,
		"state":     string(models.SessionError),
		"error":     err.Error(),
	})
}

// GetVNCWebSocketURL returns the local WebSocket proxy URL for a VNC session.
func (m *Manager) GetVNCWebSocketURL(sessionID string) (string, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.vncClient == nil {
		return "", fmt.Errorf("sessions: session %q is not a VNC connection", sessionID)
	}
	return sess.vncClient.WSURL(), nil
}

// ---------------------------------------------------------------------------
// Session recording
// ---------------------------------------------------------------------------

// StartRecording begins recording terminal I/O for a session.
// Returns the recording ID (same as session ID) on success.
func (m *Manager) StartRecording(sessionID string, cols, rows int) (string, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.recorder != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("sessions: session %q is already being recorded", sessionID)
	}
	m.mu.Unlock()

	if m.recordStore == nil {
		return "", fmt.Errorf("sessions: recording store not configured")
	}

	title := fmt.Sprintf("%s — %s", sess.Info.ProfileName, sess.Info.Host)
	rec, err := recording.NewRecorder(m.recordStore.Dir(), sessionID, title, cols, rows)
	if err != nil {
		return "", fmt.Errorf("sessions: failed to start recording: %w", err)
	}

	m.mu.Lock()
	sess.recorder = rec
	m.mu.Unlock()

	m.emitEvent("session:recording:started", map[string]interface{}{
		"sessionID": sessionID,
	})

	return sessionID, nil
}

// StopRecording stops an active recording for a session.
func (m *Manager) StopRecording(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	rec := sess.recorder
	sess.recorder = nil
	m.mu.Unlock()

	if rec == nil {
		return fmt.Errorf("sessions: session %q is not being recorded", sessionID)
	}

	if err := rec.Close(); err != nil {
		return fmt.Errorf("sessions: failed to stop recording: %w", err)
	}

	m.emitEvent("session:recording:stopped", map[string]interface{}{
		"sessionID": sessionID,
		"duration":  rec.Duration(),
		"events":    rec.EventCount(),
	})

	return nil
}

// IsRecording reports whether a session is currently being recorded.
func (m *Manager) IsRecording(sessionID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return false
	}
	return sess.recorder != nil
}

// ListRecordings returns metadata for all saved recordings.
func (m *Manager) ListRecordings() ([]recording.RecordingMeta, error) {
	if m.recordStore == nil {
		return nil, fmt.Errorf("sessions: recording store not configured")
	}
	return m.recordStore.List()
}

// GetRecording returns the full recording data (header + events) for playback.
func (m *Manager) GetRecording(id string) (*recording.Header, []recording.Event, error) {
	if m.recordStore == nil {
		return nil, nil, fmt.Errorf("sessions: recording store not configured")
	}
	return m.recordStore.Get(id)
}

// DeleteRecording removes a saved recording.
func (m *Manager) DeleteRecording(id string) error {
	if m.recordStore == nil {
		return fmt.Errorf("sessions: recording store not configured")
	}
	return m.recordStore.Delete(id)
}

// RecordingStorageInfo returns aggregate recording storage usage.
func (m *Manager) RecordingStorageInfo() (recording.StorageInfo, error) {
	if m.recordStore == nil {
		return recording.StorageInfo{}, fmt.Errorf("sessions: recording store not configured")
	}
	return m.recordStore.StorageInfo()
}

// CleanupRecordings applies age and total-size retention limits.
func (m *Manager) CleanupRecordings(maxAgeDays int, maxTotalBytes int64) (recording.CleanupResult, error) {
	if m.recordStore == nil {
		return recording.CleanupResult{}, fmt.Errorf("sessions: recording store not configured")
	}
	return m.recordStore.Cleanup(maxAgeDays, maxTotalBytes)
}

// ---------------------------------------------------------------------------
// SFTP file browser
// ---------------------------------------------------------------------------

// getSFTP returns or lazily creates the SFTP client for an SSH session.
func (m *Manager) getSFTP(sessionID string) (*sftpclient.Client, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	// Return existing SFTP client if already open.
	if sess.sftpClient != nil {
		c := sess.sftpClient
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	// Create a new SFTP subsystem.
	sshConn := sess.sshClient.SSHConn()
	if sshConn == nil {
		return nil, fmt.Errorf("sessions: SSH connection is not established")
	}

	sc, err := sftpclient.NewClient(sshConn)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	sess.sftpClient = sc
	m.mu.Unlock()

	return sc, nil
}

// SFTPList lists the contents of a remote directory.
func (m *Manager) SFTPList(sessionID, remotePath string) ([]sftpclient.FileEntry, error) {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return nil, err
	}
	return sc.List(remotePath)
}

// SFTPStat returns information about a remote path.
func (m *Manager) SFTPStat(sessionID, remotePath string) (*sftpclient.FileEntry, error) {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return nil, err
	}
	return sc.Stat(remotePath)
}

// SFTPMkdir creates a remote directory.
func (m *Manager) SFTPMkdir(sessionID, remotePath string) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.MkdirAll(remotePath)
}

// SFTPRemove deletes a remote file or empty directory.
func (m *Manager) SFTPRemove(sessionID, remotePath string) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.Remove(remotePath)
}

// SFTPRename moves or renames a remote file/directory.
func (m *Manager) SFTPRename(sessionID, oldPath, newPath string) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.Rename(oldPath, newPath)
}

// SFTPDownload downloads a remote file to a local path.
func (m *Manager) SFTPDownload(sessionID, remotePath, localPath string) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.Download(remotePath, localPath, func(p sftpclient.TransferProgress) {
		m.emitEvent("session:sftp:progress", map[string]interface{}{
			"sessionID":   sessionID,
			"direction":   "download",
			"transferred": p.Transferred,
			"total":       p.Total,
			"path":        p.Path,
			"done":        p.Done,
		})
	})
}

// SFTPUpload uploads a local file to a remote path.
func (m *Manager) SFTPUpload(sessionID, localPath, remotePath string) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.Upload(localPath, remotePath, func(p sftpclient.TransferProgress) {
		m.emitEvent("session:sftp:progress", map[string]interface{}{
			"sessionID":   sessionID,
			"direction":   "upload",
			"transferred": p.Transferred,
			"total":       p.Total,
			"path":        p.Path,
			"done":        p.Done,
		})
	})
}

// SFTPUploadBytes uploads in-memory data to a remote path.
func (m *Manager) SFTPUploadBytes(sessionID, remotePath string, data []byte) error {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return err
	}
	return sc.UploadBytes(remotePath, data, func(p sftpclient.TransferProgress) {
		m.emitEvent("session:sftp:progress", map[string]interface{}{
			"sessionID":   sessionID,
			"direction":   "upload",
			"transferred": p.Transferred,
			"total":       p.Total,
			"path":        p.Path,
			"done":        p.Done,
		})
	})
}

// SFTPOpenUpload opens a remote file for a caller-managed streaming upload.
func (m *Manager) SFTPOpenUpload(sessionID, remotePath string) (io.WriteCloser, error) {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return nil, err
	}
	return sc.CreateWriter(remotePath)
}

// SFTPPwd returns the current working directory on the remote host.
func (m *Manager) SFTPPwd(sessionID string) (string, error) {
	sc, err := m.getSFTP(sessionID)
	if err != nil {
		return "", err
	}
	return sc.Pwd()
}

// ---------------------------------------------------------------------------
// Port-forwarding management
// ---------------------------------------------------------------------------

// ListPortForwards returns the active port-forwarding rules for a session.
func (m *Manager) ListPortForwards(sessionID string) ([]models.PortForward, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		return nil, fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	fwdMgr := sess.sshClient.Forwards()
	if fwdMgr == nil {
		return []models.PortForward{}, nil
	}
	return fwdMgr.ActiveForwards(), nil
}

// StartPortForward activates a new forwarding tunnel on a live SSH session.
func (m *Manager) StartPortForward(sessionID string, rule models.PortForward) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		return fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	fwdMgr := sess.sshClient.Forwards()
	if fwdMgr == nil {
		return fmt.Errorf("sessions: forward manager not initialised")
	}

	if err := fwdMgr.StartForward(rule); err != nil {
		return err
	}

	m.emitEvent("session:forward:started", map[string]interface{}{
		"sessionID": sessionID,
		"forward":   rule,
	})
	return nil
}

// StopPortForward terminates an active forwarding tunnel.
func (m *Manager) StopPortForward(sessionID string, forwardID string) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		return fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	fwdMgr := sess.sshClient.Forwards()
	if fwdMgr == nil {
		return fmt.Errorf("sessions: forward manager not initialised")
	}

	if err := fwdMgr.StopForward(forwardID); err != nil {
		return err
	}

	m.emitEvent("session:forward:stopped", map[string]interface{}{
		"sessionID": sessionID,
		"forwardID": forwardID,
	})
	return nil
}

// PingSession sends a keepalive request to the server and returns the RTT in milliseconds.
func (m *Manager) PingSession(sessionID string) (int64, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return 0, fmt.Errorf("sessions: session %q not found", sessionID)
	}
	if sess.sshClient == nil {
		return 0, fmt.Errorf("sessions: session %q is not an SSH connection", sessionID)
	}

	return sess.sshClient.Ping()
}

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

func (m *Manager) hostKeyCallback(sessionID string, policy models.HostKeyPolicy) ssh.HostKeyCallback {
	if policy == "" {
		policy = models.HostKeyAsk
	}
	verifier := sshclient.NewHostKeyVerifier(m.store, policy)
	verifier.SetPromptHandler(func(host string, keyType string, fingerprint string) (bool, error) {
		return m.promptHostKey(sessionID, host, keyType, fingerprint)
	})
	return verifier.HostKeyCallback()
}

func (m *Manager) promptHostKey(sessionID string, host string, keyType string, fingerprint string) (bool, error) {
	if m.ctx == nil && m.eventSink == nil {
		return false, fmt.Errorf("host key prompt unavailable")
	}

	requestID := uuid.New().String()
	ch := make(chan bool, 1)

	m.hostKeyMu.Lock()
	if m.hostKeyChans == nil {
		m.hostKeyChans = make(map[string]chan bool)
	}
	m.hostKeyChans[requestID] = ch
	m.hostKeyMu.Unlock()
	defer func() {
		m.hostKeyMu.Lock()
		delete(m.hostKeyChans, requestID)
		m.hostKeyMu.Unlock()
	}()

	m.emitEvent("session:hostkey:verify", map[string]interface{}{
		"requestID":   requestID,
		"sessionID":   sessionID,
		"host":        host,
		"keyType":     keyType,
		"fingerprint": fingerprint,
	})

	select {
	case accepted := <-ch:
		return accepted, nil
	case <-time.After(60 * time.Second):
		return false, fmt.Errorf("host key prompt timed out waiting for user response")
	}
}

func (m *Manager) registerHostKeyResponseListener() {
	ctx := m.ctx
	if ctx == nil {
		return
	}

	m.hostKeyMu.Lock()
	if m.hostKeyChans == nil {
		m.hostKeyChans = make(map[string]chan bool)
	}
	if m.hostKeyListenerRegistered {
		m.hostKeyMu.Unlock()
		return
	}
	m.hostKeyListenerRegistered = true
	m.hostKeyMu.Unlock()

	wailsRuntime.EventsOn(ctx, "session:hostkey:response", m.handleHostKeyResponse)
}

func (m *Manager) handleHostKeyResponse(optionalData ...interface{}) {
	if len(optionalData) == 0 {
		return
	}
	data, ok := optionalData[0].(map[string]interface{})
	if !ok {
		return
	}
	requestID, _ := data["requestID"].(string)
	accepted, _ := data["accepted"].(bool)

	m.hostKeyMu.Lock()
	ch, exists := m.hostKeyChans[requestID]
	m.hostKeyMu.Unlock()

	if exists {
		select {
		case ch <- accepted:
		default:
		}
	}
}

// RespondHostKey supplies a host-key prompt response for non-Wails hosts.
func (m *Manager) RespondHostKey(requestID string, accepted bool) {
	m.hostKeyMu.Lock()
	ch, exists := m.hostKeyChans[requestID]
	m.hostKeyMu.Unlock()

	if exists {
		select {
		case ch <- accepted:
		default:
		}
	}
}

// buildSecretCallback returns a func() (string, error) that resolves a
// credential according to the profile's SecretMode:
//   - "saved": check keychain/vault first, prompt if missing, then persist
//   - "session-only": check an in-memory session cache, prompt once per session
//   - "ask-every-time" (or any other value): always prompt the user
func (m *Manager) buildSecretCallback(profileID string, secretType string, mode models.SecretMode, getSecret func(prompt string) (string, error)) func() (string, error) {
	secretKey := fmt.Sprintf("profile:%s:%s", profileID, secretType)

	// Session-local cache for "session-only" mode. The closure captures this
	// variable so it survives across multiple calls within the same connection.
	var sessionCached string
	var sessionCachedSet bool

	return func() (string, error) {
		// ── "saved" mode: try persistent stores first ────────────────────
		if mode == models.SecretModeSaved {
			// Try the OS keychain first.
			if m.keychainStore != nil {
				if val, err := m.keychainStore.GetSecret(secretKey); err == nil && val != "" {
					m.auditLog.LogSecretAccess(profileID, secretType)
					return val, nil
				}
			}

			// Try the encrypted file store.
			if m.secretStore != nil && m.secretStore.IsUnlocked() {
				if val, err := m.secretStore.GetSecret(secretKey); err == nil && val != "" {
					m.auditLog.LogSecretAccess(profileID, secretType)
					return val, nil
				}
			}
		}

		// ── "session-only" mode: return cached value if we already asked ─
		if mode == models.SecretModeSessionOnly && sessionCachedSet {
			return sessionCached, nil
		}

		// ── Prompt the user ──────────────────────────────────────────────
		if getSecret == nil {
			return "", fmt.Errorf("no %s available and no prompt callback provided", secretType)
		}

		prompt := fmt.Sprintf("Enter %s:", secretType)
		val, err := getSecret(prompt)
		if err != nil {
			return "", err
		}

		// ── Persist according to mode ────────────────────────────────────
		switch mode {
		case models.SecretModeSaved:
			// Prefer OS keychain, fall back to encrypted vault.
			stored := false
			if m.keychainStore != nil {
				if sErr := m.keychainStore.SetSecret(secretKey, val); sErr == nil {
					stored = true
				}
			}
			if !stored && m.secretStore != nil && m.secretStore.IsUnlocked() {
				_ = m.secretStore.SetSecret(secretKey, val)
			}

		case models.SecretModeSessionOnly:
			sessionCached = val
			sessionCachedSet = true
		}

		return val, nil
	}
}

// promptAndStoreSecret asks the user for a secret and stores it based on the mode.
func (m *Manager) promptAndStoreSecret(profileID string, secretType string, mode models.SecretMode, getSecret func(prompt string) (string, error), prompt string) (string, error) {
	if getSecret == nil {
		return "", fmt.Errorf("no %s available and no prompt callback provided", secretType)
	}
	val, err := getSecret(prompt)
	if err != nil {
		return "", err
	}
	if val == "" {
		return "", fmt.Errorf("%s cannot be empty", secretType)
	}

	if mode == models.SecretModeSaved {
		stored := false
		secretKey := fmt.Sprintf("profile:%s:%s", profileID, secretType)
		if m.keychainStore != nil {
			if sErr := m.keychainStore.SetSecret(secretKey, val); sErr == nil {
				stored = true
			}
		}
		if !stored && m.secretStore != nil && m.secretStore.IsUnlocked() {
			_ = m.secretStore.SetSecret(secretKey, val)
		}
	}

	return val, nil
}

// isGitHubAuthError checks whether an error indicates authentication failure.
func (m *Manager) isGitHubAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "token validation failed") ||
		strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "http 401") ||
		strings.Contains(msg, "http 403")
}

// closeSession shuts down the protocol client for a session.
func (m *Manager) closeSession(sess *Session) {
	m.mu.Lock()
	sess.closed = true
	cancelReconn := sess.cancelReconn
	recorder := sess.recorder
	sftpClient := sess.sftpClient
	sshClient := sess.sshClient
	telnetClient := sess.telnetClient
	rawClient := sess.rawClient
	githubCancel := sess.githubCancel
	vncClient := sess.vncClient
	ircClient := sess.ircClient
	localClient := sess.localClient

	// Break all retained client/callback references while holding the lifecycle
	// lock. This makes repeated close calls harmless and lets closed sessions be
	// collected even if a short-lived callback still holds the Session object.
	sess.cancelReconn = nil
	sess.recorder = nil
	sess.sftpClient = nil
	sess.sshClient = nil
	sess.telnetClient = nil
	sess.rawClient = nil
	sess.githubClient = nil
	sess.githubCancel = nil
	sess.vncClient = nil
	sess.ircClient = nil
	sess.localClient = nil
	sess.profile = nil
	sess.localShell = ""
	sess.localCwd = ""
	sess.reconnectCount = 0
	sess.pendingResizeCols = 0
	sess.pendingResizeRows = 0
	sess.Info.State = models.SessionDisconnected
	m.mu.Unlock()

	if cancelReconn != nil {
		cancelReconn()
	}
	if recorder != nil {
		_ = recorder.Close()
	}
	if sftpClient != nil {
		_ = sftpClient.Close()
	}
	if sshClient != nil {
		_ = sshClient.Close()
	}
	if telnetClient != nil {
		_ = telnetClient.Close()
	}
	if rawClient != nil {
		_ = rawClient.Close()
	}
	if githubCancel != nil {
		githubCancel()
	}
	if vncClient != nil {
		vncClient.Close()
	}
	if ircClient != nil {
		ircClient.Close()
	}
	if localClient != nil {
		localClient.Close()
	}
}

// ---------------------------------------------------------------------------
// Login script execution (expect-style send/wait)
// ---------------------------------------------------------------------------

// executeLoginScripts processes an expect-style login script sequence against
// a connected session. "wait" steps block until the specified text appears in
// the session output (with a timeout). "send" steps write text to the session
// input. The session's loginBuf/loginDone fields are used for pattern matching.
func (m *Manager) executeLoginScripts(sess *Session, steps []models.LoginScriptStep) {
	// Initialise the login buffer so the onData hook starts accumulating.
	sess.loginBufMu.Lock()
	sess.loginBuf = nil
	sess.loginDone = make(chan struct{})
	sess.loginBufMu.Unlock()

	defer func() {
		sess.loginBufMu.Lock()
		close(sess.loginDone)
		sess.loginBuf = nil
		sess.loginBufMu.Unlock()
	}()

	// Short delay to allow the shell to start and produce its initial prompt.
	time.Sleep(500 * time.Millisecond)

	for i, step := range steps {
		// Check session is still alive.
		m.mu.RLock()
		state := sess.Info.State
		m.mu.RUnlock()
		if state != models.SessionConnected {
			return
		}

		switch step.Type {
		case "wait":
			timeout := time.Duration(step.Timeout) * time.Second
			if timeout <= 0 {
				timeout = 10 * time.Second
			}
			if err := m.waitForOutput(sess, step.Value, timeout); err != nil {
				m.emitEvent("session:debug", map[string]interface{}{
					"sessionID": sess.Info.ID,
					"message":   fmt.Sprintf("login script step %d timed out waiting for %q", i+1, step.Value),
				})
				return
			}

		case "send":
			data := []byte(step.Value + "\n")
			if err := m.WriteToSession(sess.Info.ID, data); err != nil {
				m.emitEvent("session:debug", map[string]interface{}{
					"sessionID": sess.Info.ID,
					"message":   fmt.Sprintf("login script step %d failed to send: %v", i+1, err),
				})
				return
			}
			// Small inter-step delay for the remote to process.
			time.Sleep(200 * time.Millisecond)

		default:
			// Unknown step type — skip.
		}
	}
}

// waitForOutput polls the session's login buffer until the specified text
// appears or the timeout expires.
func (m *Manager) waitForOutput(sess *Session, pattern string, timeout time.Duration) error {
	if pattern == "" {
		return nil
	}

	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("timeout waiting for %q", pattern)
		case <-ticker.C:
			sess.loginBufMu.Lock()
			found := strings.Contains(string(sess.loginBuf), pattern)
			if found {
				// Clear the buffer up to the match so subsequent waits start fresh.
				idx := strings.Index(string(sess.loginBuf), pattern)
				sess.loginBuf = sess.loginBuf[idx+len(pattern):]
			}
			sess.loginBufMu.Unlock()
			if found {
				return nil
			}
		}
	}
}

// handleSessionClosed is called when a protocol client reports closure.
func (m *Manager) handleSessionClosed(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok || sess.closed {
		m.mu.Unlock()
		return
	}
	sess.Info.State = models.SessionDisconnected
	m.mu.Unlock()

	if m.auditLog != nil {
		m.auditLog.LogDisconnect(sessionID)
	}

	// Check whether auto-reconnect is enabled for this session.
	if m.shouldAutoReconnect(sess) {
		m.scheduleReconnect(sess, sessionID)
		return
	}

	m.emitEvent("session:closed", map[string]interface{}{
		"sessionID": sessionID,
	})
	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionDisconnected),
	})
}

// shouldAutoReconnect checks whether a session is configured for automatic reconnection.
func (m *Manager) shouldAutoReconnect(sess *Session) bool {
	if sess.profile == nil {
		return false
	}
	switch sess.profile.Protocol {
	case models.ProtocolSSH:
		if sess.profile.SSH != nil {
			return sess.profile.SSH.AutoReconnect
		}
	case models.ProtocolTelnet:
		if sess.profile.Telnet != nil {
			return sess.profile.Telnet.AutoReconnect
		}
	case models.ProtocolRAW:
		if sess.profile.RAW != nil {
			return sess.profile.RAW.AutoReconnect
		}
	case models.ProtocolIRC:
		if sess.profile.IRC != nil {
			return sess.profile.IRC.AutoReconnect
		}
	}
	return false
}

// maxReconnectAttempts returns the configured (or default) max retry count.
func (m *Manager) maxReconnectAttempts(sess *Session) int {
	maxTries := 5
	switch sess.profile.Protocol {
	case models.ProtocolSSH:
		if sess.profile.SSH != nil && sess.profile.SSH.MaxReconnectTries > 0 {
			maxTries = sess.profile.SSH.MaxReconnectTries
		}
	case models.ProtocolTelnet:
		if sess.profile.Telnet != nil && sess.profile.Telnet.MaxReconnectTries > 0 {
			maxTries = sess.profile.Telnet.MaxReconnectTries
		}
	case models.ProtocolRAW:
		if sess.profile.RAW != nil {
			maxTries = 5
		}
	case models.ProtocolIRC:
		if sess.profile.IRC != nil && sess.profile.IRC.MaxReconnectTries > 0 {
			maxTries = sess.profile.IRC.MaxReconnectTries
		}
	}
	return maxTries
}

// scheduleReconnect initiates an exponential-backoff reconnection loop for a session.
func (m *Manager) scheduleReconnect(sess *Session, sessionID string) {
	maxTries := m.maxReconnectAttempts(sess)

	m.mu.Lock()
	sess.reconnectCount++
	attempt := sess.reconnectCount
	if attempt > maxTries {
		// Give up — too many attempts.
		sess.Info.State = models.SessionDisconnected
		m.mu.Unlock()

		m.emitEvent("session:closed", map[string]interface{}{
			"sessionID": sessionID,
		})
		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionDisconnected),
			"error":     "auto-reconnect failed after maximum attempts",
		})
		return
	}

	sess.Info.State = models.SessionReconnecting
	sess.Info.Error = ""

	// Cancel any previous reconnect timer.
	if sess.cancelReconn != nil {
		sess.cancelReconn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancelReconn = cancel
	m.mu.Unlock()

	// Exponential backoff: 1s, 2s, 4s, 8s, 16s, capped at 30s.
	delaySec := 1 << (attempt - 1) // 2^(attempt-1)
	if delaySec > 30 {
		delaySec = 30
	}

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID":  sessionID,
		"state":      string(models.SessionReconnecting),
		"attempt":    attempt,
		"maxRetries": maxTries,
		"delaySec":   delaySec,
	})

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(delaySec) * time.Second):
		}

		// Clear old clients before reconnecting.
		m.mu.Lock()
		if current, ok := m.sessions[sessionID]; !ok || current != sess || sess.closed {
			m.mu.Unlock()
			return
		}
		sess.sshClient = nil
		sess.telnetClient = nil
		sess.rawClient = nil
		sess.ircClient = nil
		sess.sftpClient = nil
		sess.Info.State = models.SessionConnecting
		m.mu.Unlock()

		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionConnecting),
		})

		var err error
		switch sess.profile.Protocol {
		case models.ProtocolSSH:
			err = m.connectSSH(sess, m.buildInteractiveSecretCallback(sessionID))
		case models.ProtocolTelnet:
			err = m.connectTelnet(sess)
		case models.ProtocolRAW:
			err = m.connectRAW(sess)
		case models.ProtocolIRC:
			err = m.connectIRC(sess, m.buildInteractiveSecretCallback(sessionID))
		default:
			err = fmt.Errorf("unsupported protocol for reconnect: %s", sess.profile.Protocol)
		}

		if err != nil {
			m.mu.Lock()
			if current, ok := m.sessions[sessionID]; !ok || current != sess || sess.closed {
				m.mu.Unlock()
				m.closeSession(sess)
				return
			}
			sess.Info.State = models.SessionError
			sess.Info.Error = err.Error()
			m.mu.Unlock()

			m.emitEvent("session:state", map[string]interface{}{
				"sessionID": sessionID,
				"state":     string(models.SessionError),
				"error":     err.Error(),
			})

			// Schedule the next attempt.
			m.scheduleReconnect(sess, sessionID)
			return
		}

		// Success — reset the counter.
		m.mu.Lock()
		if current, ok := m.sessions[sessionID]; !ok || current != sess || sess.closed {
			m.mu.Unlock()
			m.closeSession(sess)
			return
		}
		sess.reconnectCount = 0
		sess.Info.State = models.SessionConnected
		sess.Info.ConnectedAt = time.Now()
		m.mu.Unlock()

		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionConnected),
		})
	}()
}

// buildInteractiveSecretCallback creates a getSecret callback that prompts
// the user via Wails events. Used during auto-reconnect flows where the
// original callback is no longer available.
func (m *Manager) buildInteractiveSecretCallback(sessionID string) func(prompt string) (string, error) {
	return func(prompt string) (string, error) {
		requestID := uuid.New().String()

		ch := make(chan string, 1)
		m.secretMu.Lock()
		if m.secretChans == nil {
			m.secretChans = make(map[string]chan string)
		}
		m.secretChans[requestID] = ch
		m.secretMu.Unlock()
		defer func() {
			m.secretMu.Lock()
			delete(m.secretChans, requestID)
			m.secretMu.Unlock()
		}()

		m.emitEvent("session:secret:prompt", map[string]interface{}{
			"requestID": requestID,
			"sessionID": sessionID,
			"title":     "Reconnection Credentials",
			"message":   prompt,
		})

		// Wait for the response with a timeout.
		var response string

		if m.ctx != nil {
			wailsRuntime.EventsOn(m.ctx, "session:secret:response", func(optionalData ...interface{}) {
				if len(optionalData) == 0 {
					return
				}
				data, ok := optionalData[0].(map[string]interface{})
				if !ok {
					return
				}
				if rid, _ := data["requestID"].(string); rid == requestID {
					val, _ := data["value"].(string)
					select {
					case ch <- val:
					default:
					}
				}
			})
		}

		select {
		case response = <-ch:
		case <-time.After(60 * time.Second):
			return "", fmt.Errorf("credential prompt timed out during reconnect")
		}

		return response, nil
	}
}

// RespondSecret supplies a credential prompt response for reconnect prompts in
// non-Wails hosts.
func (m *Manager) RespondSecret(requestID string, value string) {
	m.secretMu.Lock()
	ch, exists := m.secretChans[requestID]
	m.secretMu.Unlock()

	if exists {
		select {
		case ch <- value:
		default:
		}
	}
}

// CancelReconnect cancels any in-progress auto-reconnect for a session.
func (m *Manager) CancelReconnect(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok && sess.cancelReconn != nil {
		sess.cancelReconn()
		sess.cancelReconn = nil
		sess.reconnectCount = 0
		sess.Info.State = models.SessionDisconnected
	}
	m.mu.Unlock()

	if ok {
		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionDisconnected),
		})
	}
}

// createTabName generates a default tab name from the profile's display name
// and host.
func (m *Manager) createTabName(profile *models.Profile) string {
	if profile.DisplayName != "" {
		return profile.DisplayName
	}
	if profile.Username != "" {
		return fmt.Sprintf("%s@%s", profile.Username, profile.Host)
	}
	return profile.Host
}

// emitEvent sends an event to the Wails frontend via the runtime context.
func (m *Manager) emitEvent(eventName string, data interface{}) {
	m.mu.RLock()
	sink := m.eventSink
	m.mu.RUnlock()
	if sink != nil {
		sink(eventName, data)
	}
	if m.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(m.ctx, eventName, data)
}
