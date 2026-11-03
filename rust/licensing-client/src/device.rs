//! Installation keys and hardware recognition signals.

use std::sync::Arc;

use p256::ecdsa::{signature::Signer, Signature, SigningKey};
use rand_core::{OsRng, RngCore};
use serde::Serialize;

use crate::storage::{LicenseStorage, StorageError};

#[derive(Debug, thiserror::Error)]
pub enum DeviceError {
    #[error("device key {0:?} not found")]
    KeyNotFound(String),
    #[error("device key is corrupt")]
    CorruptKey,
    #[error(transparent)]
    Storage(#[from] StorageError),
}

/// Opaque reference to a locally held private key.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeyHandle(pub String);

/// Provider of ECDSA P-256 installation keys (spec §7.5).
pub trait DeviceKeyProvider: Send + Sync {
    fn create_key(&self, label: &str) -> Result<KeyHandle, DeviceError>;
    /// SEC1 uncompressed public key (65 bytes).
    fn public_key(&self, handle: &KeyHandle) -> Result<Vec<u8>, DeviceError>;
    /// Raw 64-byte r||s ECDSA P-256/SHA-256 signature over `message`.
    fn sign(&self, handle: &KeyHandle, message: &[u8]) -> Result<Vec<u8>, DeviceError>;
    fn delete_key(&self, handle: &KeyHandle) -> Result<(), DeviceError>;
    /// Reported to the server as `key_storage_class`.
    fn storage_class(&self) -> &'static str;
}

/// Lower-assurance key provider: the private scalar lives in local storage
/// (a 0600 file with the default `FileStorage`).
pub struct SoftwareKeyProvider {
    storage: Arc<dyn LicenseStorage>,
}

impl SoftwareKeyProvider {
    pub fn new(storage: Arc<dyn LicenseStorage>) -> Self {
        Self { storage }
    }

    fn load(&self, h: &KeyHandle) -> Result<SigningKey, DeviceError> {
        let bytes = self.storage.load(&format!("key-{}", h.0))?.ok_or_else(|| DeviceError::KeyNotFound(h.0.clone()))?;
        SigningKey::from_slice(&bytes).map_err(|_| DeviceError::CorruptKey)
    }
}

impl DeviceKeyProvider for SoftwareKeyProvider {
    fn create_key(&self, label: &str) -> Result<KeyHandle, DeviceError> {
        let label: String = label.chars().filter(|c| c.is_ascii_alphanumeric()).take(32).collect();
        let h = KeyHandle(format!("{label}-{:016x}", OsRng.next_u64()));
        let key = SigningKey::random(&mut OsRng);
        self.storage.store_atomic(&format!("key-{}", h.0), &key.to_bytes())?;
        Ok(h)
    }

    fn public_key(&self, h: &KeyHandle) -> Result<Vec<u8>, DeviceError> {
        Ok(self.load(h)?.verifying_key().to_encoded_point(false).as_bytes().to_vec())
    }

    fn sign(&self, h: &KeyHandle, message: &[u8]) -> Result<Vec<u8>, DeviceError> {
        let sig: Signature = self.load(h)?.sign(message);
        Ok(sig.to_bytes().to_vec())
    }

    fn delete_key(&self, h: &KeyHandle) -> Result<(), DeviceError> {
        Ok(self.storage.remove(&format!("key-{}", h.0))?)
    }

    fn storage_class(&self) -> &'static str {
        "software"
    }
}

/// One machine-recognition observation sent during activation.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct Observation {
    #[serde(rename = "type")]
    pub kind: &'static str,
    pub value: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct HardwareSignals {
    pub platform: &'static str,
    pub schema: u32,
    pub observations: Vec<Observation>,
}

pub fn platform() -> &'static str {
    if cfg!(windows) {
        "windows"
    } else if cfg!(target_os = "macos") {
        "macos"
    } else {
        "linux"
    }
}

/// True for values that firmware vendors ship as defaults and which therefore
/// identify nothing.
pub fn is_placeholder(v: &str) -> bool {
    const PLACEHOLDERS: &[&str] = &[
        "to be filled by o.e.m.",
        "default string",
        "system serial number",
        "system product name",
        "system manufacturer",
        "none",
        "not applicable",
        "not specified",
        "0123456789",
        "03000200-0400-0500-0006-000700080009",
    ];
    let v = v.trim().to_ascii_lowercase();
    let hex: String = v.chars().filter(|c| c.is_ascii_hexdigit()).collect();
    v.is_empty()
        || PLACEHOLDERS.contains(&v.as_str())
        || (hex.len() == v.chars().filter(|c| *c != '-').count() && (hex.chars().all(|c| c == '0') || hex.chars().all(|c| c == 'f')))
}

/// Collects hardware observations. Blocking (may read files or spawn a
/// process); call from a blocking context.
pub fn collect_signals() -> HardwareSignals {
    let mut obs = Vec::new();
    let mut push = |kind, value: String| {
        let value = value.trim().to_string();
        if !is_placeholder(&value) && value.len() <= 256 {
            obs.push(Observation { kind, value });
        }
    };
    if cfg!(target_os = "linux") {
        for (kind, file) in [
            ("system_uuid", "product_uuid"),
            ("board_serial", "board_serial"),
            ("system_vendor", "sys_vendor"),
            ("product_name", "product_name"),
            ("board_name", "board_name"),
        ] {
            // product_uuid and board_serial are usually root-only; skip if unreadable.
            if let Ok(v) = std::fs::read_to_string(format!("/sys/class/dmi/id/{file}")) {
                push(kind, v);
            }
        }
    } else if cfg!(target_os = "macos") {
        if let Some(out) = run("ioreg", &["-rd1", "-c", "IOPlatformExpertDevice"]) {
            if let Some(uuid) = out.lines().find(|l| l.contains("\"IOPlatformUUID\"")).and_then(|l| l.split('"').nth(3)) {
                push("system_uuid", uuid.to_string());
            }
        }
    } else if cfg!(windows) {
        if let Some(out) = run("powershell", &["-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_ComputerSystemProduct).UUID"]) {
            push("system_uuid", out);
        }
    }
    HardwareSignals { platform: platform(), schema: 1, observations: obs }
}

fn run(cmd: &str, args: &[&str]) -> Option<String> {
    let out = std::process::Command::new(cmd).args(args).output().ok()?;
    out.status.success().then(|| String::from_utf8_lossy(&out.stdout).into_owned())
}

/// A human-readable default device name.
pub fn default_display_name() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .ok()
        .or_else(|| std::fs::read_to_string("/etc/hostname").ok())
        .map(|s| s.trim().chars().take(64).collect::<String>())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "This computer".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn placeholders() {
        for v in ["", "None", "To be filled by O.E.M.", "00000000-0000-0000-0000-000000000000", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"] {
            assert!(is_placeholder(v), "{v}");
        }
        assert!(!is_placeholder("4C4C4544-0042-3510-8051-B4C04F4E4B32"));
        assert!(!is_placeholder("Dell Inc."));
    }
}
