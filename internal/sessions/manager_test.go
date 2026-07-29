package sessions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andydixon/nexus/internal/audit"
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

func TestDisconnectRemovesSessionAndReleasesReferences(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	sess := &Session{
		Info: models.SessionInfo{
			ID:    "session-1",
			State: models.SessionConnected,
		},
		profile: &models.Profile{ID: "profile-1"},
	}
	mgr.sessions[sess.Info.ID] = sess

	if err := mgr.Disconnect(sess.Info.ID); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if got := len(mgr.ListSessions()); got != 0 {
		t.Fatalf("ListSessions() returned %d sessions after disconnect, want 0", got)
	}
	if !sess.closed {
		t.Fatal("disconnected session was not marked closed")
	}
	if sess.profile != nil {
		t.Fatal("disconnected session retained its profile")
	}
}

func TestRepeatedDisconnectDoesNotRetainSessions(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)

	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("session-%d", i)
		mgr.sessions[id] = &Session{
			Info: models.SessionInfo{
				ID:    id,
				State: models.SessionConnected,
			},
		}
		if err := mgr.Disconnect(id); err != nil {
			t.Fatalf("Disconnect(%q) error = %v", id, err)
		}
	}

	if got := len(mgr.ListSessions()); got != 0 {
		t.Fatalf("ListSessions() retained %d explicitly closed sessions, want 0", got)
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

func TestTerminalDimension(t *testing.T) {
	if got := terminalDimension(0, 80); got != 80 {
		t.Fatalf("terminalDimension(0, 80) = %d, want 80", got)
	}
	if got := terminalDimension(132, 80); got != 132 {
		t.Fatalf("terminalDimension(132, 80) = %d, want 132", got)
	}
	if got := terminalDimension(100000, 80); got != ^uint16(0) {
		t.Fatalf("terminalDimension(100000, 80) = %d, want %d", got, ^uint16(0))
	}
}

func TestRestartLocalReusesSessionAndAppliesRequestedSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local PTY test requires a Unix shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "local-shell.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nstty size\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	auditStore := &sessionAuditStore{}
	mgr := NewManager(nil, nil, nil, audit.NewLogger(auditStore), nil)
	var outputMu sync.Mutex
	var output strings.Builder
	disconnected := make(chan string, 2)
	mgr.SetEventSink(func(name string, payload interface{}) {
		values, ok := payload.(map[string]interface{})
		if !ok {
			return
		}
		if name == "session:data" {
			encoded, _ := values["data"].(string)
			decoded, _ := base64.StdEncoding.DecodeString(encoded)
			outputMu.Lock()
			_, _ = output.Write(decoded)
			outputMu.Unlock()
		}
		if name == "session:state" && values["state"] == string(models.SessionDisconnected) {
			disconnected <- values["sessionID"].(string)
		}
	})

	sessionID, err := mgr.ConnectLocal(script)
	if err != nil {
		t.Fatalf("ConnectLocal() error = %v", err)
	}
	waitForDisconnectedSession(t, disconnected, sessionID)

	if err := mgr.RestartLocal(sessionID, 132, 43); err != nil {
		t.Fatalf("RestartLocal() error = %v", err)
	}
	waitForDisconnectedSession(t, disconnected, sessionID)

	info := mgr.GetSessionInfo(sessionID)
	if info == nil || info.ID != sessionID || info.State != models.SessionDisconnected {
		t.Fatalf("session after restart = %#v, want same disconnected session", info)
	}
	outputMu.Lock()
	got := strings.ReplaceAll(output.String(), "\r", "")
	outputMu.Unlock()
	if !strings.Contains(got, "43 132\n") {
		t.Fatalf("local output %q does not contain restarted size 43 132", got)
	}
}

func waitForDisconnectedSession(t *testing.T, states <-chan string, want string) {
	t.Helper()
	select {
	case got := <-states:
		if got != want {
			t.Fatalf("disconnected session = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for session %q to disconnect", want)
	}
}

type sessionAuditStore struct {
	mu      sync.Mutex
	entries []*models.AuditEntry
}

func (s *sessionAuditStore) AddAuditEntry(entry *models.AuditEntry) error {
	s.mu.Lock()
	s.entries = append(s.entries, entry)
	s.mu.Unlock()
	return nil
}

func (s *sessionAuditStore) ListAuditEntries(limit int) ([]*models.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*models.AuditEntry(nil), s.entries...), nil
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

func TestHandleHostKeyResponseRoutesByRequestID(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	ch := make(chan bool, 1)
	mgr.hostKeyChans = map[string]chan bool{"request-1": ch}

	mgr.handleHostKeyResponse(map[string]interface{}{
		"requestID": "request-1",
		"accepted":  true,
	})

	select {
	case accepted := <-ch:
		if !accepted {
			t.Fatal("accepted = false, want true")
		}
	default:
		t.Fatal("expected host key response to be routed")
	}
}

func TestHandleHostKeyResponseIgnoresOtherRequestIDs(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	ch := make(chan bool, 1)
	mgr.hostKeyChans = map[string]chan bool{"request-1": ch}

	mgr.handleHostKeyResponse(map[string]interface{}{
		"requestID": "request-2",
		"accepted":  true,
	})

	select {
	case <-ch:
		t.Fatal("unexpected host key response routed")
	default:
	}
}

func TestHandleSecretResponseRoutesByRequestID(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	ch := make(chan secretPromptResponse, 1)
	mgr.secretChans = map[string]chan secretPromptResponse{"request-1": ch}

	mgr.handleSecretResponse(map[string]interface{}{
		"requestID": "request-1",
		"value":     "secret",
		"cancelled": false,
	})

	select {
	case response := <-ch:
		if response.value != "secret" || response.cancelled {
			t.Fatalf("response = %#v, want non-cancelled secret", response)
		}
	default:
		t.Fatal("expected secret response to be routed")
	}
}

func TestInteractiveSecretPromptIsReleasedByReconnectCancellation(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	prompts := make(chan map[string]interface{}, 1)
	mgr.SetEventSink(func(name string, data interface{}) {
		if name == "session:secret:prompt" {
			prompts <- data.(map[string]interface{})
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := mgr.buildInteractiveSecretCallback(ctx, "session-1")("Password")
		result <- err
	}()

	select {
	case <-prompts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for secret prompt")
	}
	cancel()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("callback error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("secret prompt did not stop after reconnect cancellation")
	}

	mgr.secretMu.Lock()
	pending := len(mgr.secretChans)
	mgr.secretMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending secret prompts = %d, want 0", pending)
	}
}

func TestInteractiveSecretPromptHonoursFrontendCancellation(t *testing.T) {
	mgr := NewManager(nil, nil, nil, nil, nil)
	prompts := make(chan map[string]interface{}, 1)
	mgr.SetEventSink(func(name string, data interface{}) {
		if name == "session:secret:prompt" {
			prompts <- data.(map[string]interface{})
		}
	})

	result := make(chan error, 1)
	go func() {
		_, err := mgr.buildInteractiveSecretCallback(context.Background(), "session-1")("Password")
		result <- err
	}()

	var requestID string
	select {
	case prompt := <-prompts:
		requestID, _ = prompt["requestID"].(string)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for secret prompt")
	}
	mgr.RespondSecret(requestID, "", true)

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("callback error = %v, want frontend cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("secret prompt did not stop after frontend cancellation")
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
