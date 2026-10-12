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

mod credentials;
mod extras;
mod local;
mod protocols;

pub use credentials::is_auth_error;
use credentials::{SessionSecrets, attempt_failed};

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
    DesktopReady {
        session: String,
    },
    DesktopFrame {
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
    Rdp(Arc<clients::rdp::Client>, Option<Arc<SshClient>>),
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
            Client::Rdp(c, tunnel) => {
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
            Protocol::Rdp => self.connect_rdp(id, epoch, p, prompt),
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
            // For a remote desktop the "size" is the tab's size in pixels.
            Client::Rdp(c, _) => {
                c.resize(cols, rows);
                Ok(())
            }
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
