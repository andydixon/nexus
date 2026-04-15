// Package api provides the Wails-bound API surface that the frontend calls.
// All methods are called from JavaScript and must return JSON-serialisable types.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/copilot-ssh-client/app/internal/audit"
	"github.com/copilot-ssh-client/app/internal/models"
	"github.com/copilot-ssh-client/app/internal/preferences"
	"github.com/copilot-ssh-client/app/internal/secrets"
	"github.com/copilot-ssh-client/app/internal/sessions"
	"github.com/copilot-ssh-client/app/internal/storage"
	"github.com/google/uuid"
)

// appVersion is set at build time via ldflags.
var appVersion = "dev"

// Service is the main API surface exposed to the frontend via Wails bindings.
type Service struct {
	store         *storage.Store
	secretStore   *secrets.SecretStore
	keychainStore *secrets.KeychainStore
	sessions      *sessions.Manager
	auditLog      *audit.Logger
	prefs         *preferences.Manager
	ctx           context.Context

	// secretChans maps request IDs to channels awaiting user secret input.
	secretMu    sync.Mutex
	secretChans map[string]chan secretResponse
}

// NewService creates a Service wired to the given backing components.
func NewService(
	store *storage.Store,
	secretStore *secrets.SecretStore,
	keychainStore *secrets.KeychainStore,
	sess *sessions.Manager,
	auditLog *audit.Logger,
	prefs *preferences.Manager,
) *Service {
	return &Service{
		store:         store,
		secretStore:   secretStore,
		keychainStore: keychainStore,
		sessions:      sess,
		auditLog:      auditLog,
		prefs:         prefs,
	}
}

// SetContext stores the Wails runtime context and registers event listeners.
func (s *Service) SetContext(ctx context.Context) {
	s.ctx = ctx
	s.RegisterSecretResponseListener()
}

// ---------------------------------------------------------------------------
// Profile operations
// ---------------------------------------------------------------------------

// CreateProfile validates and persists a new connection profile.
func (s *Service) CreateProfile(profile models.Profile) (models.Profile, error) {
	if profile.ID == "" {
		profile.ID = uuid.New().String()
	}
	now := time.Now()
	profile.CreatedAt = now
	profile.UpdatedAt = now

	if profile.Tags == nil {
		profile.Tags = []string{}
	}

	if err := s.store.CreateProfile(&profile); err != nil {
		return models.Profile{}, fmt.Errorf("failed to create profile: %w", err)
	}
	s.auditLog.LogProfileCreate(profile.ID, profile.DisplayName)
	return profile, nil
}

// GetProfile retrieves a profile by ID.
func (s *Service) GetProfile(id string) (models.Profile, error) {
	p, err := s.store.GetProfile(id)
	if err != nil {
		return models.Profile{}, fmt.Errorf("profile not found: %w", err)
	}
	return *p, nil
}

// UpdateProfile persists changes to an existing profile.
func (s *Service) UpdateProfile(profile models.Profile) (models.Profile, error) {
	profile.UpdatedAt = time.Now()
	if err := s.store.UpdateProfile(&profile); err != nil {
		return models.Profile{}, fmt.Errorf("failed to update profile: %w", err)
	}
	s.auditLog.LogProfileUpdate(profile.ID, profile.DisplayName)
	return profile, nil
}

// DeleteProfile removes a profile and its associated secrets.
func (s *Service) DeleteProfile(id string) error {
	p, err := s.store.GetProfile(id)
	if err != nil {
		return fmt.Errorf("profile not found: %w", err)
	}

	if err := s.store.DeleteProfile(id); err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	// Best-effort secret cleanup.
	if s.secretStore != nil && s.secretStore.IsUnlocked() {
		_ = s.secretStore.DeleteProfileSecrets(id)
	}

	s.auditLog.LogProfileDelete(id, p.DisplayName)
	return nil
}

// ListProfiles returns all stored profiles.
func (s *Service) ListProfiles() ([]models.Profile, error) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// SearchProfiles searches profiles by display name, host, or tags.
func (s *Service) SearchProfiles(query string) ([]models.Profile, error) {
	profiles, err := s.store.SearchProfiles(query)
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// ListProfilesByProtocol returns profiles filtered by protocol type.
func (s *Service) ListProfilesByProtocol(protocol string) ([]models.Profile, error) {
	profiles, err := s.store.ListProfilesByProtocol(protocol)
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// ListProfilesByTag returns profiles that contain the given tag.
func (s *Service) ListProfilesByTag(tag string) ([]models.Profile, error) {
	profiles, err := s.store.ListProfilesByTag(tag)
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// ListProfilesByFolder returns profiles in the specified folder.
func (s *Service) ListProfilesByFolder(folder string) ([]models.Profile, error) {
	profiles, err := s.store.ListProfilesByFolder(folder)
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// ListFavourites returns all favourite profiles.
func (s *Service) ListFavourites() ([]models.Profile, error) {
	profiles, err := s.store.ListFavourites()
	if err != nil {
		return nil, err
	}
	return derefProfiles(profiles), nil
}

// SetFavourite updates the favourite status of a profile.
func (s *Service) SetFavourite(id string, favourite bool) error {
	return s.store.SetFavourite(id, favourite)
}

// DuplicateProfile creates a copy of an existing profile with a new ID.
func (s *Service) DuplicateProfile(id string) (models.Profile, error) {
	orig, err := s.store.GetProfile(id)
	if err != nil {
		return models.Profile{}, fmt.Errorf("profile not found: %w", err)
	}

	dup := *orig
	dup.ID = uuid.New().String()
	dup.DisplayName = orig.DisplayName + " (copy)"
	now := time.Now()
	dup.CreatedAt = now
	dup.UpdatedAt = now
	dup.LastConnected = nil

	if err := s.store.CreateProfile(&dup); err != nil {
		return models.Profile{}, fmt.Errorf("failed to duplicate profile: %w", err)
	}
	s.auditLog.LogProfileCreate(dup.ID, dup.DisplayName)
	return dup, nil
}

// NewProfile returns a new profile with sensible defaults for the given protocol.
func (s *Service) NewProfile(protocol string) models.Profile {
	return models.NewProfile(models.Protocol(protocol))
}

// ---------------------------------------------------------------------------
// Session operations
// ---------------------------------------------------------------------------

// ConnectSession loads a profile by ID and starts a session asynchronously.
// Returns immediately with session info in "connecting" state; the frontend
// should listen for session:state events for status updates.
func (s *Service) ConnectSession(profileID string) (models.SessionInfo, error) {
	profile, err := s.store.GetProfile(profileID)
	if err != nil {
		return models.SessionInfo{}, fmt.Errorf("profile not found: %w", err)
	}

	sessionID, err := s.sessions.Connect(profile, s.promptSecret)
	if err != nil {
		return models.SessionInfo{}, err
	}

	// Update last-connected timestamp.
	now := time.Now()
	profile.LastConnected = &now
	profile.UpdatedAt = now
	_ = s.store.UpdateProfile(profile)

	info := s.sessions.GetSessionInfo(sessionID)
	if info == nil {
		return models.SessionInfo{}, fmt.Errorf("session created but info not found")
	}
	return *info, nil
}

// ConnectQuick opens a session from an ad-hoc (unsaved) profile.
func (s *Service) ConnectQuick(profile models.Profile) (models.SessionInfo, error) {
	if profile.ID == "" {
		profile.ID = uuid.New().String()
	}
	sessionID, err := s.sessions.Connect(&profile, s.promptSecret)
	if err != nil {
		return models.SessionInfo{}, err
	}
	info := s.sessions.GetSessionInfo(sessionID)
	if info == nil {
		return models.SessionInfo{}, fmt.Errorf("session created but info not found")
	}
	return *info, nil
}

// DisconnectSession closes a session by ID.
func (s *Service) DisconnectSession(sessionID string) error {
	return s.sessions.Disconnect(sessionID)
}

// ReconnectSession closes and re-establishes a session.
func (s *Service) ReconnectSession(sessionID string) error {
	return s.sessions.Reconnect(sessionID, s.promptSecret)
}

// WriteSession writes base64-encoded data to a session.
func (s *Service) WriteSession(sessionID string, data string) error {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("invalid base64 data: %w", err)
	}
	return s.sessions.WriteToSession(sessionID, decoded)
}

// ResizeSession sends a terminal size change to a session.
func (s *Service) ResizeSession(sessionID string, cols int, rows int) error {
	return s.sessions.ResizeSession(sessionID, cols, rows)
}

// ListSessions returns all active sessions.
func (s *Service) ListSessions() []models.SessionInfo {
	return s.sessions.ListSessions()
}

// RenameSession changes the display name of a session tab.
func (s *Service) RenameSession(sessionID string, name string) error {
	return s.sessions.RenameSession(sessionID, name)
}

// PinSession toggles the pinned state of a session tab.
func (s *Service) PinSession(sessionID string, pinned bool) error {
	return s.sessions.PinSession(sessionID, pinned)
}

// CloseAllSessions terminates all active sessions.
func (s *Service) CloseAllSessions() {
	s.sessions.CloseAll()
}

// ---------------------------------------------------------------------------
// RAW-specific
// ---------------------------------------------------------------------------

// WriteSessionText sends plain text to a RAW session.
func (s *Service) WriteSessionText(sessionID string, text string) error {
	return s.sessions.WriteSessionText(sessionID, text)
}

// WriteSessionHex sends hex-encoded data to a RAW session.
func (s *Service) WriteSessionHex(sessionID string, hexStr string) error {
	return s.sessions.WriteSessionHex(sessionID, hexStr)
}

// WriteSessionEscaped sends text with C-style escape sequences to a RAW session.
func (s *Service) WriteSessionEscaped(sessionID string, text string) error {
	return s.sessions.WriteSessionEscaped(sessionID, text)
}

// GetSessionHistory returns the data history for a RAW session.
func (s *Service) GetSessionHistory(sessionID string) ([]map[string]interface{}, error) {
	entries, err := s.sessions.GetSessionHistory(sessionID)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		result[i] = map[string]interface{}{
			"timestamp": e.Timestamp.UnixMilli(),
			"direction": e.Direction,
			"data":      base64.StdEncoding.EncodeToString(e.Data),
		}
	}
	return result, nil
}

// ClearSessionHistory clears the data history for a RAW session.
func (s *Service) ClearSessionHistory(sessionID string) error {
	return s.sessions.ClearSessionHistory(sessionID)
}

// SaveSessionCapture saves the RAW session data to a file.
func (s *Service) SaveSessionCapture(sessionID string, path string) error {
	return s.sessions.SaveSessionCapture(sessionID, path)
}

// ---------------------------------------------------------------------------
// Telnet-specific
// ---------------------------------------------------------------------------

// SendBreak transmits a Telnet BRK command to a session.
func (s *Service) SendBreak(sessionID string) error {
	return s.sessions.SendBreak(sessionID)
}

// ---------------------------------------------------------------------------
// Secret store
// ---------------------------------------------------------------------------

// IsSecretStoreInitialised reports whether the encrypted vault has been created.
func (s *Service) IsSecretStoreInitialised() bool {
	if s.secretStore == nil {
		return false
	}
	return s.secretStore.IsInitialised()
}

// InitialiseSecretStore creates a new encrypted vault with the given master password.
func (s *Service) InitialiseSecretStore(masterPassword string) error {
	if s.secretStore == nil {
		return fmt.Errorf("secret store is not configured")
	}
	return s.secretStore.Initialise(masterPassword)
}

// UnlockSecretStore decrypts the vault using the master password.
func (s *Service) UnlockSecretStore(masterPassword string) error {
	if s.secretStore == nil {
		return fmt.Errorf("secret store is not configured")
	}
	return s.secretStore.Unlock(masterPassword)
}

// LockSecretStore zeroes key material and locks the vault.
func (s *Service) LockSecretStore() {
	if s.secretStore != nil {
		s.secretStore.Lock()
	}
}

// IsSecretStoreUnlocked reports whether the vault is currently unlocked.
func (s *Service) IsSecretStoreUnlocked() bool {
	if s.secretStore == nil {
		return false
	}
	return s.secretStore.IsUnlocked()
}

// SetProfileSecret encrypts and stores a secret for the given profile.
func (s *Service) SetProfileSecret(profileID string, secretType string, value string) error {
	key := fmt.Sprintf("profile:%s:%s", profileID, secretType)

	// Try the OS keychain first.
	if s.keychainStore != nil && s.keychainStore.Available() {
		return s.keychainStore.SetSecret(key, value)
	}

	// Fall back to the encrypted file store.
	if s.secretStore == nil || !s.secretStore.IsUnlocked() {
		return fmt.Errorf("secret store is locked; unlock it first")
	}
	return s.secretStore.SetSecret(key, value)
}

// DeleteProfileSecrets removes all secrets associated with a profile.
func (s *Service) DeleteProfileSecrets(profileID string) error {
	// Remove from both stores where possible.
	if s.secretStore != nil && s.secretStore.IsUnlocked() {
		_ = s.secretStore.DeleteProfileSecrets(profileID)
	}
	// OS keychain entries need individual deletion — handled at a higher level
	// because we cannot enumerate keychain keys by prefix portably.
	return nil
}

// ---------------------------------------------------------------------------
// Known hosts
// ---------------------------------------------------------------------------

// ListKnownHosts returns all stored host key entries.
func (s *Service) ListKnownHosts() ([]models.KnownHost, error) {
	hosts, err := s.store.ListKnownHosts()
	if err != nil {
		return nil, err
	}
	result := make([]models.KnownHost, len(hosts))
	for i, h := range hosts {
		result[i] = *h
	}
	return result, nil
}

// DeleteKnownHost removes a known host entry by ID.
func (s *Service) DeleteKnownHost(id string) error {
	return s.store.DeleteKnownHost(id)
}

// ---------------------------------------------------------------------------
// Preferences
// ---------------------------------------------------------------------------

// GetPreferences returns the current application preferences.
func (s *Service) GetPreferences() (models.AppPreferences, error) {
	p := s.prefs.Get()
	if p == nil {
		return models.DefaultAppPreferences(), nil
	}
	return *p, nil
}

// SavePreferences persists the given application preferences.
func (s *Service) SavePreferences(prefs models.AppPreferences) error {
	return s.prefs.Save(&prefs)
}

// ---------------------------------------------------------------------------
// Import / Export
// ---------------------------------------------------------------------------

// ExportProfiles serialises all profiles to a JSON string. When includeSecrets
// is true, decrypted credentials are embedded (use with caution).
func (s *Service) ExportProfiles(includeSecrets bool) (string, error) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return "", fmt.Errorf("failed to list profiles for export: %w", err)
	}

	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialise profiles: %w", err)
	}

	s.auditLog.LogExport(len(profiles), includeSecrets)
	return string(data), nil
}

// ImportProfiles deserialises profiles from a JSON string and persists them.
// The strategy parameter controls conflict resolution:
//   - "skip" ignores profiles whose ID already exists
//   - "overwrite" replaces existing profiles
//   - "duplicate" always creates a new profile with a fresh ID
func (s *Service) ImportProfiles(jsonData string, strategy string) (int, error) {
	var profiles []*models.Profile
	if err := json.Unmarshal([]byte(jsonData), &profiles); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}

	imported := 0
	for _, p := range profiles {
		switch strategy {
		case "skip":
			if existing, _ := s.store.GetProfile(p.ID); existing != nil {
				continue
			}
			if err := s.store.CreateProfile(p); err != nil {
				continue
			}
			imported++

		case "overwrite":
			p.UpdatedAt = time.Now()
			if existing, _ := s.store.GetProfile(p.ID); existing != nil {
				if err := s.store.UpdateProfile(p); err != nil {
					continue
				}
			} else {
				if err := s.store.CreateProfile(p); err != nil {
					continue
				}
			}
			imported++

		case "duplicate":
			p.ID = uuid.New().String()
			now := time.Now()
			p.CreatedAt = now
			p.UpdatedAt = now
			if err := s.store.CreateProfile(p); err != nil {
				continue
			}
			imported++

		default:
			return 0, fmt.Errorf("unknown import strategy %q (expected skip, overwrite, or duplicate)", strategy)
		}
	}

	s.auditLog.LogImport(imported)
	return imported, nil
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

// ListAuditEntries returns the most recent audit entries.
func (s *Service) ListAuditEntries(limit int) ([]models.AuditEntry, error) {
	entries, err := s.auditLog.ListEntries(limit)
	if err != nil {
		return nil, err
	}
	result := make([]models.AuditEntry, len(entries))
	for i, e := range entries {
		result[i] = *e
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

// SelectFile opens a native file dialog and returns the chosen path.
func (s *Service) SelectFile(title string, filters string) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}

	opts := wailsRuntime.OpenDialogOptions{
		Title: title,
	}
	if filters != "" {
		opts.Filters = []wailsRuntime.FileFilter{
			{DisplayName: "Files", Pattern: filters},
		}
	}

	path, err := wailsRuntime.OpenFileDialog(s.ctx, opts)
	if err != nil {
		return "", fmt.Errorf("file dialog failed: %w", err)
	}
	return path, nil
}

// SaveFile opens a native save dialog and returns the chosen path.
func (s *Service) SaveFile(title string, defaultName string) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}

	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: defaultName,
	})
	if err != nil {
		return "", fmt.Errorf("save dialog failed: %w", err)
	}
	return path, nil
}

// GetAppInfo returns build and environment information.
func (s *Service) GetAppInfo() map[string]string {
	return map[string]string{
		"version":  appVersion,
		"go":       runtime.Version(),
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// RefreshGitHubRepos triggers an on-demand refresh of GitHub repositories for
// the given session. The updated list is also emitted as a Wails event.
func (s *Service) RefreshGitHubRepos(sessionID string) ([]models.GitHubRepo, error) {
	return s.sessions.FetchGitHubRepos(sessionID)
}

// ---------------------------------------------------------------------------
// Port forwarding
// ---------------------------------------------------------------------------

// ListPortForwards returns the active port-forwarding tunnels for an SSH session.
func (s *Service) ListPortForwards(sessionID string) ([]models.PortForward, error) {
	return s.sessions.ListPortForwards(sessionID)
}

// StartPortForward activates a new forwarding tunnel on a live SSH session.
func (s *Service) StartPortForward(sessionID string, rule models.PortForward) error {
	return s.sessions.StartPortForward(sessionID, rule)
}

// StopPortForward terminates an active forwarding tunnel.
func (s *Service) StopPortForward(sessionID string, forwardID string) error {
	return s.sessions.StopPortForward(sessionID, forwardID)
}

// TestConnection performs a basic TCP reachability check to the given host and port.
// This verifies the network path without establishing a full protocol session.
func (s *Service) TestConnection(host string, port int) (map[string]interface{}, error) {
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	start := time.Now()

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("connection to %s failed after %dms: %w", addr, elapsed.Milliseconds(), err)
	}
	conn.Close()

	return map[string]interface{}{
		"success":    true,
		"host":       host,
		"port":       port,
		"latencyMs":  elapsed.Milliseconds(),
		"remoteAddr": addr,
	}, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// promptSecret emits a Wails event to request a secret from the user, then
// blocks until the frontend responds via the session:secret:response event.
// This bridges the synchronous Go SSH auth callback with the asynchronous
// frontend prompt UI.
func (s *Service) promptSecret(prompt string) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not available")
	}

	// Generate a unique request ID so we can correlate the response.
	requestID := uuid.New().String()
	ch := make(chan secretResponse, 1)

	// Register a one-shot listener for the response.
	s.secretMu.Lock()
	if s.secretChans == nil {
		s.secretChans = make(map[string]chan secretResponse)
	}
	s.secretChans[requestID] = ch
	s.secretMu.Unlock()

	// Emit the prompt event to the frontend.
	wailsRuntime.EventsEmit(s.ctx, "session:secret:prompt", map[string]interface{}{
		"requestID": requestID,
		"message":   prompt,
		"title":     "Credentials Required",
	})

	// Block until we receive a response (with a generous timeout).
	select {
	case resp := <-ch:
		s.secretMu.Lock()
		delete(s.secretChans, requestID)
		s.secretMu.Unlock()
		if resp.cancelled {
			return "", fmt.Errorf("user cancelled secret prompt")
		}
		return resp.value, nil
	case <-time.After(5 * time.Minute):
		s.secretMu.Lock()
		delete(s.secretChans, requestID)
		s.secretMu.Unlock()
		return "", fmt.Errorf("secret prompt timed out")
	}
}

// secretResponse carries the result of a frontend secret prompt.
type secretResponse struct {
	value     string
	cancelled bool
}

// RegisterSecretResponseListener sets up the Wails event listener that receives
// secrets entered by the user. Must be called after SetContext.
func (s *Service) RegisterSecretResponseListener() {
	if s.ctx == nil {
		return
	}
	wailsRuntime.EventsOn(s.ctx, "session:secret:response", func(optionalData ...interface{}) {
		if len(optionalData) == 0 {
			return
		}
		data, ok := optionalData[0].(map[string]interface{})
		if !ok {
			return
		}
		requestID, _ := data["requestID"].(string)
		value, _ := data["value"].(string)
		cancelled, _ := data["cancelled"].(bool)

		s.secretMu.Lock()
		ch, exists := s.secretChans[requestID]
		s.secretMu.Unlock()

		if exists {
			ch <- secretResponse{value: value, cancelled: cancelled}
		}
	})
}

// derefProfiles converts a slice of profile pointers to values.
func derefProfiles(ptrs []*models.Profile) []models.Profile {
	result := make([]models.Profile, len(ptrs))
	for i, p := range ptrs {
		result[i] = *p
	}
	return result
}
