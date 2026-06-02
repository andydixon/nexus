package sessions

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/andydixon/nexus/internal/models"
	"github.com/andydixon/nexus/internal/storage"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// NewManager creates with empty sessions map
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	// NewManager requires backing services; pass nil for lightweight tests
	// where no actual connections are made.
	mgr := NewManager(nil, nil, nil, nil, nil)
	if mgr == nil {
		t.Fatal("NewManager() returned nil")
	}
	if mgr.sessions == nil {
		t.Fatal("expected sessions map to be initialised")
	}
	if len(mgr.sessions) != 0 {
		t.Errorf("expected empty sessions map, got %d entries", len(mgr.sessions))
	}
}

// ---------------------------------------------------------------------------
// ListSessions on an empty manager
// ---------------------------------------------------------------------------

func TestListSessionsEmpty(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	sessions := mgr.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// ---------------------------------------------------------------------------
// createTabName logic
// ---------------------------------------------------------------------------

func TestCreateTabName(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)

	tests := []struct {
		name    string
		profile *models.Profile
		want    string
	}{
		{
			name: "uses display name when set",
			profile: &models.Profile{
				DisplayName: "Production DB",
				Host:        "db.prod.com",
				Username:    "admin",
			},
			want: "Production DB",
		},
		{
			name: "falls back to user@host when no display name",
			profile: &models.Profile{
				DisplayName: "",
				Host:        "server.local",
				Username:    "root",
			},
			want: "root@server.local",
		},
		{
			name: "falls back to host only when no display name or username",
			profile: &models.Profile{
				DisplayName: "",
				Host:        "switch.local",
				Username:    "",
			},
			want: "switch.local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mgr.createTabName(tt.profile)
			if got != tt.want {
				t.Errorf("createTabName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RenameSession on non-existent session
// ---------------------------------------------------------------------------

func TestRenameSessionNotFound(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	err := mgr.RenameSession("nonexistent-id", "New Name")
	if err == nil {
		t.Error("expected error when renaming non-existent session")
	}
}

// ---------------------------------------------------------------------------
// PinSession on non-existent session
// ---------------------------------------------------------------------------

func TestPinSessionNotFound(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	err := mgr.PinSession("nonexistent-id", true)
	if err == nil {
		t.Error("expected error when pinning non-existent session")
	}
}

// ---------------------------------------------------------------------------
// GetSession on non-existent session
// ---------------------------------------------------------------------------

func TestGetSessionNotFound(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	_, err := mgr.GetSession("nonexistent-id")
	if err == nil {
		t.Error("expected error when getting non-existent session")
	}
}

// ---------------------------------------------------------------------------
// Disconnect on non-existent session
// ---------------------------------------------------------------------------

func TestDisconnectNotFound(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	err := mgr.Disconnect("nonexistent-id")
	if err == nil {
		t.Error("expected error when disconnecting non-existent session")
	}
}

// ---------------------------------------------------------------------------
// CloseAll on empty manager does not panic
// ---------------------------------------------------------------------------

func TestCloseAllEmpty(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	// Should not panic.
	mgr.CloseAll()
	if len(mgr.ListSessions()) != 0 {
		t.Error("expected 0 sessions after CloseAll")
	}
}

func TestHostKeyCallbackAcceptNewStoresHost(t *testing.T) {
	store := newSessionTestStore(t)
	mgr := NewManager(store, nil, nil, nil, nil)
	callback := mgr.hostKeyCallback("session-1", models.HostKeyAcceptNew)

	key := testPublicKey(t)
	if err := callback("jump.example:2222", nil, key); err != nil {
		t.Fatalf("HostKeyCallback() error = %v", err)
	}

	saved, err := store.GetKnownHost("jump.example", 2222)
	if err != nil {
		t.Fatalf("GetKnownHost() error = %v", err)
	}
	if saved == nil {
		t.Fatal("expected host key to be saved")
	}
	if saved.Host != "jump.example" {
		t.Fatalf("saved host = %q, want jump.example", saved.Host)
	}
	if saved.Port != 2222 {
		t.Fatalf("saved port = %d, want 2222", saved.Port)
	}
}

func TestHostKeyCallbackStrictRejectsUnknownHost(t *testing.T) {
	mgr := NewManager(newSessionTestStore(t), nil, nil, nil, nil)
	callback := mgr.hostKeyCallback("session-1", models.HostKeyStrict)

	err := callback("unknown.example:22", nil, testPublicKey(t))
	if err == nil {
		t.Fatal("expected strict policy to reject unknown host")
	}
}

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	return key
}

func newSessionTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
