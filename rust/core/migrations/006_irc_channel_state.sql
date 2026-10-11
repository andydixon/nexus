-- Migration 006: Remember the channels open for each saved IRC profile.

CREATE TABLE IF NOT EXISTS irc_channel_state (
    profile_id TEXT NOT NULL,
    room_name TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (profile_id, room_name)
);

CREATE INDEX IF NOT EXISTS idx_irc_channel_state_profile_position
    ON irc_channel_state (profile_id, position);

INSERT INTO schema_version (version, applied_at)
VALUES (6, datetime('now'));
