// Package knownhosts manages SSH known host entries, providing verification,
// storage, and management of host key fingerprints.
package knownhosts

import (
	"fmt"
	"time"

	"github.com/andydixon/link/internal/models"
	"github.com/google/uuid"
)

// KnownHostStorage is the persistence interface used by the Store.
type KnownHostStorage interface {
	GetKnownHost(host string, port int) (*models.KnownHost, error)
	SaveKnownHost(kh *models.KnownHost) error
	ListKnownHosts() ([]*models.KnownHost, error)
	DeleteKnownHost(id string) error
}

// Store manages SSH known host entries. It provides verification against
// previously accepted host keys and allows new keys to be trusted.
type Store struct {
	storage KnownHostStorage
}

// NewStore creates a Store backed by the given storage implementation.
func NewStore(storage KnownHostStorage) *Store {
	return &Store{storage: storage}
}

// Verify checks whether the presented host key matches a previously accepted
// entry. Returns true if the fingerprint matches, false if there is no record,
// and an error if the key has changed (possible MITM).
func (s *Store) Verify(host string, port int, keyType string, fingerprint string, publicKey string) (bool, error) {
	existing, err := s.storage.GetKnownHost(host, port)
	if err != nil {
		return false, fmt.Errorf("knownhosts: lookup failed: %w", err)
	}
	if existing == nil {
		// No prior record — the caller should prompt the user.
		return false, nil
	}

	if existing.Fingerprint == fingerprint {
		// Key matches — update the last-seen timestamp.
		existing.LastSeen = time.Now()
		_ = s.storage.SaveKnownHost(existing)
		return true, nil
	}

	// Fingerprint mismatch — the host key has changed.
	return false, fmt.Errorf(
		"knownhosts: host key for %s:%d has changed (was %s %s, now %s %s); "+
			"this could indicate a man-in-the-middle attack",
		host, port,
		existing.KeyType, existing.Fingerprint,
		keyType, fingerprint,
	)
}

// Accept stores a new host key as trusted. If an entry already exists for the
// host/port combination it is overwritten.
func (s *Store) Accept(host string, port int, keyType string, fingerprint string, publicKey string) error {
	now := time.Now()
	kh := &models.KnownHost{
		ID:          uuid.New().String(),
		Host:        host,
		Port:        port,
		KeyType:     keyType,
		Fingerprint: fingerprint,
		PublicKey:   publicKey,
		FirstSeen:   now,
		LastSeen:    now,
		Trusted:     true,
	}
	if err := s.storage.SaveKnownHost(kh); err != nil {
		return fmt.Errorf("knownhosts: failed to accept key: %w", err)
	}
	return nil
}

// List returns all stored known host entries.
func (s *Store) List() ([]*models.KnownHost, error) {
	entries, err := s.storage.ListKnownHosts()
	if err != nil {
		return nil, fmt.Errorf("knownhosts: failed to list entries: %w", err)
	}
	return entries, nil
}

// Delete removes a known host entry by ID.
func (s *Store) Delete(id string) error {
	if err := s.storage.DeleteKnownHost(id); err != nil {
		return fmt.Errorf("knownhosts: failed to delete entry: %w", err)
	}
	return nil
}
