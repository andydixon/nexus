//! SSH key file helpers that need no SSH crypto: spotting private keys, listing `~/.ssh`, and
//! sanitising key and upload file names.

use crate::Result;
use std::io::Read;
use std::path::{Path, PathBuf};

const PRIVATE_KEY_MARKERS: [&str; 6] = [
    "BEGIN OPENSSH PRIVATE KEY",
    "BEGIN RSA PRIVATE KEY",
    "BEGIN DSA PRIVATE KEY",
    "BEGIN EC PRIVATE KEY",
    "BEGIN ED25519 PRIVATE KEY",
    "BEGIN PRIVATE KEY",
];

const IGNORED: [&str; 8] = [
    "authorized_keys",
    "authorized_keys2",
    "config",
    "known_hosts",
    "known_hosts.old",
    "ssh_config",
    "ssh_known_hosts",
    "ssh_known_hosts2",
];

/// Whether the first 512 bytes look like a private key. Unreadable or empty paths are `false`
/// (this is also Go's `FileIsPrivateKey`).
pub fn is_likely_private_key(path: &Path) -> bool {
    let mut head = Vec::with_capacity(512);
    let Ok(f) = std::fs::File::open(path) else { return false };
    if f.take(512).read_to_end(&mut head).is_err() || head.is_empty() {
        return false;
    }
    head.starts_with(b"openssh-key-v1")
        || PRIVATE_KEY_MARKERS.iter().any(|m| head.windows(m.len()).any(|w| w == m.as_bytes()))
}

/// Candidate private keys in `~/.ssh`, sorted. A missing home or `.ssh` gives an empty list.
pub fn list_ssh_keys() -> Result<Vec<PathBuf>> {
    match std::env::home_dir() {
        Some(home) => list_ssh_keys_in(&home.join(".ssh")),
        None => Ok(vec![]),
    }
}

pub fn list_ssh_keys_in(dir: &Path) -> Result<Vec<PathBuf>> {
    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(vec![]),
        Err(e) => return Err(e.into()),
    };
    let mut keys = Vec::new();
    for entry in entries {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            continue;
        }
        let name = entry.file_name().to_string_lossy().into_owned();
        if IGNORED.contains(&name.as_str()) || name.ends_with(".pub") || name.ends_with(".old") {
            continue;
        }
        let path = dir.join(&name);
        if is_likely_private_key(&path) {
            keys.push(path);
        }
    }
    keys.sort();
    Ok(keys)
}

/// Trimmed name of ASCII letters, digits, `-` and `_`.
pub fn validate_ssh_key_name(name: &str) -> Result<String> {
    let name = name.trim();
    if name.is_empty() {
        return Err("key name is required".into());
    }
    if !name.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_') {
        return Err("key name contains invalid characters (use alphanumeric, hyphens, underscores)".into());
    }
    Ok(name.into())
}

/// The base name of an uploaded file (either slash style), rejecting `.`, `..` and NULs.
pub fn safe_sftp_upload_name(file_name: &str) -> Result<String> {
    let name = file_name.trim().replace('\\', "/");
    if name.is_empty() {
        return Err("invalid file name".into());
    }
    // Go's path.Base: trailing slashes dropped; all slashes gives "/".
    let trimmed = name.trim_end_matches('/');
    let base = if trimmed.is_empty() { "/" } else { trimmed.rsplit('/').next().unwrap_or(trimmed) };
    if matches!(base, "" | "." | ".." | "/") || base.contains('\0') {
        return Err("invalid file name".into());
    }
    Ok(base.into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn upload_names() {
        assert_eq!(safe_sftp_upload_name("../report <final>.txt").unwrap(), "report <final>.txt");
        assert_eq!(safe_sftp_upload_name(r"C:\Users\me\a.txt").unwrap(), "a.txt");
        assert_eq!(safe_sftp_upload_name("dir/b.txt/").unwrap(), "b.txt");
        for bad in ["..", "folder/..", r"folder\..", "", "  ", "///", ".", "a\0b"] {
            assert!(safe_sftp_upload_name(bad).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn key_names() {
        assert_eq!(validate_ssh_key_name(" id_ed25519-work ").unwrap(), "id_ed25519-work");
        for bad in ["", "   ", "a/b", "../x", "a b", "kéy"] {
            assert!(validate_ssh_key_name(bad).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn lists_private_keys() {
        let dir = std::env::temp_dir().join(format!("nexus-keys-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(dir.join("subdir")).unwrap();
        let key = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n";
        for (name, body) in [
            ("id_ed25519", key),
            ("id_rsa", "-----BEGIN RSA PRIVATE KEY-----\n"),
            ("id_ed25519.pub", key),
            ("config", key),
            ("key.old", key),
            ("notes.txt", "hello"),
            ("empty", ""),
        ] {
            std::fs::write(dir.join(name), body).unwrap();
        }
        let keys = list_ssh_keys_in(&dir).unwrap();
        assert_eq!(keys, vec![dir.join("id_ed25519"), dir.join("id_rsa")]);
        assert!(!is_likely_private_key(Path::new("")));
        assert!(list_ssh_keys_in(&dir.join("missing")).unwrap().is_empty());
        std::fs::remove_dir_all(dir).unwrap();
    }
}
