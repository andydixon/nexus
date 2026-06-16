package document

import (
	"errors"
	"sync"
)

// Slot identifies one of the documents in a session.
type Slot string

const (
	SlotOriginal Slot = "original"
	SlotCompare  Slot = "compare"
)

// Session tracks loaded documents and addresses them by slot.
// The original slot is the active working document; compare is
// used by the diff tooling.
type Session struct {
	mu        sync.RWMutex
	documents map[Slot]*Document
}

// NewSession returns an empty session.
func NewSession() *Session {
	return &Session{documents: make(map[Slot]*Document)}
}

// Load replaces the document in the given slot.
func (s *Session) Load(slot Slot, name string, src []byte) *Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := New(name, src)
	s.documents[slot] = d
	return d
}

// Get returns the document for a slot, or an error if not loaded.
func (s *Session) Get(slot Slot) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.documents[slot]
	if !ok {
		return nil, errors.New("slot not loaded: " + string(slot))
	}
	return d, nil
}

// Has reports whether a slot has a document loaded.
func (s *Session) Has(slot Slot) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.documents[slot]
	return ok
}

// Drop removes a slot.
func (s *Session) Drop(slot Slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.documents, slot)
}
