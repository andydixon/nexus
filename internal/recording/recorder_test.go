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

func TestRecorderStopsAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	rec, err := newRecorderWithLimit(dir, "limited", "Limited", 80, 24, 512)
	if err != nil {
		t.Fatalf("newRecorderWithLimit failed: %v", err)
	}

	for i := 0; i < 50; i++ {
		rec.WriteOutput([]byte(strings.Repeat("x", 80)))
	}
	if rec.EventCount() >= 50 {
		t.Fatalf("expected recording to stop before all events were written")
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "limited.cast"))
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Size() > 512 {
		t.Fatalf("recording size = %d, want <= 512", info.Size())
	}
}

func TestStoreGetRejectsOversizedPlayback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.cast")
	header := `{"version":2,"width":80,"height":24,"timestamp":1}` + "\n"
	if err := os.WriteFile(path, []byte(header+strings.Repeat("x", 256)), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	store := NewStore(dir)
	_, _, err := store.getWithLimit("huge", 128)
	if err == nil {
		t.Fatal("expected oversized playback to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want too large", err.Error())
	}
}

func TestStoreCleanupByAgeAndSize(t *testing.T) {
	dir := t.TempDir()
	makeRecording := func(id string, created time.Time, payload string) {
		rec, err := NewRecorder(dir, id, id, 80, 24)
		if err != nil {
			t.Fatalf("NewRecorder(%s) failed: %v", id, err)
		}
		rec.WriteOutput([]byte(payload))
		if err := rec.Close(); err != nil {
			t.Fatalf("Close(%s) failed: %v", id, err)
		}
		path := filepath.Join(dir, id+".cast")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) failed: %v", id, err)
		}
		parts := strings.SplitN(string(data), "\n", 2)
		var hdr Header
		if err := json.Unmarshal([]byte(parts[0]), &hdr); err != nil {
			t.Fatalf("header parse(%s) failed: %v", id, err)
		}
		hdr.Timestamp = created.Unix()
		line, err := marshalJSONLine(hdr)
		if err != nil {
			t.Fatalf("header encode(%s) failed: %v", id, err)
		}
		rest := ""
		if len(parts) == 2 {
			rest = parts[1]
		}
		if err := os.WriteFile(path, append(line, []byte(rest)...), 0600); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", id, err)
		}
		if err := os.Chtimes(path, created, created); err != nil {
			t.Fatalf("Chtimes(%s) failed: %v", id, err)
		}
	}

	old := time.Now().Add(-45 * 24 * time.Hour)
	recent := time.Now().Add(-2 * time.Hour)
	makeRecording("old", old, "old")
	makeRecording("recent", recent, strings.Repeat("recent", 50))

	store := NewStore(dir)
	result, err := store.Cleanup(30, 0)
	if err != nil {
		t.Fatalf("Cleanup by age failed: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.cast")); !os.IsNotExist(err) {
		t.Fatalf("old recording still exists, stat err=%v", err)
	}

	info, err := store.StorageInfo()
	if err != nil {
		t.Fatalf("StorageInfo failed: %v", err)
	}
	result, err = store.Cleanup(0, info.TotalBytes-1)
	if err != nil {
		t.Fatalf("Cleanup by size failed: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
}

func TestStoreRejectsTraversalRecordingID(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Delete("../outside"); err == nil {
		t.Fatal("expected traversal recording ID to be rejected")
	}
}
