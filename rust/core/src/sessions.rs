//! The session manager: one entry per open tab, each owning a protocol client. A port of
//! internal/sessions — same lifecycle, reconnect backoff, login scripts, recording hooks and
//! credential resolution — reporting to the UI through `Event`s.

use crate::Result;
use crate::clients::local::LocalShell;
use crate::clients::ssh::{HostKeyCheck, HostKeyInfo, SecretFn, SshClient};
use crate::clients::{self, Callbacks, github, irc, raw, serial, telnet};
use crate::models::*;
use crate::recording::{self, Recorder};
use crate::secrets::{SecretKind, Secrets};
use crate::storage::Store;
use parking_lot::Mutex;
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc;
use std::time::Duration;
use zeroize::Zeroizing;

/// Everything the UI hears about. Prompts carry their reply channel; dropping it cancels.
pub enum Event {
    Data {
        session: String,
        data: Vec<u8>,
    },
    State {
        session: String,
        state: SessionState,
        error: String,
        retry: Option<Retry>,
    },
    Closed {
        session: String,
    },
    RawData {
        session: String,
        data: Vec<u8>,
        direction: String,
        timestamp: i64,
    },
    Debug {
        session: String,
        message: String,
    },
    GithubStatus {
        session: String,
        status: String,
    },
    GithubRepos {
        session: String,
        login: String,
        repos: Vec<GitHubRepo>,
    },
    GithubError {
        session: String,
        error: String,
    },
    /// Something the user should know about that didn't stop the session (e.g. a credential that
    /// couldn't be saved).
    Warning {
        session: String,
        message: String,
    },
    Irc {
        session: String,
        event: irc::Event,
    },
    ForwardStarted {
        session: String,
        forward: PortForward,
    },
    ForwardStopped {
        session: String,
        forward_id: String,
    },
    ForwardError {
        session: String,
        forward_id: String,
        label: String,
        error: String,
    },
    RecordingStarted {
        session: String,
    },
    RecordingStopped {
        session: String,
        duration: f64,
        events: usize,
    },
    SftpProgress {
        session: String,
        progress: clients::sftp::Progress,
    },
    SecretPrompt {
        session: String,
        title: String,
        message: String,
        reply: mpsc::Sender<String>,
    },
    /// A host key needs the user's decision: unknown (`stored` is None) or changed (`stored` is
    /// the fingerprint on record).
    HostKeyVerify {
        session: String,
        host: String,
        key_type: String,
        fingerprint: String,
        stored: Option<String>,
        reply: mpsc::Sender<HostKeyDecision>,
    },
    VncReady {
        session: String,
    },
    VncFrame {
        session: String,
    },
    /// The remote desktop's clipboard changed.
    VncClipboard {
        session: String,
        text: String,
    },
}

/// The user's answer to a host-key prompt.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum HostKeyDecision {
    Reject,
    /// Connect, leaving the known-hosts record as it is.
    AcceptOnce,
    /// Connect and record this key (replacing a changed one).
    AcceptAndSave,
}

#[derive(Clone, Copy, Debug)]
pub struct Retry {
    pub attempt: i64,
    pub max: i64,
    pub delay_secs: u64,
}

pub type EventSink = Arc<dyn Fn(Event) + Send + Sync>;
/// Asks the user for a credential (blocks the calling worker thread).
pub type Prompt = Arc<dyn Fn(&str) -> Result<String> + Send + Sync>;

#[derive(Clone)]
enum Client {
    None,
    Ssh(Arc<SshClient>),
    Telnet(Arc<telnet::TelnetClient>),
    Raw(Arc<raw::RawClient>),
    Serial(Arc<serial::SerialClient>),
    Local(Arc<LocalShell>),
    Github(Arc<github::Client>, Arc<AtomicBool>),
    Irc(Arc<irc::Client>, Option<Arc<SshClient>>),
    Vnc(Arc<clients::vnc::Client>, Option<Arc<SshClient>>),
}

impl Client {
    fn close(&self) {
        match self {
            Client::None => {}
            Client::Ssh(c) => c.close(),
            Client::Telnet(c) => c.close(),
            Client::Raw(c) => c.close(),
            Client::Serial(c) => c.close(),
            Client::Local(c) => c.close(),
            Client::Github(_, stop) => stop.store(true, Ordering::SeqCst),
            Client::Irc(c, tunnel) => {
                c.close();
                if let Some(t) = tunnel {
                    t.close();
                }
            }
            Client::Vnc(c, tunnel) => {
                c.close();
                if let Some(t) = tunnel {
                    t.close();
                }
            }
        }
    }
}

#[derive(Default)]
struct LoginBuf {
    active: bool,
    buf: Vec<u8>,
}

struct Session {
    info: SessionInfo,
    /// Distinguishes this Session object from a later one with the same id (Go compared pointers).
    epoch: u64,
    closed: bool,
    client: Client,
    profile: Option<Profile>,
    local_shell: String,
    local_cwd: Option<PathBuf>,
    recorder: Option<Arc<Recorder>>,
    reconnect_count: i64,
    cancel_reconnect: Option<Arc<AtomicBool>>,
    pending_resize: Option<(u16, u16)>,
    login: Arc<Mutex<LoginBuf>>,
    /// Credential state kept across reconnects of this session.
    session_secrets: SessionSecrets,
}

type SessionSecrets = Arc<Mutex<SecretState>>;

#[derive(Default)]
struct SecretState {
    /// `SecretMode::SessionOnly` values, asked once per session. Wiped from memory on drop.
    session_only: HashMap<String, Zeroizing<String>>,
    /// `SecretMode::Saved` values typed during this attempt; stored only once it connects, so a
    /// typo never ends up saved.
    pending: HashMap<String, Zeroizing<String>>,
    /// Saved values this attempt used.
    used: HashSet<String>,
    /// Saved values the server rejected: the next attempt asks again instead of reusing them.
    rejected: HashSet<String>,
}

/// After a failed attempt, typed credentials are dropped and, when the server refused them, the
/// saved ones used are skipped next time so the user can enter a replacement.
fn attempt_failed(state: &SessionSecrets, error: &str) {
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
    ["unable to authenticate", "authentication failed", "permission denied", "no supported methods"].iter().any(|p| m.contains(p))
}

pub struct Manager {
    sessions: Mutex<HashMap<String, Session>>,
    store: Arc<Store>,
    secrets: Arc<Secrets>,
    recordings: recording::Store,
    sink: Mutex<Option<EventSink>>,
    next_epoch: std::sync::atomic::AtomicU64,
}

fn err<T>(msg: String) -> Result<T> {
    Err(msg.into())
}

impl Manager {
    pub fn new(store: Arc<Store>, secrets: Arc<Secrets>, recordings_dir: PathBuf) -> Arc<Manager> {
        Arc::new(Manager {
            sessions: Mutex::new(HashMap::new()),
            store,
            secrets,
            recordings: recording::Store::new(recordings_dir),
            sink: Mutex::new(None),
            next_epoch: 1.into(),
        })
    }

    pub fn set_sink(&self, sink: EventSink) {
        *self.sink.lock() = Some(sink);
    }

    fn emit(&self, e: Event) {
        let sink = self.sink.lock().clone();
        if let Some(s) = sink {
            s(e);
        }
    }

    fn emit_state(&self, id: &str, state: SessionState, error: &str) {
        self.emit(Event::State { session: id.into(), state, error: error.into(), retry: None });
    }

    /// True while `id` still names the Session object with `epoch` and it isn't closed.
    fn is_current(&self, id: &str, epoch: u64) -> bool {
        self.sessions.lock().get(id).is_some_and(|s| s.epoch == epoch && !s.closed)
    }

    fn with<R>(&self, id: &str, f: impl FnOnce(&mut Session) -> R) -> Result<R> {
        let mut map = self.sessions.lock();
        match map.get_mut(id) {
            Some(s) => Ok(f(s)),
            None => err(format!("sessions: session {id:?} not found")),
        }
    }

    fn client(&self, id: &str) -> Result<Client> {
        self.with(id, |s| s.client.clone())
    }

    fn new_session(&self, info: SessionInfo, profile: Option<Profile>, shell: String, session_secrets: SessionSecrets) -> u64 {
        let epoch = self.next_epoch.fetch_add(1, Ordering::SeqCst);
        let s = Session {
            info,
            epoch,
            closed: false,
            client: Client::None,
            profile,
            local_shell: shell,
            local_cwd: None,
            recorder: None,
            reconnect_count: 0,
            cancel_reconnect: None,
            pending_resize: None,
            login: Arc::default(),
            session_secrets,
        };
        let id = s.info.id.clone();
        self.sessions.lock().insert(id, s);
        epoch
    }

    /// Opens a session for `profile` and connects it on a worker thread. Returns the session id
    /// immediately; progress arrives as `Event::State`.
    pub fn connect(self: &Arc<Self>, profile: Profile) -> String {
        self.connect_with(profile, SessionSecrets::default())
    }

    /// `connect` with session-only credentials carried over from a previous session (reconnect).
    fn connect_with(self: &Arc<Self>, profile: Profile, session_secrets: SessionSecrets) -> String {
        let id = new_id();
        let prompt = self.prompt_for(&id, "Credentials Required", Duration::from_secs(300));
        let info = SessionInfo {
            id: id.clone(),
            profile_id: profile.id.clone(),
            profile_name: profile.display_name.clone(),
            protocol: profile.protocol,
            host: profile.host.clone(),
            port: profile.port,
            state: SessionState::Connecting,
            tab_name: profile.tab_name(),
            pinned: false,
            connected_at: now(),
            error: String::new(),
        };
        let epoch = self.new_session(info, Some(profile.clone()), String::new(), session_secrets);
        self.emit_state(&id, SessionState::Connecting, "");
        let me = self.clone();
        let id2 = id.clone();
        std::thread::spawn(move || {
            let r = me.connect_protocol(&id2, epoch, &profile, prompt);
            me.finish_connect(&id2, epoch, r, |me| {
                me.store.add_audit("connect", &profile.id, &id2, &format!("Connected via {} to {}", profile.protocol.id(), profile.host))
            });
        });
        id
    }

    fn finish_connect(&self, id: &str, epoch: u64, r: Result<Client>, on_ok: impl FnOnce(&Self)) {
        let mut map = self.sessions.lock();
        let current = map.get_mut(id).filter(|s| s.epoch == epoch && !s.closed);
        match (r, current) {
            (Ok(client), None) => {
                drop(map);
                client.close();
            }
            (Err(_), None) => {}
            (Err(e), Some(s)) => {
                s.info.state = SessionState::Error;
                s.info.error = e.to_string();
                attempt_failed(&s.session_secrets, &e.to_string());
                drop(map);
                self.emit_state(id, SessionState::Error, &e.to_string());
            }
            (Ok(client), Some(s)) => {
                let typed = std::mem::take(&mut s.session_secrets.lock().pending);
                s.client = client.clone();
                s.info.state = SessionState::Connected;
                s.info.connected_at = now();
                s.reconnect_count = 0;
                let pending = s.pending_resize.take();
                drop(map);
                if let Some((c, r)) = pending {
                    let _ = self.resize_client(&client, c, r);
                }
                for (key, value) in typed {
                    if let Err(e) = self.secrets.set_checked(&key, &value) {
                        self.emit(Event::Warning { session: id.into(), message: format!("Credential not saved: {e}") });
                    }
                }
                self.emit_state(id, SessionState::Connected, "");
                on_ok(self);
            }
        }
    }

    fn connect_protocol(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let _ = self.with(id, |s| {
            let mut st = s.session_secrets.lock();
            st.pending.clear();
            st.used.clear();
        });
        match p.protocol {
            Protocol::Ssh => self.connect_ssh(id, epoch, p, prompt),
            Protocol::Telnet => {
                let debug = p.telnet.as_ref().is_some_and(|t| t.negotiation_debug).then(|| {
                    let (me, id) = (self.clone(), id.to_string());
                    Arc::new(move |m: String| me.emit(Event::Debug { session: id.clone(), message: m })) as clients::DebugFn
                });
                let c = Arc::new(telnet::TelnetClient::new(p, self.terminal_callbacks(id, epoch), debug));
                c.connect()?;
                self.start_login_scripts(id, epoch, p.telnet.as_ref().map(|t| t.login_scripts.clone()).unwrap_or_default());
                Ok(Client::Telnet(c))
            }
            Protocol::Raw => {
                let cb = self.terminal_callbacks(id, epoch);
                let (me, sid) = (self.clone(), id.to_string());
                let on_data: raw::RawDataFn =
                    Arc::new(move |d, dir, ts| me.emit(Event::RawData { session: sid.clone(), data: d.to_vec(), direction: dir.into(), timestamp: ts }));
                let c = Arc::new(raw::RawClient::new(p, on_data, cb.on_error, cb.on_close));
                c.connect()?;
                Ok(Client::Raw(c))
            }
            Protocol::Serial => {
                let c = Arc::new(serial::SerialClient::new(p, self.terminal_callbacks(id, epoch)));
                c.connect()?;
                Ok(Client::Serial(c))
            }
            Protocol::Github => self.connect_github(id, epoch, p, prompt),
            Protocol::Irc => self.connect_irc(id, epoch, p, prompt),
            Protocol::Vnc => self.connect_vnc(id, epoch, p, prompt),
            Protocol::Local => err("unsupported protocol \"local\"".into()),
        }
    }

    /// Callbacks for terminal-style clients: output to the UI, recording and login scripts.
    fn terminal_callbacks(self: &Arc<Self>, id: &str, epoch: u64) -> Callbacks {
        let login = self.sessions.lock().get(id).map(|s| s.login.clone()).unwrap_or_default();
        let (me, sid) = (self.clone(), id.to_string());
        let on_data = Arc::new(move |d: &[u8]| {
            me.emit(Event::Data { session: sid.clone(), data: d.to_vec() });
            let rec = me.sessions.lock().get(&sid).and_then(|s| s.recorder.clone());
            if let Some(r) = rec {
                r.write_output(d);
            }
            let mut l = login.lock();
            if l.active {
                l.buf.extend_from_slice(d);
                if l.buf.len() > 16384 {
                    let cut = l.buf.len() - 16384;
                    l.buf.drain(..cut);
                }
            }
        });
        let (me, sid) = (self.clone(), id.to_string());
        let on_error = Arc::new(move |e: String| {
            let ok = me
                .with(&sid, |s| {
                    if s.epoch != epoch {
                        return false;
                    }
                    s.info.state = SessionState::Error;
                    s.info.error = e.clone();
                    true
                })
                .unwrap_or(false);
            if ok {
                me.emit_state(&sid, SessionState::Error, &e);
            }
        });
        let (me, sid) = (self.clone(), id.to_string());
        let on_close = Arc::new(move || {
            let me = me.clone();
            let sid = sid.clone();
            // Close callbacks can fire inside a client's own lock; handle them off-thread.
            std::thread::spawn(move || me.handle_closed(&sid, epoch));
        });
        Callbacks { on_data, on_error, on_close }
    }

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

    // --- per-protocol connect -----------------------------------------------------------

    fn connect_ssh(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let mut p = p.clone();
        let sc = p.ssh.get_or_insert_with(SshConfig::defaults).clone();
        let password = self.secret_fn(id, &p.id, SecretKind::Password, sc.secret_mode, prompt.clone());
        let passphrase = self.secret_fn(id, &p.id, SecretKind::Passphrase, sc.key_secret_mode, prompt);
        let c = SshClient::new(&p, self.terminal_callbacks(id, epoch));
        let stored = |k: SecretKind| self.secrets.get(&k.key(&p.id)).unwrap_or_default();
        c.set_stored_secrets(stored(SecretKind::Totp), stored(SecretKind::ProxyPassword));
        c.connect(self.host_key_check(id, sc.host_key_policy), Some(password), Some(passphrase))?;
        for pf in sc.port_forwards.iter().filter(|f| f.enabled) {
            match c.start_forward(pf.clone()) {
                Ok(()) => self.emit(Event::ForwardStarted { session: id.into(), forward: pf.clone() }),
                Err(e) => self.emit(Event::ForwardError { session: id.into(), forward_id: pf.id.clone(), label: pf.label.clone(), error: e.to_string() }),
            }
        }
        self.start_login_scripts(id, epoch, sc.login_scripts.clone());
        Ok(Client::Ssh(c))
    }

    /// An SSH connection used only to tunnel another protocol (IRC, VNC).
    #[allow(clippy::too_many_arguments)]
    fn tunnel(
        self: &Arc<Self>,
        id: &str,
        profile_id: &str,
        host: &str,
        port: i64,
        user: &str,
        method: &str,
        key: &str,
        agent: bool,
        mode: SecretMode,
        kind: &str,
        prompt: Prompt,
    ) -> Result<Arc<SshClient>> {
        let mut tp = Profile::new(Protocol::Ssh);
        tp.host = host.into();
        tp.port = if port == 0 { 22 } else { port };
        tp.username = user.into();
        let c = tp.ssh.as_mut().unwrap();
        c.auth_methods = vec![if method.is_empty() { "publickey".into() } else { method.into() }];
        c.private_key_path = key.into();
        c.agent_auth = agent;
        let (pw_kind, pp_kind) =
            if kind == "vnc" { (SecretKind::VncSshPassword, SecretKind::VncSshPassphrase) } else { (SecretKind::IrcSshPassword, SecretKind::IrcSshPassphrase) };
        let password = (method == "password").then(|| self.secret_fn(id, profile_id, pw_kind, mode, prompt.clone()));
        let passphrase = self.secret_fn(id, profile_id, pp_kind, mode, prompt);
        let t = SshClient::new(&tp, Callbacks::noop());
        t.connect_tunnel_only(self.host_key_check(id, HostKeyPolicy::Ask), password, Some(passphrase))?;
        Ok(t)
    }

    fn connect_github(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let gh = p.github.clone().ok_or_else(|| format!("GitHub configuration missing on profile {:?}", p.id))?;
        let token = self.secret_fn(id, &p.id, SecretKind::GithubToken, gh.secret_mode, prompt.clone())().map_err(|e| format!("obtaining GitHub token: {e}"))?;
        if token.is_empty() {
            return err("GitHub token cannot be empty".into());
        }
        let mut client = github::Client::new(&gh.base_url, &token);
        let login = match client.validate_token() {
            Ok(l) => l,
            Err(e) => {
                let msg = e.to_string().to_lowercase();
                let auth = ["token validation failed", "authentication failed", "http 401", "http 403"].iter().any(|k| msg.contains(k));
                let retry = auth.then(|| prompt("GitHub token did not work. Please enter a valid token:").ok()).flatten().filter(|t| !t.is_empty());
                let Some(t) = retry else { return err(format!("GitHub authentication failed: {e}")) };
                if gh.secret_mode == SecretMode::Saved {
                    let _ = self.with(id, |s| s.session_secrets.lock().pending.insert(SecretKind::GithubToken.key(&p.id), Zeroizing::new(t.clone())));
                }
                client = github::Client::new(&gh.base_url, &t);
                client.validate_token().map_err(|e| format!("GitHub authentication failed: {e}"))?
            }
        };
        let client = Arc::new(client);
        if !self.is_current(id, epoch) {
            return err(format!("session {id:?} was closed while connecting"));
        }
        let fetch = {
            let (me, sid, client) = (self.clone(), id.to_string(), client.clone());
            move |status: &str| {
                me.emit(Event::GithubStatus { session: sid.clone(), status: status.into() });
                let (me2, sid2) = (me.clone(), sid.clone());
                client.fetch_repos_with_progress(move |done, total| {
                    if total > 0 {
                        me2.emit(Event::GithubStatus { session: sid2.clone(), status: format!("Loading commit info ({done}/{total})") });
                    }
                })
            }
        };
        let repos = fetch("Loading repositories...").map_err(|e| format!("fetching repositories: {e}"))?;
        self.emit(Event::GithubRepos { session: id.into(), login: login.clone(), repos });
        let interval = Duration::from_secs(if gh.refresh_interval_secs < 60 { 300 } else { gh.refresh_interval_secs as u64 });
        let stop = Arc::new(AtomicBool::new(false));
        let (me, sid, stop2) = (self.clone(), id.to_string(), stop.clone());
        std::thread::spawn(move || {
            loop {
                let deadline = std::time::Instant::now() + interval;
                while std::time::Instant::now() < deadline {
                    if stop2.load(Ordering::SeqCst) {
                        return;
                    }
                    std::thread::sleep(Duration::from_millis(500));
                }
                match fetch("Refreshing repositories...") {
                    Ok(repos) => me.emit(Event::GithubRepos { session: sid.clone(), login: login.clone(), repos }),
                    Err(e) => me.emit(Event::GithubError { session: sid.clone(), error: e.to_string() }),
                }
            }
        });
        Ok(Client::Github(client, stop))
    }

    fn connect_irc(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let ic = p.irc.clone().ok_or("IRC configuration missing")?;
        let server_pass = if ic.server_password_required {
            self.secret_fn(id, &p.id, SecretKind::IrcServerPassword, ic.server_password_mode, prompt.clone())()
                .map_err(|e| format!("IRC server password: {e}"))?
        } else {
            String::new()
        };
        let ns_pass = if ic.nick_serv_identify {
            self.secret_fn(id, &p.id, SecretKind::IrcNickServPassword, ic.nick_serv_mode, prompt.clone())().map_err(|e| format!("NickServ password: {e}"))?
        } else {
            String::new()
        };
        let mut rooms = ic.auto_join_rooms.clone();
        if !p.id.is_empty() {
            let last = self.store.irc_last_rooms(&p.id).map_err(|e| format!("loading remembered IRC rooms: {e}"))?;
            rooms = crate::storage::dedup_rooms(&[&rooms, &last]);
        }
        let tunnel = if ic.ssh_tunnel {
            Some(self.tunnel(
                id,
                &p.id,
                &ic.ssh_host,
                ic.ssh_port,
                &ic.ssh_username,
                &ic.ssh_auth_method,
                &ic.ssh_private_key,
                ic.ssh_agent_auth,
                ic.ssh_secret_mode,
                "irc",
                prompt,
            )?)
        } else {
            None
        };
        let cb = self.terminal_callbacks(id, epoch);
        let (me, sid) = (self.clone(), id.to_string());
        let on_event: irc::EventFn = Arc::new(move |e| me.emit(Event::Irc { session: sid.clone(), event: e }));
        let (me, sid, pid) = (self.clone(), id.to_string(), p.id.clone());
        let on_rooms: irc::RoomsChangedFn = Arc::new(move |rooms: Vec<String>| {
            if pid.is_empty() {
                return;
            }
            if let Err(e) = me.store.save_irc_last_rooms(&pid, &rooms) {
                me.emit(Event::Irc {
                    session: sid.clone(),
                    event: irc::Event {
                        kind: "system".into(),
                        target: irc::SERVER_TARGET.into(),
                        text: format!("Could not remember open IRC rooms: {e}"),
                        timestamp: chrono::Utc::now().timestamp_millis(),
                        ..Default::default()
                    },
                });
            }
        });
        let connector: Option<irc::Connector> = tunnel.clone().map(|t| {
            let (host, port) = (p.host.clone(), p.port as u16);
            Arc::new(move || {
                let s = t.open_tunnel(&host, port)?;
                s.set_read_timeout(Some(Duration::from_millis(50)))?;
                Ok(Box::new(s) as Box<dyn irc::ReadWrite>)
            }) as irc::Connector
        });
        let cfg = irc::Config {
            server: p.host.clone(),
            port: p.port as u16,
            nick: ic.nick.clone(),
            user: if p.username.is_empty() { ic.nick.clone() } else { p.username.clone() },
            real_name: ic.real_name.clone(),
            use_tls: ic.use_tls,
            server_pass,
            auto_join_rooms: rooms,
            nickserv_identify: ic.nick_serv_identify,
            nickserv_account: ic.nick_serv_account.clone(),
            nickserv_password: ns_pass,
            connector,
            connect_timeout: Duration::from_secs(p.connect_timeout.max(1) as u64),
            on_event,
            on_error: cb.on_error,
            on_close: cb.on_close,
            on_rooms_changed: on_rooms,
        };
        let c = irc::Client::new(cfg);
        if let Err(e) = c.connect() {
            if let Some(t) = tunnel {
                t.close();
            }
            return Err(e);
        }
        Ok(Client::Irc(c, tunnel))
    }

    fn connect_vnc(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let vc = p.vnc.clone().ok_or("VNC configuration missing")?;
        let host = if vc.vnc_host.is_empty() { p.host.clone() } else { vc.vnc_host.clone() };
        let port = if vc.vnc_port == 0 { 5900 } else { vc.vnc_port } as u16;
        let tunnel = if vc.ssh_tunnel {
            Some(self.tunnel(
                id,
                &p.id,
                &vc.ssh_host,
                vc.ssh_port,
                &vc.ssh_username,
                &vc.ssh_auth_method,
                &vc.ssh_private_key,
                vc.ssh_agent_auth,
                vc.ssh_secret_mode,
                "vnc",
                prompt.clone(),
            )?)
        } else {
            None
        };
        let password = self.secret_fn(id, &p.id, SecretKind::VncPassword, vc.secret_mode, prompt);
        let cb = self.terminal_callbacks(id, epoch);
        let (me, sid) = (self.clone(), id.to_string());
        let (me2, sid2) = (self.clone(), id.to_string());
        let clip: clients::vnc::ClipboardFn = Arc::new(move |text| me2.emit(Event::VncClipboard { session: sid2.clone(), text }));
        let c = connect_vnc_client(
            &host,
            port,
            &vc,
            p.connect_timeout,
            password,
            tunnel.clone(),
            cb,
            Arc::new(move || me.emit(Event::VncFrame { session: sid.clone() })),
            clip,
        );
        match c {
            Ok(c) => {
                self.emit(Event::VncReady { session: id.into() });
                Ok(Client::Vnc(c, tunnel))
            }
            Err(e) => {
                if let Some(t) = tunnel {
                    t.close();
                }
                Err(e)
            }
        }
    }

    // --- local shell --------------------------------------------------------------------

    pub fn connect_local(self: &Arc<Self>, shell: &str) -> String {
        let id = new_id();
        let info = SessionInfo {
            id: id.clone(),
            profile_id: String::new(),
            profile_name: "Local Machine".into(),
            protocol: Protocol::Local,
            host: "localhost".into(),
            port: 0,
            state: SessionState::Connecting,
            tab_name: "Local".into(),
            pinned: false,
            connected_at: now(),
            error: String::new(),
        };
        let epoch = self.new_session(info, None, shell.into(), SessionSecrets::default());
        self.emit_state(&id, SessionState::Connecting, "");
        let me = self.clone();
        let id2 = id.clone();
        std::thread::spawn(move || me.start_local(&id2, epoch, 80, 24));
        id
    }

    /// Starts a fresh shell in an exited local tab, keeping its scrollback and last directory.
    pub fn restart_local(self: &Arc<Self>, id: &str, cols: u16, rows: u16) -> Result<()> {
        let epoch = self.with(id, |s| {
            if s.info.protocol != Protocol::Local {
                return Err(format!("sessions: session {id:?} is not local"));
            }
            if matches!(s.client, Client::Local(_)) || matches!(s.info.state, SessionState::Connecting | SessionState::Connected) {
                return Err(format!("sessions: local session {id:?} is still running"));
            }
            s.info.state = SessionState::Connecting;
            s.info.error.clear();
            Ok(s.epoch)
        })??;
        self.emit_state(id, SessionState::Connecting, "");
        let (me, id2) = (self.clone(), id.to_string());
        std::thread::spawn(move || me.start_local(&id2, epoch, cols, rows));
        Ok(())
    }

    fn start_local(self: &Arc<Self>, id: &str, epoch: u64, cols: u16, rows: u16) {
        let Ok((shell, cwd, pending)) = self.with(id, |s| (s.local_shell.clone(), s.local_cwd.clone(), s.pending_resize.take())) else { return };
        let (cols, rows) = pending.unwrap_or((cols, rows));
        let mut cb = self.terminal_callbacks(id, epoch);
        // A local shell exiting keeps its tab ("press enter to restart").
        let (me, sid) = (self.clone(), id.to_string());
        cb.on_close = Arc::new(move || {
            let (me, sid) = (me.clone(), sid.clone());
            std::thread::spawn(move || me.local_closed(&sid, epoch));
        });
        let c = LocalShell::new(&shell, cwd, cols, rows, cb);
        let r = c.start().map(|_| Client::Local(c.clone()));
        self.finish_connect(id, epoch, r, |me| me.store.add_audit("connect", "", id, "Connected via local to localhost"));
    }

    fn local_closed(self: &Arc<Self>, id: &str, epoch: u64) {
        let cwd = self.with(id, |s| {
            if s.epoch != epoch || s.closed {
                return None;
            }
            match std::mem::replace(&mut s.client, Client::None) {
                Client::Local(c) => Some(c.working_directory()),
                other => {
                    s.client = other;
                    None
                }
            }
        });
        if let Ok(Some(cwd)) = cwd {
            let _ = self.with(id, |s| s.local_cwd = Some(cwd));
            self.handle_closed(id, epoch);
        }
    }

    // --- lifecycle ----------------------------------------------------------------------

    /// Explicit close: the tab is gone, so no reconnect.
    pub fn disconnect(&self, id: &str) -> Result<()> {
        let s = self.sessions.lock().remove(id).ok_or_else(|| format!("sessions: session {id:?} not found"))?;
        self.close_session(s);
        self.store.add_audit("disconnect", "", id, "Session disconnected");
        Ok(())
    }

    /// Closes and re-opens a session from its profile, keeping the tab name and pin; returns the
    /// new session id.
    pub fn reconnect(self: &Arc<Self>, id: &str) -> Result<String> {
        let s = self.sessions.lock().remove(id).ok_or_else(|| format!("sessions: session {id:?} not found"))?;
        let (profile, tab, pinned, proto, shell) = (s.profile.clone(), s.info.tab_name.clone(), s.info.pinned, s.info.protocol, s.local_shell.clone());
        let session_secrets = s.session_secrets.clone();
        self.close_session(s);
        let new = match (proto, profile) {
            (Protocol::Local, _) => self.connect_local(&shell),
            (_, Some(p)) => self.connect_with(p, session_secrets),
            _ => return err("sessions: nothing to reconnect".into()),
        };
        let _ = self.with(&new, |s| {
            s.info.tab_name = tab;
            s.info.pinned = pinned;
        });
        Ok(new)
    }

    fn close_session(&self, mut s: Session) {
        s.closed = true;
        if let Some(c) = s.cancel_reconnect.take() {
            c.store(true, Ordering::SeqCst);
        }
        if let Some(r) = s.recorder.take() {
            let _ = r.close();
        }
        s.client.close();
    }

    fn handle_closed(self: &Arc<Self>, id: &str, epoch: u64) {
        let profile = {
            let mut map = self.sessions.lock();
            let Some(s) = map.get_mut(id).filter(|s| s.epoch == epoch && !s.closed) else { return };
            s.info.state = SessionState::Disconnected;
            if !matches!(s.client, Client::Local(_)) {
                s.client = Client::None;
            }
            s.profile.clone()
        };
        self.store.add_audit("disconnect", "", id, "Session disconnected");
        if let Some(p) = profile.filter(|p| p.auto_reconnect()) {
            self.schedule_reconnect(id, epoch, p);
            return;
        }
        self.emit(Event::Closed { session: id.into() });
        self.emit_state(id, SessionState::Disconnected, "");
    }

    /// Exponential backoff 1, 2, 4 … 30 s, up to the profile's attempt limit.
    fn schedule_reconnect(self: &Arc<Self>, id: &str, epoch: u64, p: Profile) {
        let max = p.max_reconnect_tries();
        let cancel = Arc::new(AtomicBool::new(false));
        let attempt = self.with(id, |s| {
            s.reconnect_count += 1;
            if s.reconnect_count > max {
                s.info.state = SessionState::Disconnected;
                return None;
            }
            s.info.state = SessionState::Reconnecting;
            s.info.error.clear();
            if let Some(c) = s.cancel_reconnect.replace(cancel.clone()) {
                c.store(true, Ordering::SeqCst);
            }
            Some(s.reconnect_count)
        });
        let Ok(attempt) = attempt else { return };
        let Some(attempt) = attempt else {
            self.emit(Event::Closed { session: id.into() });
            self.emit_state(id, SessionState::Disconnected, "auto-reconnect failed after maximum attempts");
            return;
        };
        let delay = (1u64 << (attempt - 1).min(5)).min(30);
        self.emit(Event::State {
            session: id.into(),
            state: SessionState::Reconnecting,
            error: String::new(),
            retry: Some(Retry { attempt, max, delay_secs: delay }),
        });
        let (me, id) = (self.clone(), id.to_string());
        std::thread::spawn(move || {
            let deadline = std::time::Instant::now() + Duration::from_secs(delay);
            while std::time::Instant::now() < deadline {
                if cancel.load(Ordering::SeqCst) {
                    return;
                }
                std::thread::sleep(Duration::from_millis(100));
            }
            let ok = me
                .with(&id, |s| {
                    if s.epoch != epoch || s.closed || cancel.load(Ordering::SeqCst) {
                        return false;
                    }
                    s.client = Client::None;
                    s.info.state = SessionState::Connecting;
                    true
                })
                .unwrap_or(false);
            if !ok {
                return;
            }
            me.emit_state(&id, SessionState::Connecting, "");
            let prompt = me.prompt_for(&id, "Reconnection Credentials", Duration::from_secs(300));
            match me.connect_protocol(&id, epoch, &p, prompt) {
                Ok(c) => me.finish_connect(&id, epoch, Ok(c), |_| {}),
                Err(e) => {
                    let current = me.with(&id, |s| {
                        if s.epoch != epoch || s.closed {
                            return false;
                        }
                        s.info.state = SessionState::Error;
                        s.info.error = e.to_string();
                        attempt_failed(&s.session_secrets, &e.to_string());
                        true
                    });
                    if current.unwrap_or(false) {
                        me.emit_state(&id, SessionState::Error, &e.to_string());
                        me.schedule_reconnect(&id, epoch, p);
                    }
                }
            }
        });
    }

    pub fn cancel_reconnect(&self, id: &str) {
        let found = self
            .with(id, |s| {
                if let Some(c) = s.cancel_reconnect.take() {
                    c.store(true, Ordering::SeqCst);
                    s.reconnect_count = 0;
                    s.info.state = SessionState::Disconnected;
                }
            })
            .is_ok();
        if found {
            self.emit_state(id, SessionState::Disconnected, "");
        }
    }

    pub fn close_all(&self) {
        let all: Vec<Session> = self.sessions.lock().drain().map(|(_, s)| s).collect();
        for s in all {
            self.close_session(s);
        }
    }

    // --- login scripts ------------------------------------------------------------------

    fn start_login_scripts(self: &Arc<Self>, id: &str, epoch: u64, steps: Vec<LoginScriptStep>) {
        if steps.is_empty() {
            return;
        }
        let Ok(login) = self.with(id, |s| s.login.clone()) else { return };
        *login.lock() = LoginBuf { active: true, buf: vec![] };
        let (me, id) = (self.clone(), id.to_string());
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(500));
            for (i, step) in steps.iter().enumerate() {
                // Wait until the connect has been recorded as Connected.
                let mut waited = 0;
                while me.with(&id, |s| s.info.state).ok() == Some(SessionState::Connecting) && waited < 100 {
                    std::thread::sleep(Duration::from_millis(50));
                    waited += 1;
                }
                if !me.is_current(&id, epoch) || me.with(&id, |s| s.info.state).ok() != Some(SessionState::Connected) {
                    break;
                }
                match step.kind.as_str() {
                    "wait" => {
                        let timeout = Duration::from_secs(if step.timeout <= 0 { 10 } else { step.timeout as u64 });
                        if !wait_for_output(&login, &step.value, timeout) {
                            me.emit(Event::Debug {
                                session: id.clone(),
                                message: format!("login script step {} timed out waiting for {:?}", i + 1, step.value),
                            });
                            break;
                        }
                    }
                    "send" => {
                        if let Err(e) = me.write(&id, format!("{}\n", step.value).as_bytes()) {
                            me.emit(Event::Debug { session: id.clone(), message: format!("login script step {} failed to send: {e}", i + 1) });
                            break;
                        }
                        std::thread::sleep(Duration::from_millis(200));
                    }
                    _ => {}
                }
            }
            *login.lock() = LoginBuf::default();
        });
    }

    // --- I/O ----------------------------------------------------------------------------

    pub fn write(&self, id: &str, data: &[u8]) -> Result<()> {
        let (client, rec) = self.with(id, |s| (s.client.clone(), s.recorder.clone()))?;
        if let Some(r) = rec {
            r.write_input(data);
        }
        match client {
            Client::Ssh(c) => c.write(data),
            Client::Telnet(c) => c.write(data),
            Client::Raw(c) => c.write(data),
            Client::Serial(c) => c.write(data),
            Client::Local(c) => c.write(data),
            _ => err(format!("sessions: no active client for session {id:?}")),
        }
    }

    fn resize_client(&self, c: &Client, cols: u16, rows: u16) -> Result<()> {
        if cols == 0 || rows == 0 {
            return Ok(());
        }
        match c {
            Client::Ssh(c) => c.resize(cols as u32, rows as u32),
            Client::Telnet(c) => c.resize(cols, rows),
            Client::Local(c) => c.resize(cols, rows),
            _ => Ok(()),
        }
    }

    /// Resizes the remote PTY; before a client exists the size is kept and applied on connect.
    pub fn resize(&self, id: &str, cols: u16, rows: u16) -> Result<()> {
        let c = self.with(id, |s| {
            if matches!(s.client, Client::None) {
                s.pending_resize = Some((cols, rows));
            }
            s.client.clone()
        })?;
        self.resize_client(&c, cols, rows)
    }

    pub fn info(&self, id: &str) -> Option<SessionInfo> {
        self.sessions.lock().get(id).map(|s| s.info.clone())
    }

    pub fn list(&self) -> Vec<SessionInfo> {
        self.sessions.lock().values().map(|s| s.info.clone()).collect()
    }

    pub fn profile(&self, id: &str) -> Option<Profile> {
        self.sessions.lock().get(id).and_then(|s| s.profile.clone())
    }

    pub fn rename(&self, id: &str, name: &str) -> Result<()> {
        self.with(id, |s| s.info.tab_name = name.into())
    }

    pub fn pin(&self, id: &str, pinned: bool) -> Result<()> {
        self.with(id, |s| s.info.pinned = pinned)
    }

    pub fn send_break(&self, id: &str) -> Result<()> {
        match self.client(id)? {
            Client::Telnet(c) => c.send_break(),
            Client::Serial(c) => c.send_break(),
            _ => err(format!("sessions: session {id:?} does not support break")),
        }
    }

    pub fn set_serial_lines(&self, id: &str, dtr: bool, rts: bool) -> Result<()> {
        match self.client(id)? {
            Client::Serial(c) => c.set_lines(dtr, rts),
            _ => err(format!("sessions: session {id:?} is not a serial connection")),
        }
    }

    // --- RAW ----------------------------------------------------------------------------

    fn raw(&self, id: &str) -> Result<Arc<raw::RawClient>> {
        match self.client(id)? {
            Client::Raw(c) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a RAW connection")),
        }
    }

    pub fn raw_write_text(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_text(s)
    }

    pub fn raw_write_hex(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_hex(s)
    }

    pub fn raw_write_escaped(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_escaped(s)
    }

    pub fn raw_history(&self, id: &str) -> Result<Vec<raw::HistoryEntry>> {
        Ok(self.raw(id)?.history())
    }

    pub fn raw_clear_history(&self, id: &str) -> Result<()> {
        self.raw(id)?.clear_history();
        Ok(())
    }

    pub fn raw_save_capture(&self, id: &str, path: &std::path::Path) -> Result<()> {
        self.raw(id)?.save_capture(path)
    }

    // --- IRC ----------------------------------------------------------------------------

    pub fn irc(&self, id: &str) -> Result<Arc<irc::Client>> {
        match self.client(id)? {
            Client::Irc(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not an IRC connection")),
        }
    }

    // --- GitHub -------------------------------------------------------------------------

    pub fn github(&self, id: &str) -> Result<Arc<github::Client>> {
        match self.client(id)? {
            Client::Github(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a GitHub connection")),
        }
    }

    pub fn vnc(&self, id: &str) -> Result<Arc<clients::vnc::Client>> {
        match self.client(id)? {
            Client::Vnc(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a VNC connection")),
        }
    }

    // --- SSH extras ---------------------------------------------------------------------

    pub fn ssh(&self, id: &str) -> Result<Arc<SshClient>> {
        match self.client(id)? {
            Client::Ssh(c) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not an SSH connection")),
        }
    }

    pub fn ping(&self, id: &str) -> Result<i64> {
        self.ssh(id)?.ping()
    }

    pub fn cwd(&self, id: &str) -> Result<String> {
        self.ssh(id)?.run_command("pwd")
    }

    pub fn start_forward(&self, id: &str, rule: PortForward) -> Result<()> {
        self.ssh(id)?.start_forward(rule.clone())?;
        self.emit(Event::ForwardStarted { session: id.into(), forward: rule });
        Ok(())
    }

    pub fn stop_forward(&self, id: &str, fid: &str) -> Result<()> {
        self.ssh(id)?.stop_forward(fid)?;
        self.emit(Event::ForwardStopped { session: id.into(), forward_id: fid.into() });
        Ok(())
    }

    pub fn forwards(&self, id: &str) -> Result<Vec<PortForward>> {
        Ok(self.ssh(id)?.active_forwards())
    }

    pub fn sftp_download(&self, id: &str, remote: &str, local: &std::path::Path) -> Result<()> {
        let s = self.ssh(id)?.sftp()?;
        clients::sftp::download(&s, remote, local, &|p| self.emit(Event::SftpProgress { session: id.into(), progress: p }))
    }

    pub fn sftp_upload(&self, id: &str, local: &std::path::Path, remote: &str) -> Result<()> {
        let s = self.ssh(id)?.sftp()?;
        clients::sftp::upload(&s, local, remote, &|p| self.emit(Event::SftpProgress { session: id.into(), progress: p }))
    }

    // --- recording ----------------------------------------------------------------------

    pub fn start_recording(&self, id: &str, cols: u16, rows: u16) -> Result<()> {
        let (exists, title) = self.with(id, |s| (s.recorder.is_some(), format!("{} — {}", s.info.profile_name, s.info.host)))?;
        if exists {
            return err(format!("sessions: session {id:?} is already being recorded"));
        }
        let r = Arc::new(
            Recorder::new(self.recordings.dir(), id, &title, cols as i64, rows as i64).map_err(|e| format!("sessions: failed to start recording: {e}"))?,
        );
        self.with(id, |s| s.recorder = Some(r))?;
        self.emit(Event::RecordingStarted { session: id.into() });
        Ok(())
    }

    pub fn stop_recording(&self, id: &str) -> Result<()> {
        let r = self.with(id, |s| s.recorder.take())?.ok_or_else(|| format!("sessions: session {id:?} is not being recorded"))?;
        r.close().map_err(|e| format!("sessions: failed to stop recording: {e}"))?;
        self.emit(Event::RecordingStopped { session: id.into(), duration: r.duration(), events: r.event_count() });
        Ok(())
    }

    pub fn is_recording(&self, id: &str) -> bool {
        self.with(id, |s| s.recorder.is_some()).unwrap_or(false)
    }

    pub fn recordings(&self) -> &recording::Store {
        &self.recordings
    }
}

/// Builds and connects a VNC client from a profile's settings, optionally through an SSH tunnel.
#[allow(clippy::too_many_arguments)]
fn connect_vnc_client(
    host: &str,
    port: u16,
    vc: &VncConfig,
    timeout: i64,
    password: SecretFn,
    tunnel: Option<Arc<SshClient>>,
    cb: Callbacks,
    on_frame: Arc<dyn Fn() + Send + Sync>,
    on_clipboard: clients::vnc::ClipboardFn,
) -> Result<Arc<clients::vnc::Client>> {
    let connector: Option<irc::Connector> = tunnel.map(|t| {
        let host = host.to_string();
        Arc::new(move || {
            let s = t.open_tunnel(&host, port)?;
            s.set_read_timeout(Some(Duration::from_millis(50)))?;
            Ok(Box::new(s) as Box<dyn irc::ReadWrite>)
        }) as irc::Connector
    });
    let mut cfg = clients::vnc::Config {
        host: host.into(),
        port,
        password: None,
        shared: vc.shared_session,
        view_only: vc.view_only,
        quality: vc.quality.clamp(0, 9) as u8,
        connector,
        connect_timeout: Duration::from_secs(timeout.max(1) as u64),
        on_frame,
        on_clipboard,
        on_error: cb.on_error,
        on_close: cb.on_close,
        ..Default::default()
    };
    let c = clients::vnc::Client::new(cfg.clone());
    match c.connect() {
        Ok(()) => Ok(c),
        // Ask for the password only when the server wants one.
        Err(e) if e.to_string().to_lowercase().contains("password") => {
            cfg.password = Some(password()?);
            let c = clients::vnc::Client::new(cfg);
            c.connect()?;
            Ok(c)
        }
        Err(e) => Err(e),
    }
}

fn wait_for_output(login: &Mutex<LoginBuf>, pattern: &str, timeout: Duration) -> bool {
    if pattern.is_empty() {
        return true;
    }
    let deadline = std::time::Instant::now() + timeout;
    while std::time::Instant::now() < deadline {
        {
            let mut l = login.lock();
            let text = String::from_utf8_lossy(&l.buf).into_owned();
            if let Some(i) = text.find(pattern) {
                let consumed = text[..i + pattern.len()].len();
                let n = consumed.min(l.buf.len());
                l.buf.drain(..n);
                return true;
            }
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::secrets::{Keychain, Secrets, Vault};
    use std::sync::atomic::AtomicUsize;

    fn manager(name: &str) -> (Arc<Manager>, PathBuf) {
        let dir = std::env::temp_dir().join(format!("nexus-secretfn-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let store = Arc::new(Store::open(&dir.join("nexus.db")).unwrap());
        let secrets = Arc::new(Secrets { vault: Vault::new(dir.join("vault")), keychain: Keychain::new("NexusTerminalTest"), keychain_ok: false });
        (Manager::new(store, secrets, dir.join("recordings")), dir)
    }

    fn counting_prompt(n: Arc<AtomicUsize>) -> Prompt {
        Arc::new(move |_| {
            n.fetch_add(1, Ordering::SeqCst);
            Ok("hunter2".into())
        })
    }

    #[test]
    fn saved_credentials_are_stored_only_after_success_and_reasked_after_rejection() {
        let (m, dir) = manager("saved");
        let warnings: Arc<Mutex<Vec<String>>> = Arc::default();
        let w = warnings.clone();
        m.set_sink(Arc::new(move |e| {
            if let Event::Warning { message, .. } = e {
                w.lock().push(message);
            }
        }));
        let p = Profile { host: "127.0.0.1".into(), port: 1, ..Profile::new(Protocol::Raw) };
        let key = SecretKind::Password.key(&p.id);
        let info = |id: &str| SessionInfo {
            id: id.into(),
            profile_id: p.id.clone(),
            profile_name: String::new(),
            protocol: Protocol::Raw,
            host: p.host.clone(),
            port: p.port,
            state: SessionState::Connecting,
            tab_name: String::new(),
            pinned: false,
            connected_at: now(),
            error: String::new(),
        };
        let asked = Arc::new(AtomicUsize::new(0));
        let resolve = |m: &Arc<Manager>, id: &str| m.secret_fn(id, &p.id, SecretKind::Password, SecretMode::Saved, counting_prompt(asked.clone()))().unwrap();

        // Locked store: the credential can't be saved, and the user is told.
        let e = m.new_session(info("s0"), Some(p.clone()), String::new(), SessionSecrets::default());
        resolve(&m, "s0");
        m.finish_connect("s0", e, Ok(Client::None), |_| {});
        assert_eq!(warnings.lock().len(), 1);

        m.secrets.vault.initialise("pw").unwrap();
        let e = m.new_session(info("s1"), Some(p.clone()), String::new(), SessionSecrets::default());
        // A typed password the server rejects is never stored.
        resolve(&m, "s1");
        m.finish_connect("s1", e, Err("ssh: unable to authenticate".into()), |_| {});
        assert!(m.secrets.get(&key).is_none());
        // Typed again and accepted: stored.
        resolve(&m, "s1");
        m.finish_connect("s1", e, Ok(Client::None), |_| {});
        assert_eq!(m.secrets.get(&key).as_deref(), Some("hunter2"));
        assert_eq!(asked.load(Ordering::SeqCst), 3);

        // The saved value is reused; once the server rejects it, the next attempt asks again.
        let e = m.new_session(info("s2"), Some(p.clone()), String::new(), SessionSecrets::default());
        resolve(&m, "s2");
        assert_eq!(asked.load(Ordering::SeqCst), 3);
        m.finish_connect("s2", e, Err("VNC authentication failed".into()), |_| {});
        resolve(&m, "s2");
        assert_eq!(asked.load(Ordering::SeqCst), 4);
        // A network error doesn't count as a rejection.
        m.finish_connect("s2", e, Err("connection refused".into()), |_| {});
        resolve(&m, "s2");
        assert_eq!(asked.load(Ordering::SeqCst), 4);
        m.close_all();
        let _ = std::fs::remove_dir_all(dir);
    }

    #[test]
    fn session_only_secrets_survive_reconnects() {
        let (m, dir) = manager("cache");
        let p = Profile { host: "127.0.0.1".into(), port: 1, ..Profile::new(Protocol::Raw) };
        let info = SessionInfo {
            id: "s1".into(),
            profile_id: p.id.clone(),
            profile_name: String::new(),
            protocol: Protocol::Raw,
            host: p.host.clone(),
            port: p.port,
            state: SessionState::Connecting,
            tab_name: String::new(),
            pinned: false,
            connected_at: now(),
            error: String::new(),
        };
        m.new_session(info, Some(p.clone()), String::new(), SessionSecrets::default());
        let asked = Arc::new(AtomicUsize::new(0));
        // Two separate resolutions (as two connects of the same session make) prompt once.
        for _ in 0..2 {
            let f = m.secret_fn("s1", &p.id, SecretKind::IrcNickServPassword, SecretMode::SessionOnly, counting_prompt(asked.clone()));
            assert_eq!(f().unwrap(), "hunter2");
        }
        assert_eq!(asked.load(Ordering::SeqCst), 1);
        // Ask-every-time still prompts each time.
        let f = m.secret_fn("s1", &p.id, SecretKind::IrcNickServPassword, SecretMode::AskEveryTime, counting_prompt(asked.clone()));
        f().unwrap();
        assert_eq!(asked.load(Ordering::SeqCst), 2);
        // An explicit reconnect carries the cache to the new session id.
        let new = m.reconnect("s1").unwrap();
        let f = m.secret_fn(&new, &p.id, SecretKind::IrcNickServPassword, SecretMode::SessionOnly, counting_prompt(asked.clone()));
        assert_eq!(f().unwrap(), "hunter2");
        assert_eq!(asked.load(Ordering::SeqCst), 2);
        m.close_all();
        let _ = std::fs::remove_dir_all(dir);
    }

    #[test]
    fn changed_host_keys_can_be_rejected_accepted_once_or_updated() {
        use crate::clients::ssh::HostKeyInfo;
        let (m, dir) = manager("hostkey");
        let answers: Arc<Mutex<Vec<HostKeyDecision>>> = Arc::default();
        let prompts: Arc<Mutex<Vec<Option<String>>>> = Arc::default();
        let (a, pr) = (answers.clone(), prompts.clone());
        m.set_sink(Arc::new(move |e| {
            if let Event::HostKeyVerify { stored, reply, .. } = e {
                pr.lock().push(stored);
                let _ = reply.send(a.lock().remove(0));
            }
        }));
        let info = |fp: &str| HostKeyInfo { host: "h".into(), port: 22, key_type: "ssh-ed25519".into(), fingerprint: fp.into(), public_key_b64: "AAAA".into() };
        let stored = |m: &Manager| m.store.get_known_host("h", 22).unwrap().map(|k| k.fingerprint);
        // Unknown key: accept once records nothing; accept & save records it.
        let check = m.host_key_check("s", HostKeyPolicy::Ask);
        answers.lock().push(HostKeyDecision::AcceptOnce);
        check(&info("A")).unwrap();
        assert_eq!(stored(&m), None);
        answers.lock().push(HostKeyDecision::AcceptAndSave);
        check(&info("A")).unwrap();
        assert_eq!(stored(&m).as_deref(), Some("A"));
        assert_eq!(prompts.lock().as_slice(), [None, None]);
        // Same key again: no prompt.
        check(&info("A")).unwrap();
        assert_eq!(prompts.lock().len(), 2);
        // Changed key: reject, accept once (record untouched), accept & update (record replaced).
        answers.lock().push(HostKeyDecision::Reject);
        assert!(check(&info("B")).unwrap_err().contains("has changed"));
        answers.lock().push(HostKeyDecision::AcceptOnce);
        check(&info("B")).unwrap();
        assert_eq!(stored(&m).as_deref(), Some("A"));
        answers.lock().push(HostKeyDecision::AcceptAndSave);
        check(&info("B")).unwrap();
        assert_eq!(stored(&m).as_deref(), Some("B"));
        assert_eq!(prompts.lock()[2..], [Some("A".to_string()), Some("A".into()), Some("A".into())]);
        assert_eq!(m.store.list_known_hosts().unwrap().len(), 1);
        // Strict and accept-new never prompt on a changed key.
        for policy in [HostKeyPolicy::Strict, HostKeyPolicy::AcceptNew] {
            assert!(m.host_key_check("s", policy)(&info("C")).unwrap_err().contains("has changed"));
        }
        assert_eq!(prompts.lock().len(), 5);
        let _ = std::fs::remove_dir_all(dir);
    }
}
