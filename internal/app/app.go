// Package app provides the core application lifecycle for Nexus Terminal.
// It wires together storage, secrets, sessions, preferences, known hosts,
// and the audit log, and exposes Wails lifecycle hooks.
package app

import (
	"context"
	"log"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/andydixon/nexus/internal/api"
	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/config"
	"github.com/andydixon/nexus/internal/knownhosts"
	"github.com/andydixon/nexus/internal/preferences"
	"github.com/andydixon/nexus/internal/recording"
	"github.com/andydixon/nexus/internal/secrets"
	"github.com/andydixon/nexus/internal/sessions"
	"github.com/andydixon/nexus/internal/storage"
)

// App is the central application struct. Wails binds its lifecycle methods
// (Startup, Shutdown, DomReady) and it holds references to every subsystem.
type App struct {
	ctx context.Context
	mu  sync.Mutex

	Config         *config.AppConfig
	Store          *storage.Store
	SecretStore    *secrets.SecretStore
	KeychainStore  *secrets.KeychainStore
	SessionManager *sessions.Manager
	Prefs          *preferences.Manager
	KnownHosts     *knownhosts.Store
	AuditLog       *audit.Logger
	Service        *api.Service
}

// NewApp creates a new App instance. Subsystems are not initialised until
// Startup is called by the Wails runtime. The API service is pre-created
// so it can be bound by Wails before Startup runs.
func NewApp() *App {
	a := &App{}
	// The API service is created eagerly so Wails can bind it. Its backing
	// stores are nil at this point; they are wired during Startup.
	a.Service = &api.Service{}
	return a
}

// APIService returns the Wails-bound API service instance. This must be
// called before wails.Run so the binding is registered.
func (a *App) APIService() *api.Service {
	return a.Service
}

// Startup is called by Wails when the application starts. It loads
// configuration, ensures required directories exist, and initialises all
// subsystems (storage, secret store, session manager, preferences).
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ctx = ctx

	// Load platform-specific configuration paths.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}
	a.Config = cfg

	if err := cfg.EnsureDirectories(); err != nil {
		log.Fatalf("failed to create application directories: %v", err)
	}

	// Initialise SQLite storage (migrations run automatically on open).
	store, err := storage.NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open storage: %v", err)
	}
	a.Store = store

	// Initialise secret stores (keychain preferred, encrypted file fallback).
	a.SecretStore = secrets.NewSecretStore(cfg.SecretPath)
	a.KeychainStore = secrets.NewKeychainStore("NexusTerminal")

	// Initialise the audit logger backed by SQLite.
	a.AuditLog = audit.NewLogger(store)

	// Initialise known hosts store.
	a.KnownHosts = knownhosts.NewStore(store)

	// Initialise preferences manager.
	a.Prefs = preferences.NewManager(store)
	if err := a.Prefs.Load(); err != nil {
		log.Printf("warning: failed to load preferences, using defaults: %v", err)
	}

	// Initialise session recording store.
	recordDir := filepath.Join(cfg.DataDir, "recordings")
	recordStore := recording.NewStore(recordDir)

	// Initialise the session manager.
	a.SessionManager = sessions.NewManager(store, a.SecretStore, a.KeychainStore, a.AuditLog, recordStore)
	a.SessionManager.SetContext(ctx)

	// Wire the API service with all backing stores now that they exist.
	*a.Service = *api.NewService(store, a.SecretStore, a.KeychainStore, a.SessionManager, a.AuditLog, a.Prefs)
	a.Service.SetContext(ctx)

	log.Printf("Nexus Terminal initialised – data directory: %s", cfg.DataDir)
}

// Shutdown is called by Wails when the application is closing. It ensures all
// sessions are cleanly terminated and persistent storage is flushed.
func (a *App) Shutdown(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.SessionManager != nil {
		a.SessionManager.CloseAll()
	}

	if a.Store != nil {
		if err := a.Store.Close(); err != nil {
			log.Printf("warning: error closing storage: %v", err)
		}
	}

	// Lock the secret store to zero key material.
	if a.SecretStore != nil {
		a.SecretStore.Lock()
	}

	log.Println("Nexus Terminal shut down cleanly")
}

// DomReady is called by Wails once the frontend DOM is fully loaded. It emits
// a "backend:ready" event so the frontend knows the backend is prepared.
func (a *App) DomReady(ctx context.Context) {
	runtime.EventsEmit(ctx, "backend:ready", map[string]interface{}{
		"message": "Backend is ready",
	})
	log.Println("DOM ready – backend:ready event emitted")
}
