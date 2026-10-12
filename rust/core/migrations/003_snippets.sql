-- Migration 003: Add snippets table for the command snippet library.
-- Stores reusable commands that can be sent to active sessions.

CREATE TABLE IF NOT EXISTS snippets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    folder TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO schema_version (version, applied_at) VALUES (3, datetime('now'));
