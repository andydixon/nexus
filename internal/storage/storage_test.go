package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andydixon/nexus/internal/models"
	"github.com/google/uuid"
)

// setupTestStore creates an isolated Store backed by a temporary SQLite database.
func setupTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// makeSSHProfile returns a minimal valid SSH profile for testing.
func makeSSHProfile(name, host string) *models.Profile {
	p := models.NewProfile(models.ProtocolSSH)
	p.DisplayName = name
	p.Host = host
	p.Username = "testuser"
	return &p
}

// ---------------------------------------------------------------------------
// Profile CRUD
// ---------------------------------------------------------------------------

func TestCreateAndGetProfile(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("My Server", "10.0.0.1")

	if err := store.CreateProfile(p); err != nil {
		t.Fatalf("CreateProfile() error = %v", err)
	}

	got, err := store.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if got.DisplayName != "My Server" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "My Server")
	}
	if got.Host != "10.0.0.1" {
		t.Errorf("Host = %q, want %q", got.Host, "10.0.0.1")
	}
	if got.Protocol != models.ProtocolSSH {
		t.Errorf("Protocol = %q, want %q", got.Protocol, models.ProtocolSSH)
	}
}

func TestUpdateProfile(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("Original", "host.example.com")
	_ = store.CreateProfile(p)

	p.DisplayName = "Updated"
	p.Host = "new-host.example.com"
	p.UpdatedAt = time.Now()
	if err := store.UpdateProfile(p); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}

	got, err := store.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if got.DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "Updated")
	}
	if got.Host != "new-host.example.com" {
		t.Errorf("Host = %q, want %q", got.Host, "new-host.example.com")
	}
}

func TestDeleteProfile(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("ToDelete", "host.local")
	_ = store.CreateProfile(p)

	if err := store.DeleteProfile(p.ID); err != nil {
		t.Fatalf("DeleteProfile() error = %v", err)
	}
	if _, err := store.GetProfile(p.ID); err == nil {
		t.Error("expected error when getting deleted profile")
	}
}

func TestDeleteProfileNotFound(t *testing.T) {
	store := setupTestStore(t)
	if err := store.DeleteProfile("nonexistent"); err == nil {
		t.Error("expected error when deleting non-existent profile")
	}
}

// ---------------------------------------------------------------------------
// List and search
// ---------------------------------------------------------------------------

func TestListProfiles(t *testing.T) {
	store := setupTestStore(t)
	_ = store.CreateProfile(makeSSHProfile("Alpha", "a.example.com"))
	_ = store.CreateProfile(makeSSHProfile("Beta", "b.example.com"))

	profiles, err := store.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles() error = %v", err)
	}
	if len(profiles) != 2 {
		t.Errorf("ListProfiles() returned %d profiles, want 2", len(profiles))
	}
}

func TestSearchProfiles(t *testing.T) {
	store := setupTestStore(t)
	_ = store.CreateProfile(makeSSHProfile("Production Web", "web.prod.com"))
	_ = store.CreateProfile(makeSSHProfile("Staging DB", "db.staging.com"))

	results, err := store.SearchProfiles("prod")
	if err != nil {
		t.Fatalf("SearchProfiles() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchProfiles(\"prod\") returned %d results, want 1", len(results))
	}
	if results[0].DisplayName != "Production Web" {
		t.Errorf("DisplayName = %q, want %q", results[0].DisplayName, "Production Web")
	}
}

func TestSearchProfilesNoResults(t *testing.T) {
	store := setupTestStore(t)
	_ = store.CreateProfile(makeSSHProfile("Foo", "foo.local"))

	results, err := store.SearchProfiles("zzz-no-match")
	if err != nil {
		t.Fatalf("SearchProfiles() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestListProfilesByProtocol(t *testing.T) {
	store := setupTestStore(t)

	ssh := makeSSHProfile("SSH Server", "ssh.local")
	_ = store.CreateProfile(ssh)

	telnet := models.NewProfile(models.ProtocolTelnet)
	telnet.DisplayName = "Telnet Switch"
	telnet.Host = "switch.local"
	_ = store.CreateProfile(&telnet)

	sshResults, err := store.ListProfilesByProtocol("ssh")
	if err != nil {
		t.Fatalf("ListProfilesByProtocol(ssh) error = %v", err)
	}
	if len(sshResults) != 1 {
		t.Errorf("expected 1 SSH profile, got %d", len(sshResults))
	}

	telnetResults, err := store.ListProfilesByProtocol("telnet")
	if err != nil {
		t.Fatalf("ListProfilesByProtocol(telnet) error = %v", err)
	}
	if len(telnetResults) != 1 {
		t.Errorf("expected 1 Telnet profile, got %d", len(telnetResults))
	}
}

// ---------------------------------------------------------------------------
// Favourites
// ---------------------------------------------------------------------------

func TestFavourites(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("Fav Server", "fav.local")
	_ = store.CreateProfile(p)

	// Initially no favourites.
	favs, err := store.ListFavourites()
	if err != nil {
		t.Fatalf("ListFavourites() error = %v", err)
	}
	if len(favs) != 0 {
		t.Fatalf("expected 0 favourites, got %d", len(favs))
	}

	// Mark as favourite.
	if err := store.SetFavourite(p.ID, true); err != nil {
		t.Fatalf("SetFavourite(true) error = %v", err)
	}
	favs, _ = store.ListFavourites()
	if len(favs) != 1 {
		t.Fatalf("expected 1 favourite, got %d", len(favs))
	}

	// Unmark.
	if err := store.SetFavourite(p.ID, false); err != nil {
		t.Fatalf("SetFavourite(false) error = %v", err)
	}
	favs, _ = store.ListFavourites()
	if len(favs) != 0 {
		t.Errorf("expected 0 favourites after unmark, got %d", len(favs))
	}
}

// ---------------------------------------------------------------------------
// Known hosts
// ---------------------------------------------------------------------------

func TestKnownHostCRUD(t *testing.T) {
	store := setupTestStore(t)
	now := time.Now()

	kh := &models.KnownHost{
		ID:          uuid.New().String(),
		Host:        "server.example.com",
		Port:        22,
		KeyType:     "ssh-ed25519",
		Fingerprint: "SHA256:abcdef1234567890",
		PublicKey:   "AAAAC3NzaC1lZDI1NTE5...",
		FirstSeen:   now,
		LastSeen:    now,
		Trusted:     true,
	}

	// Save and retrieve.
	if err := store.SaveKnownHost(kh); err != nil {
		t.Fatalf("SaveKnownHost() error = %v", err)
	}
	got, err := store.GetKnownHost("server.example.com", 22)
	if err != nil {
		t.Fatalf("GetKnownHost() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetKnownHost() returned nil")
	}
	if got.Fingerprint != kh.Fingerprint {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, kh.Fingerprint)
	}

	// List should contain it.
	hosts, err := store.ListKnownHosts()
	if err != nil {
		t.Fatalf("ListKnownHosts() error = %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("expected 1 known host, got %d", len(hosts))
	}

	// Delete.
	if err := store.DeleteKnownHost(kh.ID); err != nil {
		t.Fatalf("DeleteKnownHost() error = %v", err)
	}
	hosts, _ = store.ListKnownHosts()
	if len(hosts) != 0 {
		t.Errorf("expected 0 known hosts after delete, got %d", len(hosts))
	}
}

func TestGetKnownHostNotFound(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetKnownHost("no-such-host", 22)
	if err != nil {
		t.Fatalf("GetKnownHost() error = %v", err)
	}
	if got != nil {
		t.Error("expected nil for unknown host")
	}
}

// ---------------------------------------------------------------------------
// Preferences
// ---------------------------------------------------------------------------

func TestPreferencesDefaults(t *testing.T) {
	store := setupTestStore(t)

	// No preferences stored yet — should return nil.
	prefs, err := store.GetPreferences()
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs != nil {
		t.Error("expected nil preferences on empty database")
	}
}

func TestSavePreferencesDoesNotError(t *testing.T) {
	store := setupTestStore(t)

	prefs := models.DefaultAppPreferences()
	prefs.ConfirmOnClose = true
	prefs.DefaultFontSize = 16
	prefs.Theme = "custom-dark"

	if err := store.SavePreferences(&prefs); err != nil {
		t.Fatalf("SavePreferences() error = %v", err)
	}

	got, err := store.GetPreferences()
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetPreferences() returned nil after save")
	}
	if got.ConfirmOnClose != true {
		t.Errorf("ConfirmOnClose = %v, want true", got.ConfirmOnClose)
	}
	if got.DefaultFontSize != 16 {
		t.Errorf("DefaultFontSize = %d, want 16", got.DefaultFontSize)
	}
	if got.Theme != "custom-dark" {
		t.Errorf("Theme = %q, want %q", got.Theme, "custom-dark")
	}
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

func TestAuditEntries(t *testing.T) {
	store := setupTestStore(t)

	entry := &models.AuditEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Action:    "connect",
		ProfileID: "profile-1",
		SessionID: "session-1",
		Details:   "Connected via SSH to 10.0.0.1",
	}

	if err := store.AddAuditEntry(entry); err != nil {
		t.Fatalf("AddAuditEntry() error = %v", err)
	}

	entries, err := store.ListAuditEntries(10)
	if err != nil {
		t.Fatalf("ListAuditEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Action != "connect" {
		t.Errorf("Action = %q, want %q", entries[0].Action, "connect")
	}
	if entries[0].Details != "Connected via SSH to 10.0.0.1" {
		t.Errorf("Details = %q, want %q", entries[0].Details, "Connected via SSH to 10.0.0.1")
	}
}

func TestAuditEntriesLimit(t *testing.T) {
	store := setupTestStore(t)

	for i := 0; i < 5; i++ {
		_ = store.AddAuditEntry(&models.AuditEntry{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Action:    "test",
			Details:   "entry",
		})
	}

	entries, err := store.ListAuditEntries(3)
	if err != nil {
		t.Fatalf("ListAuditEntries() error = %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 audit entries with limit, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// UpdateLastConnected
// ---------------------------------------------------------------------------

func TestUpdateLastConnected(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("Connectable", "conn.local")
	_ = store.CreateProfile(p)

	// Initially last_connected should be nil.
	got, err := store.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if got.LastConnected != nil {
		t.Error("expected LastConnected to be nil initially")
	}

	// Update last connected.
	if err := store.UpdateLastConnected(p.ID); err != nil {
		t.Fatalf("UpdateLastConnected() error = %v", err)
	}

	got, err = store.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile() after update error = %v", err)
	}
	if got.LastConnected == nil {
		t.Fatal("expected LastConnected to be set after update")
	}
	// Should be within the last few seconds.
	elapsed := time.Since(*got.LastConnected)
	if elapsed > 5*time.Second {
		t.Errorf("LastConnected is too old: %v ago", elapsed)
	}
}

func TestUpdateLastConnectedNotFound(t *testing.T) {
	store := setupTestStore(t)
	if err := store.UpdateLastConnected("nonexistent"); err == nil {
		t.Error("expected error when updating non-existent profile")
	}
}

// ---------------------------------------------------------------------------
// Export / Import
// ---------------------------------------------------------------------------

func TestExportProfiles(t *testing.T) {
	store := setupTestStore(t)
	_ = store.CreateProfile(makeSSHProfile("Export A", "a.example.com"))
	_ = store.CreateProfile(makeSSHProfile("Export B", "b.example.com"))

	data, err := store.ExportProfiles(false)
	if err != nil {
		t.Fatalf("ExportProfiles() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExportProfiles() returned empty data")
	}

	// The exported JSON should be parseable as a slice of profiles.
	var profiles []*models.Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		t.Fatalf("failed to unmarshal exported data: %v", err)
	}
	if len(profiles) != 2 {
		t.Errorf("expected 2 exported profiles, got %d", len(profiles))
	}
}

func TestExportProfilesRedactsInlineSSHSecrets(t *testing.T) {
	store := setupTestStore(t)
	profile := makeSSHProfile("Export Secret", "secret.example.com")
	profile.SSH.ProxyPassword = "proxy-secret"
	profile.SSH.TOTPSecret = "totp-secret"
	if err := store.CreateProfile(profile); err != nil {
		t.Fatalf("CreateProfile() error = %v", err)
	}

	data, err := store.ExportProfiles(false)
	if err != nil {
		t.Fatalf("ExportProfiles(false) error = %v", err)
	}
	if strings.Contains(string(data), "proxy-secret") || strings.Contains(string(data), "totp-secret") {
		t.Fatalf("ExportProfiles(false) leaked inline SSH secrets: %s", string(data))
	}

	data, err = store.ExportProfiles(true)
	if err != nil {
		t.Fatalf("ExportProfiles(true) error = %v", err)
	}
	if !strings.Contains(string(data), "proxy-secret") || !strings.Contains(string(data), "totp-secret") {
		t.Fatalf("ExportProfiles(true) did not include inline SSH secrets: %s", string(data))
	}
}

func TestImportProfiles(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("Import Me", "import.local")
	_ = store.CreateProfile(p)

	// Export from source store.
	data, err := store.ExportProfiles(false)
	if err != nil {
		t.Fatalf("ExportProfiles() error = %v", err)
	}

	// Import into a fresh store.
	store2 := setupTestStore(t)
	n, err := store2.ImportProfiles(data, "skip")
	if err != nil {
		t.Fatalf("ImportProfiles() error = %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 imported profile, got %d", n)
	}

	profiles, _ := store2.ListProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile in target store, got %d", len(profiles))
	}
	if profiles[0].DisplayName != "Import Me" {
		t.Errorf("DisplayName = %q, want %q", profiles[0].DisplayName, "Import Me")
	}
}

func TestImportDuplicateHandling(t *testing.T) {
	store := setupTestStore(t)
	p := makeSSHProfile("Original", "orig.local")
	_ = store.CreateProfile(p)

	data, err := store.ExportProfiles(false)
	if err != nil {
		t.Fatalf("ExportProfiles() error = %v", err)
	}

	// "skip" strategy — importing same profile again should import 0.
	n, err := store.ImportProfiles(data, "skip")
	if err != nil {
		t.Fatalf("ImportProfiles(skip) error = %v", err)
	}
	if n != 0 {
		t.Errorf("skip strategy: expected 0 imports, got %d", n)
	}

	// "overwrite" strategy — should overwrite the existing profile.
	n, err = store.ImportProfiles(data, "overwrite")
	if err != nil {
		t.Fatalf("ImportProfiles(overwrite) error = %v", err)
	}
	if n != 1 {
		t.Errorf("overwrite strategy: expected 1 import, got %d", n)
	}
	// Total profiles should still be 1.
	all, _ := store.ListProfiles()
	if len(all) != 1 {
		t.Errorf("after overwrite: expected 1 profile, got %d", len(all))
	}

	// "duplicate" strategy — should create a new entry with a fresh ID.
	n, err = store.ImportProfiles(data, "duplicate")
	if err != nil {
		t.Fatalf("ImportProfiles(duplicate) error = %v", err)
	}
	if n != 1 {
		t.Errorf("duplicate strategy: expected 1 import, got %d", n)
	}
	all, _ = store.ListProfiles()
	if len(all) != 2 {
		t.Errorf("after duplicate: expected 2 profiles, got %d", len(all))
	}
}

func TestImportInvalidStrategy(t *testing.T) {
	store := setupTestStore(t)
	// The strategy check is inside the per-profile loop, so we need at least
	// one profile in the import data for it to trigger.
	data := []byte(`[{"id":"x","displayName":"X","protocol":"ssh","host":"h","port":22,"tags":[],"createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:00:00Z"}]`)
	_, err := store.ImportProfiles(data, "badstrategy")
	if err == nil {
		t.Error("expected error for invalid import strategy")
	}
}

func TestImportInvalidJSON(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.ImportProfiles([]byte("not json"), "skip")
	if err == nil {
		t.Error("expected error for invalid JSON import data")
	}
}

// ---------------------------------------------------------------------------
// Migration idempotency
// ---------------------------------------------------------------------------

func TestMigrateIdempotent(t *testing.T) {
	// NewStore already runs migrate() internally. Creating a second Store
	// against the same database verifies that migrations are idempotent.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate-test.db")

	s1, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("first NewStore() error = %v", err)
	}
	s1.Close()

	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("second NewStore() error = %v (migration not idempotent?)", err)
	}
	s2.Close()
}

func TestMigrateCreatesTablesForCRUD(t *testing.T) {
	// Verify that a fresh store can perform basic CRUD — proving that
	// migration created the required tables.
	store := setupTestStore(t)

	p := makeSSHProfile("Migration Test", "mig.local")
	if err := store.CreateProfile(p); err != nil {
		t.Fatalf("CreateProfile after migration failed: %v", err)
	}
	if _, err := store.GetProfile(p.ID); err != nil {
		t.Fatalf("GetProfile after migration failed: %v", err)
	}
	if _, err := store.ListKnownHosts(); err != nil {
		t.Fatalf("ListKnownHosts after migration failed: %v", err)
	}
	if _, err := store.ListAuditEntries(10); err != nil {
		t.Fatalf("ListAuditEntries after migration failed: %v", err)
	}
	if _, err := store.GetPreferences(); err != nil {
		t.Fatalf("GetPreferences after migration failed: %v", err)
	}
}
