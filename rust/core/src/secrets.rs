//! Credential storage: the OS keychain first, then an Argon2id + AES-256-GCM vault file that is
//! byte-compatible with the Go app's `vault/vault.json`.

use crate::Result;
use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::Engine;
use base64::engine::general_purpose::STANDARD as B64;
use rand::RngCore;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::Mutex;

const VAULT_FILE: &str = "vault.json";
const MAX_VAULT_BYTES: u64 = 10 * 1024 * 1024;

/// Argon2id with the Go parameters: t=3, m=128 MiB, p=4, 32-byte key.
pub fn derive_key(password: &str, salt: &[u8]) -> Result<[u8; 32]> {
    let params = argon2::Params::new(128 * 1024, 3, 4, Some(32)).map_err(|e| e.to_string())?;
    let mut key = [0u8; 32];
    argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params)
        .hash_password_into(password.as_bytes(), salt, &mut key)
        .map_err(|e| e.to_string())?;
    Ok(key)
}

pub fn random_bytes<const N: usize>() -> [u8; N] {
    let mut b = [0u8; N];
    rand::thread_rng().fill_bytes(&mut b);
    b
}

/// nonce(12) ‖ ciphertext ‖ tag
pub fn seal(key: &[u8; 32], plaintext: &[u8]) -> Vec<u8> {
    let nonce = random_bytes::<12>();
    let mut out = nonce.to_vec();
    out.extend(Aes256Gcm::new(key.into()).encrypt(Nonce::from_slice(&nonce), plaintext).expect("aes-gcm encrypt"));
    out
}

pub fn open(key: &[u8; 32], data: &[u8]) -> Result<Vec<u8>> {
    if data.len() < 12 {
        return Err("ciphertext too short".into());
    }
    Aes256Gcm::new(key.into())
        .decrypt(Nonce::from_slice(&data[..12]), &data[12..])
        .map_err(|_| "decryption failed".into())
}

#[derive(Serialize, Deserialize)]
struct VaultFile {
    version: i64,
    salt: String,
    secrets: BTreeMap<String, String>,
}

struct Unlocked {
    key: [u8; 32],
    vault: VaultFile,
}

impl Drop for Unlocked {
    fn drop(&mut self) {
        self.key.fill(0);
    }
}

pub struct Vault {
    dir: PathBuf,
    state: Mutex<Option<Unlocked>>,
}

impl Vault {
    pub fn new(dir: PathBuf) -> Vault {
        Vault { dir, state: Mutex::new(None) }
    }

    fn path(&self) -> PathBuf {
        self.dir.join(VAULT_FILE)
    }

    pub fn is_initialised(&self) -> bool {
        self.path().exists()
    }

    pub fn is_unlocked(&self) -> bool {
        self.state.lock().unwrap().is_some()
    }

    pub fn initialise(&self, password: &str) -> Result<()> {
        if self.is_initialised() {
            return Err("secrets: vault already initialised".into());
        }
        let salt = random_bytes::<32>();
        let u = Unlocked {
            key: derive_key(password, &salt)?,
            vault: VaultFile { version: 1, salt: B64.encode(salt), secrets: BTreeMap::new() },
        };
        std::fs::create_dir_all(&self.dir)?;
        self.save(&u)?;
        *self.state.lock().unwrap() = Some(u);
        Ok(())
    }

    pub fn unlock(&self, password: &str) -> Result<()> {
        let meta = std::fs::metadata(self.path()).map_err(|e| format!("secrets: failed to load vault: {e}"))?;
        if meta.len() > MAX_VAULT_BYTES {
            return Err("secrets: vault file is too large".into());
        }
        let vault: VaultFile = serde_json::from_slice(&std::fs::read(self.path())?)?;
        let salt = B64.decode(&vault.salt).map_err(|e| format!("secrets: corrupt vault salt: {e}"))?;
        let u = Unlocked { key: derive_key(password, &salt)?, vault };
        // An empty vault accepts any password, as in Go; otherwise one entry must decrypt.
        if let Some(v) = u.vault.secrets.values().next() {
            let data = B64.decode(v).unwrap_or_default();
            open(&u.key, &data).map_err(|_| "secrets: incorrect master password or corrupt vault")?;
        }
        *self.state.lock().unwrap() = Some(u);
        Ok(())
    }

    pub fn lock(&self) {
        *self.state.lock().unwrap() = None;
    }

    fn save(&self, u: &Unlocked) -> Result<()> {
        let data = serde_json::to_vec_pretty(&u.vault)?;
        write_private(&self.path(), &data)
    }

    pub fn get(&self, key: &str) -> Result<String> {
        let st = self.state.lock().unwrap();
        let u = st.as_ref().ok_or("secrets: vault is locked")?;
        let enc = u.vault.secrets.get(key).ok_or_else(|| format!("secrets: key {key:?} not found"))?;
        let data = B64.decode(enc).map_err(|e| format!("failed to decode secret: {e}"))?;
        Ok(String::from_utf8_lossy(&open(&u.key, &data)?).into_owned())
    }

    pub fn set(&self, key: &str, value: &str) -> Result<()> {
        let mut st = self.state.lock().unwrap();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        let enc = B64.encode(seal(&u.key, value.as_bytes()));
        u.vault.secrets.insert(key.to_string(), enc);
        self.save(u)
    }

    pub fn delete_prefix(&self, prefix: &str) -> Result<()> {
        let mut st = self.state.lock().unwrap();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        u.vault.secrets.retain(|k, _| !k.starts_with(prefix));
        self.save(u)
    }

    pub fn delete(&self, key: &str) -> Result<()> {
        let mut st = self.state.lock().unwrap();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        u.vault.secrets.remove(key);
        self.save(u)
    }
}

/// Writes a file readable only by the owner.
pub fn write_private(path: &std::path::Path, data: &[u8]) -> Result<()> {
    use std::io::Write;
    let mut opts = std::fs::OpenOptions::new();
    opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
    let mut f = opts.open(path)?;
    f.write_all(data)?;
    f.sync_all()?;
    Ok(())
}

pub const KEYCHAIN_SERVICE: &str = "NexusTerminal";
/// The service name Alchementary Link wrote under; read as a fallback (§7 of the port plan).
pub const LEGACY_KEYCHAIN_SERVICE: &str = "AlchementaryLink";

fn entry(service: &str, key: &str) -> keyring::Result<keyring::Entry> {
    // go-keyring's Windows target is "service:user"; keyring's default differs.
    if cfg!(windows) {
        keyring::Entry::new_with_target(&format!("{service}:{key}"), service, key)
    } else {
        keyring::Entry::new(service, key)
    }
}

/// The OS keychain. Every call can fail (no secret service, locked keyring); callers fall back
/// to the vault.
pub struct Keychain {
    service: String,
    legacy: Option<String>,
}

impl Keychain {
    pub fn new(service: &str, legacy: Option<&str>) -> Keychain {
        Keychain { service: service.into(), legacy: legacy.map(Into::into) }
    }

    pub fn available(&self) -> bool {
        let probe = "__nexus_keychain_probe__";
        entry(&self.service, probe).and_then(|e| e.set_password("probe").and_then(|_| e.delete_credential())).is_ok()
    }

    /// Reads a secret; a miss under the current service falls back to the legacy one and copies
    /// the value forward. Legacy items are never deleted, so the old app keeps working.
    pub fn get(&self, key: &str) -> Option<String> {
        if let Ok(v) = entry(&self.service, key).and_then(|e| e.get_password()) {
            return Some(v);
        }
        let legacy = self.legacy.as_deref()?;
        let v = entry(legacy, key).and_then(|e| e.get_password()).ok()?;
        let _ = self.set(key, &v);
        Some(v)
    }

    pub fn set(&self, key: &str, value: &str) -> Result<()> {
        entry(&self.service, key)
            .and_then(|e| e.set_password(value))
            .map_err(|e| format!("keychain: failed to store secret {key:?}: {e}").into())
    }

    pub fn delete(&self, key: &str) {
        let _ = entry(&self.service, key).and_then(|e| e.delete_credential());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_go_vault_fixture() {
        let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/vault");
        let v = Vault::new(dir);
        assert!(v.is_initialised());
        assert!(v.unlock("wrong").is_err());
        v.unlock("correct horse battery staple").unwrap();
        assert_eq!(v.get("profile:00000000-0000-0000-0000-000000000001:password").unwrap(), "s3cret-ssh");
        assert_eq!(v.get("profile:00000000-0000-0000-0000-000000000001:totp").unwrap(), "JBSWY3DPEHPK3PXP");
    }

    #[test]
    fn vault_round_trip() {
        let dir = std::env::temp_dir().join(format!("nexus-vault-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let v = Vault::new(dir.clone());
        v.initialise("pw").unwrap();
        v.set("a", "1").unwrap();
        v.lock();
        assert!(v.get("a").is_err());
        v.unlock("pw").unwrap();
        assert_eq!(v.get("a").unwrap(), "1");
        std::fs::remove_dir_all(dir).unwrap();
    }
}
