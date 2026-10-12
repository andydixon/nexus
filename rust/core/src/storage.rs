//! SQLite persistence: profiles, known hosts, preferences, audit log, snippets and IRC channel
//! state. The schema and migration files are the Go app's, so existing databases open unchanged.

use crate::Result;
use crate::models::*;
use chrono::{DateTime, SecondsFormat};
use parking_lot::Mutex;
use rusqlite::{Connection, OptionalExtension, Row, params};
use std::path::Path;

const MIGRATIONS: &[(i64, &str)] = &[
    (1, include_str!("../migrations/001_initial.sql")),
    (2, include_str!("../migrations/002_github_protocol.sql")),
    (3, include_str!("../migrations/003_snippets.sql")),
    (4, include_str!("../migrations/004_vnc_protocol.sql")),
    (5, include_str!("../migrations/005_irc_protocol.sql")),
    (6, include_str!("../migrations/006_irc_channel_state.sql")),
    (7, include_str!("../migrations/007_serial_protocol.sql")),
    (8, include_str!("../migrations/008_rdp_protocol.sql")),
];

const PROFILE_COLS: &str = "id, display_name, description, protocol, host, port, username,
    tags, folder, environment, notes, favourite, last_connected,
    connect_timeout, read_timeout,
    terminal_font_family, terminal_font_size, terminal_scrollback, terminal_cursor_style,
    ssh_config, telnet_config, raw_config, github_config, vnc_config, irc_config, serial_config, rdp_config,
    created_at, updated_at";

/// Formats like Go's `time.RFC3339` (whole seconds, `Z` for UTC).
pub fn fmt_time(t: &Time) -> String {
    t.to_rfc3339_opts(SecondsFormat::Secs, true)
}

pub fn parse_time(s: &str) -> Time {
    DateTime::parse_from_rfc3339(s).unwrap_or_else(|_| zero_time())
}

/// Audit-log entries kept; older ones are pruned as new ones arrive.
pub const MAX_AUDIT_ROWS: i64 = 10_000;

pub struct Store {
    db: Mutex<Connection>,
}

fn json_col<T: serde::Serialize>(v: &Option<T>) -> Option<String> {
    v.as_ref().and_then(|v| serde_json::to_string(v).ok())
}

fn parse_col<T: serde::de::DeserializeOwned>(s: Option<String>) -> Option<T> {
    let s = s.filter(|s| !s.is_empty() && s != "null")?;
    serde_json::from_str(&s).inspect_err(|e| tracing::warn!(error = %e, "ignoring unreadable profile config")).ok()
}

fn scan_profile(r: &Row) -> rusqlite::Result<Profile> {
    let protocol: String = r.get(3)?;
    let tags: String = r.get::<_, Option<String>>(7)?.unwrap_or_default();
    let last: Option<String> = r.get(12)?;
    Ok(Profile {
        id: r.get(0)?,
        display_name: r.get(1)?,
        description: r.get::<_, Option<String>>(2)?.unwrap_or_default(),
        protocol: Protocol::from_id(&protocol).unwrap_or_default(),
        host: r.get(4)?,
        port: r.get(5)?,
        username: r.get::<_, Option<String>>(6)?.unwrap_or_default(),
        tags: serde_json::from_str::<Option<Vec<String>>>(&tags).ok().flatten().unwrap_or_default(),
        folder: r.get::<_, Option<String>>(8)?.unwrap_or_default(),
        environment: r.get::<_, Option<String>>(9)?.unwrap_or_default(),
        notes: r.get::<_, Option<String>>(10)?.unwrap_or_default(),
        favourite: r.get::<_, Option<i64>>(11)?.unwrap_or(0) == 1,
        last_connected: last.filter(|s| !s.is_empty()).and_then(|s| DateTime::parse_from_rfc3339(&s).ok()),
        connect_timeout: r.get::<_, Option<i64>>(13)?.unwrap_or(0),
        read_timeout: r.get::<_, Option<i64>>(14)?.unwrap_or(0),
        terminal_font_family: r.get::<_, Option<String>>(15)?.unwrap_or_default(),
        terminal_font_size: r.get::<_, Option<i64>>(16)?.unwrap_or(0),
        terminal_scrollback: r.get::<_, Option<i64>>(17)?.unwrap_or(0),
        terminal_cursor_style: r.get::<_, Option<String>>(18)?.unwrap_or_default(),
        ssh: parse_col(r.get(19)?),
        telnet: parse_col(r.get(20)?),
        raw: parse_col(r.get(21)?),
        github: parse_col(r.get(22)?),
        vnc: parse_col(r.get(23)?),
        irc: parse_col(r.get(24)?),
        serial: parse_col(r.get(25)?),
        rdp: parse_col(r.get(26)?),
        created_at: parse_time(&r.get::<_, String>(27)?),
        updated_at: parse_time(&r.get::<_, String>(28)?),
    })
}

fn scan_known_host(r: &Row) -> rusqlite::Result<KnownHost> {
    Ok(KnownHost {
        id: r.get(0)?,
        host: r.get(1)?,
        port: r.get(2)?,
        key_type: r.get(3)?,
        fingerprint: r.get(4)?,
        public_key: r.get(5)?,
        first_seen: parse_time(&r.get::<_, String>(6)?),
        last_seen: parse_time(&r.get::<_, String>(7)?),
        trusted: r.get::<_, Option<i64>>(8)?.unwrap_or(1) == 1,
    })
}

fn scan_snippet(r: &Row) -> rusqlite::Result<Snippet> {
    let tags: String = r.get(5)?;
    Ok(Snippet {
        id: r.get(0)?,
        name: r.get(1)?,
        command: r.get(2)?,
        description: r.get(3)?,
        folder: r.get(4)?,
        tags: serde_json::from_str::<Option<Vec<String>>>(&tags).ok().flatten().unwrap_or_default(),
        created_at: parse_time(&r.get::<_, String>(6)?),
        updated_at: parse_time(&r.get::<_, String>(7)?),
    })
}

impl Store {
    pub fn open(path: &Path) -> Result<Store> {
        let db = Connection::open(path).map_err(|e| format!("storage: failed to open database: {e}"))?;
        db.busy_timeout(std::time::Duration::from_secs(5))?;
        db.pragma_update(None, "journal_mode", "WAL")?;
        let s = Store { db: Mutex::new(db) };
        s.migrate().map_err(|e| format!("storage: migration failed: {e}"))?;
        Ok(s)
    }

    fn migrate(&self) -> Result<()> {
        let db = self.db.lock();
        db.execute_batch("CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)")?;
        let current: i64 = db.query_row("SELECT COALESCE(MAX(version), 0) FROM schema_version", [], |r| r.get(0))?;
        for (ver, sql) in MIGRATIONS {
            if *ver > current {
                db.execute_batch(sql).map_err(|e| format!("failed to apply migration {ver:03}: {e}"))?;
            }
        }
        Ok(())
    }

    fn profile_params(p: &Profile) -> [Box<dyn rusqlite::ToSql>; 26] {
        [
            Box::new(p.display_name.clone()),
            Box::new(p.description.clone()),
            Box::new(p.protocol.id()),
            Box::new(p.host.clone()),
            Box::new(p.port),
            Box::new(p.username.clone()),
            Box::new(serde_json::to_string(&p.tags).unwrap_or_else(|_| "[]".into())),
            Box::new(p.folder.clone()),
            Box::new(p.environment.clone()),
            Box::new(p.notes.clone()),
            Box::new(p.favourite as i64),
            Box::new(p.last_connected.as_ref().map(fmt_time)),
            Box::new(p.connect_timeout),
            Box::new(p.read_timeout),
            Box::new(p.terminal_font_family.clone()),
            Box::new(p.terminal_font_size),
            Box::new(p.terminal_scrollback),
            Box::new(p.terminal_cursor_style.clone()),
            Box::new(json_col(&p.ssh)),
            Box::new(json_col(&p.telnet)),
            Box::new(json_col(&p.raw)),
            Box::new(json_col(&p.github)),
            Box::new(json_col(&p.vnc)),
            Box::new(json_col(&p.irc)),
            Box::new(json_col(&p.serial)),
            Box::new(json_col(&p.rdp)),
        ]
    }

    pub fn create_profile(&self, p: &Profile) -> Result<()> {
        let mut args: Vec<Box<dyn rusqlite::ToSql>> = vec![Box::new(p.id.clone())];
        args.extend(Self::profile_params(p));
        args.push(Box::new(fmt_time(&p.created_at)));
        args.push(Box::new(fmt_time(&p.updated_at)));
        let sql = format!("INSERT INTO profiles ({PROFILE_COLS}) VALUES ({})", vec!["?"; 29].join(","));
        self.db
            .lock()
            .execute(&sql, rusqlite::params_from_iter(args.iter().map(|b| b.as_ref())))
            .map_err(|e| format!("storage: failed to create profile: {e}"))?;
        Ok(())
    }

    pub fn update_profile(&self, p: &Profile) -> Result<()> {
        let mut args: Vec<Box<dyn rusqlite::ToSql>> = Self::profile_params(p).into_iter().collect();
        args.push(Box::new(fmt_time(&p.updated_at)));
        args.push(Box::new(p.id.clone()));
        let n = self
            .db
            .lock()
            .execute(
                "UPDATE profiles SET display_name=?, description=?, protocol=?, host=?, port=?, username=?,
                 tags=?, folder=?, environment=?, notes=?, favourite=?, last_connected=?,
                 connect_timeout=?, read_timeout=?,
                 terminal_font_family=?, terminal_font_size=?, terminal_scrollback=?, terminal_cursor_style=?,
                 ssh_config=?, telnet_config=?, raw_config=?, github_config=?, vnc_config=?, irc_config=?, serial_config=?, rdp_config=?,
                 updated_at=? WHERE id=?",
                rusqlite::params_from_iter(args.iter().map(|b| b.as_ref())),
            )
            .map_err(|e| format!("storage: failed to update profile: {e}"))?;
        if n == 0 {
            return Err(format!("storage: profile {:?} not found", p.id).into());
        }
        Ok(())
    }

    pub fn get_profile(&self, id: &str) -> Result<Profile> {
        self.db
            .lock()
            .query_row(&format!("SELECT {PROFILE_COLS} FROM profiles WHERE id = ?"), [id], scan_profile)
            .optional()?
            .ok_or_else(|| format!("storage: profile {id:?} not found").into())
    }

    pub fn delete_profile(&self, id: &str) -> Result<()> {
        let mut db = self.db.lock();
        let tx = db.transaction()?;
        tx.execute("DELETE FROM irc_channel_state WHERE profile_id = ?", [id])?;
        if tx.execute("DELETE FROM profiles WHERE id = ?", [id])? == 0 {
            return Err(format!("storage: profile {id:?} not found").into());
        }
        tx.commit()?;
        Ok(())
    }

    pub fn list_profiles(&self) -> Result<Vec<Profile>> {
        let db = self.db.lock();
        let mut st = db.prepare(&format!("SELECT {PROFILE_COLS} FROM profiles ORDER BY display_name"))?;
        let rows = st.query_map([], scan_profile)?.collect::<rusqlite::Result<Vec<_>>>()?;
        Ok(rows)
    }

    pub fn set_favourite(&self, id: &str, fav: bool) -> Result<()> {
        let n = self.db.lock().execute("UPDATE profiles SET favourite = ?, updated_at = ? WHERE id = ?", params![fav as i64, fmt_time(&now()), id])?;
        if n == 0 {
            return Err(format!("storage: profile {id:?} not found").into());
        }
        Ok(())
    }

    pub fn update_last_connected(&self, id: &str) -> Result<()> {
        let now = fmt_time(&now());
        self.db.lock().execute("UPDATE profiles SET last_connected = ?, updated_at = ? WHERE id = ?", params![now, now, id])?;
        Ok(())
    }

    /// Imports profiles with a skip/overwrite/duplicate strategy; returns how many were written.
    pub fn import_profiles(&self, profiles: Vec<Profile>, strategy: &str) -> Result<usize> {
        if !matches!(strategy, "skip" | "overwrite" | "duplicate") {
            return Err(format!("storage: unknown import strategy {strategy:?}").into());
        }
        let mut n = 0;
        for mut p in profiles {
            let exists = self.get_profile(&p.id).is_ok();
            match (strategy, exists) {
                ("skip", true) => continue,
                ("overwrite", true) => {
                    self.update_profile(&p)?;
                    n += 1;
                    continue;
                }
                ("duplicate", true) => {
                    p.id = new_id();
                    p.created_at = now();
                    p.updated_at = p.created_at;
                }
                _ => {}
            }
            self.create_profile(&p)?;
            n += 1;
        }
        Ok(n)
    }

    /// The stored key of this type for `host:port`. A host can have one key per type (an
    /// imported known_hosts usually brings ed25519, RSA and ECDSA), and comparing the offered
    /// key with a row of another type would look like a changed key.
    pub fn get_known_host(&self, host: &str, port: i64, key_type: &str) -> Result<Option<KnownHost>> {
        Ok(self
            .db
            .lock()
            .query_row(
                "SELECT id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted
                 FROM known_hosts WHERE host = ? AND port = ? AND key_type = ?",
                params![host, port, key_type],
                scan_known_host,
            )
            .optional()?)
    }

    pub fn save_known_host(&self, kh: &KnownHost) -> Result<()> {
        self.db.lock().execute(
            "INSERT INTO known_hosts (id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
             ON CONFLICT(host, port, key_type) DO UPDATE SET
               fingerprint=excluded.fingerprint, public_key=excluded.public_key,
               last_seen=excluded.last_seen, trusted=excluded.trusted",
            params![
                if kh.id.is_empty() { new_id() } else { kh.id.clone() },
                kh.host,
                kh.port,
                kh.key_type,
                kh.fingerprint,
                kh.public_key,
                fmt_time(&kh.first_seen),
                fmt_time(&kh.last_seen),
                kh.trusted as i64
            ],
        )?;
        Ok(())
    }

    pub fn list_known_hosts(&self) -> Result<Vec<KnownHost>> {
        let db = self.db.lock();
        let mut st = db.prepare(
            "SELECT id, host, port, key_type, fingerprint, public_key, first_seen, last_seen, trusted
             FROM known_hosts ORDER BY host, port",
        )?;
        let rows = st.query_map([], scan_known_host)?.collect::<rusqlite::Result<Vec<_>>>()?;
        Ok(rows)
    }

    pub fn delete_known_host(&self, id: &str) -> Result<()> {
        if self.db.lock().execute("DELETE FROM known_hosts WHERE id = ?", [id])? == 0 {
            return Err(format!("storage: known host {id:?} not found").into());
        }
        Ok(())
    }

    /// Records an action; the log keeps the newest [`MAX_AUDIT_ROWS`] entries.
    pub fn add_audit(&self, action: &str, profile_id: &str, session_id: &str, details: &str) {
        let db = self.db.lock();
        let _ = db.execute(
            "INSERT INTO audit_log (id, timestamp, action, profile_id, session_id, details) VALUES (?, ?, ?, ?, ?, ?)",
            params![new_id(), fmt_time(&now()), action, profile_id, session_id, details],
        );
        let _ = db.execute("DELETE FROM audit_log WHERE rowid <= (SELECT MAX(rowid) FROM audit_log) - ?", [MAX_AUDIT_ROWS]);
    }

    pub fn list_audit(&self, limit: i64) -> Result<Vec<AuditEntry>> {
        let db = self.db.lock();
        let mut st = db.prepare("SELECT id, timestamp, action, profile_id, session_id, details FROM audit_log ORDER BY timestamp DESC LIMIT ?")?;
        let rows = st
            .query_map([limit], |r| {
                Ok(AuditEntry {
                    id: r.get(0)?,
                    timestamp: parse_time(&r.get::<_, String>(1)?),
                    action: r.get(2)?,
                    profile_id: r.get::<_, Option<String>>(3)?.unwrap_or_default(),
                    session_id: r.get::<_, Option<String>>(4)?.unwrap_or_default(),
                    details: r.get::<_, Option<String>>(5)?.unwrap_or_default(),
                })
            })?
            .collect::<rusqlite::Result<Vec<_>>>()?;
        Ok(rows)
    }

    pub fn irc_last_rooms(&self, profile_id: &str) -> Result<Vec<String>> {
        let db = self.db.lock();
        let mut st = db.prepare("SELECT room_name FROM irc_channel_state WHERE profile_id = ? ORDER BY position, room_name")?;
        let rows = st.query_map([profile_id], |r| r.get(0))?.collect::<rusqlite::Result<Vec<String>>>()?;
        Ok(rows)
    }

    pub fn save_irc_last_rooms(&self, profile_id: &str, rooms: &[String]) -> Result<()> {
        let profile_id = profile_id.trim();
        if profile_id.is_empty() {
            return Err("storage: IRC channel state requires a profile ID".into());
        }
        let rooms = dedup_rooms(&[rooms]);
        let mut db = self.db.lock();
        let tx = db.transaction()?;
        tx.execute("DELETE FROM irc_channel_state WHERE profile_id = ?", [profile_id])?;
        let now = fmt_time(&now());
        for (i, room) in rooms.iter().enumerate() {
            tx.execute(
                "INSERT INTO irc_channel_state (profile_id, room_name, position, updated_at) VALUES (?, ?, ?, ?)",
                params![profile_id, room, i as i64, now],
            )?;
        }
        tx.commit()?;
        Ok(())
    }

    pub fn preferences(&self) -> AppPreferences {
        let v: Option<String> = self.db.lock().query_row("SELECT value FROM preferences WHERE key = '_all'", [], |r| r.get(0)).optional().ok().flatten();
        let mut p = v.and_then(|v| serde_json::from_str::<AppPreferences>(&v).ok()).unwrap_or_default();
        p.apply_defaults();
        p
    }

    pub fn save_preferences(&self, p: &AppPreferences) -> Result<()> {
        self.db.lock().execute(
            "INSERT INTO preferences (key, value) VALUES ('_all', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            [serde_json::to_string(p)?],
        )?;
        Ok(())
    }

    /// A value saved under `key` (app state such as the restorable tab layout).
    pub fn setting(&self, key: &str) -> Option<String> {
        self.db.lock().query_row("SELECT value FROM preferences WHERE key = ?", [key], |r| r.get(0)).optional().ok().flatten()
    }

    pub fn set_setting(&self, key: &str, value: &str) -> Result<()> {
        self.db.lock().execute("INSERT INTO preferences (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", [key, value])?;
        Ok(())
    }

    pub fn list_snippets(&self) -> Result<Vec<Snippet>> {
        let db = self.db.lock();
        let mut st = db.prepare("SELECT id, name, command, description, folder, tags, created_at, updated_at FROM snippets ORDER BY name ASC")?;
        let rows = st.query_map([], scan_snippet)?.collect::<rusqlite::Result<Vec<_>>>()?;
        Ok(rows)
    }

    pub fn create_snippet(&self, s: &Snippet) -> Result<()> {
        self.db.lock().execute(
            "INSERT INTO snippets (id, name, command, description, folder, tags, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            params![s.id, s.name, s.command, s.description, s.folder, serde_json::to_string(&s.tags)?, fmt_time(&s.created_at), fmt_time(&s.updated_at)],
        )?;
        Ok(())
    }

    pub fn update_snippet(&self, s: &Snippet) -> Result<()> {
        self.db.lock().execute(
            "UPDATE snippets SET name=?, command=?, description=?, folder=?, tags=?, updated_at=? WHERE id=?",
            params![s.name, s.command, s.description, s.folder, serde_json::to_string(&s.tags)?, fmt_time(&s.updated_at), s.id],
        )?;
        Ok(())
    }

    pub fn delete_snippet(&self, id: &str) -> Result<()> {
        self.db.lock().execute("DELETE FROM snippets WHERE id = ?", [id])?;
        Ok(())
    }
}

/// Trims, drops blanks and removes case-insensitive duplicates, keeping first-seen order.
pub fn dedup_rooms(groups: &[&[String]]) -> Vec<String> {
    let mut seen = std::collections::HashSet::new();
    let mut out = vec![];
    for g in groups {
        for r in g.iter() {
            let r = r.trim();
            if !r.is_empty() && seen.insert(r.to_lowercase()) {
                out.push(r.to_string());
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn audit_log_keeps_the_newest_entries() {
        let tmp = std::env::temp_dir().join(format!("nexus-audit-{}.db", std::process::id()));
        let s = Store::open(&tmp).unwrap();
        {
            let mut db = s.db.lock();
            let tx = db.transaction().unwrap();
            for n in 0..MAX_AUDIT_ROWS {
                tx.execute("INSERT INTO audit_log (id, timestamp, action) VALUES (?, '2020-01-01T00:00:00Z', 'old')", [n.to_string()]).unwrap();
            }
            tx.commit().unwrap();
        }
        for _ in 0..3 {
            s.add_audit("new", "", "", "");
        }
        let count = |action: &str| s.db.lock().query_row("SELECT COUNT(*) FROM audit_log WHERE action = ?", [action], |r| r.get::<_, i64>(0)).unwrap();
        assert_eq!((count("old"), count("new")), (MAX_AUDIT_ROWS - 3, 3));
        drop(s);
        for ext in ["", "-wal", "-shm"] {
            let _ = std::fs::remove_file(format!("{}{ext}", tmp.display()));
        }
    }

    #[test]
    fn profiles_and_app_state_round_trip() {
        let tmp = std::env::temp_dir().join(format!("nexus-store-{}.db", std::process::id()));
        let s = Store::open(&tmp).unwrap();
        let mut ssh = Profile::new(Protocol::Ssh);
        ssh.host = "box".into();
        ssh.username = "me".into();
        ssh.favourite = true;
        ssh.tags = vec!["a".into(), "b".into()];
        let c = ssh.ssh.as_mut().unwrap();
        c.env_vars.insert("LANG".into(), "en_GB.UTF-8".into());
        c.login_scripts = vec![LoginScriptStep { kind: "wait".into(), value: "$".into(), timeout: 5 }];
        c.port_forwards = vec![PortForward { id: "f1".into(), bind_port: 8080, dest_host: "db".into(), dest_port: 5432, enabled: true, ..Default::default() }];
        ssh.normalize();
        s.create_profile(&ssh).unwrap();
        let ssh = ssh.with_secs();
        assert_eq!(s.get_profile(&ssh.id).unwrap(), ssh);
        s.update_profile(&ssh).unwrap();
        assert_eq!(s.get_profile(&ssh.id).unwrap(), ssh, "updates are lossless");
        let mut rdp = Profile::new(Protocol::Rdp);
        rdp.host = "win".into();
        rdp.username = "me".into();
        rdp.rdp.as_mut().unwrap().domain = "CORP".into();
        rdp.normalize();
        s.create_profile(&rdp).unwrap();
        assert_eq!(s.get_profile(&rdp.id).unwrap(), rdp.clone().with_secs());
        let mut serial = Profile::new(Protocol::Serial);
        serial.host = "/dev/ttyUSB0".into();
        serial.normalize();
        s.create_profile(&serial).unwrap();
        assert_eq!(s.list_profiles().unwrap().len(), 3);

        s.save_irc_last_rooms(&ssh.id, &["#rust".into(), "#nexus".into()]).unwrap();
        assert_eq!(s.irc_last_rooms(&ssh.id).unwrap(), ["#rust", "#nexus"]);
        let t = parse_time("2026-01-02T03:04:05Z");
        let kh = KnownHost {
            id: "k1".into(),
            host: "box".into(),
            port: 22,
            key_type: "ssh-ed25519".into(),
            fingerprint: "SHA256:abc".into(),
            public_key: String::new(),
            first_seen: t,
            last_seen: t,
            trusted: true,
        };
        s.save_known_host(&kh).unwrap();
        let kh = s.list_known_hosts().unwrap().remove(0);
        assert_eq!(kh.fingerprint, "SHA256:abc");
        s.delete_known_host(&kh.id).unwrap();
        assert!(s.list_known_hosts().unwrap().is_empty());
        assert!(s.delete_known_host(&kh.id).is_err(), "unknown ids are an error");
        s.create_snippet(&Snippet {
            id: "s1".into(),
            name: "up".into(),
            command: "uptime".into(),
            description: String::new(),
            folder: String::new(),
            tags: vec![],
            created_at: t,
            updated_at: t,
        })
        .unwrap();
        assert_eq!(s.list_snippets().unwrap()[0].command, "uptime");
        let prefs = AppPreferences { terminal_theme: "dracula".into(), ..Default::default() };
        s.save_preferences(&prefs).unwrap();
        assert_eq!(s.preferences().terminal_theme, "dracula");
        drop(s);
        for ext in ["", "-wal", "-shm"] {
            let _ = std::fs::remove_file(format!("{}{ext}", tmp.display()));
        }
    }

    impl Profile {
        /// Storage keeps whole seconds.
        fn with_secs(mut self) -> Self {
            self.created_at = parse_time(&fmt_time(&self.created_at));
            self.updated_at = parse_time(&fmt_time(&self.updated_at));
            self
        }
    }

    #[test]
    fn known_hosts_are_looked_up_per_key_type() {
        let tmp = std::env::temp_dir().join(format!("nexus-kh-{}.db", std::process::id()));
        let _ = std::fs::remove_file(&tmp);
        let s = Store::open(&tmp).unwrap();
        let n = now();
        for (kt, fp) in [("ssh-ed25519", "SHA256:ed"), ("ssh-rsa", "SHA256:rsa")] {
            s.save_known_host(&KnownHost {
                id: String::new(),
                host: "h".into(),
                port: 22,
                key_type: kt.into(),
                fingerprint: fp.into(),
                public_key: "AAAA".into(),
                first_seen: n,
                last_seen: n,
                trusted: true,
            })
            .unwrap();
        }
        // An imported known_hosts brings several key types per host; the offered key is
        // compared with the row of its own type, not whichever row comes first.
        assert_eq!(s.get_known_host("h", 22, "ssh-rsa").unwrap().unwrap().fingerprint, "SHA256:rsa");
        assert_eq!(s.get_known_host("h", 22, "ssh-ed25519").unwrap().unwrap().fingerprint, "SHA256:ed");
        assert!(s.get_known_host("h", 22, "ecdsa-sha2-nistp256").unwrap().is_none());
        drop(s);
        let _ = std::fs::remove_file(tmp);
    }
}
