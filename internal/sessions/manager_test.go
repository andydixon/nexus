package sessions

import (
	"testing"

	"github.com/andydixon/nexus/internal/models"
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
