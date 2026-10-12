-- Migration 007: Add Serial protocol support
-- Adds the 'serial' value to the protocol CHECK constraint and a serial_config column.

CREATE TABLE IF NOT EXISTS profiles_new (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    description TEXT DEFAULT '',
    protocol TEXT NOT NULL CHECK(
        protocol IN ('ssh', 'telnet', 'raw', 'github', 'vnc', 'irc', 'serial')
    ),
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
    terminal_font_size INTEGER DEFAULT 14,
    terminal_scrollback INTEGER DEFAULT 10000,
    terminal_cursor_style TEXT DEFAULT 'block',
    ssh_config TEXT,
    telnet_config TEXT,
    raw_config TEXT,
    github_config TEXT,
    vnc_config TEXT,
    irc_config TEXT,
    serial_config TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT
    OR IGNORE INTO profiles_new (
        id,
        display_name,
        description,
        protocol,
        host,
        port,
        username,
        tags,
        folder,
        environment,
        notes,
        favourite,
        last_connected,
        connect_timeout,
        read_timeout,
        terminal_font_family,
        terminal_font_size,
        terminal_scrollback,
        terminal_cursor_style,
        ssh_config,
        telnet_config,
        raw_config,
        github_config,
        vnc_config,
        irc_config,
        serial_config,
        created_at,
        updated_at
    )
SELECT
    id,
    display_name,
    description,
    protocol,
    host,
    port,
    username,
    tags,
    folder,
    environment,
    notes,
    favourite,
    last_connected,
    connect_timeout,
    read_timeout,
    terminal_font_family,
    terminal_font_size,
    terminal_scrollback,
    terminal_cursor_style,
    ssh_config,
    telnet_config,
    raw_config,
    github_config,
    vnc_config,
    irc_config,
    NULL,
    created_at,
    updated_at
FROM
    profiles;

DROP TABLE profiles;

ALTER TABLE
    profiles_new RENAME TO profiles;

INSERT INTO
    schema_version (version, applied_at)
VALUES
    (7, datetime('now'));
