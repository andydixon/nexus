// Package app provides the core application lifecycle for Nexus Terminal.
// It wires together storage, secrets, sessions, preferences, known hosts,
// and the audit log, and exposes Wails lifecycle hooks.
package app

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/copilot-ssh-client/app/internal/config"
	"github.com/copilot-ssh-client/app/internal/secrets"
)

// Storage abstracts the persistent data layer (SQLite).
type Storage interface {
	Close() error
}

// SessionManager tracks active connection sessions.
type SessionManager interface {
	CloseAll()
}

// Preferences manages user application preferences.
type Preferences interface{}

// KnownHostStore manages trusted SSH host key records.
type KnownHostStore interface{}

// AuditLog records auditable application events.
type AuditLog interface{}

// App is the central application struct. Wails binds its lifecycle methods
// (Startup, Shutdown, DomReady) and it holds references to every subsystem.
type App struct {
	ctx context.Context
	mu  sync.Mutex

	Config         *config.AppConfig
	Storage        Storage
	SecretStore    *secrets.SecretStore
	SessionManager SessionManager
	Preferences    Preferences
	KnownHostStore KnownHostStore
	AuditLog       AuditLog
}

// NewApp creates a new App instance. Subsystems are not initialised until
// Startup is called by the Wails runtime.
func NewApp() *App {
	return &App{}
}

// Startup is called by Wails when the application starts. It loads
// configuration, ensures required directories exist, and initialises all
// subsystems (storage, secret store, session manager, preferences).
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ctx = ctx

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}
	a.Config = cfg

	if err := cfg.EnsureDirectories(); err != nil {
		log.Fatalf("failed to create application directories: %v", err)
	}

	// Initialise the encrypted secret store.
	a.SecretStore = secrets.NewSecretStore(cfg.SecretPath)

	// Storage, session manager, preferences, known host store and audit log
	// will be wired in when their implementations are available.
	// For now we log successful initialisation.
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

	if a.Storage != nil {
		if err := a.Storage.Close(); err != nil {
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
	fmt.Println("DOM ready – backend:ready event emitted")
}
