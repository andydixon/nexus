// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Local shells and login scripts.

use super::*;

impl Manager {
    // --- local shell --------------------------------------------------------------------

    pub fn connect_local(self: &Arc<Self>, shell: &str) -> String {
        self.connect_local_in(shell, None)
    }

    /// A local shell starting in `cwd` (when it still exists), e.g. a restored tab.
    pub fn connect_local_in(self: &Arc<Self>, shell: &str, cwd: Option<PathBuf>) -> String {
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
        if let Some(dir) = cwd.filter(|d| d.is_dir()) {
            let _ = self.with(&id, |s| s.local_cwd = Some(dir));
        }
        self.emit_state(&id, SessionState::Connecting, "");
        let me = self.clone();
        let id2 = id.clone();
        std::thread::spawn(move || me.start_local(&id2, epoch, 80, 24));
        id
    }

    /// A local session's shell and its current directory.
    pub fn local_state(&self, id: &str) -> Option<(String, Option<PathBuf>)> {
        self.with(id, |s| {
            let cwd = match &s.client {
                Client::Local(c) => Some(c.working_directory()),
                _ => s.local_cwd.clone(),
            };
            (s.local_shell.clone(), cwd)
        })
        .ok()
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

    pub(super) fn start_local(self: &Arc<Self>, id: &str, epoch: u64, cols: u16, rows: u16) {
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

    pub(super) fn local_closed(self: &Arc<Self>, id: &str, epoch: u64) {
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
}

impl Manager {
    // --- login scripts ------------------------------------------------------------------

    pub(super) fn start_login_scripts(self: &Arc<Self>, id: &str, epoch: u64, steps: Vec<LoginScriptStep>) {
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
}

pub(super) fn wait_for_output(login: &Mutex<LoginBuf>, pattern: &str, timeout: Duration) -> bool {
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
