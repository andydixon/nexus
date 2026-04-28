// Package api provides the Wails-bound API surface that the frontend calls.
// All methods are called from JavaScript and must return JSON-serialisable types.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/exportimport"
	"github.com/andydixon/nexus/internal/models"
	"github.com/andydixon/nexus/internal/preferences"
	"github.com/andydixon/nexus/internal/secrets"
	"github.com/andydixon/nexus/internal/sessions"
	"github.com/andydixon/nexus/internal/sshconfig"
	"github.com/andydixon/nexus/internal/storage"
	"github.com/andydixon/nexus/internal/updater"
	"github.com/google/uuid"
)

// appVersion is set at build time via ldflags.
var appVersion = "dev"

// appUpdateURL is set at build time via ldflags.
var appUpdateURL = ""

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

// ListSSHKeys returns absolute paths to candidate SSH private keys in ~/.ssh.
func (s *Service) ListSSHKeys() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}, nil
	}

	dir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	ignored := map[string]bool{
		"authorized_keys":  true,
		"authorized_keys2": true,
		"config":           true,
		"known_hosts":      true,
		"known_hosts.old":  true,
		"ssh_config":       true,
		"ssh_known_hosts":  true,
		"ssh_known_hosts2": true,
	}

	keys := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if ignored[name] {
			continue
		}
		if strings.HasSuffix(name, ".pub") {
			continue
		}
		if strings.HasSuffix(name, ".old") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if !isLikelyPrivateKey(fullPath) {
			continue
		}
		keys = append(keys, fullPath)
	}

	sort.Strings(keys)
	return keys, nil
}

func isLikelyPrivateKey(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && n == 0 {
		return false
	}
	head := buf[:n]

	if bytes.HasPrefix(head, []byte("openssh-key-v1")) {
		return true
	}

	if bytes.Contains(head, []byte("BEGIN OPENSSH PRIVATE KEY")) {
		return true
	}
	if bytes.Contains(head, []byte("BEGIN RSA PRIVATE KEY")) {
		return true
	}
	if bytes.Contains(head, []byte("BEGIN DSA PRIVATE KEY")) {
		return true
	}
	if bytes.Contains(head, []byte("BEGIN EC PRIVATE KEY")) {
		return true
	}
	if bytes.Contains(head, []byte("BEGIN ED25519 PRIVATE KEY")) {
		return true
	}
	if bytes.Contains(head, []byte("BEGIN PRIVATE KEY")) {
		return true
	}

	return false
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

// CancelReconnect cancels an in-progress auto-reconnect for a session.
func (s *Service) CancelReconnect(sessionID string) {
	s.sessions.CancelReconnect(sessionID)
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
// TOTP / 2FA
// ---------------------------------------------------------------------------

// SetTOTPSecret stores a TOTP secret for the given profile, encrypted in
// the secret vault (or OS keychain).
func (s *Service) SetTOTPSecret(profileID string, secret string) error {
	return s.SetProfileSecret(profileID, "totp", secret)
}

// GetTOTPCode generates the current 6-digit TOTP code for the given profile.
// Used for the "Test" button in the profile editor.
func (s *Service) GetTOTPCode(profileID string) (string, error) {
	// Look up the TOTP secret from the vault.
	totpKey := fmt.Sprintf("profile:%s:totp", profileID)
	var secret string

	if s.keychainStore != nil && s.keychainStore.Available() {
		if val, err := s.keychainStore.GetSecret(totpKey); err == nil && val != "" {
			secret = val
		}
	}
	if secret == "" && s.secretStore != nil && s.secretStore.IsUnlocked() {
		if val, err := s.secretStore.GetSecret(totpKey); err == nil && val != "" {
			secret = val
		}
	}
	if secret == "" {
		return "", fmt.Errorf("no TOTP secret configured for this profile")
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		return "", fmt.Errorf("failed to generate TOTP code: %w", err)
	}
	return code, nil
}

// HasTOTPSecret checks whether a profile has a TOTP secret stored.
func (s *Service) HasTOTPSecret(profileID string) bool {
	totpKey := fmt.Sprintf("profile:%s:totp", profileID)
	if s.keychainStore != nil && s.keychainStore.Available() {
		if val, err := s.keychainStore.GetSecret(totpKey); err == nil && val != "" {
			return true
		}
	}
	if s.secretStore != nil && s.secretStore.IsUnlocked() {
		if val, err := s.secretStore.GetSecret(totpKey); err == nil && val != "" {
			return true
		}
	}
	return false
}

// DeleteTOTPSecret removes the TOTP secret for the given profile.
func (s *Service) DeleteTOTPSecret(profileID string) error {
	totpKey := fmt.Sprintf("profile:%s:totp", profileID)
	if s.keychainStore != nil && s.keychainStore.Available() {
		_ = s.keychainStore.DeleteSecret(totpKey)
	}
	if s.secretStore != nil && s.secretStore.IsUnlocked() {
		_ = s.secretStore.DeleteSecret(totpKey)
	}
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
// Snippets (command library)
// ---------------------------------------------------------------------------

// ListSnippets returns all saved command snippets.
func (s *Service) ListSnippets() ([]models.Snippet, error) {
	snippets, err := s.store.ListSnippets()
	if err != nil {
		return nil, err
	}
	result := make([]models.Snippet, len(snippets))
	for i, sn := range snippets {
		result[i] = *sn
	}
	return result, nil
}

// CreateSnippet saves a new command snippet.
func (s *Service) CreateSnippet(snippet models.Snippet) (models.Snippet, error) {
	if snippet.ID == "" {
		snippet.ID = uuid.New().String()
	}
	now := time.Now()
	snippet.CreatedAt = now
	snippet.UpdatedAt = now
	if snippet.Tags == nil {
		snippet.Tags = []string{}
	}
	if err := s.store.CreateSnippet(&snippet); err != nil {
		return models.Snippet{}, fmt.Errorf("failed to create snippet: %w", err)
	}
	return snippet, nil
}

// UpdateSnippet updates an existing command snippet.
func (s *Service) UpdateSnippet(snippet models.Snippet) (models.Snippet, error) {
	snippet.UpdatedAt = time.Now()
	if err := s.store.UpdateSnippet(&snippet); err != nil {
		return models.Snippet{}, fmt.Errorf("failed to update snippet: %w", err)
	}
	return snippet, nil
}

// DeleteSnippet removes a command snippet by ID.
func (s *Service) DeleteSnippet(id string) error {
	return s.store.DeleteSnippet(id)
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

// FileExists returns true if the path exists and is not a directory.
func (s *Service) FileExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}

// FileIsPrivateKey returns true if the path appears to be an SSH private key.
func (s *Service) FileIsPrivateKey(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	return isLikelyPrivateKey(path), nil
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

// DownloadGitHubRepoArchive downloads a repository zip archive to a local file.
func (s *Service) DownloadGitHubRepoArchive(sessionID string, fullName string, ref string) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}
	if fullName == "" {
		return "", fmt.Errorf("repository name cannot be empty")
	}

	parts := strings.Split(fullName, "/")
	repoName := parts[len(parts)-1]
	defaultName := repoName + ".zip"
	if ref != "" {
		safeRef := strings.ReplaceAll(ref, "/", "-")
		defaultName = fmt.Sprintf("%s-%s.zip", repoName, safeRef)
	}

	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Download Repository",
		DefaultFilename: defaultName,
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Zip Archive (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("save dialog failed: %w", err)
	}
	if path == "" {
		return "", fmt.Errorf("download cancelled")
	}
	if filepath.Ext(path) != ".zip" {
		path += ".zip"
	}

	if err := s.sessions.DownloadGitHubArchive(sessionID, fullName, ref, path); err != nil {
		return "", err
	}
	return path, nil
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

// ---------------------------------------------------------------------------
// Session recording
// ---------------------------------------------------------------------------

// StartRecording begins recording terminal I/O for a session.
func (s *Service) StartRecording(sessionID string, cols int, rows int) (string, error) {
	return s.sessions.StartRecording(sessionID, cols, rows)
}

// StopRecording stops an active recording for a session.
func (s *Service) StopRecording(sessionID string) error {
	return s.sessions.StopRecording(sessionID)
}

// IsRecording reports whether a session is currently being recorded.
func (s *Service) IsRecording(sessionID string) bool {
	return s.sessions.IsRecording(sessionID)
}

// ListRecordings returns metadata for all saved recordings.
func (s *Service) ListRecordings() ([]map[string]interface{}, error) {
	metas, err := s.sessions.ListRecordings()
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, len(metas))
	for i, m := range metas {
		result[i] = map[string]interface{}{
			"id":        m.ID,
			"sessionId": m.SessionID,
			"title":     m.Title,
			"width":     m.Width,
			"height":    m.Height,
			"duration":  m.Duration,
			"size":      m.Size,
			"createdAt": m.CreatedAt.Format(time.RFC3339),
			"filePath":  m.FilePath,
		}
	}
	return result, nil
}

// GetRecording returns the full recording data for playback.
func (s *Service) GetRecording(id string) (map[string]interface{}, error) {
	hdr, events, err := s.sessions.GetRecording(id)
	if err != nil {
		return nil, err
	}

	evtList := make([][]interface{}, len(events))
	for i, e := range events {
		evtList[i] = []interface{}{e.Elapsed, string(e.Type), e.Data}
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"version":   hdr.Version,
			"width":     hdr.Width,
			"height":    hdr.Height,
			"timestamp": hdr.Timestamp,
			"title":     hdr.Title,
		},
		"events": evtList,
	}, nil
}

// DeleteRecording removes a saved recording.
func (s *Service) DeleteRecording(id string) error {
	return s.sessions.DeleteRecording(id)
}

// ---------------------------------------------------------------------------
// Encrypted export / import
// ---------------------------------------------------------------------------

// ExportProfilesEncrypted encrypts and exports selected profiles to a file.
// The profileIDs parameter selects which profiles to include; if empty, all
// profiles are exported. Returns the file path on success.
func (s *Service) ExportProfilesEncrypted(profileIDs []string, password string) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}

	var profiles []models.Profile
	if len(profileIDs) == 0 {
		// Export all profiles.
		all, err := s.store.ListProfiles()
		if err != nil {
			return "", fmt.Errorf("failed to list profiles: %w", err)
		}
		for _, p := range all {
			profiles = append(profiles, *p)
		}
	} else {
		for _, id := range profileIDs {
			p, err := s.store.GetProfile(id)
			if err != nil {
				continue // skip missing profiles
			}
			profiles = append(profiles, *p)
		}
	}

	if len(profiles) == 0 {
		return "", fmt.Errorf("no profiles to export")
	}

	// Prompt for save location.
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export Encrypted Profiles",
		DefaultFilename: "nexus-profiles.nexus",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Nexus Export (*.nexus)", Pattern: "*.nexus"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("save dialog failed: %w", err)
	}
	if path == "" {
		return "", fmt.Errorf("export cancelled")
	}

	// Ensure .nexus extension.
	if filepath.Ext(path) != ".nexus" {
		path += ".nexus"
	}

	if err := exportimport.EncryptAndExport(profiles, password, path); err != nil {
		return "", err
	}

	s.auditLog.LogExport(len(profiles), false)
	return path, nil
}

// ImportProfilesEncrypted decrypts and imports profiles from an encrypted file.
// Returns the number of profiles successfully imported.
func (s *Service) ImportProfilesEncrypted(password string, strategy string) (int, error) {
	if s.ctx == nil {
		return 0, fmt.Errorf("application context not set")
	}

	// Prompt for file selection.
	path, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Import Encrypted Profiles",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Nexus Export (*.nexus)", Pattern: "*.nexus"},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("open dialog failed: %w", err)
	}
	if path == "" {
		return 0, fmt.Errorf("import cancelled")
	}

	profiles, err := exportimport.DecryptAndImport(path, password)
	if err != nil {
		return 0, err
	}

	// Import using the existing strategy logic.
	imported := 0
	for _, p := range profiles {
		switch strategy {
		case "skip":
			if existing, _ := s.store.GetProfile(p.ID); existing != nil {
				continue
			}
			if err := s.store.CreateProfile(&p); err != nil {
				continue
			}
			imported++

		case "overwrite":
			p.UpdatedAt = time.Now()
			if existing, _ := s.store.GetProfile(p.ID); existing != nil {
				if err := s.store.UpdateProfile(&p); err != nil {
					continue
				}
			} else {
				if err := s.store.CreateProfile(&p); err != nil {
					continue
				}
			}
			imported++

		case "duplicate":
			p.ID = uuid.New().String()
			now := time.Now()
			p.CreatedAt = now
			p.UpdatedAt = now
			if err := s.store.CreateProfile(&p); err != nil {
				continue
			}
			imported++

		default:
			return 0, fmt.Errorf("unknown import strategy %q", strategy)
		}
	}

	s.auditLog.LogImport(imported)
	return imported, nil
}

// ---------------------------------------------------------------------------
// SFTP file browser
// ---------------------------------------------------------------------------

// SFTPList lists the contents of a remote directory.
func (s *Service) SFTPList(sessionID string, remotePath string) ([]map[string]interface{}, error) {
	entries, err := s.sessions.SFTPList(sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		result[i] = map[string]interface{}{
			"name":       e.Name,
			"path":       e.Path,
			"size":       e.Size,
			"isDir":      e.IsDir,
			"mode":       e.Mode,
			"modifiedAt": e.ModifiedAt,
		}
	}
	return result, nil
}

// SFTPMkdir creates a remote directory (including parents).
func (s *Service) SFTPMkdir(sessionID string, remotePath string) error {
	return s.sessions.SFTPMkdir(sessionID, remotePath)
}

// SFTPStat returns information about a remote path.
func (s *Service) SFTPStat(sessionID string, remotePath string) (map[string]interface{}, error) {
	entry, err := s.sessions.SFTPStat(sessionID, remotePath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"name":       entry.Name,
		"path":       entry.Path,
		"size":       entry.Size,
		"isDir":      entry.IsDir,
		"mode":       entry.Mode,
		"modifiedAt": entry.ModifiedAt,
	}, nil
}

// SFTPRemove deletes a remote file or empty directory.
func (s *Service) SFTPRemove(sessionID string, remotePath string) error {
	return s.sessions.SFTPRemove(sessionID, remotePath)
}

// SFTPRename moves or renames a remote file/directory.
func (s *Service) SFTPRename(sessionID string, oldPath string, newPath string) error {
	return s.sessions.SFTPRename(sessionID, oldPath, newPath)
}

// SFTPDownload downloads a remote file. Opens a native save dialog for the
// local destination.
func (s *Service) SFTPDownload(sessionID string, remotePath string) error {
	if s.ctx == nil {
		return fmt.Errorf("application context not set")
	}

	localPath, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Save File",
		DefaultFilename: filepath.Base(remotePath),
	})
	if err != nil {
		return fmt.Errorf("save dialog failed: %w", err)
	}
	if localPath == "" {
		return fmt.Errorf("download cancelled")
	}

	return s.sessions.SFTPDownload(sessionID, remotePath, localPath)
}

// SFTPUpload uploads a local file to a remote directory. Opens a native file
// dialog to select the local file.
func (s *Service) SFTPUpload(sessionID string, remotePath string) error {
	if s.ctx == nil {
		return fmt.Errorf("application context not set")
	}

	localPath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select File to Upload",
	})
	if err != nil {
		return fmt.Errorf("open dialog failed: %w", err)
	}
	if localPath == "" {
		return fmt.Errorf("upload cancelled")
	}

	destPath := remotePath + "/" + filepath.Base(localPath)
	return s.sessions.SFTPUpload(sessionID, localPath, destPath)
}

// SFTPUploadData uploads raw file data to a remote directory.
// Data is expected as base64-encoded bytes from the frontend.
func (s *Service) SFTPUploadData(sessionID string, remoteDir string, fileName string, base64Data string) error {
	if base64Data == "" {
		return fmt.Errorf("empty upload payload")
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return fmt.Errorf("failed to decode upload data: %w", err)
	}

	safeName := filepath.Base(fileName)
	if safeName == "" || safeName == "." || safeName == "/" {
		return fmt.Errorf("invalid file name")
	}

	destPath := path.Join(remoteDir, safeName)
	return s.sessions.SFTPUploadBytes(sessionID, destPath, data)
}

// SFTPPwd returns the current working directory on the remote host.
func (s *Service) SFTPPwd(sessionID string) (string, error) {
	return s.sessions.SFTPPwd(sessionID)
}

// GetSessionCwd returns a best-effort working directory for SSH sessions.
func (s *Service) GetSessionCwd(sessionID string) (string, error) {
	return s.sessions.GetSessionCwd(sessionID)
}

// ---------------------------------------------------------------------------
// OpenSSH config import
// ---------------------------------------------------------------------------

// ImportSSHConfig parses the given SSH config file (or the default
// ~/.ssh/config if path is empty) and imports the Host blocks as profiles.
func (s *Service) ImportSSHConfig(configPath string, folder string) (int, error) {
	if configPath == "" {
		configPath = sshconfig.DefaultConfigPath()
	}

	blocks, err := sshconfig.ParseFile(configPath)
	if err != nil {
		return 0, err
	}

	profiles := sshconfig.ToProfiles(blocks, folder)
	imported := 0
	for _, p := range profiles {
		if err := s.store.CreateProfile(&p); err != nil {
			continue // skip duplicates or errors
		}
		imported++
	}

	s.auditLog.LogImport(imported)
	return imported, nil
}

// ---------------------------------------------------------------------------
// Automatic update checking
// ---------------------------------------------------------------------------

// CheckForUpdates queries GitHub Releases for a newer version.
func (s *Service) CheckForUpdates() (map[string]interface{}, error) {
	ctx := context.Background()
	var info *updater.UpdateInfo
	var err error

	if appUpdateURL != "" {
		url := strings.ReplaceAll(appUpdateURL, "{platform}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{os}", runtime.GOOS)
		url = strings.ReplaceAll(url, "{arch}", runtime.GOARCH)
		checker := updater.NewURLChecker(url, appVersion)
		info, err = checker.Check(ctx)
	} else {
		checker := updater.NewChecker(appVersion)
		info, err = checker.Check(ctx)
	}
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"available":      info.Available,
		"currentVersion": info.CurrentVersion,
		"latestVersion":  info.LatestVersion,
		"releaseURL":     info.ReleaseURL,
		"releaseName":    info.ReleaseName,
		"releaseNotes":   info.ReleaseNotes,
		"publishedAt":    info.PublishedAt,
	}, nil
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
