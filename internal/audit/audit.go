// Package audit provides structured audit logging for security-relevant
// operations. Secrets are never included in audit entries.
package audit

import (
	"fmt"
	"time"

	"github.com/andydixon/nexus/internal/models"
	"github.com/google/uuid"
)

// AuditStore is the persistence interface used by the Logger.
type AuditStore interface {
	AddAuditEntry(entry *models.AuditEntry) error
	ListAuditEntries(limit int) ([]*models.AuditEntry, error)
}

// Logger writes structured audit entries for connection events, profile
// changes, secret access, and import/export operations.
type Logger struct {
	store AuditStore
}

// NewLogger creates a Logger backed by the given store.
func NewLogger(store AuditStore) *Logger {
	return &Logger{store: store}
}

// Log records a generic audit entry. Fields left empty are stored as-is.
func (l *Logger) Log(action string, profileID string, sessionID string, details string) {
	entry := &models.AuditEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Action:    action,
		ProfileID: profileID,
		SessionID: sessionID,
		Details:   details,
	}
	// Best-effort logging — errors are silently discarded to avoid
	// disrupting the caller's operation.
	_ = l.store.AddAuditEntry(entry)
}

// LogConnect records a session connection event.
func (l *Logger) LogConnect(profileID string, sessionID string, protocol string, host string) {
	l.Log("connect", profileID, sessionID,
		fmt.Sprintf("Connected via %s to %s", protocol, host))
}

// LogDisconnect records a session disconnection event.
func (l *Logger) LogDisconnect(sessionID string) {
	l.Log("disconnect", "", sessionID, "Session disconnected")
}

// LogProfileCreate records the creation of a new profile.
func (l *Logger) LogProfileCreate(profileID string, name string) {
	l.Log("profile.create", profileID, "",
		fmt.Sprintf("Created profile %q", name))
}

// LogProfileUpdate records an update to an existing profile.
func (l *Logger) LogProfileUpdate(profileID string, name string) {
	l.Log("profile.update", profileID, "",
		fmt.Sprintf("Updated profile %q", name))
}

// LogProfileDelete records the deletion of a profile.
func (l *Logger) LogProfileDelete(profileID string, name string) {
	l.Log("profile.delete", profileID, "",
		fmt.Sprintf("Deleted profile %q", name))
}

// LogSecretAccess records access to stored credentials. The secret value is
// never recorded — only the type of secret accessed.
func (l *Logger) LogSecretAccess(profileID string, secretType string) {
	l.Log("secret.access", profileID, "",
		fmt.Sprintf("Accessed %s credential", secretType))
}

// LogImport records a bulk profile import.
func (l *Logger) LogImport(count int) {
	l.Log("import", "", "",
		fmt.Sprintf("Imported %d profile(s)", count))
}

// LogExport records a profile export, noting whether secrets were included.
func (l *Logger) LogExport(count int, includeSecrets bool) {
	secretsNote := "without secrets"
	if includeSecrets {
		secretsNote = "with secrets"
	}
	l.Log("export", "", "",
		fmt.Sprintf("Exported %d profile(s) %s", count, secretsNote))
}

// ListEntries returns recent audit entries, delegating to the backing store.
func (l *Logger) ListEntries(limit int) ([]*models.AuditEntry, error) {
	return l.store.ListAuditEntries(limit)
}
