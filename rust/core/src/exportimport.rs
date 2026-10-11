//! Password-encrypted profile exports: `[version 1][salt 32][nonce 12][AES-256-GCM ciphertext]`
//! over a JSON bundle, with the vault's Argon2id parameters. `.link` files from Alchementary
//! Link are the same format.

use crate::Result;
use crate::models::Profile;
use crate::secrets::{derive_key, random_bytes};
use aes_gcm::aead::{Aead, KeyInit};
use aes_gcm::{Aes256Gcm, Nonce};
use serde::{Deserialize, Serialize};
use std::path::Path;

const VERSION: u8 = 1;
const MAX_IMPORT_BYTES: u64 = 10 * 1024 * 1024;
pub const EXTENSION: &str = "nexus";

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Bundle {
    version: i64,
    exported_at: String,
    #[serde(default)]
    profiles: Vec<Profile>,
    count: usize,
}

pub fn encrypt_and_export(profiles: &[Profile], password: &str, path: &Path) -> Result<()> {
    if password.is_empty() {
        return Err("exportimport: password cannot be empty".into());
    }
    if profiles.is_empty() {
        return Err("exportimport: no profiles to export".into());
    }
    let bundle = Bundle {
        version: 1,
        exported_at: chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Secs, true),
        profiles: profiles.to_vec(),
        count: profiles.len(),
    };
    let plain = serde_json::to_vec_pretty(&bundle)?;
    let salt = random_bytes::<32>();
    let nonce = random_bytes::<12>();
    let key = derive_key(password, &salt)?;
    let ct = Aes256Gcm::new((&key).into()).encrypt(Nonce::from_slice(&nonce), plain.as_slice()).map_err(|e| e.to_string())?;
    let mut out = vec![VERSION];
    out.extend(salt);
    out.extend(nonce);
    out.extend(ct);
    crate::secrets::write_private(path, &out)
}

pub fn decrypt_and_import(path: &Path, password: &str) -> Result<Vec<Profile>> {
    if password.is_empty() {
        return Err("exportimport: password cannot be empty".into());
    }
    if std::fs::metadata(path)?.len() > MAX_IMPORT_BYTES {
        return Err("exportimport: file is too large (max 10 MiB)".into());
    }
    let data = std::fs::read(path)?;
    if data.len() < 1 + 32 + 12 + 1 + 16 {
        return Err("exportimport: file is too small or corrupt".into());
    }
    if data[0] != VERSION {
        return Err(format!("exportimport: unsupported format version {}", data[0]).into());
    }
    let key = derive_key(password, &data[1..33])?;
    let plain = Aes256Gcm::new((&key).into())
        .decrypt(Nonce::from_slice(&data[33..45]), &data[45..])
        .map_err(|_| "exportimport: decryption failed — wrong password or corrupt file")?;
    let bundle: Bundle = serde_json::from_slice(&plain).map_err(|e| format!("exportimport: invalid bundle JSON: {e}"))?;
    Ok(bundle.profiles)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_go_export_and_round_trips() {
        let fx = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/profiles.link");
        assert!(decrypt_and_import(&fx, "nope").is_err());
        let ps = decrypt_and_import(&fx, "export-pass").unwrap();
        assert_eq!(ps.len(), 6);
        assert_eq!(ps[0].ssh.as_ref().unwrap().jump_hosts[0].port, 2222);
        let out = std::env::temp_dir().join(format!("nexus-export-{}.nexus", std::process::id()));
        encrypt_and_export(&ps, "pw", &out).unwrap();
        assert_eq!(decrypt_and_import(&out, "pw").unwrap(), ps);
        std::fs::remove_file(out).unwrap();
    }
}
