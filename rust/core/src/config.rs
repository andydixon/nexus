//! Data directories, and the one-time move of Alchementary Link data into Nexus Terminal's.

use std::path::{Path, PathBuf};

pub const APP_DIR: &str = "NexusTerminal";
pub const LEGACY_APP_DIR: &str = "AlchementaryLink";

#[derive(Clone, Debug)]
pub struct AppConfig {
    pub data_dir: PathBuf,
    pub db_path: PathBuf,
    pub vault_dir: PathBuf,
    pub recordings_dir: PathBuf,
    /// Set when this run copied data across from Alchementary Link.
    pub migrated_from: Option<PathBuf>,
}

/// `~/Library/Application Support` on macOS, `%APPDATA%` on Windows, `$XDG_CONFIG_HOME` or
/// `~/.config` elsewhere — the same bases the Go app used.
pub fn base_dir() -> PathBuf {
    let home = || std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" }).map(PathBuf::from).unwrap_or_default();
    if cfg!(target_os = "macos") {
        home().join("Library/Application Support")
    } else if cfg!(windows) {
        std::env::var_os("APPDATA").map(PathBuf::from).unwrap_or_else(|| home().join("AppData").join("Roaming"))
    } else {
        std::env::var_os("XDG_CONFIG_HOME").map(PathBuf::from).unwrap_or_else(|| home().join(".config"))
    }
}

impl AppConfig {
    pub fn in_dir(data_dir: PathBuf) -> AppConfig {
        AppConfig {
            db_path: data_dir.join("nexus.db"),
            vault_dir: data_dir.join("vault"),
            recordings_dir: data_dir.join("recordings"),
            data_dir,
            migrated_from: None,
        }
    }

    /// Resolves the data directory, migrating legacy data first, and creates the directories.
    pub fn load() -> std::io::Result<AppConfig> {
        let base = base_dir();
        let mut cfg = AppConfig::in_dir(base.join(APP_DIR));
        cfg.migrated_from = migrate_legacy(&base.join(LEGACY_APP_DIR), &cfg)?;
        for d in [&cfg.data_dir, &cfg.vault_dir] {
            create_private_dir(d)?;
        }
        Ok(cfg)
    }
}

fn create_private_dir(d: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(d)?;
    #[cfg(unix)]
    std::fs::set_permissions(d, std::os::unix::fs::PermissionsExt::from_mode(0o700))?;
    Ok(())
}

/// Copies (never moves) `AlchementaryLink/{link.db, vault/, recordings/}` into the Nexus
/// directory when Nexus has no database yet. A pre-rebrand `NexusTerminal/nexus.db` wins as-is.
pub fn migrate_legacy(legacy: &Path, cfg: &AppConfig) -> std::io::Result<Option<PathBuf>> {
    let legacy_db = legacy.join("link.db");
    if cfg.db_path.exists() || !legacy_db.exists() {
        return Ok(None);
    }
    create_private_dir(&cfg.data_dir)?;
    // Fold the WAL into the main file so the copy is complete.
    if let Ok(c) = rusqlite::Connection::open(&legacy_db) {
        let _ = c.pragma_update(None, "wal_checkpoint", "TRUNCATE");
    }
    std::fs::copy(&legacy_db, &cfg.db_path)?;
    copy_dir(&legacy.join("vault"), &cfg.vault_dir)?;
    copy_dir(&legacy.join("recordings"), &cfg.recordings_dir)?;
    std::fs::write(cfg.data_dir.join("migrated-from"), legacy.to_string_lossy().as_bytes())?;
    Ok(Some(legacy.to_path_buf()))
}

fn copy_dir(from: &Path, to: &Path) -> std::io::Result<()> {
    let Ok(rd) = std::fs::read_dir(from) else { return Ok(()) };
    create_private_dir(to)?;
    for e in rd.flatten() {
        if e.file_type()?.is_file() && !to.join(e.file_name()).exists() {
            std::fs::copy(e.path(), to.join(e.file_name()))?;
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("nexus-cfg-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    #[test]
    fn migrates_legacy_once_and_keeps_source() {
        let base = tmp("mig");
        let legacy = base.join(LEGACY_APP_DIR);
        std::fs::create_dir_all(legacy.join("vault")).unwrap();
        std::fs::write(legacy.join("link.db"), b"db").unwrap();
        std::fs::write(legacy.join("vault/vault.json"), b"{}").unwrap();
        let cfg = AppConfig::in_dir(base.join(APP_DIR));
        assert_eq!(migrate_legacy(&legacy, &cfg).unwrap(), Some(legacy.clone()));
        assert_eq!(std::fs::read(&cfg.db_path).unwrap(), b"db");
        assert!(cfg.vault_dir.join("vault.json").exists());
        assert!(legacy.join("link.db").exists());
        // Second run: Nexus already has a database, nothing happens.
        std::fs::write(legacy.join("link.db"), b"newer").unwrap();
        assert_eq!(migrate_legacy(&legacy, &cfg).unwrap(), None);
        assert_eq!(std::fs::read(&cfg.db_path).unwrap(), b"db");
        std::fs::remove_dir_all(base).unwrap();
    }

    #[test]
    fn no_legacy_no_migration() {
        let base = tmp("none");
        let cfg = AppConfig::in_dir(base.join(APP_DIR));
        assert_eq!(migrate_legacy(&base.join(LEGACY_APP_DIR), &cfg).unwrap(), None);
    }
}
