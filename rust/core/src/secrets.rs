//! Credential storage: the OS keychain first, then an Argon2id + AES-256-GCM vault file that is
//! byte-compatible with the Go app's `vault/vault.json`.

use crate::Result;
use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::Engine;
use base64::engine::general_purpose::STANDARD as B64;
use parking_lot::Mutex;
use rand::RngCore;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::path::PathBuf;
use zeroize::Zeroizing;

const VAULT_FILE: &str = "vault.json";
const MAX_VAULT_BYTES: u64 = 10 * 1024 * 1024;

/// A credential stored per profile under `profile:<id>:<kind>`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum SecretKind {
    Password,
    Passphrase,
    Totp,
    GithubToken,
    VncPassword,
    VncSshPassword,
    VncSshPassphrase,
    IrcServerPassword,
    IrcNickServPassword,
    IrcSshPassword,
    IrcSshPassphrase,
    ProxyPassword,
}

impl SecretKind {
    /// Every kind, so deleting a profile can purge them all from the keychain. A new variant
    /// must be added here as well as to `as_str`.
    pub const ALL: [SecretKind; 12] = [
        Self::Password,
        Self::Passphrase,
        Self::Totp,
        Self::GithubToken,
        Self::VncPassword,
        Self::VncSshPassword,
        Self::VncSshPassphrase,
        Self::IrcServerPassword,
        Self::IrcNickServPassword,
        Self::IrcSshPassword,
        Self::IrcSshPassphrase,
        Self::ProxyPassword,
    ];

    /// The storage name, shared with the Go app.
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Password => "password",
            Self::Passphrase => "passphrase",
            Self::Totp => "totp",
            Self::GithubToken => "github-token",
            Self::VncPassword => "vnc-password",
            Self::VncSshPassword => "vnc-ssh-password",
            Self::VncSshPassphrase => "vnc-ssh-passphrase",
            Self::IrcServerPassword => "irc-server-password",
            Self::IrcNickServPassword => "irc-nickserv-password",
            Self::IrcSshPassword => "irc-ssh-password",
            Self::IrcSshPassphrase => "irc-ssh-passphrase",
            Self::ProxyPassword => "proxy-password",
        }
    }

    pub fn key(self, profile_id: &str) -> String {
        format!("profile:{profile_id}:{}", self.as_str())
    }
}

/// Argon2id with the Go parameters: t=3, m=128 MiB, p=4, 32-byte key. The key is wiped on drop.
pub fn derive_key(password: &str, salt: &[u8]) -> Result<Zeroizing<[u8; 32]>> {
    let params = argon2::Params::new(128 * 1024, 3, 4, Some(32)).map_err(|e| e.to_string())?;
    let mut key = Zeroizing::new([0u8; 32]);
    argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params)
        .hash_password_into(password.as_bytes(), salt, &mut *key)
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
    Aes256Gcm::new(key.into()).decrypt(Nonce::from_slice(&data[..12]), &data[12..]).map_err(|_| "decryption failed".into())
}

#[derive(Serialize, Deserialize)]
struct VaultFile {
    version: i64,
    salt: String,
    secrets: BTreeMap<String, String>,
    /// A known value sealed under the key, so even an empty vault can tell a wrong master
    /// password. Absent in vaults written by Go or older builds; added on the next write.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    verifier: String,
}

const VERIFIER: &[u8] = b"nexus-vault-verifier";

fn verifier(key: &[u8; 32]) -> String {
    B64.encode(seal(key, VERIFIER))
}

struct Unlocked {
    key: Zeroizing<[u8; 32]>,
    vault: VaultFile,
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
        self.state.lock().is_some()
    }

    pub fn initialise(&self, password: &str) -> Result<()> {
        if self.is_initialised() {
            return Err("secrets: vault already initialised".into());
        }
        let salt = random_bytes::<32>();
        let key = derive_key(password, &salt)?;
        let vault = VaultFile { version: 1, salt: B64.encode(salt), secrets: BTreeMap::new(), verifier: verifier(&key) };
        let u = Unlocked { key, vault };
        std::fs::create_dir_all(&self.dir)?;
        self.save(&u)?;
        *self.state.lock() = Some(u);
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
        // The verifier must open; without one (older vaults) one entry must decrypt, and an empty
        // vault accepts any password, as in Go.
        let check = if u.vault.verifier.is_empty() { u.vault.secrets.values().next() } else { Some(&u.vault.verifier) };
        if let Some(v) = check {
            let data = B64.decode(v).unwrap_or_default();
            open(&u.key, &data).map_err(|_| "secrets: incorrect master password or corrupt vault")?;
        }
        *self.state.lock() = Some(u);
        Ok(())
    }

    pub fn lock(&self) {
        *self.state.lock() = None;
    }

    fn save(&self, u: &Unlocked) -> Result<()> {
        let data = serde_json::to_vec_pretty(&u.vault)?;
        write_private(&self.path(), &data)
    }

    pub fn get(&self, key: &str) -> Result<String> {
        let st = self.state.lock();
        let u = st.as_ref().ok_or("secrets: vault is locked")?;
        let enc = u.vault.secrets.get(key).ok_or_else(|| format!("secrets: key {key:?} not found"))?;
        let data = B64.decode(enc).map_err(|e| format!("failed to decode secret: {e}"))?;
        Ok(String::from_utf8_lossy(&open(&u.key, &data)?).into_owned())
    }

    pub fn set(&self, key: &str, value: &str) -> Result<()> {
        let mut st = self.state.lock();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        let enc = B64.encode(seal(&u.key, value.as_bytes()));
        u.vault.secrets.insert(key.to_string(), enc);
        if u.vault.verifier.is_empty() {
            u.vault.verifier = verifier(&u.key);
        }
        self.save(u)
    }

    pub fn delete_prefix(&self, prefix: &str) -> Result<()> {
        let mut st = self.state.lock();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        u.vault.secrets.retain(|k, _| !k.starts_with(prefix));
        self.save(u)
    }

    pub fn delete(&self, key: &str) -> Result<()> {
        let mut st = self.state.lock();
        let u = st.as_mut().ok_or("secrets: vault is locked")?;
        u.vault.secrets.remove(key);
        self.save(u)
    }
}

/// Writes a file readable only by the owner, atomically: the data goes to a sibling temp file
/// that replaces `path` only once it is fully written and synced, so a crash can't leave a
/// truncated vault or export behind (and an existing file's wider mode is never kept).
pub fn write_private(path: &std::path::Path, data: &[u8]) -> Result<()> {
    use std::io::Write;
    let name = path.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default();
    let tmp = path.with_file_name(format!(".{name}.tmp-{}", std::process::id()));
    let mut opts = std::fs::OpenOptions::new();
    opts.write(true).create(true).truncate(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
    let written = (|| {
        let mut f = opts.open(&tmp)?;
        f.write_all(data)?;
        f.sync_all()?;
        std::fs::rename(&tmp, path)
    })();
    if written.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    Ok(written?)
}

pub const KEYCHAIN_SERVICE: &str = "NexusTerminal";

/// Runs a keychain call on a plain thread: the D-Bus backend may start its own async runtime,
/// which panics if the caller is already inside tokio (as SSH authentication is).
fn off_runtime<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    std::thread::spawn(f).join().expect("keychain thread panicked")
}

fn entry(service: &str, key: &str) -> keyring::Result<keyring::Entry> {
    keyring::Entry::new(service, key)
}

/// The OS keychain. Every call can fail (no secret service, locked keyring); callers fall back
/// to the vault.
pub struct Keychain {
    service: String,
}

impl Keychain {
    pub fn new(service: &str) -> Keychain {
        Keychain { service: service.into() }
    }

    /// Probes with a read of an entry that doesn't exist: "no entry" proves the keychain answers,
    /// without writing an item at every launch (which wakes D-Bus and can prompt on macOS). If
    /// writes later fail anyway, `Secrets::set_checked` falls back to the vault.
    pub fn available(&self) -> bool {
        let service = self.service.clone();
        off_runtime(move || matches!(entry(&service, "__nexus_keychain_probe__").and_then(|e| e.get_password()), Ok(_) | Err(keyring::Error::NoEntry)))
    }

    pub fn get(&self, key: &str) -> Option<String> {
        let (service, key) = (self.service.clone(), key.to_string());
        off_runtime(move || entry(&service, &key).and_then(|e| e.get_password())).ok()
    }

    pub fn set(&self, key: &str, value: &str) -> Result<()> {
        let (service, key2, value) = (self.service.clone(), key.to_string(), value.to_string());
        off_runtime(move || entry(&service, &key2).and_then(|e| e.set_password(&value)))
            .map_err(|e| format!("keychain: failed to store secret {key:?}: {e}").into())
    }

    pub fn delete(&self, key: &str) {
        let (service, key) = (self.service.clone(), key.to_string());
        let _ = off_runtime(move || entry(&service, &key).and_then(|e| e.delete_credential()));
    }
}

/// Keychain first, vault second — how every credential lookup in the app works.
pub struct Secrets {
    pub vault: Vault,
    pub keychain: Keychain,
    /// Probed once at startup (the probe writes an item, so don't repeat it per lookup).
    pub keychain_ok: bool,
}

impl Secrets {
    pub fn new(vault: Vault, keychain: Keychain) -> Secrets {
        let keychain_ok = keychain.available();
        Secrets { vault, keychain, keychain_ok }
    }

    pub fn get(&self, key: &str) -> Option<String> {
        if self.keychain_ok
            && let Some(v) = self.keychain.get(key).filter(|v| !v.is_empty())
        {
            return Some(v);
        }
        self.vault.get(key).ok().filter(|v| !v.is_empty())
    }

    /// Stores where possible: the keychain, else the vault if it's unlocked.
    pub fn set_checked(&self, key: &str, value: &str) -> Result<()> {
        if self.keychain_ok && self.keychain.set(key, value).is_ok() {
            return Ok(());
        }
        if !self.vault.is_unlocked() {
            return Err("secret store is locked; unlock it first".into());
        }
        self.vault.set(key, value)
    }

    pub fn delete(&self, key: &str) {
        if self.keychain_ok {
            self.keychain.delete(key);
        }
        let _ = self.vault.delete(key);
    }

    /// Removes every credential saved for a profile from both stores.
    pub fn delete_profile(&self, profile_id: &str) {
        if self.keychain_ok {
            for kind in SecretKind::ALL {
                self.keychain.delete(&kind.key(profile_id));
            }
        }
        let _ = self.vault.delete_prefix(&format!("profile:{profile_id}:"));
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

    #[test]
    fn empty_vault_rejects_a_wrong_password_and_old_vaults_gain_a_verifier() {
        let dir = std::env::temp_dir().join(format!("nexus-vault-verifier-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let v = Vault::new(dir.clone());
        v.initialise("pw").unwrap();
        v.lock();
        assert!(v.unlock("typo").is_err(), "an empty vault must not accept any password");
        v.unlock("pw").unwrap();
        // A vault from Go/older builds (no verifier) keeps working and gains one on write.
        let path = dir.join(VAULT_FILE);
        let mut f: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        f.as_object_mut().unwrap().remove("verifier");
        std::fs::write(&path, f.to_string()).unwrap();
        v.lock();
        v.unlock("anything").unwrap();
        v.lock();
        v.unlock("pw").unwrap();
        v.set("a", "1").unwrap();
        v.lock();
        assert!(std::fs::read_to_string(&path).unwrap().contains("verifier"));
        assert!(v.unlock("typo").is_err());
        v.unlock("pw").unwrap();
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn private_writes_are_atomic_and_owner_only() {
        let dir = std::env::temp_dir().join(format!("nexus-wp-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("secret.bin");
        // A pre-existing world-readable file is replaced, not reused.
        std::fs::write(&path, b"old").unwrap();
        #[cfg(unix)]
        std::fs::set_permissions(&path, std::os::unix::fs::PermissionsExt::from_mode(0o644)).unwrap();
        write_private(&path, b"new").unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), b"new");
        #[cfg(unix)]
        assert_eq!(std::os::unix::fs::PermissionsExt::mode(&std::fs::metadata(&path).unwrap().permissions()) & 0o777, 0o600);
        // No temp file is left behind, on success or when the target directory is unwritable.
        assert_eq!(std::fs::read_dir(&dir).unwrap().count(), 1);
        assert!(write_private(&dir.join("missing").join("x"), b"x").is_err());
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn deleting_a_profile_clears_every_vault_kind() {
        let dir = std::env::temp_dir().join(format!("nexus-delprof-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let s = Secrets { vault: Vault::new(dir.clone()), keychain: Keychain::new("NexusTerminalTest"), keychain_ok: false };
        s.vault.initialise("pw").unwrap();
        for kind in SecretKind::ALL {
            s.vault.set(&kind.key("p1"), "v").unwrap();
        }
        s.vault.set("profile:p2:password", "keep").unwrap();
        s.delete_profile("p1");
        assert!(SecretKind::ALL.iter().all(|k| s.get(&k.key("p1")).is_none()));
        assert_eq!(s.get("profile:p2:password").as_deref(), Some("keep"));
        std::fs::remove_dir_all(dir).unwrap();
    }
}
