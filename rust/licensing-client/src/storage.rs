//! Local persistence. Everything stored here is treated as attacker-controlled
//! and re-verified on load.

use std::collections::HashMap;
use std::fs;
use std::io::Write;
use std::path::PathBuf;
use std::sync::Mutex;

#[derive(Debug, thiserror::Error)]
pub enum StorageError {
    #[error("invalid storage key {0:?}")]
    InvalidKey(String),
    #[error("storage I/O: {0}")]
    Io(#[from] std::io::Error),
}

/// Key/value storage for licence state (spec §7.4).
pub trait LicenseStorage: Send + Sync {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>, StorageError>;
    fn store_atomic(&self, key: &str, value: &[u8]) -> Result<(), StorageError>;
    fn remove(&self, key: &str) -> Result<(), StorageError>;
}

/// One file per key in a directory, replaced atomically via rename.
///
/// shortcut: no interprocess lock; concurrent writers are last-writer-wins
/// (each write is still atomic). Add a lock file if hosts run several processes
/// that renew at once.
pub struct FileStorage {
    dir: PathBuf,
}

impl FileStorage {
    pub fn new(dir: impl Into<PathBuf>) -> Self {
        Self { dir: dir.into() }
    }

    /// `<platform data dir>/licensing/<product_id>/<sub>`.
    pub fn default_dir(product_id: &str, sub: &str) -> Option<PathBuf> {
        let env = |k| std::env::var_os(k).map(PathBuf::from);
        let base = if cfg!(windows) {
            env("APPDATA")?
        } else if cfg!(target_os = "macos") {
            env("HOME")?.join("Library/Application Support")
        } else {
            env("XDG_DATA_HOME").or_else(|| env("HOME").map(|h| h.join(".local/share")))?
        };
        Some(base.join("licensing").join(product_id).join(sub))
    }

    fn path(&self, key: &str) -> Result<PathBuf, StorageError> {
        if key.is_empty() || key.len() > 128 || !key.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_') {
            return Err(StorageError::InvalidKey(key.into()));
        }
        Ok(self.dir.join(key))
    }
}

impl LicenseStorage for FileStorage {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>, StorageError> {
        match fs::read(self.path(key)?) {
            Ok(b) => Ok(Some(b)),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e.into()),
        }
    }

    fn store_atomic(&self, key: &str, value: &[u8]) -> Result<(), StorageError> {
        let path = self.path(key)?;
        fs::create_dir_all(&self.dir)?;
        let tmp = self.dir.join(format!(".{key}.{}.tmp", std::process::id()));
        // A leftover from a crashed run with the same pid is unlinked (never followed); create_new
        // then refuses to write through a planted link when the process runs elevated.
        let _ = fs::remove_file(&tmp);
        let mut opts = fs::OpenOptions::new();
        opts.write(true).create_new(true);
        #[cfg(unix)]
        std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
        let mut f = opts.open(&tmp)?;
        f.write_all(value)?;
        f.sync_all()?;
        drop(f);
        fs::rename(&tmp, &path).inspect_err(|_| {
            let _ = fs::remove_file(&tmp);
        })?;
        Ok(())
    }

    fn remove(&self, key: &str) -> Result<(), StorageError> {
        match fs::remove_file(self.path(key)?) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e.into()),
            _ => Ok(()),
        }
    }
}

/// In-memory storage for tests and for hosts that persist state themselves.
#[derive(Default)]
pub struct MemoryStorage(Mutex<HashMap<String, Vec<u8>>>);

impl LicenseStorage for MemoryStorage {
    fn load(&self, key: &str) -> Result<Option<Vec<u8>>, StorageError> {
        Ok(self.0.lock().unwrap_or_else(|e| e.into_inner()).get(key).cloned())
    }
    fn store_atomic(&self, key: &str, value: &[u8]) -> Result<(), StorageError> {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).insert(key.into(), value.to_vec());
        Ok(())
    }
    fn remove(&self, key: &str) -> Result<(), StorageError> {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).remove(key);
        Ok(())
    }
}
