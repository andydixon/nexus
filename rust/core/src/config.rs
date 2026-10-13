// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Data directories.

use std::path::{Path, PathBuf};

pub const APP_DIR: &str = "NexusTerminal";

#[derive(Clone, Debug)]
pub struct AppConfig {
    pub data_dir: PathBuf,
    pub db_path: PathBuf,
    pub vault_dir: PathBuf,
    pub recordings_dir: PathBuf,
}

/// `~/Library/Application Support` on macOS, `%APPDATA%` on Windows, `$XDG_CONFIG_HOME` or
/// `~/.config` elsewhere.
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
        AppConfig { db_path: data_dir.join("nexus.db"), vault_dir: data_dir.join("vault"), recordings_dir: data_dir.join("recordings"), data_dir }
    }

    /// Resolves the data directory and creates it.
    pub fn load() -> std::io::Result<AppConfig> {
        let cfg = AppConfig::in_dir(base_dir().join(APP_DIR));
        for d in [&cfg.data_dir, &cfg.vault_dir] {
            create_private_dir(d)?;
        }
        Ok(cfg)
    }
}

/// Creates `d` (and parents) readable only by the owner.
pub fn create_private_dir(d: &Path) -> std::io::Result<()> {
    std::fs::create_dir_all(d)?;
    #[cfg(unix)]
    std::fs::set_permissions(d, std::os::unix::fs::PermissionsExt::from_mode(0o700))?;
    Ok(())
}
