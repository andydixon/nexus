package knownhosts

import (
	"testing"

	"github.com/andydixon/link/internal/models"
)

// ---------------------------------------------------------------------------
// Mock storage implementation
// ---------------------------------------------------------------------------

type mockStorage struct {
	hosts map[string]*models.KnownHost // keyed by "host:port"
}

func newMockStorage() *mockStorage {
	return &mockStorage{hosts: make(map[string]*models.KnownHost)}
}

func (m *mockStorage) key(host string, port int) string {
	return host + ":" + string(rune(port))
}

func (m *mockStorage) GetKnownHost(host string, port int) (*models.KnownHost, error) {
	kh, ok := m.hosts[m.key(host, port)]
	if !ok {
		return nil, nil
	}
	return kh, nil
}

func (m *mockStorage) SaveKnownHost(kh *models.KnownHost) error {
	m.hosts[m.key(kh.Host, kh.Port)] = kh
	return nil
}

func (m *mockStorage) ListKnownHosts() ([]*models.KnownHost, error) {
	result := make([]*models.KnownHost, 0, len(m.hosts))
	for _, kh := range m.hosts {
		result = append(result, kh)
	}
	return result, nil
}

func (m *mockStorage) DeleteKnownHost(id string) error {
	for k, kh := range m.hosts {
		if kh.ID == id {
			delete(m.hosts, k)
			return nil
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Verify unknown host returns false (no error)
// ---------------------------------------------------------------------------

func TestVerifyUnknownHost(t *testing.T) {
	store := NewStore(newMockStorage())

	trusted, err := store.Verify("unknown.example.com", 22, "ssh-ed25519", "SHA256:abc", "pubkey")
	if err != nil {
		t.Fatalf("Verify() unexpected error = %v", err)
	}
	if trusted {
		t.Error("expected trusted = false for unknown host")
	}
}

// ---------------------------------------------------------------------------
// Accept then verify returns true
// ---------------------------------------------------------------------------

func TestAcceptThenVerify(t *testing.T) {
	store := NewStore(newMockStorage())

	if err := store.Accept("server.example.com", 22, "ssh-ed25519", "SHA256:abc", "pubkey"); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	trusted, err := store.Verify("server.example.com", 22, "ssh-ed25519", "SHA256:abc", "pubkey")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !trusted {
		t.Error("expected trusted = true after Accept()")
	}
}

// ---------------------------------------------------------------------------
// Changed fingerprint returns error
// ---------------------------------------------------------------------------

func TestVerifyChangedFingerprint(t *testing.T) {
	store := NewStore(newMockStorage())

	// Accept with fingerprint A.
	if err := store.Accept("host.local", 22, "ssh-rsa", "SHA256:original", "pubkeyA"); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	// Verify with fingerprint B — should return an error indicating a change.
	trusted, err := store.Verify("host.local", 22, "ssh-rsa", "SHA256:changed", "pubkeyB")
	if err == nil {
		t.Fatal("expected error for changed fingerprint")
	}
	if trusted {
		t.Error("expected trusted = false for changed fingerprint")
	}
}

// ---------------------------------------------------------------------------
// List and Delete operations
// ---------------------------------------------------------------------------

func TestListAndDelete(t *testing.T) {
	mock := newMockStorage()
	store := NewStore(mock)

	_ = store.Accept("host-a.local", 22, "ssh-ed25519", "fp-a", "pk-a")
	_ = store.Accept("host-b.local", 22, "ssh-rsa", "fp-b", "pk-b")

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Delete the first entry.
	if err := store.Delete(entries[0].ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	entries, _ = store.List()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after delete, got %d", len(entries))
	}
}
