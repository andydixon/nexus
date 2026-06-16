// Package recording provides session recording in asciicast v2 format.
// Recordings capture terminal I/O with timestamps, enabling playback of
// complete sessions for auditing, training, or troubleshooting.
//
// File format: https://docs.asciinema.org/manual/asciicast/v2/
//
// The header line is a JSON object with metadata (version, dimensions, etc.)
// followed by one JSON array per event: [elapsed_secs, event_type, data].
package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EventType distinguishes input from output events.
type EventType string

const (
	// EventOutput represents data sent from the remote host to the terminal.
	EventOutput EventType = "o"
	// EventInput represents data sent from the user to the remote host.
	EventInput EventType = "i"

	// MaxRecordingBytes bounds a single active recording file.
	MaxRecordingBytes = int64(250 * 1024 * 1024)
	// MaxPlaybackBytes bounds the file size loaded for playback.
	MaxPlaybackBytes = int64(100 * 1024 * 1024)
)

// Header is the asciicast v2 file header (first line).
type Header struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Event is a single recorded I/O event: [elapsed_seconds, type, data].
type Event struct {
	Elapsed float64   `json:"-"`
	Type    EventType `json:"-"`
	Data    string    `json:"-"`
}

// MarshalJSON serialises an event as the asciicast v2 array format.
func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal([]interface{}{e.Elapsed, string(e.Type), e.Data})
}

// RecordingMeta describes a saved recording without loading the full data.
type RecordingMeta struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Duration  float64   `json:"duration"` // seconds
	Size      int64     `json:"size"`     // file size in bytes
	CreatedAt time.Time `json:"createdAt"`
	FilePath  string    `json:"filePath"`
}

// Recorder captures terminal I/O for a single session.
type Recorder struct {
	mu           sync.Mutex
	file         *os.File
	startTime    time.Time
	header       Header
	eventCount   int
	closed       bool
	filePath     string
	bytesWritten int64
	maxBytes     int64
}

// NewRecorder creates a new recording file and writes the header.
func NewRecorder(dir, id, title string, cols, rows int) (*Recorder, error) {
	return newRecorderWithLimit(dir, id, title, cols, rows, MaxRecordingBytes)
}

func newRecorderWithLimit(dir, id, title string, cols, rows int, maxBytes int64) (*Recorder, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("recording: failed to create directory: %w", err)
	}

	filename := fmt.Sprintf("%s.cast", id)
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("recording: failed to create file: %w", err)
	}

	now := time.Now()
	hdr := Header{
		Version:   2,
		Width:     cols,
		Height:    rows,
		Timestamp: now.Unix(),
		Title:     title,
		Env: map[string]string{
			"TERM": "xterm-256color",
		},
	}

	headerLine, err := marshalJSONLine(hdr)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("recording: failed to encode header: %w", err)
	}
	n, err := f.Write(headerLine)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("recording: failed to write header: %w", err)
	}

	return &Recorder{
		file:         f,
		startTime:    now,
		header:       hdr,
		filePath:     path,
		bytesWritten: int64(n),
		maxBytes:     maxBytes,
	}, nil
}

// WriteOutput records terminal output (data from remote host).
func (r *Recorder) WriteOutput(data []byte) {
	r.writeEvent(EventOutput, data)
}

// WriteInput records terminal input (data from user).
func (r *Recorder) WriteInput(data []byte) {
	r.writeEvent(EventInput, data)
}

// writeEvent appends a timestamped event to the recording file.
func (r *Recorder) writeEvent(eventType EventType, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed || r.file == nil {
		return
	}

	elapsed := time.Since(r.startTime).Seconds()
	evt := Event{
		Elapsed: elapsed,
		Type:    eventType,
		Data:    string(data),
	}

	line, err := marshalJSONLine(evt)
	if err != nil {
		return
	}
	if r.maxBytes > 0 && r.bytesWritten+int64(len(line)) > r.maxBytes {
		r.closed = true
		_ = r.file.Sync()
		_ = r.file.Close()
		r.file = nil
		return
	}

	n, err := r.file.Write(line)
	if err != nil {
		return
	}
	r.bytesWritten += int64(n)
	r.eventCount++
}

// UpdateSize records a terminal resize (not part of asciicast v2 events,
// but useful for future playback fidelity). Currently a no-op placeholder.
func (r *Recorder) UpdateSize(cols, rows int) {
	// asciicast v2 does not have a standard resize event. Future extension
	// could use a custom event type like "r" with JSON dimensions.
}

// Close flushes and closes the recording file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if r.file != nil {
		if err := r.file.Sync(); err != nil {
			r.file.Close()
			return fmt.Errorf("recording: sync failed: %w", err)
		}
		err := r.file.Close()
		r.file = nil
		return err
	}
	return nil
}

// Duration returns the elapsed time since recording started.
func (r *Recorder) Duration() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.startTime).Seconds()
}

// FilePath returns the absolute path to the recording file.
func (r *Recorder) FilePath() string {
	return r.filePath
}

// EventCount returns the number of events recorded so far.
func (r *Recorder) EventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eventCount
}

// ---------------------------------------------------------------------------
// Recording store — manages listing, reading, and deleting recordings.
// ---------------------------------------------------------------------------

// Store manages the recording directory.
type Store struct {
	dir string
}

// NewStore creates a recording store at the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the recordings directory path.
func (s *Store) Dir() string {
	return s.dir
}

// List returns metadata for all recordings in the store.
func (s *Store) List() ([]RecordingMeta, error) {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("recording: failed to read directory: %w", err)
	}

	var results []RecordingMeta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cast") {
			continue
		}

		meta, err := s.readMeta(entry.Name())
		if err != nil {
			continue // skip unreadable files
		}
		results = append(results, *meta)
	}

	// Sort by creation time, newest first.
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	return results, nil
}

// Get reads the full recording (header + events) from a file.
func (s *Store) Get(id string) (*Header, []Event, error) {
	return s.getWithLimit(id, MaxPlaybackBytes)
}

func (s *Store) getWithLimit(id string, maxBytes int64) (*Header, []Event, error) {
	path, err := s.recordingPath(id)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("recording: failed to stat file: %w", err)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, nil, fmt.Errorf("recording: file is too large for playback (max %d bytes)", maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("recording: failed to read file: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return nil, nil, fmt.Errorf("recording: empty file")
	}

	// Parse header (first line).
	var hdr Header
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		return nil, nil, fmt.Errorf("recording: invalid header: %w", err)
	}

	// Parse events (remaining lines).
	events := make([]Event, 0, len(lines)-1)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if len(raw) != 3 {
			continue
		}

		var elapsed float64
		var evtType, evtData string
		if err := json.Unmarshal(raw[0], &elapsed); err != nil {
			continue
		}
		if err := json.Unmarshal(raw[1], &evtType); err != nil {
			continue
		}
		if err := json.Unmarshal(raw[2], &evtData); err != nil {
			continue
		}

		events = append(events, Event{
			Elapsed: elapsed,
			Type:    EventType(evtType),
			Data:    evtData,
		})
	}

	return &hdr, events, nil
}

// Delete removes a recording file.
func (s *Store) Delete(id string) error {
	path, err := s.recordingPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("recording: failed to delete: %w", err)
	}
	return nil
}

// StorageInfo summarizes recording storage usage.
type StorageInfo struct {
	Count             int        `json:"count"`
	TotalBytes        int64      `json:"totalBytes"`
	OldestCreatedAt   *time.Time `json:"oldestCreatedAt,omitempty"`
	NewestCreatedAt   *time.Time `json:"newestCreatedAt,omitempty"`
	MaxRecordingBytes int64      `json:"maxRecordingBytes"`
	MaxPlaybackBytes  int64      `json:"maxPlaybackBytes"`
}

// CleanupResult describes files removed by retention cleanup.
type CleanupResult struct {
	Deleted    int   `json:"deleted"`
	FreedBytes int64 `json:"freedBytes"`
}

// StorageInfo returns aggregate metadata for recordings.
func (s *Store) StorageInfo() (StorageInfo, error) {
	metas, err := s.List()
	if err != nil {
		return StorageInfo{}, err
	}
	info := StorageInfo{
		Count:             len(metas),
		MaxRecordingBytes: MaxRecordingBytes,
		MaxPlaybackBytes:  MaxPlaybackBytes,
	}
	for i := range metas {
		meta := metas[i]
		info.TotalBytes += meta.Size
		if info.OldestCreatedAt == nil || meta.CreatedAt.Before(*info.OldestCreatedAt) {
			t := meta.CreatedAt
			info.OldestCreatedAt = &t
		}
		if info.NewestCreatedAt == nil || meta.CreatedAt.After(*info.NewestCreatedAt) {
			t := meta.CreatedAt
			info.NewestCreatedAt = &t
		}
	}
	return info, nil
}

// Cleanup removes recordings older than maxAgeDays and/or enough oldest files
// to bring total storage below maxTotalBytes. Non-positive limits are ignored.
func (s *Store) Cleanup(maxAgeDays int, maxTotalBytes int64) (CleanupResult, error) {
	metas, err := s.List()
	if err != nil {
		return CleanupResult{}, err
	}

	result := CleanupResult{}
	deleted := map[string]bool{}
	total := int64(0)
	for _, meta := range metas {
		total += meta.Size
	}

	deleteMeta := func(meta RecordingMeta) {
		if deleted[meta.ID] {
			return
		}
		if err := s.Delete(meta.ID); err == nil {
			deleted[meta.ID] = true
			result.Deleted++
			result.FreedBytes += meta.Size
			total -= meta.Size
		}
	}

	if maxAgeDays > 0 {
		cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
		for _, meta := range metas {
			if meta.CreatedAt.Before(cutoff) {
				deleteMeta(meta)
			}
		}
	}

	if maxTotalBytes > 0 && total > maxTotalBytes {
		sort.Slice(metas, func(i, j int) bool {
			return metas[i].CreatedAt.Before(metas[j].CreatedAt)
		})
		for _, meta := range metas {
			if total <= maxTotalBytes {
				break
			}
			deleteMeta(meta)
		}
	}

	return result, nil
}

func (s *Store) recordingPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("recording: invalid recording ID")
	}
	return filepath.Join(s.dir, id+".cast"), nil
}

func marshalJSONLine(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readMeta reads just the header from a .cast file to produce metadata.
func (s *Store) readMeta(filename string) (*RecordingMeta, error) {
	path := filepath.Join(s.dir, filename)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	var hdr Header
	dec := json.NewDecoder(f)
	if err := dec.Decode(&hdr); err != nil {
		return nil, err
	}

	// Calculate duration by scanning for last event timestamp.
	var duration float64
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil || len(arr) < 1 {
			continue
		}
		var elapsed float64
		if err := json.Unmarshal(arr[0], &elapsed); err == nil {
			duration = elapsed
		}
	}

	id := strings.TrimSuffix(filename, ".cast")

	return &RecordingMeta{
		ID:        id,
		SessionID: id, // recording ID matches session ID by default
		Title:     hdr.Title,
		Width:     hdr.Width,
		Height:    hdr.Height,
		Duration:  duration,
		Size:      info.Size(),
		CreatedAt: time.Unix(hdr.Timestamp, 0),
		FilePath:  path,
	}, nil
}
