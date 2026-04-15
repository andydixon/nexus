package recording

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderWriteAndClose(t *testing.T) {
	dir := t.TempDir()
	id := "test-session-001"

	rec, err := NewRecorder(dir, id, "Test Recording", 80, 24)
	if err != nil {
		t.Fatalf("NewRecorder failed: %v", err)
	}

	// Write some output events.
	rec.WriteOutput([]byte("Welcome to the server\r\n"))
	rec.WriteOutput([]byte("$ "))

	// Write some input events.
	rec.WriteInput([]byte("ls -la\r\n"))

	// Write more output.
	rec.WriteOutput([]byte("total 42\r\n"))

	if rec.EventCount() != 4 {
		t.Errorf("expected 4 events, got %d", rec.EventCount())
	}

	if err := rec.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the file exists and has correct format.
	path := filepath.Join(dir, id+".cast")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read recording: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 5 { // 1 header + 4 events
		t.Fatalf("expected at least 5 lines, got %d", len(lines))
	}

	// Verify header.
	var hdr Header
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("failed to parse header: %v", err)
	}
	if hdr.Version != 2 {
		t.Errorf("expected version 2, got %d", hdr.Version)
	}
	if hdr.Width != 80 || hdr.Height != 24 {
		t.Errorf("expected 80x24, got %dx%d", hdr.Width, hdr.Height)
	}

	// Verify first event is output.
	var evt []json.RawMessage
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatalf("failed to parse first event: %v", err)
	}
	if len(evt) != 3 {
		t.Fatalf("expected 3-element array, got %d", len(evt))
	}
	var evtType string
	json.Unmarshal(evt[1], &evtType)
	if evtType != "o" {
		t.Errorf("expected event type 'o', got %q", evtType)
	}
}

func TestStoreListAndGet(t *testing.T) {
	dir := t.TempDir()

	// Create a recording.
	rec, err := NewRecorder(dir, "session-abc", "My Session", 120, 40)
	if err != nil {
		t.Fatalf("NewRecorder failed: %v", err)
	}
	rec.WriteOutput([]byte("hello world\r\n"))
	time.Sleep(10 * time.Millisecond)
	rec.WriteOutput([]byte("done\r\n"))
	rec.Close()

	// Use the store to list.
	store := NewStore(dir)
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(metas))
	}
	if metas[0].ID != "session-abc" {
		t.Errorf("expected ID 'session-abc', got %q", metas[0].ID)
	}
	if metas[0].Width != 120 || metas[0].Height != 40 {
		t.Errorf("unexpected dimensions: %dx%d", metas[0].Width, metas[0].Height)
	}

	// Read full recording.
	hdr, events, err := store.Get("session-abc")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if hdr.Title != "My Session" {
		t.Errorf("expected title 'My Session', got %q", hdr.Title)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()

	rec, err := NewRecorder(dir, "to-delete", "Delete Me", 80, 24)
	if err != nil {
		t.Fatalf("NewRecorder failed: %v", err)
	}
	rec.WriteOutput([]byte("test\n"))
	rec.Close()

	store := NewStore(dir)

	// Verify it exists.
	metas, _ := store.List()
	if len(metas) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(metas))
	}

	// Delete.
	if err := store.Delete("to-delete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone.
	metas, _ = store.List()
	if len(metas) != 0 {
		t.Errorf("expected 0 recordings, got %d", len(metas))
	}
}

func TestDoubleClose(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewRecorder(dir, "double-close", "Test", 80, 24)
	if err != nil {
		t.Fatalf("NewRecorder failed: %v", err)
	}
	rec.WriteOutput([]byte("test"))

	if err := rec.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	// Second close should be a no-op.
	if err := rec.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}
