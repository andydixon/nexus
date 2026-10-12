//! Credential resolution (saved, session-only, ask-every-time), prompts and host-key checks.

use super::*;

pub(super) type SessionSecrets = Arc<Mutex<SecretState>>;

#[derive(Default)]
pub(super) struct SecretState {
    /// `SecretMode::SessionOnly` values, asked once per session. Wiped from memory on drop.
    pub(super) session_only: HashMap<String, Zeroizing<String>>,
    /// `SecretMode::Saved` values typed during this attempt; stored only once it connects, so a
    /// typo never ends up saved.
    pub(super) pending: HashMap<String, Zeroizing<String>>,
    /// Saved values this attempt used.
    pub(super) used: HashSet<String>,
    /// Saved values the server rejected: the next attempt asks again instead of reusing them.
    pub(super) rejected: HashSet<String>,
}

/// After a failed attempt, typed credentials are dropped and, when the server refused them, the
/// saved ones used are skipped next time so the user can enter a replacement.
pub(super) fn attempt_failed(state: &SessionSecrets, error: &str) {
    let mut st = state.lock();
    st.pending.clear();
    if is_auth_error(error) {
        let used = std::mem::take(&mut st.used);
        st.rejected.extend(used);
    }
}

/// Whether a connect error means the server refused the credentials (as opposed to a network or
/// host-key failure).
pub fn is_auth_error(msg: &str) -> bool {
    let m = msg.to_lowercase();
    // The last two are CredSSP's (RDP with NLA) wording for a rejected password.
    ["unable to authenticate", "authentication failed", "permission denied", "no supported methods", "logondenied", "logon failure"]
        .iter()
        .any(|p| m.contains(p))
}

impl Manager {
    // --- credentials --------------------------------------------------------------------

    /// Resolves a credential per its SecretMode (Go's buildSecretCallback): saved values come from
    /// the keychain then the vault (unless the server just rejected them), and newly typed ones
    /// are saved once the connection succeeds; session-only values are asked once per session and
    /// reused on reconnects (so e.g. a NickServ password entered once keeps identifying after a
    /// drop).
    pub fn secret_fn(&self, session: &str, profile_id: &str, kind: SecretKind, mode: SecretMode, prompt: Prompt) -> SecretFn {
        let key = kind.key(profile_id);
        let (secrets, store) = (self.secrets.clone(), self.store.clone());
        let (pid, kind) = (profile_id.to_string(), kind.as_str());
        let state: SessionSecrets = self.sessions.lock().get(session).map(|s| s.session_secrets.clone()).unwrap_or_default();
        Arc::new(move || {
            if mode == SecretMode::Saved {
                let mut st = state.lock();
                if let Some(v) = st.pending.get(&key) {
                    return Ok(v.to_string());
                }
                if !st.rejected.remove(&key)
                    && let Some(v) = secrets.get(&key)
                {
                    st.used.insert(key.clone());
                    store.add_audit("secret.access", &pid, "", &format!("Accessed {kind} credential"));
                    return Ok(v);
                }
            }
            if mode == SecretMode::SessionOnly
                && let Some(v) = state.lock().session_only.get(&key).map(|v| v.to_string())
            {
                return Ok(v);
            }
            let v = prompt(&format!("Enter {kind}:"))?;
            let mut st = state.lock();
            match mode {
                SecretMode::Saved => {
                    st.pending.insert(key.clone(), Zeroizing::new(v.clone()));
                }
                SecretMode::SessionOnly => {
                    st.session_only.insert(key.clone(), Zeroizing::new(v.clone()));
                }
                SecretMode::AskEveryTime => {}
            }
            Ok(v)
        })
    }

    /// Blocks until the user answers a credential prompt (5 minutes, or cancel).
    pub fn prompt(&self, session: &str, title: &str, message: &str, timeout: Duration) -> Result<String> {
        let (tx, rx) = mpsc::channel();
        self.emit(Event::SecretPrompt { session: session.into(), title: title.into(), message: message.into(), reply: tx });
        match rx.recv_timeout(timeout) {
            Ok(v) => Ok(v),
            Err(mpsc::RecvTimeoutError::Timeout) => err("secret prompt timed out".into()),
            Err(_) => err("user cancelled secret prompt".into()),
        }
    }

    /// The standard interactive prompt for a session's credentials.
    pub fn prompt_for(self: &Arc<Self>, session: &str, title: &str, timeout: Duration) -> Prompt {
        let (me, sid, title) = (self.clone(), session.to_string(), title.to_string());
        Arc::new(move |msg| me.prompt(&sid, &title, msg, timeout))
    }

    /// Host-key policy check against the known-hosts table (Go's HostKeyVerifier). Under the
    /// `ask` policy both unknown and changed keys go to the user, who can reject, accept once
    /// (connect without touching the record) or accept and save/update the record.
    pub fn host_key_check(self: &Arc<Self>, session: &str, policy: HostKeyPolicy) -> HostKeyCheck {
        let (me, sid) = (self.clone(), session.to_string());
        Arc::new(move |k: &HostKeyInfo| {
            let stored = me.store.get_known_host(&k.host, k.port as i64).map_err(|e| format!("failed to query known hosts: {e}"))?;
            let save = || {
                let n = now();
                me.store
                    .save_known_host(&KnownHost {
                        id: String::new(),
                        host: k.host.clone(),
                        port: k.port as i64,
                        key_type: k.key_type.clone(),
                        fingerprint: k.fingerprint.clone(),
                        public_key: k.public_key_b64.clone(),
                        first_seen: n,
                        last_seen: n,
                        trusted: true,
                    })
                    .map_err(|e| e.to_string())
            };
            let touch = |mut s: KnownHost| {
                s.last_seen = now();
                let _ = me.store.save_known_host(&s);
                Ok(())
            };
            let changed = |s: &KnownHost| {
                format!("host key for {} has changed! stored fingerprint: {}, received: {} — possible MITM attack", k.host, s.fingerprint, k.fingerprint)
            };
            let ask = |stored: Option<&KnownHost>| -> std::result::Result<HostKeyDecision, String> {
                let (tx, rx) = mpsc::channel();
                me.emit(Event::HostKeyVerify {
                    session: sid.clone(),
                    host: k.host.clone(),
                    key_type: k.key_type.clone(),
                    fingerprint: k.fingerprint.clone(),
                    stored: stored.map(|s| s.fingerprint.clone()),
                    reply: tx,
                });
                match rx.recv_timeout(Duration::from_secs(60)) {
                    Ok(d) => Ok(d),
                    Err(mpsc::RecvTimeoutError::Disconnected) => Ok(HostKeyDecision::Reject),
                    Err(_) => Err("host key prompt failed: host key prompt timed out waiting for user response".into()),
                }
            };
            tracing::info!(host = %k.host, port = k.port, key_type = %k.key_type, fingerprint = %k.fingerprint, ?policy, known = stored.is_some(), "checking host key");
            match (policy, stored) {
                (HostKeyPolicy::Insecure, None) | (HostKeyPolicy::AcceptNew, None) => save(),
                (HostKeyPolicy::Insecure, Some(s)) => touch(s),
                (HostKeyPolicy::Strict, None) => Err(format!("host key for {}:{} is not known (policy: strict)", k.host, k.port)),
                (HostKeyPolicy::Ask, None) => match ask(None)? {
                    HostKeyDecision::AcceptAndSave => save(),
                    HostKeyDecision::AcceptOnce => Ok(()),
                    HostKeyDecision::Reject => Err(format!("host key for {}:{} rejected by user", k.host, k.port)),
                },
                (_, Some(s)) if s.fingerprint == k.fingerprint => touch(s),
                (HostKeyPolicy::Ask, Some(s)) => match ask(Some(&s))? {
                    HostKeyDecision::AcceptAndSave => {
                        me.store.delete_known_host(&s.id).map_err(|e| e.to_string())?;
                        save()?;
                        me.store.add_audit(
                            "hostkey.update",
                            "",
                            &sid,
                            &format!("Replaced host key for {}:{} ({} -> {})", k.host, k.port, s.fingerprint, k.fingerprint),
                        );
                        Ok(())
                    }
                    HostKeyDecision::AcceptOnce => {
                        me.store.add_audit(
                            "hostkey.accept-once",
                            "",
                            &sid,
                            &format!("Accepted changed host key for {}:{} once ({})", k.host, k.port, k.fingerprint),
                        );
                        Ok(())
                    }
                    HostKeyDecision::Reject => Err(format!("{} (rejected by user)", changed(&s))),
                },
                (_, Some(s)) => Err(changed(&s)),
            }
        })
    }
}
