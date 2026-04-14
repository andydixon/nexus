// Package preferences manages application preferences with in-memory caching
// and persistence to a backing store.
package preferences

import (
	"fmt"
	"sync"

	"github.com/copilot-ssh-client/app/internal/models"
)

// PreferenceStore is the persistence interface used by the Manager.
type PreferenceStore interface {
	GetPreferences() (*models.AppPreferences, error)
	SavePreferences(prefs *models.AppPreferences) error
}

// Manager handles application preferences with defaults and caching.
// It is safe for concurrent use.
type Manager struct {
	store PreferenceStore
	cache *models.AppPreferences
	mu    sync.RWMutex
}

// NewManager creates a preferences Manager backed by the given store.
// Call Load to populate the cache from disc.
func NewManager(store PreferenceStore) *Manager {
	return &Manager{store: store}
}

// Load reads preferences from the backing store into memory. If no
// preferences have been saved yet, sensible defaults are applied.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefs, err := m.store.GetPreferences()
	if err != nil {
		return fmt.Errorf("preferences: failed to load: %w", err)
	}

	if prefs == nil {
		defaults := models.DefaultAppPreferences()
		m.cache = &defaults
	} else {
		m.cache = prefs
		m.applyDefaults()
	}
	return nil
}

// Get returns the current preferences. If Load has not been called, defaults
// are returned.
func (m *Manager) Get() *models.AppPreferences {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.cache == nil {
		defaults := models.DefaultAppPreferences()
		return &defaults
	}
	// Return a copy to prevent callers mutating the cache.
	cp := *m.cache
	return &cp
}

// Save persists the given preferences to the backing store and updates the
// in-memory cache.
func (m *Manager) Save(prefs *models.AppPreferences) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.SavePreferences(prefs); err != nil {
		return fmt.Errorf("preferences: failed to save: %w", err)
	}
	cp := *prefs
	m.cache = &cp
	return nil
}

// applyDefaults fills in zero-valued fields from the default preferences.
// Caller must hold m.mu.
func (m *Manager) applyDefaults() {
	defaults := models.DefaultAppPreferences()
	if m.cache.Theme == "" {
		m.cache.Theme = defaults.Theme
	}
	if m.cache.DefaultProtocol == "" {
		m.cache.DefaultProtocol = defaults.DefaultProtocol
	}
	if m.cache.SidebarWidth == 0 {
		m.cache.SidebarWidth = defaults.SidebarWidth
	}
	if m.cache.DefaultFontFamily == "" {
		m.cache.DefaultFontFamily = defaults.DefaultFontFamily
	}
	if m.cache.DefaultFontSize == 0 {
		m.cache.DefaultFontSize = defaults.DefaultFontSize
	}
	if m.cache.DefaultScrollback == 0 {
		m.cache.DefaultScrollback = defaults.DefaultScrollback
	}
	if m.cache.DefaultCursorStyle == "" {
		m.cache.DefaultCursorStyle = defaults.DefaultCursorStyle
	}
}
