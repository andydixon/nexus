//! The app-facing API (a port of internal/api.Service minus the Wails plumbing): profiles,
//! sessions, credentials, known hosts, preferences, snippets, import/export, SSH keys, updates.
//! File dialogs live in the UI; methods here take paths.

use crate::clients::sftp::{self, FileEntry};
use crate::config::AppConfig;
use crate::models::*;
use crate::secrets::SecretKind;
use crate::secrets::{KEYCHAIN_SERVICE, Keychain, Secrets, Vault};
use crate::sessions::Manager;
use crate::storage::Store;
use crate::{Result, exportimport, keyfiles, sshconfig, updater, validation};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

/// The release version (`NEXUS_VERSION` from release.sh, e.g. "v1.2.0-abc1234"), else the crate version.
pub const VERSION: &str = match option_env!("NEXUS_VERSION") {
    Some(v) => v,
    None => env!("CARGO_PKG_VERSION"),
};
const MAX_IMPORT_JSON_BYTES: usize = 5 * 1024 * 1024;

pub struct Service {
    pub config: AppConfig,
    pub store: Arc<Store>,
    pub secrets: Arc<Secrets>,
    pub sessions: Arc<Manager>,
}

#[derive(Clone, Debug)]
pub struct SshKeyInfo {
    pub name: String,
    pub kind: String,
    pub public_key: String,
    pub private_key_path: PathBuf,
}

impl Service {
    pub fn open(config: AppConfig) -> Result<Service> {
        let store = Arc::new(Store::open(&config.db_path)?);
        let secrets = Arc::new(Secrets::new(Vault::new(config.vault_dir.clone()), Keychain::new(KEYCHAIN_SERVICE)));
        let sessions = Manager::new(store.clone(), secrets.clone(), config.recordings_dir.clone());
        Ok(Service { config, store, secrets, sessions })
    }

    // --- profiles -----------------------------------------------------------------------

    pub fn create_profile(&self, mut p: Profile) -> Result<Profile> {
        if p.id.is_empty() {
            p.id = new_id();
        }
        p.normalize();
        p.created_at = now();
        p.updated_at = p.created_at;
        validation::validate_profile(&p)?;
        self.store.create_profile(&p).map_err(|e| format!("failed to create profile: {e}"))?;
        self.store.add_audit("profile.create", &p.id, "", &format!("Created profile {:?}", p.display_name));
        Ok(p)
    }

    pub fn update_profile(&self, mut p: Profile) -> Result<Profile> {
        p.normalize();
        p.updated_at = now();
        validation::validate_profile(&p)?;
        self.store.update_profile(&p).map_err(|e| format!("failed to update profile: {e}"))?;
        self.store.add_audit("profile.update", &p.id, "", &format!("Updated profile {:?}", p.display_name));
        Ok(p)
    }

    pub fn delete_profile(&self, id: &str) -> Result<()> {
        let p = self.store.get_profile(id).map_err(|e| format!("profile not found: {e}"))?;
        self.store.delete_profile(id).map_err(|e| format!("failed to delete profile: {e}"))?;
        self.secrets.delete_profile(id);
        self.store.add_audit("profile.delete", id, "", &format!("Deleted profile {:?}", p.display_name));
        Ok(())
    }

    pub fn duplicate_profile(&self, id: &str) -> Result<Profile> {
        let mut d = self.store.get_profile(id).map_err(|e| format!("profile not found: {e}"))?;
        d.id = new_id();
        d.display_name += " (copy)";
        d.created_at = now();
        d.updated_at = d.created_at;
        d.last_connected = None;
        self.store.create_profile(&d).map_err(|e| format!("failed to duplicate profile: {e}"))?;
        self.store.add_audit("profile.create", &d.id, "", &format!("Created profile {:?}", d.display_name));
        Ok(d)
    }

    pub fn profiles(&self) -> Vec<Profile> {
        self.store.list_profiles().unwrap_or_default()
    }

    pub fn set_favourite(&self, id: &str, fav: bool) -> Result<()> {
        self.store.set_favourite(id, fav)
    }

    // --- sessions -----------------------------------------------------------------------

    /// Opens a saved profile; returns the new session id.
    pub fn connect_profile(&self, id: &str) -> Result<String> {
        let mut p = self.store.get_profile(id).map_err(|e| format!("profile not found: {e}"))?;
        let sid = self.sessions.connect(p.clone());
        p.last_connected = Some(now());
        p.updated_at = now();
        let _ = self.store.update_profile(&p);
        Ok(sid)
    }

    /// Opens an unsaved profile (Quick Connect).
    /// Validates like a saved profile (so e.g. an empty SSH username fails here, not at auth).
    pub fn connect_quick(&self, mut p: Profile) -> Result<String> {
        if p.id.is_empty() {
            p.id = new_id();
        }
        if p.display_name.trim().is_empty() {
            p.display_name = p.host.clone();
        }
        p.normalize();
        validation::validate_profile(&p).map_err(|e| e.to_string())?;
        Ok(self.sessions.connect(p))
    }

    pub fn connect_local(&self) -> String {
        let shell = self.store.preferences().local_shell;
        self.sessions.connect_local(&shell)
    }

    pub fn reconnect(&self, id: &str) -> Result<String> {
        self.sessions.reconnect(id)
    }

    // --- credentials --------------------------------------------------------------------

    pub fn set_profile_secret(&self, profile_id: &str, kind: SecretKind, value: &str) -> Result<()> {
        self.secrets.set_checked(&kind.key(profile_id), value)
    }

    pub fn has_totp(&self, profile_id: &str) -> bool {
        self.secrets.get(&SecretKind::Totp.key(profile_id)).is_some()
    }

    pub fn totp_code(&self, profile_id: &str) -> Result<String> {
        let s = self.secrets.get(&SecretKind::Totp.key(profile_id)).ok_or("no TOTP secret configured for this profile")?;
        crate::clients::ssh::totp_code(&s).map_err(|e| format!("failed to generate TOTP code: {e}").into())
    }

    pub fn delete_totp(&self, profile_id: &str) {
        self.secrets.delete(&SecretKind::Totp.key(profile_id));
    }

    // --- known hosts, prefs, snippets, audit --------------------------------------------

    pub fn known_hosts(&self) -> Vec<KnownHost> {
        self.store.list_known_hosts().unwrap_or_default()
    }

    pub fn delete_known_host(&self, id: &str) -> Result<()> {
        self.store.delete_known_host(id)
    }

    pub fn preferences(&self) -> AppPreferences {
        self.store.preferences()
    }

    pub fn save_preferences(&self, p: &AppPreferences) -> Result<()> {
        self.store.save_preferences(p)
    }

    pub fn snippets(&self) -> Vec<Snippet> {
        self.store.list_snippets().unwrap_or_default()
    }

    pub fn save_snippet(&self, mut s: Snippet) -> Result<Snippet> {
        if s.name.trim().is_empty() || s.command.trim().is_empty() {
            return Err("snippet name and command are required".into());
        }
        s.updated_at = now();
        if s.id.is_empty() {
            s.id = new_id();
            s.created_at = s.updated_at;
            self.store.create_snippet(&s)?;
        } else {
            self.store.update_snippet(&s)?;
        }
        Ok(s)
    }

    pub fn delete_snippet(&self, id: &str) -> Result<()> {
        self.store.delete_snippet(id)
    }

    // --- import / export ----------------------------------------------------------------

    fn import(&self, mut profiles: Vec<Profile>, strategy: &str) -> Result<usize> {
        if !matches!(strategy, "skip" | "overwrite" | "duplicate") {
            return Err(format!("unknown import strategy {strategy:?} (expected skip, overwrite, or duplicate)").into());
        }
        for (i, p) in profiles.iter_mut().enumerate() {
            p.normalize();
            validation::validate_profile(p).map_err(|e| format!("profile import entry {i} is invalid: {e}"))?;
        }
        let mut n = 0;
        for mut p in profiles {
            let exists = self.store.get_profile(&p.id).is_ok();
            let ok = match strategy {
                "skip" if exists => continue,
                "overwrite" if exists => {
                    p.updated_at = now();
                    self.store.update_profile(&p).is_ok()
                }
                "duplicate" => {
                    p.id = new_id();
                    p.created_at = now();
                    p.updated_at = p.created_at;
                    self.store.create_profile(&p).is_ok()
                }
                _ => self.store.create_profile(&p).is_ok(),
            };
            n += ok as usize;
        }
        self.store.add_audit("import", "", "", &format!("Imported {n} profile(s)"));
        Ok(n)
    }

    /// Plain JSON (as the old Export button wrote): secrets are redacted unless asked for.
    pub fn export_json(&self, include_secrets: bool) -> Result<String> {
        let mut ps = self.profiles();
        if !include_secrets {
            for p in &mut ps {
                if let Some(s) = &mut p.ssh {
                    redact_login_script(&mut s.login_scripts);
                }
                if let Some(t) = &mut p.telnet {
                    redact_login_script(&mut t.login_scripts);
                }
            }
        }
        self.store.add_audit(
            "export",
            "",
            "",
            &format!("Exported {} profile(s) {}", ps.len(), if include_secrets { "with secrets" } else { "without secrets" }),
        );
        Ok(serde_json::to_string_pretty(&ps)?)
    }

    pub fn import_json(&self, json: &str, strategy: &str) -> Result<usize> {
        if json.len() > MAX_IMPORT_JSON_BYTES {
            return Err(format!("import data is too large (max {MAX_IMPORT_JSON_BYTES} bytes)").into());
        }
        let ps: Vec<Option<Profile>> = serde_json::from_str(json).map_err(|e| format!("invalid JSON: {e}"))?;
        let ps = ps
            .into_iter()
            .enumerate()
            .map(|(i, p)| p.ok_or_else(|| format!("profile import entry {i} is null")))
            .collect::<std::result::Result<Vec<_>, _>>()?;
        self.import(ps, strategy)
    }

    /// Encrypted export of the given profiles (all when empty) to `path` (`.nexus` appended).
    pub fn export_encrypted(&self, ids: &[String], password: &str, path: &Path) -> Result<PathBuf> {
        let ps: Vec<Profile> = if ids.is_empty() { self.profiles() } else { ids.iter().filter_map(|id| self.store.get_profile(id).ok()).collect() };
        if ps.is_empty() {
            return Err("no profiles to export".into());
        }
        let mut path = path.to_path_buf();
        if path.extension().and_then(|e| e.to_str()) != Some(exportimport::EXTENSION) {
            path.as_mut_os_string().push(format!(".{}", exportimport::EXTENSION));
        }
        exportimport::encrypt_and_export(&ps, password, &path)?;
        self.store.add_audit("export", "", "", &format!("Exported {} profile(s) without secrets", ps.len()));
        Ok(path)
    }

    /// Imports a `.nexus` (or legacy `.link`) export.
    pub fn import_encrypted(&self, path: &Path, password: &str, strategy: &str) -> Result<usize> {
        let ps = exportimport::decrypt_and_import(path, password)?;
        self.import(ps, strategy)
    }

    /// Imports Host blocks as profiles; returns (imported, skipped). A block without `User` gets
    /// the local user name, as `ssh` itself would; blocks that still don't validate are skipped.
    pub fn import_ssh_config(&self, path: Option<&Path>, folder: &str) -> Result<(usize, usize)> {
        let path = path.map(Path::to_path_buf).or_else(sshconfig::default_config_path).ok_or("no ~/.ssh/config found")?;
        let blocks = sshconfig::parse_file(&path)?;
        let local_user = std::env::var("USER").or_else(|_| std::env::var("USERNAME")).unwrap_or_default();
        let (mut n, mut skipped) = (0, 0);
        for mut p in sshconfig::to_profiles(&blocks, folder) {
            if p.username.is_empty() {
                p.username = local_user.clone();
            }
            for jh in p.ssh.iter_mut().flat_map(|s| s.jump_hosts.iter_mut()) {
                if jh.username.is_empty() {
                    jh.username = local_user.clone();
                }
            }
            if validation::validate_profile(&p).is_ok() && self.store.create_profile(&p).is_ok() {
                n += 1;
            } else {
                skipped += 1;
            }
        }
        self.store.add_audit("import", "", "", &format!("Imported {n} profile(s) from SSH config, skipped {skipped}"));
        Ok((n, skipped))
    }

    // --- SSH keys -----------------------------------------------------------------------

    fn ssh_dir() -> Result<PathBuf> {
        let home = std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" }).ok_or("unable to determine home directory")?;
        Ok(PathBuf::from(home).join(".ssh"))
    }

    pub fn ssh_key_pairs(&self) -> Result<Vec<SshKeyInfo>> {
        let dir = Self::ssh_dir()?;
        let Ok(rd) = std::fs::read_dir(&dir) else { return Ok(vec![]) };
        let mut out = vec![];
        let mut names: Vec<String> = rd.flatten().filter_map(|e| e.file_name().to_str().and_then(|n| n.strip_suffix(".pub")).map(String::from)).collect();
        names.sort();
        for base in names {
            let priv_path = dir.join(&base);
            let Ok(pubk) = std::fs::read_to_string(dir.join(format!("{base}.pub"))) else { continue };
            if !priv_path.is_file() || pubk.len() > 64 * 1024 {
                continue;
            }
            let pubk = pubk.trim().to_string();
            let algo = pubk.split_whitespace().next().unwrap_or("");
            let kind = ["ed25519", "ecdsa", "rsa", "dsa"].into_iter().find(|k| algo.contains(k)).unwrap_or("unknown");
            out.push(SshKeyInfo { name: base, kind: kind.into(), public_key: pubk, private_key_path: priv_path });
        }
        Ok(out)
    }

    /// Generates a key pair in `~/.ssh` in-process (the passphrase never reaches a command line).
    pub fn generate_ssh_key(&self, name: &str, kind: &str, bits: u32, passphrase: &str) -> Result<()> {
        crate::clients::ssh::generate_key_pair(&Self::ssh_dir()?, name, kind, bits, passphrase).map(|_| ())
    }

    pub fn delete_ssh_key(&self, name: &str) -> Result<()> {
        let name = keyfiles::validate_ssh_key_name(name)?;
        let p = Self::ssh_dir()?.join(&name);
        let mut errs = vec![];
        for (what, f) in [("private key", p.clone()), ("public key", p.with_extension("pub"))] {
            if let Err(e) = std::fs::remove_file(&f)
                && e.kind() != std::io::ErrorKind::NotFound
            {
                errs.push(format!("{what}: {e}"));
            }
        }
        if errs.is_empty() { Ok(()) } else { Err(format!("failed to delete: {}", errs.join("; ")).into()) }
    }

    // --- SFTP ---------------------------------------------------------------------------

    pub fn sftp_list(&self, sid: &str, path: &str) -> Result<Vec<FileEntry>> {
        sftp::list(&self.sessions.ssh(sid)?.sftp()?, path)
    }

    pub fn sftp_pwd(&self, sid: &str) -> Result<String> {
        sftp::pwd(&self.sessions.ssh(sid)?.sftp()?)
    }

    pub fn sftp_mkdir(&self, sid: &str, path: &str) -> Result<()> {
        sftp::mkdir(&self.sessions.ssh(sid)?.sftp()?, path)
    }

    pub fn sftp_remove(&self, sid: &str, path: &str) -> Result<()> {
        sftp::remove(&self.sessions.ssh(sid)?.sftp()?, path)
    }

    pub fn sftp_rename(&self, sid: &str, from: &str, to: &str) -> Result<()> {
        sftp::rename(&self.sessions.ssh(sid)?.sftp()?, from, to)
    }

    /// Uploads a local file into `remote_dir`, keeping its file name.
    pub fn sftp_upload_path(&self, sid: &str, remote_dir: &str, local: &Path) -> Result<String> {
        let name = keyfiles::safe_sftp_upload_name(&local.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default())?;
        let remote = sftp::join(remote_dir, &name);
        self.sessions.sftp_upload(sid, local, &remote)?;
        Ok(remote)
    }

    // --- misc ---------------------------------------------------------------------------

    /// TCP reachability and latency in milliseconds.
    pub fn test_connection(&self, host: &str, port: i64) -> Result<u128> {
        if host.is_empty() {
            return Err("host is required".into());
        }
        if !(1..=65535).contains(&port) {
            return Err("port must be between 1 and 65535".into());
        }
        let addr = if host.contains(':') { format!("[{host}]:{port}") } else { format!("{host}:{port}") };
        let start = Instant::now();
        let mut last = None;
        for sa in std::net::ToSocketAddrs::to_socket_addrs(&addr.as_str())? {
            match std::net::TcpStream::connect_timeout(&sa, Duration::from_secs(10)) {
                Ok(_) => return Ok(start.elapsed().as_millis()),
                Err(e) => last = Some(e),
            }
        }
        Err(format!("connection to {addr} failed after {}ms: {}", start.elapsed().as_millis(), last.map_or("no addresses".into(), |e| e.to_string())).into())
    }

    pub fn check_for_updates(&self) -> Result<updater::UpdateInfo> {
        let url = option_env!("NEXUS_UPDATE_URL").unwrap_or(updater::DEFAULT_UPDATE_URL);
        updater::check_url(&updater::expand_update_url(url), VERSION)
    }

    pub fn audit_entries(&self, limit: i64) -> Vec<AuditEntry> {
        self.store.list_audit(limit).unwrap_or_default()
    }
}

/// Blanks `send` steps that answer a password-like prompt (the `wait` before them), the documented
/// way to script a login, so a "without secrets" export doesn't carry them.
fn redact_login_script(steps: &mut [LoginScriptStep]) {
    let mut after_secret_prompt = false;
    for s in steps {
        match s.kind.as_str() {
            "wait" => {
                let v = s.value.to_lowercase();
                after_secret_prompt = ["password", "passphrase", "passcode", "pin", "token", "secret"].iter().any(|k| v.contains(k));
            }
            "send" if after_secret_prompt => {
                s.value.clear();
                after_secret_prompt = false;
            }
            _ => {}
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn login_script_passwords_are_redacted() {
        let step = |k: &str, v: &str| LoginScriptStep { kind: k.into(), value: v.into(), timeout: 0 };
        let mut steps = vec![step("wait", "login:"), step("send", "andy"), step("wait", "Password:"), step("send", "mysecretpassword"), step("send", "uptime")];
        redact_login_script(&mut steps);
        let values: Vec<&str> = steps.iter().map(|s| s.value.as_str()).collect();
        assert_eq!(values, ["login:", "andy", "Password:", "", "uptime"]);
    }
}
