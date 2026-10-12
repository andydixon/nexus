-- Initial schema for Alchementary Link
-- Creates the core tables for profiles, known hosts, preferences, and audit log.

CREATE TABLE IF NOT EXISTS profiles (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    description TEXT DEFAULT '',
    protocol TEXT NOT NULL CHECK(protocol IN ('ssh', 'telnet', 'raw')),
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    username TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    folder TEXT DEFAULT '',
    environment TEXT DEFAULT '',
    notes TEXT DEFAULT '',
    favourite INTEGER DEFAULT 0,
    last_connected TEXT,
    connect_timeout INTEGER DEFAULT 30,
    read_timeout INTEGER DEFAULT 0,
    terminal_font_family TEXT DEFAULT '',
    terminal_font_size INTEGER DEFAULT 0,
    terminal_scrollback INTEGER DEFAULT 0,
    terminal_cursor_style TEXT DEFAULT '',
    ssh_config TEXT,
    telnet_config TEXT,
    raw_config TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS known_hosts (
    id TEXT PRIMARY KEY,
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    key_type TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    public_key TEXT NOT NULL,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    trusted INTEGER DEFAULT 1,
    UNIQUE(host, port, key_type)
);

CREATE TABLE IF NOT EXISTS preferences (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id TEXT PRIMARY KEY,
    timestamp TEXT NOT NULL,
    action TEXT NOT NULL,
    profile_id TEXT DEFAULT '',
    session_id TEXT DEFAULT '',
    details TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (1, datetime('now'));
