// Package storage provides a SQLite-backed persistence layer for profiles,
// known hosts, audit entries, and application preferences.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/copilot-ssh-client/app/internal/models"
	"github.com/copilot-ssh-client/app/internal/storage/migrations"
	"github.com/google/uuid"
)

// Store wraps a SQLite database and exposes typed CRUD methods for
// all application entities.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) the SQLite database at dbPath, applies any
// pending migrations, and returns an initialised Store.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("storage: failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("storage: database not reachable: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("storage: migration failed: %w", err)
	}
	return s, nil
}

// Close shuts down the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Migrations
// ---------------------------------------------------------------------------

// migrate reads embedded SQL files and applies them in order.
func (s *Store) migrate() error {
	// Ensure the schema_version table exists before querying it.
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	var current int
	row := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}

	entries, err := migrations.MigrationFS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("failed to read migration files: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		// Extract version number from filename prefix (e.g. "001_initial.sql" → 1).
		var ver int
		if _, err := fmt.Sscanf(name, "%03d_", &ver); err != nil {
			continue
		}
		if ver <= current {
			continue
		}

		data, err := migrations.MigrationFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", name, err)
		}
		if _, err := s.db.Exec(string(data)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Profile operations
// ---------------------------------------------------------------------------

// CreateProfile inserts a new profile into the database.
func (s *Store) CreateProfile(p *models.Profile) error {
	tagsJSON, _ := json.Marshal(p.Tags)
	sshJSON, _ := json.Marshal(p.SSH)
	telnetJSON, _ := json.Marshal(p.Telnet)
	rawJSON, _ := json.Marshal(p.RAW)

	_, err := s.db.Exec(`INSERT INTO profiles (
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.DisplayName, p.Description, string(p.Protocol),
		p.Host, p.Port, p.Username,
		string(tagsJSON), p.Folder, p.Environment, p.Notes,
		boolToInt(p.Favourite), timeToStr(p.LastConnected),
		p.ConnectTimeout, p.ReadTimeout,
		p.TerminalFontFamily, p.TerminalFontSize, p.TerminalScrollback, p.TerminalCursorStyle,
		nullableJSON(p.SSH, sshJSON), nullableJSON(p.Telnet, telnetJSON), nullableJSON(p.RAW, rawJSON),
		p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("storage: failed to create profile: %w", err)
	}
	return nil
}

// GetProfile retrieves a single profile by ID.
func (s *Store) GetProfile(id string) (*models.Profile, error) {
	row := s.db.QueryRow(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles WHERE id = ?`, id)

	p, err := scanProfile(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("storage: profile %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: failed to get profile: %w", err)
	}
	return p, nil
}

// UpdateProfile overwrites an existing profile.
func (s *Store) UpdateProfile(p *models.Profile) error {
	tagsJSON, _ := json.Marshal(p.Tags)
	sshJSON, _ := json.Marshal(p.SSH)
	telnetJSON, _ := json.Marshal(p.Telnet)
	rawJSON, _ := json.Marshal(p.RAW)

	res, err := s.db.Exec(`UPDATE profiles SET
		display_name=?, description=?, protocol=?, host=?, port=?, username=?,
		tags=?, folder=?, environment=?, notes=?, favourite=?, last_connected=?,
		connect_timeout=?, read_timeout=?,
		terminal_font_family=?, terminal_font_size=?, terminal_scrollback=?, terminal_cursor_style=?,
		ssh_config=?, telnet_config=?, raw_config=?,
		updated_at=?
	WHERE id=?`,
		p.DisplayName, p.Description, string(p.Protocol),
		p.Host, p.Port, p.Username,
		string(tagsJSON), p.Folder, p.Environment, p.Notes,
		boolToInt(p.Favourite), timeToStr(p.LastConnected),
		p.ConnectTimeout, p.ReadTimeout,
		p.TerminalFontFamily, p.TerminalFontSize, p.TerminalScrollback, p.TerminalCursorStyle,
		nullableJSON(p.SSH, sshJSON), nullableJSON(p.Telnet, telnetJSON), nullableJSON(p.RAW, rawJSON),
		p.UpdatedAt.Format(time.RFC3339),
		p.ID,
	)
	if err != nil {
		return fmt.Errorf("storage: failed to update profile: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("storage: profile %q not found", p.ID)
	}
	return nil
}

// DeleteProfile removes a profile by ID.
func (s *Store) DeleteProfile(id string) error {
	res, err := s.db.Exec("DELETE FROM profiles WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("storage: failed to delete profile: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("storage: profile %q not found", id)
	}
	return nil
}

// ListProfiles returns all profiles ordered by display name.
func (s *Store) ListProfiles() ([]*models.Profile, error) {
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles ORDER BY display_name`)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list profiles: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// SearchProfiles returns profiles whose display name, host, or tags contain
// the query string (case-insensitive).
func (s *Store) SearchProfiles(query string) ([]*models.Profile, error) {
	q := "%" + query + "%"
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles
	WHERE display_name LIKE ? OR host LIKE ? OR tags LIKE ?
	ORDER BY display_name`, q, q, q)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to search profiles: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// ListProfilesByProtocol returns profiles filtered by protocol.
func (s *Store) ListProfilesByProtocol(protocol string) ([]*models.Profile, error) {
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles WHERE protocol = ? ORDER BY display_name`, protocol)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list profiles by protocol: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// ListProfilesByTag returns profiles that contain the specified tag.
func (s *Store) ListProfilesByTag(tag string) ([]*models.Profile, error) {
	q := `%"` + tag + `"%`
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles WHERE tags LIKE ? ORDER BY display_name`, q)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list profiles by tag: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// ListProfilesByFolder returns profiles in the specified folder.
func (s *Store) ListProfilesByFolder(folder string) ([]*models.Profile, error) {
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles WHERE folder = ? ORDER BY display_name`, folder)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list profiles by folder: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// ListFavourites returns all profiles marked as favourites.
func (s *Store) ListFavourites() ([]*models.Profile, error) {
	rows, err := s.db.Query(`SELECT
		id, display_name, description, protocol, host, port, username,
		tags, folder, environment, notes, favourite, last_connected,
		connect_timeout, read_timeout,
		terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
		ssh_config, telnet_config, raw_config,
		created_at, updated_at
	FROM profiles WHERE favourite = 1 ORDER BY display_name`)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list favourites: %w", err)
	}
	defer rows.Close()
	return scanProfiles(rows)
}

// SetFavourite updates the favourite flag on a profile.
func (s *Store) SetFavourite(id string, favourite bool) error {
	res, err := s.db.Exec("UPDATE profiles SET favourite = ?, updated_at = ? WHERE id = ?",
		boolToInt(favourite), time.Now().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("storage: failed to update favourite: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("storage: profile %q not found", id)
	}
	return nil
}

// UpdateLastConnected sets the last_connected timestamp for the given profile
// to the current time.
func (s *Store) UpdateLastConnected(id string) error {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec("UPDATE profiles SET last_connected = ?, updated_at = ? WHERE id = ?",
		now, now, id)
	if err != nil {
		return fmt.Errorf("storage: failed to update last_connected: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("storage: profile %q not found", id)
	}
	return nil
}

// ExportProfiles serialises all profiles as JSON. If includeSecrets is false
// the export omits credential-related fields (currently a placeholder — all
// secrets live in the SecretStore, not in profiles).
func (s *Store) ExportProfiles(includeSecrets bool) ([]byte, error) {
	profiles, err := s.ListProfiles()
	if err != nil {
		return nil, fmt.Errorf("storage: export failed: %w", err)
	}
	return json.MarshalIndent(profiles, "", "  ")
}

// ImportProfiles deserialises profiles from JSON and inserts them according to
// the given strategy: "skip" ignores duplicates, "overwrite" replaces them,
// and "duplicate" creates new entries with fresh IDs. Returns the number of
// profiles imported.
func (s *Store) ImportProfiles(data []byte, strategy string) (int, error) {
	var profiles []*models.Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return 0, fmt.Errorf("storage: invalid import data: %w", err)
	}

	imported := 0
	for _, p := range profiles {
		existing, _ := s.GetProfile(p.ID)

		switch strategy {
		case "skip":
			if existing != nil {
				continue
			}
		case "overwrite":
			if existing != nil {
				if err := s.UpdateProfile(p); err != nil {
					return imported, err
				}
				imported++
				continue
			}
		case "duplicate":
			if existing != nil {
				p.ID = uuid.New().String()
				p.CreatedAt = time.Now()
				p.UpdatedAt = time.Now()
			}
		default:
			return 0, fmt.Errorf("storage: unknown import strategy %q", strategy)
		}

		if err := s.CreateProfile(p); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// ---------------------------------------------------------------------------
// Known hosts
// ---------------------------------------------------------------------------

// GetKnownHost retrieves a known host entry by host, port, and key type.
func (s *Store) GetKnownHost(host string, port int) (*models.KnownHost, error) {
	row := s.db.QueryRow(`SELECT id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted
		FROM known_hosts WHERE host = ? AND port = ?`, host, port)

	kh := &models.KnownHost{}
	var firstSeen, lastSeen string
	var trusted int
	if err := row.Scan(&kh.ID, &kh.Host, &kh.Port, &kh.KeyType, &kh.Fingerprint,
		&kh.PublicKey, &firstSeen, &lastSeen, &trusted); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: failed to get known host: %w", err)
	}
	kh.FirstSeen, _ = time.Parse(time.RFC3339, firstSeen)
	kh.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
	kh.Trusted = trusted == 1
	return kh, nil
}

// SaveKnownHost inserts or updates a known host entry.
func (s *Store) SaveKnownHost(kh *models.KnownHost) error {
	if kh.ID == "" {
		kh.ID = uuid.New().String()
	}
	_, err := s.db.Exec(`INSERT INTO known_hosts (id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, port, key_type) DO UPDATE SET
			fingerprint=excluded.fingerprint, public_key=excluded.public_key,
			last_seen=excluded.last_seen, trusted=excluded.trusted`,
		kh.ID, kh.Host, kh.Port, kh.KeyType, kh.Fingerprint, kh.PublicKey,
		kh.FirstSeen.Format(time.RFC3339), kh.LastSeen.Format(time.RFC3339),
		boolToInt(kh.Trusted))
	if err != nil {
		return fmt.Errorf("storage: failed to save known host: %w", err)
	}
	return nil
}

// ListKnownHosts returns all stored known host entries.
func (s *Store) ListKnownHosts() ([]*models.KnownHost, error) {
	rows, err := s.db.Query(`SELECT id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted
		FROM known_hosts ORDER BY host, port`)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list known hosts: %w", err)
	}
	defer rows.Close()

	var hosts []*models.KnownHost
	for rows.Next() {
		kh := &models.KnownHost{}
		var firstSeen, lastSeen string
		var trusted int
		if err := rows.Scan(&kh.ID, &kh.Host, &kh.Port, &kh.KeyType, &kh.Fingerprint,
			&kh.PublicKey, &firstSeen, &lastSeen, &trusted); err != nil {
			return nil, fmt.Errorf("storage: failed to scan known host: %w", err)
		}
		kh.FirstSeen, _ = time.Parse(time.RFC3339, firstSeen)
		kh.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		kh.Trusted = trusted == 1
		hosts = append(hosts, kh)
	}
	return hosts, rows.Err()
}

// DeleteKnownHost removes a known host entry by ID.
func (s *Store) DeleteKnownHost(id string) error {
	res, err := s.db.Exec("DELETE FROM known_hosts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("storage: failed to delete known host: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("storage: known host %q not found", id)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

// AddAuditEntry inserts an audit log entry.
func (s *Store) AddAuditEntry(entry *models.AuditEntry) error {
	_, err := s.db.Exec(`INSERT INTO audit_log (id, timestamp, action, profile_id, session_id, details)
		VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Timestamp.Format(time.RFC3339), entry.Action,
		entry.ProfileID, entry.SessionID, entry.Details)
	if err != nil {
		return fmt.Errorf("storage: failed to add audit entry: %w", err)
	}
	return nil
}

// ListAuditEntries returns the most recent audit entries, limited to count.
func (s *Store) ListAuditEntries(limit int) ([]*models.AuditEntry, error) {
	rows, err := s.db.Query(`SELECT id, timestamp, action, profile_id, session_id, details
		FROM audit_log ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to list audit entries: %w", err)
	}
	defer rows.Close()

	var entries []*models.AuditEntry
	for rows.Next() {
		e := &models.AuditEntry{}
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Action, &e.ProfileID, &e.SessionID, &e.Details); err != nil {
			return nil, fmt.Errorf("storage: failed to scan audit entry: %w", err)
		}
		e.Timestamp, _ = time.Parse(time.RFC3339, ts)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ---------------------------------------------------------------------------
// Preferences (key-value)
// ---------------------------------------------------------------------------

// GetPreferences reads the stored preferences JSON blob and deserialises it
// into an AppPreferences struct. Returns nil if no preferences have been saved.
func (s *Store) GetPreferences() (*models.AppPreferences, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM preferences WHERE key = ?", "_all").Scan(&value)
	if err != nil {
		// No preferences saved yet — return nil to signal defaults should be used.
		return nil, nil
	}

	prefs := &models.AppPreferences{}
	if err := json.Unmarshal([]byte(value), prefs); err != nil {
		return nil, fmt.Errorf("storage: failed to unmarshal preferences: %w", err)
	}
	return prefs, nil
}

// SavePreferences serialises the AppPreferences as a JSON blob and persists
// it to the preferences table under a single key.
func (s *Store) SavePreferences(prefs *models.AppPreferences) error {
	data, err := json.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("storage: failed to marshal preferences: %w", err)
	}

	_, err = s.db.Exec(`INSERT INTO preferences (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "_all", string(data))
	if err != nil {
		return fmt.Errorf("storage: failed to save preferences: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// scanProfile reads a single profile row from the given scanner.
func scanProfile(row *sql.Row) (*models.Profile, error) {
	p := &models.Profile{}
	var (
		protocol, tagsJSON                     string
		sshJSON, telnetJSON, rawJSON           sql.NullString
		lastConn                               sql.NullString
		favourite                              int
		createdAt, updatedAt                   string
	)

	err := row.Scan(
		&p.ID, &p.DisplayName, &p.Description, &protocol,
		&p.Host, &p.Port, &p.Username,
		&tagsJSON, &p.Folder, &p.Environment, &p.Notes,
		&favourite, &lastConn,
		&p.ConnectTimeout, &p.ReadTimeout,
		&p.TerminalFontFamily, &p.TerminalFontSize, &p.TerminalScrollback, &p.TerminalCursorStyle,
		&sshJSON, &telnetJSON, &rawJSON,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.Protocol = models.Protocol(protocol)
	p.Favourite = favourite == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastConn.Valid && lastConn.String != "" {
		t, err := time.Parse(time.RFC3339, lastConn.String)
		if err == nil {
			p.LastConnected = &t
		}
	}

	_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
	if p.Tags == nil {
		p.Tags = []string{}
	}

	if sshJSON.Valid && sshJSON.String != "" {
		p.SSH = &models.SSHConfig{}
		_ = json.Unmarshal([]byte(sshJSON.String), p.SSH)
	}
	if telnetJSON.Valid && telnetJSON.String != "" {
		p.Telnet = &models.TelnetConfig{}
		_ = json.Unmarshal([]byte(telnetJSON.String), p.Telnet)
	}
	if rawJSON.Valid && rawJSON.String != "" {
		p.RAW = &models.RAWConfig{}
		_ = json.Unmarshal([]byte(rawJSON.String), p.RAW)
	}

	return p, nil
}

// scanProfiles iterates over rows and returns a slice of profiles.
func scanProfiles(rows *sql.Rows) ([]*models.Profile, error) {
	var result []*models.Profile
	for rows.Next() {
		p := &models.Profile{}
		var (
			protocol, tagsJSON                     string
			sshJSON, telnetJSON, rawJSON           sql.NullString
			lastConn                               sql.NullString
			favourite                              int
			createdAt, updatedAt                   string
		)

		err := rows.Scan(
			&p.ID, &p.DisplayName, &p.Description, &protocol,
			&p.Host, &p.Port, &p.Username,
			&tagsJSON, &p.Folder, &p.Environment, &p.Notes,
			&favourite, &lastConn,
			&p.ConnectTimeout, &p.ReadTimeout,
			&p.TerminalFontFamily, &p.TerminalFontSize, &p.TerminalScrollback, &p.TerminalCursorStyle,
			&sshJSON, &telnetJSON, &rawJSON,
			&createdAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("storage: failed to scan profile row: %w", err)
		}

		p.Protocol = models.Protocol(protocol)
		p.Favourite = favourite == 1
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastConn.Valid && lastConn.String != "" {
			t, err := time.Parse(time.RFC3339, lastConn.String)
			if err == nil {
				p.LastConnected = &t
			}
		}

		_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
		if p.Tags == nil {
			p.Tags = []string{}
		}

		if sshJSON.Valid && sshJSON.String != "" {
			p.SSH = &models.SSHConfig{}
			_ = json.Unmarshal([]byte(sshJSON.String), p.SSH)
		}
		if telnetJSON.Valid && telnetJSON.String != "" {
			p.Telnet = &models.TelnetConfig{}
			_ = json.Unmarshal([]byte(telnetJSON.String), p.Telnet)
		}
		if rawJSON.Valid && rawJSON.String != "" {
			p.RAW = &models.RAWConfig{}
			_ = json.Unmarshal([]byte(rawJSON.String), p.RAW)
		}

		result = append(result, p)
	}
	return result, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func timeToStr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

// nullableJSON returns nil when the source object pointer is nil,
// otherwise the marshalled JSON bytes as a string.
func nullableJSON(ptr interface{}, data []byte) interface{} {
	if ptr == nil || string(data) == "null" {
		return nil
	}
	return string(data)
}
