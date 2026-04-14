// Package sessions orchestrates multiple simultaneous SSH, Telnet, and RAW TCP
// sessions. It coordinates between protocol clients, the storage layer, the
// secret store, and the Wails runtime for event emission.
package sessions

import (
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/copilot-ssh-client/app/internal/audit"
	"github.com/copilot-ssh-client/app/internal/models"
	"github.com/copilot-ssh-client/app/internal/rawclient"
	"github.com/copilot-ssh-client/app/internal/secrets"
	"github.com/copilot-ssh-client/app/internal/sshclient"
	"github.com/copilot-ssh-client/app/internal/storage"
	"github.com/copilot-ssh-client/app/internal/telnetclient"

	"context"
)

// Session wraps a protocol client with metadata and state.
type Session struct {
	Info         models.SessionInfo
	sshClient    *sshclient.Client
	telnetClient *telnetclient.Client
	rawClient    *rawclient.Client
	profile      *models.Profile
}

// Manager handles the lifecycle of all active sessions.
// It coordinates between protocol clients, the storage layer,
// the secret store, and the Wails runtime for event emission.
type Manager struct {
	mu            sync.RWMutex
	sessions      map[string]*Session
	store         *storage.Store
	secretStore   *secrets.SecretStore
	keychainStore *secrets.KeychainStore
	auditLog      *audit.Logger
	ctx           context.Context // Wails app context for event emission
}

// NewManager creates a Manager wired to the given backing services.
func NewManager(store *storage.Store, secretStore *secrets.SecretStore, keychainStore *secrets.KeychainStore, auditLog *audit.Logger) *Manager {
	return &Manager{
		sessions:      make(map[string]*Session),
		store:         store,
		secretStore:   secretStore,
		keychainStore: keychainStore,
		auditLog:      auditLog,
	}
}

// SetContext stores the Wails runtime context for event emission.
func (m *Manager) SetContext(ctx context.Context) {
	m.ctx = ctx
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

	var err error
	switch profile.Protocol {
	case models.ProtocolSSH:
		err = m.connectSSH(sess, getSecret)
	case models.ProtocolTelnet:
		err = m.connectTelnet(sess)
	case models.ProtocolRAW:
		err = m.connectRAW(sess)
	default:
		err = fmt.Errorf("unsupported protocol %q", profile.Protocol)
	}

	if err != nil {
		m.mu.Lock()
		sess.Info.State = models.SessionError
		sess.Info.Error = err.Error()
		m.mu.Unlock()

		m.emitEvent("session:state", map[string]interface{}{
			"sessionID": sessionID,
			"state":     string(models.SessionError),
			"error":     err.Error(),
		})
		return "", fmt.Errorf("sessions: connection failed: %w", err)
	}

	m.mu.Lock()
	sess.Info.State = models.SessionConnected
	sess.Info.ConnectedAt = time.Now()
	m.mu.Unlock()

	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionConnected),
	})

	m.auditLog.LogConnect(profile.ID, sessionID, string(profile.Protocol), profile.Host)
	return sessionID, nil
}

// Disconnect closes a specific session.
func (m *Manager) Disconnect(sessionID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}
	m.mu.Unlock()

	m.closeSession(sess)
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
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}

	switch {
	case sess.sshClient != nil:
		return sess.sshClient.Write(data)
	case sess.telnetClient != nil:
		return sess.telnetClient.Write(data)
	case sess.rawClient != nil:
		return sess.rawClient.Write(data)
	}
	return fmt.Errorf("sessions: no active client for session %q", sessionID)
}

// ResizeSession sends a terminal resize to a session.
func (m *Manager) ResizeSession(sessionID string, cols, rows int) error {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("sessions: session %q not found", sessionID)
	}

	switch {
	case sess.sshClient != nil:
		return sess.sshClient.Resize(cols, rows)
	case sess.telnetClient != nil:
		return sess.telnetClient.Resize(cols, rows)
	}
	// RAW sessions have no concept of terminal size.
	return nil
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
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}
	m.mu.RUnlock()

	for _, sess := range sessions {
		m.closeSession(sess)
	}

	m.mu.Lock()
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()
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

	// Build credential callbacks that check keychain/store before prompting.
	getPassword := m.buildSecretCallback(profile.ID, "password", getSecret)
	getPassphrase := m.buildSecretCallback(profile.ID, "passphrase", getSecret)

	if err := client.Connect(getPassword, getPassphrase); err != nil {
		return err
	}

	sess.sshClient = client
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

	sess.telnetClient = client
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

	sess.rawClient = client
	return nil
}

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

// buildSecretCallback returns a func() (string, error) that first checks the
// OS keychain, then the encrypted vault, then prompts the user via getSecret.
func (m *Manager) buildSecretCallback(profileID string, secretType string, getSecret func(prompt string) (string, error)) func() (string, error) {
	return func() (string, error) {
		secretKey := fmt.Sprintf("profile:%s:%s", profileID, secretType)

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

		// Prompt the user via the callback.
		if getSecret == nil {
			return "", fmt.Errorf("no %s available and no prompt callback provided", secretType)
		}

		prompt := fmt.Sprintf("Enter %s:", secretType)
		return getSecret(prompt)
	}
}

// closeSession shuts down the protocol client for a session.
func (m *Manager) closeSession(sess *Session) {
	if sess.sshClient != nil {
		sess.sshClient.Close()
	}
	if sess.telnetClient != nil {
		sess.telnetClient.Close()
	}
	if sess.rawClient != nil {
		sess.rawClient.Close()
	}

	m.mu.Lock()
	sess.Info.State = models.SessionDisconnected
	m.mu.Unlock()
}

// handleSessionClosed is called when a protocol client reports closure.
func (m *Manager) handleSessionClosed(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		sess.Info.State = models.SessionDisconnected
	}
	m.mu.Unlock()

	m.auditLog.LogDisconnect(sessionID)

	m.emitEvent("session:closed", map[string]interface{}{
		"sessionID": sessionID,
	})
	m.emitEvent("session:state", map[string]interface{}{
		"sessionID": sessionID,
		"state":     string(models.SessionDisconnected),
	})
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
	if m.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(m.ctx, eventName, data)
}
