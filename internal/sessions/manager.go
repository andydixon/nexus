// Package sessions orchestrates multiple simultaneous SSH, Telnet, RAW TCP,
// and GitHub sessions. It coordinates between protocol clients, the storage
// layer, the secret store, and the Wails runtime for event emission.
package sessions

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/githubclient"
	"github.com/andydixon/nexus/internal/models"
	"github.com/andydixon/nexus/internal/rawclient"
	"github.com/andydixon/nexus/internal/recording"
	"github.com/andydixon/nexus/internal/secrets"
	"github.com/andydixon/nexus/internal/sftpclient"
	"github.com/andydixon/nexus/internal/sshclient"
	"github.com/andydixon/nexus/internal/storage"
	"github.com/andydixon/nexus/internal/telnetclient"
)

// Session wraps a protocol client with metadata and state.
type Session struct {
	Info           models.SessionInfo
	sshClient      *sshclient.Client
	telnetClient   *telnetclient.Client
	rawClient      *rawclient.Client
	githubClient   *githubclient.Client
	githubCancel   context.CancelFunc // stops the auto-refresh goroutine
	sftpClient     *sftpclient.Client // SFTP subsystem, nil until requested
	profile        *models.Profile
	recorder       *recording.Recorder // active recording, nil if not recording
	reconnectCount int                 // number of reconnection attempts so far
	cancelReconn   context.CancelFunc  // cancels an in-progress reconnect backoff

	// Login script support — temporary buffer for expect-style matching.
	loginBuf   []byte
	loginBufMu sync.Mutex
	loginDone  chan struct{} // closed when login script completes or is abandoned
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
	recordStore   *recording.Store
	ctx           context.Context // Wails app context for event emission
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
			return
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
	}()

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

	// Record input if recording is active.
	m.mu.RLock()
	rec := sess.recorder
	m.mu.RUnlock()
	if rec != nil {
		rec.WriteInput(data)
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

	if err := client.Connect(getPassword, getPassphrase); err != nil {
		return err
	}

	sess.sshClient = client

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

	sess.telnetClient = client

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

	sess.rawClient = client
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
		return fmt.Errorf("GitHub authentication failed: %w", err)
	}

	sess.githubClient = client

	// Perform the initial fetch.
	repos, err := client.FetchRepos(context.Background())
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
	sess.githubCancel = cancel

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updated, fetchErr := client.FetchRepos(ctx)
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
			"transferred": p.Transferred,
			"total":       p.Total,
			"path":        p.Path,
			"done":        p.Done,
		})
	})
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

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

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

// closeSession shuts down the protocol client for a session.
func (m *Manager) closeSession(sess *Session) {
	// Cancel any in-progress reconnect backoff.
	if sess.cancelReconn != nil {
		sess.cancelReconn()
		sess.cancelReconn = nil
	}

	// Stop any active recording.
	if sess.recorder != nil {
		sess.recorder.Close()
		sess.recorder = nil
	}

	// Close SFTP subsystem if open.
	if sess.sftpClient != nil {
		sess.sftpClient.Close()
		sess.sftpClient = nil
	}

	if sess.sshClient != nil {
		sess.sshClient.Close()
	}
	if sess.telnetClient != nil {
		sess.telnetClient.Close()
	}
	if sess.rawClient != nil {
		sess.rawClient.Close()
	}
	if sess.githubCancel != nil {
		sess.githubCancel()
	}

	m.mu.Lock()
	sess.Info.State = models.SessionDisconnected
	m.mu.Unlock()
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
	if ok {
		sess.Info.State = models.SessionDisconnected
	}
	m.mu.Unlock()

	m.auditLog.LogDisconnect(sessionID)

	// Check whether auto-reconnect is enabled for this session.
	if ok && m.shouldAutoReconnect(sess) {
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
		"sessionID": sessionID,
		"state":     string(models.SessionReconnecting),
		"attempt":   attempt,
		"maxRetries": maxTries,
		"delaySec":  delaySec,
	})

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(delaySec) * time.Second):
		}

		// Clear old clients before reconnecting.
		m.mu.Lock()
		sess.sshClient = nil
		sess.telnetClient = nil
		sess.rawClient = nil
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
		default:
			err = fmt.Errorf("unsupported protocol for reconnect: %s", sess.profile.Protocol)
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

			// Schedule the next attempt.
			m.scheduleReconnect(sess, sessionID)
			return
		}

		// Success — reset the counter.
		m.mu.Lock()
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
		// Use the same event-based flow as the initial connection.
		requestID := uuid.New().String()

		ch := make(chan string, 1)
		m.mu.Lock()
		// Store a temporary secret channel keyed by requestID.
		if m.ctx != nil {
			wailsRuntime.EventsEmit(m.ctx, "session:secret:prompt", map[string]interface{}{
				"requestID": requestID,
				"sessionID": sessionID,
				"title":     "Reconnection Credentials",
				"message":   prompt,
			})
		}
		m.mu.Unlock()

		// Wait for the response with a timeout.
		var response string
		cancel := make(chan struct{})

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
			close(cancel)
			return "", fmt.Errorf("credential prompt timed out during reconnect")
		}

		return response, nil
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
	if m.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(m.ctx, eventName, data)
}
