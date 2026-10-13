//! The main window: title bar, connection sidebar, tab strip and workspace (terminals, split
//! panes, protocol viewers), right dock (SFTP or the profile editor), status bar, command
//! palette, prompts and toasts. Session events from nexus-core are drained here every frame.

use crate::state::{Pane, Request, SettingsTab, Shared, Tab, ToastKind};
use crate::terminal::{CursorStyle, MenuAction, TermView};
use crate::theme::{self, ThemeKind, Tokens};
use crate::views::View;
use crate::{chrome, forwards, profile_editor, quick_connect, recordings, settings, sftp, sidebar, snippets, ssh_keys, tabs, transfer, triggers};
use egui::{Key, KeyboardShortcut, Modifiers, Ui};
use nexus_core::models::{Profile, Protocol, SessionInfo, SessionState};
use nexus_core::service::Service;
use nexus_core::sessions::{Event, HostKeyDecision};
use parking_lot::Mutex;
use std::collections::VecDeque;
use std::path::PathBuf;
use std::sync::{Arc, mpsc};

pub struct Options {
    pub screenshot: Option<PathBuf>,
    pub theme: Option<String>,
    /// Open a local terminal at startup and type this command once connected (testing aid).
    pub run_local: Option<String>,
    /// Open a window at startup: editor, quick, settings[:tab], keys, snippets, triggers,
    /// recordings, palette, export, import (screenshot checks).
    pub open: Option<String>,
    /// Connect saved profiles (by name or id) at startup.
    pub connect: Vec<String>,
    /// Command-palette actions (by label) to run once sessions have had time to connect.
    pub actions: Vec<String>,
    /// Replies to the first credential prompts, in order (screenshots of token-protected sessions).
    pub answers: Vec<String>,
    /// How long to wait before taking the screenshot.
    pub shot_delay: std::time::Duration,
}

/// Errors from worker threads, drained into toasts each frame.
static POSTED: Mutex<Vec<String>> = Mutex::new(Vec::new());

pub fn post_error(e: String) {
    POSTED.lock().push(e);
}

/// For the panic hook: the panicking thread may already hold the lock.
pub fn post_error_nonblocking(e: String) {
    if let Some(mut p) = POSTED.try_lock() {
        p.push(e);
    }
}

/// A credential or host-key question from a connecting session.
pub enum Prompt {
    /// `value` is wiped when the prompt is dropped (answered, cancelled or abandoned).
    Secret {
        session: String,
        title: String,
        message: String,
        value: zeroize::Zeroizing<String>,
        reply: mpsc::Sender<String>,
    },
    HostKey {
        host: String,
        key_type: String,
        fingerprint: String,
        stored: Option<String>,
        reply: mpsc::Sender<HostKeyDecision>,
    },
}

/// Modal dialogs owned by the main window.
pub enum Dialog {
    /// A profile already has a live session.
    Reuse {
        profile: String,
        existing: String,
    },
    /// Unencrypted VNC without an SSH tunnel.
    DirectVnc {
        profile: String,
    },
    ConfirmClose {
        session: String,
    },
    ConfirmDisconnectMany {
        sessions: Vec<String>,
    },
    Rename {
        session: String,
        name: String,
    },
    Broadcast {
        picks: Vec<(String, String, bool)>,
    },
    /// Multi-line paste into a terminal without bracketed paste.
    ConfirmPaste {
        session: String,
        text: String,
    },
    /// Guided help for an auth or host-key failure.
    Guided {
        session: String,
        title: String,
        body: String,
        known_hosts: bool,
    },
    Delete {
        profiles: Vec<String>,
    },
    Input {
        title: String,
        label: String,
        value: String,
        then: InputThen,
    },
}

pub enum InputThen {
    Tags { profiles: Vec<String>, replace: bool },
    MoveGroup { profiles: Vec<String> },
}

pub struct App {
    pub s: Shared,
    /// The tab layout as last saved for restoring (see restore.rs), and when it was last checked.
    pub saved_layout: Option<String>,
    layout_checked: f64,
    /// A profile sync in flight; when the next automatic one is due; whether it was asked for.
    sync_job: Option<crate::settings::Job<Result<nexus_core::service::SyncReport, String>>>,
    sync_due: f64,
    sync_manual: bool,
    rx: mpsc::Receiver<Event>,
    pub theme: ThemeKind,
    pub editor: profile_editor::ProfileEditor,
    pub quick: quick_connect::QuickConnect,
    pub settings: settings::Settings,
    pub keys: ssh_keys::SshKeys,
    pub transfer: transfer::Transfer,
    pub sftp: sftp::Sftp,
    pub forwards: forwards::Forwards,
    pub snippets: snippets::Snippets,
    pub triggers: triggers::Triggers,
    pub recordings: recordings::Recordings,
    pub sidebar: sidebar::Sidebar,
    pub palette: Option<chrome::Palette>,
    pub prompts: VecDeque<Prompt>,
    pub dialog: Option<Dialog>,
    pub tabs_ui: tabs::TabsUi,
    pub latency: Option<(String, Result<i64, String>)>,
    pub update: Option<nexus_core::updater::UpdateInfo>,
    screenshot: Option<(PathBuf, std::time::Instant, std::time::Duration)>,
    shot_settle: u32,
    actions: Vec<String>,
    answers: Vec<String>,
    run_local: Option<(String, Option<String>)>,
    /// Apply `prefs.sidebar_width` to the panel on the next frame (after Settings changed it).
    resize_sidebar: bool,
}

const fn sc(m: Modifiers, k: Key) -> KeyboardShortcut {
    KeyboardShortcut::new(m, k)
}
const CMD: Modifiers = Modifiers::COMMAND;
const CMD_SHIFT: Modifiers = Modifiers::COMMAND.plus(Modifiers::SHIFT);

impl App {
    pub fn new(cc: &eframe::CreationContext, svc: Arc<Service>, opts: Options) -> App {
        theme::install_fonts(&cc.egui_ctx);
        let (tx, rx) = mpsc::channel();
        let ctx = cc.egui_ctx.clone();
        let tx = Mutex::new(tx);
        svc.sessions.set_sink(Arc::new(move |e| {
            let _ = tx.lock().send(e);
            ctx.request_repaint();
        }));
        let s = Shared::new(svc.clone(), cc.egui_ctx.clone());
        let theme = opts.theme.as_deref().and_then(ThemeKind::from_id).unwrap_or_else(|| theme_for(&s.prefs.theme));
        theme::apply(&cc.egui_ctx, theme);
        let mut app = App {
            s,
            rx,
            theme,
            editor: Default::default(),
            quick: Default::default(),
            settings: Default::default(),
            keys: Default::default(),
            transfer: Default::default(),
            sftp: Default::default(),
            forwards: Default::default(),
            snippets: Default::default(),
            triggers: Default::default(),
            recordings: Default::default(),
            sidebar: Default::default(),
            palette: None,
            prompts: VecDeque::new(),
            dialog: None,
            tabs_ui: Default::default(),
            latency: None,
            update: None,
            screenshot: opts.screenshot.map(|p| (p, std::time::Instant::now(), opts.shot_delay)),
            shot_settle: 0,
            actions: opts.actions,
            answers: opts.answers,
            run_local: opts.run_local.map(|c| (c, None)),
            resize_sidebar: false,
            saved_layout: None,
            layout_checked: 0.0,
            sync_job: None,
            sync_due: 0.0,
            sync_manual: false,
        };
        let restore = opts.connect.is_empty() && app.run_local.is_none() && opts.open.is_none() && app.s.prefs.restore_sessions;
        if restore {
            crate::restore::restore(&mut app);
        }
        for c in &opts.connect {
            match app.s.profiles.iter().find(|p| &p.id == c || &p.display_name == c).map(|p| p.id.clone()) {
                Some(id) => app.connect_profile(&id, true),
                None => eprintln!("--connect: no profile named {c:?}"),
            }
        }
        if let Some(w) = opts.open.as_deref() {
            app.open_window(w);
        }
        if let Some((_, sid)) = &mut app.run_local {
            let id = app.s.svc.connect_local();
            *sid = Some(id.clone());
            app.add_session(&id, true);
        }
        // Background update check, once per launch.
        if app.screenshot.is_none() && app.s.prefs.check_for_updates {
            let svc2 = svc.clone();
            let ctx = cc.egui_ctx.clone();
            std::thread::spawn(move || {
                if let Ok(info) = svc2.check_for_updates() {
                    ctx.data_mut(|d| d.insert_temp(egui::Id::new("nexus-update"), Arc::new(info)));
                    ctx.request_repaint();
                }
            });
        }
        app
    }

    fn open_window(&mut self, w: &str) {
        let (w, arg) = w.split_once(':').unwrap_or((w, ""));
        match w {
            "editor" => match self.s.profiles.iter().find(|p| p.protocol.id() == arg || p.id == arg).map(|p| p.id.clone()) {
                Some(id) => self.editor.open_edit(&self.s, &id),
                None => self.editor.open_new(&self.s, Protocol::from_id(arg), ""),
            },
            "quick" => self.quick.open(),
            "settings" => self.settings.open(match arg {
                "terminal" => SettingsTab::Terminal,
                "security" => SettingsTab::Security,
                "knownhosts" => SettingsTab::KnownHosts,
                "diagnostics" => SettingsTab::Diagnostics,
                "help" => SettingsTab::Help,
                "about" => SettingsTab::About,
                _ => SettingsTab::General,
            }),
            "keys" => self.keys.open(),
            "snippets" => self.snippets.open(),
            "triggers" => self.triggers.open(),
            "recordings" => self.recordings.open(),
            "palette" => self.palette = Some(Default::default()),
            "export" => self.transfer.export_encrypted(vec![]),
            "import" => self.transfer.import_encrypted(),
            _ => eprintln!("--open: unknown window {w:?}"),
        }
    }

    // --- sessions -------------------------------------------------------------------------

    fn view_for(&self, info: &SessionInfo, profile: Option<&Profile>) -> View {
        let prefs = &self.s.prefs;
        match (info.protocol, profile) {
            (Protocol::Raw, Some(p)) => View::Raw(Box::new(crate::raw_view::RawView::new(&info.id, p))),
            (Protocol::Irc, Some(p)) => View::Irc(Box::new(crate::irc_view::IrcView::new(&info.id, p))),
            (Protocol::Github, Some(p)) => View::Github(Box::new(crate::github_view::GithubView::new(&info.id, &p.display_name))),
            (Protocol::Vnc | Protocol::Rdp, Some(p)) => View::Desktop(Box::new(crate::desktop_view::DesktopView::new(&info.id, p))),
            (_, p) => {
                let (scroll, size, cursor) = match p {
                    Some(p) => (p.terminal_scrollback, p.terminal_font_size, p.terminal_cursor_style.clone()),
                    None => (prefs.default_scrollback, prefs.default_font_size, prefs.default_cursor_style.clone()),
                };
                View::Terminal(Box::new(TermView::new(scroll.max(0) as usize, size as f32, CursorStyle::from_id(&cursor))))
            }
        }
    }

    /// Registers a just-opened session as a pane (and a tab unless it's a split child).
    pub fn add_session(&mut self, id: &str, tab: bool) {
        let Some(info) = self.s.svc.sessions.info(id) else {
            return;
        };
        let profile = self.s.svc.sessions.profile(id);
        let view = self.view_for(&info, profile.as_ref());
        let lines = profile.as_ref().and_then(|p| p.serial.as_ref()).map_or((true, true), |c| (c.dtr_on_connect, c.rts_on_connect));
        self.s.panes.insert(id.into(), Pane { info, view, recording: false, local_exited: false, connected_since: None, highlight_until: 0.0, lines });
        if tab {
            self.s.tabs.push(Tab { id: id.into(), activity: false, split: None });
            self.s.active = Some(id.into());
            self.s.focused = Some(id.into());
        }
    }

    /// Connect a saved profile, asking first if it already has a live session.
    pub fn connect_profile(&mut self, id: &str, skip_checks: bool) {
        let Some(p) = self.s.profile(id).cloned() else {
            self.s.error("Profile not found");
            return;
        };
        if !skip_checks {
            let live = self.s.panes.values().find(|pn| {
                pn.info.profile_id == id
                    && matches!(self.s.session_state(&pn.info.id), Some(SessionState::Connected | SessionState::Connecting | SessionState::Reconnecting))
            });
            if let Some(pn) = live {
                self.dialog = Some(Dialog::Reuse { profile: id.into(), existing: pn.info.id.clone() });
                return;
            }
            if p.protocol == Protocol::Vnc && !p.vnc.as_ref().is_some_and(|v| v.ssh_tunnel) {
                self.dialog = Some(Dialog::DirectVnc { profile: id.into() });
                return;
            }
        }
        match self.s.svc.connect_profile(id) {
            Ok(sid) => {
                self.add_session(&sid, true);
                self.s.reload_profiles();
                self.s.info(format!("Connecting to {}…", if p.display_name.is_empty() { &p.host } else { &p.display_name }));
            }
            Err(e) => self.s.error(format!("Connection failed: {e}")),
        }
    }

    pub fn connect_local(&mut self) {
        let sid = self.s.svc.connect_local();
        self.add_session(&sid, true);
        self.s.info("Opening local terminal…");
    }

    /// Closes a session's UI and backend. Tabs close their split children too.
    pub fn close_session(&mut self, id: &str) {
        let children: Vec<String> = self.s.tab(id).and_then(|t| t.split.as_ref()).map(|sp| sp.sessions()).unwrap_or_default();
        for c in children.iter().filter(|c| *c != id) {
            self.close_one(c);
        }
        if let Some(pos) = self.s.tabs.iter().position(|t| t.id == id) {
            self.s.tabs.remove(pos);
            if self.s.active.as_deref() == Some(id) {
                // The nearest neighbour becomes active.
                let next = self.s.tabs.get(pos).or_else(|| self.s.tabs.get(pos.wrapping_sub(1))).map(|t| t.id.clone());
                self.s.active = next.clone();
                self.s.focused = next;
            }
        } else {
            crate::splits::remove_session(&mut self.s, id);
        }
        self.close_one(id);
    }

    fn close_one(&mut self, id: &str) {
        self.sftp.close(id);
        self.triggers.cleanup(id);
        self.s.panes.remove(id);
        if self.s.focused.as_deref() == Some(id) {
            self.s.focused = self.s.active.clone();
        }
        let svc = self.s.svc.clone();
        let id = id.to_string();
        std::thread::spawn(move || {
            let _ = svc.sessions.disconnect(&id);
        });
    }

    /// Close with the "confirm on close" preference (not for GitHub tabs).
    pub fn request_close(&mut self, id: &str) {
        let proto = self.s.pane(id).map(|p| p.info.protocol);
        let live = matches!(self.s.session_state(id), Some(SessionState::Connected));
        if self.s.prefs.confirm_on_close && live && proto != Some(Protocol::Github) {
            self.dialog = Some(Dialog::ConfirmClose { session: id.into() });
        } else {
            self.close_session(id);
        }
    }

    pub fn reconnect(&mut self, id: &str) {
        let old = self.s.pane(id).map(|p| p.info.tab_name.clone());
        match self.s.svc.reconnect(id) {
            Ok(new) => {
                // Keep the tab in place; swap in the new session id.
                let info = self.s.svc.sessions.info(&new);
                if let (Some(mut pane), Some(info)) = (self.s.panes.remove(id), info) {
                    pane.info = info;
                    pane.local_exited = false;
                    if let View::Terminal(t) = &mut pane.view {
                        t.feed(b"\r\n\x1b[2m--- reconnecting ---\x1b[0m\r\n");
                    }
                    self.s.panes.insert(new.clone(), pane);
                }
                for t in &mut self.s.tabs {
                    if t.id == id {
                        t.id = new.clone();
                    }
                    if let Some(sp) = &mut t.split {
                        sp.rename(id, &new);
                    }
                }
                if self.s.active.as_deref() == Some(id) {
                    self.s.active = Some(new.clone());
                }
                if self.s.focused.as_deref() == Some(id) {
                    self.s.focused = Some(new.clone());
                }
                self.s.info(format!("Reconnecting {}…", old.unwrap_or_default()));
            }
            Err(e) => self.s.error(format!("Reconnect failed: {e}")),
        }
    }

    /// Sends user input to a session and, in broadcast mode, to the other targets.
    pub fn send_input(&mut self, from: &str, data: &[u8]) {
        if data.is_empty() {
            return;
        }
        let svc = self.s.svc.clone();
        let _ = svc.sessions.write(from, data);
        if self.s.broadcast {
            for id in self.s.all_sessions() {
                if id == from || !self.s.broadcast_targets.as_ref().is_none_or(|t| t.contains(&id)) {
                    continue;
                }
                if self.s.session_state(&id) == Some(SessionState::Connected) && self.s.pane(&id).is_some_and(|p| matches!(p.view, View::Terminal(_))) {
                    let _ = svc.sessions.write(&id, data);
                }
            }
        }
    }

    // --- events ---------------------------------------------------------------------------

    fn drain_events(&mut self) {
        for e in POSTED.lock().drain(..) {
            self.s.error(e);
        }
        while let Ok(e) = self.rx.try_recv() {
            self.on_event(e);
        }
        if let Some(info) = self.s.ctx.data_mut(|d| d.remove_temp::<Arc<nexus_core::updater::UpdateInfo>>(egui::Id::new("nexus-update"))) {
            self.update = Some((*info).clone());
        }
    }

    fn mark_activity(&mut self, session: &str) {
        let active = self.s.active.clone();
        if let Some(t) = self.s.tabs.iter_mut().find(|t| t.id == session || t.split.as_ref().is_some_and(|sp| sp.sessions().iter().any(|x| x == session)))
            && active.as_deref() != Some(&t.id)
        {
            t.activity = true;
        }
    }

    fn on_event(&mut self, e: Event) {
        match e {
            Event::Data { session, data } => {
                if let Some(View::Terminal(t)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    t.feed(&data);
                }
                self.mark_activity(&session);
                let now = self.s.now;
                if self.triggers.check(&mut self.s, &session, &data)
                    && let Some(p) = self.s.pane_mut(&session)
                {
                    p.highlight_until = now + 1.5;
                }
            }
            Event::State { session, state, error, retry } => self.on_state(&session, state, &error, retry),
            Event::Closed { session } => {
                self.sftp.close(&session);
                self.triggers.cleanup(&session);
            }
            Event::RawData { session, data, direction, timestamp } => {
                if let Some(View::Raw(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_data(&data, &direction, timestamp);
                }
                self.mark_activity(&session);
            }
            Event::Debug { .. } => {}
            Event::GithubStatus { session, status } => {
                if let Some(View::Github(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_status(status);
                }
            }
            Event::GithubRepos { session, login, repos } => {
                if let Some(View::Github(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_repos(login, repos);
                }
            }
            Event::GithubError { session, error } => {
                if let Some(View::Github(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_error(error);
                }
            }
            Event::Warning { message, .. } => self.s.error(message),
            Event::Info { message, .. } => self.s.success(message),
            Event::Irc { session, event } => {
                if let Some(View::Irc(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_event(event);
                }
                self.mark_activity(&session);
            }
            Event::ForwardStarted { forward, .. } => {
                let label = if forward.label.is_empty() {
                    format!("{}:{} → {}:{}", forward.bind_host, forward.bind_port, forward.dest_host, forward.dest_port)
                } else {
                    forward.label.clone()
                };
                self.s.success(format!("Forward started: {label}"));
            }
            Event::ForwardStopped { forward_id, .. } => self.s.info(format!("Forward stopped: {forward_id}")),
            Event::ForwardError { error, .. } => self.s.error(format!("Forward error: {error}")),
            Event::RecordingStarted { session } => {
                if let Some(p) = self.s.pane_mut(&session) {
                    p.recording = true;
                }
            }
            Event::RecordingStopped { session, duration, events } => {
                if let Some(p) = self.s.pane_mut(&session) {
                    p.recording = false;
                }
                self.s.success(format!("Recording saved ({:.0}s, {events} events)", duration));
            }
            Event::SftpProgress { session, progress } => self.sftp.on_progress(&session, progress),
            Event::SecretPrompt { session, title, message, reply } if !self.answers.is_empty() => {
                let _ = (session, title, message, reply.send(self.answers.remove(0)));
            }
            Event::SecretPrompt { session, title, message, reply } => {
                self.prompts.push_back(Prompt::Secret { session, title, message, value: Default::default(), reply });
            }
            Event::HostKeyVerify { host, key_type, fingerprint, stored, reply, .. } => {
                self.prompts.push_back(Prompt::HostKey { host, key_type, fingerprint, stored, reply });
            }
            Event::DesktopReady { session } => {
                if let Some(View::Desktop(v)) = self.s.panes.get_mut(&session).map(|p| &mut p.view) {
                    v.on_ready();
                }
            }
            Event::DesktopFrame { .. } => {}
            Event::DesktopClipboard { text, .. } => self.remote_clipboard(&text),
        }
    }

    fn on_state(&mut self, session: &str, state: SessionState, error: &str, retry: Option<nexus_core::sessions::Retry>) {
        // A reconnect replaces the info wholesale.
        if let Some(info) = self.s.svc.sessions.info(session)
            && let Some(p) = self.s.pane_mut(session)
        {
            p.info = info;
            p.info.state = state;
            if !error.is_empty() {
                p.info.error = error.into();
            }
        }
        let Some(p) = self.s.pane_mut(session) else {
            return;
        };
        let local = p.info.protocol == Protocol::Local;
        match state {
            SessionState::Connected => {
                if let Some((cmd, Some(sid))) = &self.run_local
                    && sid == session
                {
                    let _ = self.s.svc.sessions.write(session, format!("{cmd}\n").as_bytes());
                    self.run_local = None;
                }
                let Some(p) = self.s.pane_mut(session) else {
                    return;
                };
                p.local_exited = false;
                p.connected_since = Some(chrono::Local::now());
                // The PTY was created before the view knew its size: send it now.
                if let View::Terminal(t) = &mut p.view {
                    let (c, r) = t.size();
                    let _ = self.s.svc.sessions.resize(session, c, r);
                }
            }
            SessionState::Disconnected if local => {
                p.local_exited = true;
                if let View::Terminal(t) = &mut p.view {
                    t.feed(b"\r\n\x1b[2m[Exited - press enter to restart]\x1b[0m\r\n");
                }
            }
            SessionState::Reconnecting => {
                if let Some(r) = retry {
                    self.s.info(format!("Reconnecting (attempt {}/{}) in {}s…", r.attempt, r.max, r.delay_secs));
                }
            }
            SessionState::Error if !error.is_empty() => {
                if let Some((title, body, kh)) = classify_error(error) {
                    self.dialog = Some(Dialog::Guided { session: session.into(), title: title.into(), body: format!("{body}\n\n{error}"), known_hosts: kh });
                }
            }
            _ => {}
        }
    }

    // --- requests, shortcuts, commands ----------------------------------------------------

    fn handle_requests(&mut self) {
        for r in std::mem::take(&mut self.s.requests) {
            match r {
                Request::NewProfile { protocol, folder } => self.editor.open_new(&self.s, protocol, &folder),
                Request::EditProfile(id) => self.editor.open_edit(&self.s, &id),
                Request::Connect(id) => self.connect_profile(&id, false),
                Request::ConnectQuick(p) => {
                    let name = if p.display_name.is_empty() { p.host.clone() } else { p.display_name.clone() };
                    match self.s.svc.connect_quick(p) {
                        Ok(sid) => {
                            self.add_session(&sid, true);
                            self.s.info(format!("Connecting to {name}…"));
                        }
                        Err(e) => self.s.error(format!("Can't connect to {name}: {e}")),
                    }
                }
                Request::ReloadProfiles => {
                    self.s.reload_profiles();
                    // A profile changed: sync it out shortly (batched with any further edits).
                    self.sync_due = self.sync_due.min(self.s.now + 3.0);
                }
                Request::SyncProfiles => {
                    self.sync_manual = true;
                    self.sync_due = 0.0;
                }
                Request::ReloadPreferences => {
                    self.s.prefs = self.s.svc.preferences();
                    self.resize_sidebar = true;
                    self.theme = theme_for(&self.s.prefs.theme);
                    theme::apply(&self.s.ctx, self.theme);
                }
                Request::ExportEncrypted(ids) => self.transfer.export_encrypted(ids),
                Request::ImportEncrypted => self.transfer.import_encrypted(),
                Request::Sftp(id) => {
                    let at = match self.s.pane(&id).map(|p| &p.view) {
                        Some(View::Terminal(t)) => t.reported_cwd.as_ref().map(|c| c.1.clone()),
                        _ => None,
                    };
                    self.sftp.toggle(&id, at);
                }
                Request::SendToActive(text) => match self.s.focused_session() {
                    Some(id) if self.s.session_state(&id) == Some(SessionState::Connected) => self.send_input(&id, text.as_bytes()),
                    _ => self.s.error("No active connected session"),
                },
                Request::Write(id, data) => self.send_input(&id, &data),
            }
        }
    }

    fn shortcuts(&mut self, ctx: &egui::Context) {
        let pressed = |s: KeyboardShortcut| ctx.input_mut(|i| i.consume_shortcut(&s));
        if pressed(sc(CMD, Key::K)) {
            self.palette = if self.palette.is_some() { None } else { Some(chrome::Palette::default()) };
        }
        if pressed(sc(CMD, Key::N)) {
            self.s.request(Request::NewProfile { protocol: None, folder: String::new() });
        }
        if pressed(sc(CMD, Key::W))
            && let Some(id) = self.s.focused_session()
        {
            self.request_close(&id);
        }
        if pressed(sc(CMD_SHIFT, Key::Tab)) {
            self.cycle_tab(-1);
        }
        if pressed(sc(CMD, Key::Tab)) {
            self.cycle_tab(1);
        }
        for (i, k) in [Key::Num1, Key::Num2, Key::Num3, Key::Num4, Key::Num5, Key::Num6, Key::Num7, Key::Num8, Key::Num9].into_iter().enumerate() {
            if pressed(sc(CMD, k))
                && let Some(t) = tabs::ordered(&self.s).get(i)
            {
                let id = t.clone();
                self.activate(&id);
            }
        }
        if pressed(sc(CMD, Key::Comma)) {
            self.settings.open(SettingsTab::General);
        }
        if pressed(sc(CMD, Key::F)) {
            self.open_find();
        }
        if pressed(sc(CMD_SHIFT, Key::D)) {
            crate::splits::split_focused(self, crate::splits::Dir::Right);
        }
        if pressed(sc(CMD_SHIFT, Key::E)) {
            crate::splits::split_focused(self, crate::splits::Dir::Down);
        }
        if pressed(sc(CMD_SHIFT, Key::R))
            && let Some(id) = self.s.focused_session()
        {
            self.toggle_recording(&id);
        }
        if pressed(sc(CMD_SHIFT, Key::B)) {
            self.toggle_broadcast();
        }
        if pressed(sc(CMD_SHIFT, Key::S)) {
            self.snippets.open();
        }
        if pressed(sc(CMD_SHIFT, Key::Q)) {
            self.quick.open();
        }
        if pressed(sc(Modifiers::NONE, Key::F11)) {
            let full = ctx.input(|i| i.viewport().fullscreen.unwrap_or(false));
            ctx.send_viewport_cmd(egui::ViewportCommand::Fullscreen(!full));
        }
    }

    pub fn activate(&mut self, id: &str) {
        self.s.active = Some(id.into());
        self.s.focused = Some(id.into());
        if let Some(t) = self.s.tabs.iter_mut().find(|t| t.id == id) {
            t.activity = false;
        }
        if let Some(View::Terminal(t)) = self.s.panes.get_mut(id).map(|p| &mut p.view) {
            t.request_focus();
        }
    }

    fn cycle_tab(&mut self, dir: i32) {
        let order = tabs::ordered(&self.s);
        if order.is_empty() {
            return;
        }
        let cur = self.s.active.as_ref().and_then(|a| order.iter().position(|x| x == a)).unwrap_or(0) as i32;
        let next = (cur + dir).rem_euclid(order.len() as i32) as usize;
        let id = order[next].clone();
        self.activate(&id);
    }

    pub fn open_find(&mut self) {
        if let Some(id) = self.s.focused_session()
            && let Some(View::Terminal(t)) = self.s.panes.get_mut(&id).map(|p| &mut p.view)
        {
            if t.find.is_none() {
                t.find = Some(Default::default());
            }
            self.tabs_ui.find_focus = true;
        }
    }

    pub fn toggle_recording(&mut self, id: &str) {
        let svc = self.s.svc.clone();
        if svc.sessions.is_recording(id) {
            if let Err(e) = svc.sessions.stop_recording(id) {
                self.s.error(e.to_string());
            }
        } else {
            let (c, r) = match self.s.pane(id).map(|p| &p.view) {
                Some(View::Terminal(t)) => t.size(),
                _ => (80, 24),
            };
            match svc.sessions.start_recording(id, c, r) {
                Ok(()) => self.s.info("Recording started"),
                Err(e) => self.s.error(e.to_string()),
            }
        }
    }

    /// Telnet or serial break.
    pub fn send_break(&mut self, id: &str) {
        match self.s.svc.sessions.send_break(id) {
            Ok(()) => self.s.info("Break sent"),
            Err(e) => self.s.error(e.to_string()),
        }
    }

    /// Toggles a serial modem line: `dtr` true for DTR, false for RTS.
    pub fn toggle_line(&mut self, id: &str, dtr: bool) {
        let Some((d, r)) = self.s.pane(id).map(|p| p.lines) else { return };
        let (d, r) = if dtr { (!d, r) } else { (d, !r) };
        match self.s.svc.sessions.set_serial_lines(id, d, r) {
            Ok(()) => {
                if let Some(p) = self.s.pane_mut(id) {
                    p.lines = (d, r);
                }
                self.s.info(format!("DTR {} · RTS {}", if d { "on" } else { "off" }, if r { "on" } else { "off" }));
            }
            Err(e) => self.s.error(e.to_string()),
        }
    }

    /// A remote host (OSC 52 or VNC) wants to set the clipboard: allowed per preference, and always
    /// announced so a silent swap can't set up a paste into another shell.
    pub fn remote_clipboard(&mut self, text: &str) {
        if self.s.prefs.allow_remote_clipboard {
            self.s.ctx.copy_text(text.into());
            self.s.info(format!("Remote host set the clipboard: {}", clipboard_summary(text)));
        } else {
            self.s.info("Blocked a remote clipboard write (Settings → General to allow)");
        }
    }

    /// Profile sync: at launch, a few seconds after a profile changes, every 10 minutes and on
    /// request, when a folder and password are set. Automatic runs stay quiet unless something
    /// changed or a manual one failed.
    fn drive_sync(&mut self, ctx: &egui::Context) {
        if let Some(r) = crate::settings::poll(&mut self.sync_job) {
            let manual = std::mem::take(&mut self.sync_manual);
            match r {
                Ok(rep) => {
                    if rep.pulled + rep.removed > 0 {
                        self.s.reload_profiles();
                        self.s.info(format!("Profile sync: {} updated, {} removed", rep.pulled, rep.removed));
                    } else if manual {
                        self.s.success(if rep.pushed { "Profiles synced" } else { "Profiles already in sync" });
                    }
                    if rep.skipped > 0 {
                        self.s.error(format!("Profile sync skipped {} invalid profile(s) from the shared file", rep.skipped));
                    }
                }
                Err(e) if manual => self.s.error(format!("Profile sync failed: {e}")),
                Err(e) => tracing::warn!(error = %e, "profile sync failed"),
            }
        }
        if self.screenshot.is_some() || self.sync_job.is_some() || self.s.now < self.sync_due {
            return;
        }
        self.sync_due = self.s.now + 600.0;
        if self.s.prefs.sync_folder.trim().is_empty() {
            if std::mem::take(&mut self.sync_manual) {
                self.s.error("Choose a sync folder first");
            }
            return;
        }
        let svc = self.s.svc.clone();
        self.sync_job = Some(crate::settings::Job::spawn(ctx, move || svc.sync_profiles().map_err(|e| e.to_string())));
    }

    pub fn toggle_broadcast(&mut self) {
        self.s.broadcast = !self.s.broadcast;
        if !self.s.broadcast {
            self.s.broadcast_targets = None;
        }
        self.s.info(if self.s.broadcast { "Broadcast mode enabled — input sent to all sessions" } else { "Broadcast mode disabled" });
    }

    pub fn pick_broadcast_targets(&mut self) {
        let focused = self.s.focused_session();
        let picks: Vec<(String, String, bool)> = self
            .s
            .panes
            .values()
            .filter(|p| Some(&p.info.id) != focused.as_ref() && self.s.session_state(&p.info.id) == Some(SessionState::Connected))
            .map(|p| (p.info.id.clone(), p.info.tab_name.clone(), self.s.broadcast_targets.as_ref().is_none_or(|t| t.contains(&p.info.id))))
            .collect();
        if picks.is_empty() {
            self.s.error("No other connected sessions to broadcast to");
            return;
        }
        self.dialog = Some(Dialog::Broadcast { picks });
    }

    /// A terminal's context-menu action.
    pub fn terminal_menu(&mut self, id: &str, a: MenuAction) {
        match a {
            MenuAction::Paste => {
                if let Ok(text) = arboard::Clipboard::new().and_then(|mut c| c.get_text())
                    && let Some(View::Terminal(t)) = self.s.panes.get_mut(id).map(|p| &mut p.view)
                {
                    let mut out = crate::terminal::TermResponse::default();
                    t.paste(&text, &mut out);
                    if let Some(text) = out.confirm_paste {
                        self.dialog = Some(Dialog::ConfirmPaste { session: id.into(), text });
                    }
                    let input = out.input;
                    self.send_input(id, &input);
                }
            }
            MenuAction::Find => {
                self.s.focused = Some(id.into());
                self.open_find();
            }
            MenuAction::SendSnippet => self.snippets.open(),
            MenuAction::PortForwards => {
                if self.s.pane(id).is_some_and(|p| p.info.protocol == Protocol::Ssh) {
                    self.forwards.open(id);
                } else {
                    self.s.error("Port forwards are only available on SSH sessions");
                }
            }
            MenuAction::SplitRight | MenuAction::SplitDown => {
                self.s.focused = Some(id.into());
                crate::splits::split_focused(self, if a == MenuAction::SplitRight { crate::splits::Dir::Right } else { crate::splits::Dir::Down });
            }
            MenuAction::Export => {
                if let Some(View::Terminal(t)) = self.s.panes.get(id).map(|p| &p.view) {
                    let text = t.buffer_text();
                    if let Some(path) = rfd::FileDialog::new().set_file_name("terminal-output.txt").save_file() {
                        match std::fs::write(&path, text) {
                            Ok(()) => self.s.success(format!("Saved {}", path.display())),
                            Err(e) => self.s.error(format!("Export failed: {e}")),
                        }
                    }
                }
            }
            MenuAction::Clear => {
                if let Some(View::Terminal(t)) = self.s.panes.get_mut(id).map(|p| &mut p.view) {
                    t.clear();
                }
            }
            MenuAction::Reset => {
                if let Some(View::Terminal(t)) = self.s.panes.get_mut(id).map(|p| &mut p.view) {
                    t.reset();
                }
            }
            // Handled inside the terminal view.
            MenuAction::Copy | MenuAction::SelectAll | MenuAction::PrevPrompt | MenuAction::NextPrompt | MenuAction::CopyOutput => {}
        }
    }

    fn file_drops(&mut self, ctx: &egui::Context) {
        let dropped: Vec<PathBuf> = ctx.input(|i| i.raw.dropped_files.iter().map(|f| f.path().to_path_buf()).filter(|p| !p.as_os_str().is_empty()).collect());
        if dropped.is_empty() {
            return;
        }
        match self.s.focused_session() {
            Some(id) if self.s.pane(&id).is_some_and(|p| matches!(p.view, View::Terminal(_))) => self.sftp.drop_files(&mut self.s, &id, dropped),
            _ => self.s.error("Drop files on a terminal session"),
        }
    }

    // --- screenshot automation ---------------------------------------------------------

    fn screenshot(&mut self, ctx: &egui::Context) {
        let Some((path, started, delay)) = &mut self.screenshot else {
            return;
        };
        let (elapsed, delay) = (started.elapsed(), *delay);
        if !self.actions.is_empty() && elapsed >= delay / 2 {
            for a in std::mem::take(&mut self.actions) {
                if !chrome::run_labelled(self, ctx, &a) {
                    eprintln!("--do: no palette command {a:?}");
                }
            }
            ctx.request_repaint();
            return;
        }
        let path = path.clone();
        if elapsed < delay {
            std::thread::sleep(std::time::Duration::from_millis(15));
            ctx.request_repaint();
            return;
        }
        if self.shot_settle == 0 {
            self.shot_settle = 1;
            ctx.send_viewport_cmd(egui::ViewportCommand::Screenshot(egui::UserData::default()));
        }
        let shot = ctx.input(|i| i.events.iter().find_map(|e| if let egui::Event::Screenshot { image, .. } = e { Some(image.clone()) } else { None }));
        if let Some(img) = shot {
            let [w, h] = img.size;
            let raw: Vec<u8> = img.pixels.iter().flat_map(|p| p.to_array()).collect();
            if let Err(e) = image::save_buffer(&path, &raw, w as u32, h as u32, image::ExtendedColorType::Rgba8) {
                eprintln!("screenshot: {e}");
            }
            ctx.send_viewport_cmd(egui::ViewportCommand::Close);
        }
        ctx.request_repaint();
    }
}

/// App chrome theme from the "theme" preference (the web build used dark/light).
pub fn theme_for(pref: &str) -> ThemeKind {
    ThemeKind::from_id(pref).unwrap_or(if pref == "light" { ThemeKind::Light } else { ThemeKind::Pro })
}

/// Recognises auth and host-key failures for the guided error dialog.
pub fn classify_error(msg: &str) -> Option<(&'static str, &'static str, bool)> {
    let m = msg.to_lowercase();
    if nexus_core::sessions::is_auth_error(&m) {
        return Some(("Authentication Failed", "The server rejected the provided credentials. Check username, password, keys, and auth methods.", false));
    }
    if ["host key", "fingerprint"].iter().any(|p| m.contains(p)) {
        return Some(("Host Key Verification Failed", "The server's host key could not be verified. Manage known hosts or update the profile's policy.", true));
    }
    None
}

impl eframe::App for App {
    fn persist_egui_memory(&self) -> bool {
        self.screenshot.is_none()
    }

    fn ui(&mut self, ui: &mut Ui, _frame: &mut eframe::Frame) {
        let ctx = ui.ctx().clone();
        let t = Tokens::get(&ctx);
        self.s.now = ctx.input(|i| i.time);
        self.drain_events();
        self.handle_requests();
        // Dialogs and the palette take the keyboard while open.
        if self.palette.is_none() && self.dialog.is_none() && self.prompts.is_empty() {
            self.shortcuts(&ctx);
        }
        if crate::chrome::modal_key(&ctx, Key::Escape) && ctx.input(|i| i.viewport().fullscreen.unwrap_or(false)) && self.palette.is_none() {
            ctx.send_viewport_cmd(egui::ViewportCommand::Fullscreen(false));
        }
        self.file_drops(&ctx);

        egui::Panel::top("titlebar").exact_size(38.0).frame(egui::Frame::NONE.fill(t.chrome)).show(ui, |ui| chrome::title_bar(self, ui));
        if self.s.prefs.show_status_bar {
            egui::Panel::bottom("status").exact_size(26.0).frame(egui::Frame::NONE.fill(t.chrome)).show(ui, |ui| chrome::status_bar(self, ui));
        }
        let width = self.s.prefs.sidebar_width.clamp(180, 500) as f32;
        let mut panel = egui::Panel::left("sidebar").default_size(width);
        if std::mem::take(&mut self.resize_sidebar) {
            panel = panel.exact_size(width);
        }
        let side = panel.min_size(180.0).max_size(500.0).resizable(true).frame(egui::Frame::NONE.fill(t.dock)).show(ui, |ui| sidebar::ui(self, ui));
        let w = side.response.rect.width().round() as i64;
        if (w - self.s.prefs.sidebar_width).abs() > 2 && !ctx.input(|i| i.pointer.any_down()) {
            self.s.prefs.sidebar_width = w;
            let _ = self.s.svc.save_preferences(&self.s.prefs);
        }
        if self.editor.is_open() {
            egui::Panel::right("editor")
                .default_size(460.0)
                .min_size(380.0)
                .max_size(800.0)
                .resizable(true)
                .frame(egui::Frame::NONE.fill(t.dock).inner_margin(6))
                .show(ui, |ui| {
                    self.editor.ui(ui, &mut self.s);
                });
        } else if let Some(id) = self.sftp.open_for().map(String::from) {
            if self.s.pane(&id).is_some() {
                egui::Panel::right("sftp")
                    .default_size(380.0)
                    .min_size(280.0)
                    .max_size(800.0)
                    .resizable(true)
                    .frame(egui::Frame::NONE.fill(t.dock).inner_margin(6))
                    .show(ui, |ui| {
                        self.sftp.ui(ui, &mut self.s);
                    });
            } else {
                self.sftp.close(&id);
            }
        }
        egui::CentralPanel::default().frame(egui::Frame::NONE.fill(t.dock)).show(ui, |ui| tabs::workspace(self, ui));

        // Floating windows and modals.
        let prompt_open = !self.prompts.is_empty();
        ctx.data_mut(|d| d.insert_temp(egui::Id::new("nexus-prompt-open"), prompt_open));
        self.quick.ui(&ctx, &mut self.s);
        self.settings.ui(&ctx, &mut self.s);
        self.keys.ui(&ctx, &mut self.s);
        self.transfer.ui(&ctx, &mut self.s);
        self.sftp.dialogs(&ctx, &mut self.s);
        self.forwards.ui(&ctx, &mut self.s);
        self.snippets.ui(&ctx, &mut self.s);
        self.triggers.ui(&ctx, &mut self.s);
        self.recordings.ui(&ctx, &mut self.s);
        chrome::dialogs(self, &ctx);
        chrome::prompts(self, &ctx);
        chrome::palette(self, &ctx);
        chrome::toasts(self, &ctx);
        self.screenshot(&ctx);
        self.drive_sync(&ctx);
        if self.screenshot.is_none() && self.s.prefs.restore_sessions && self.s.now - self.layout_checked > 1.0 {
            self.layout_checked = self.s.now;
            crate::restore::save_if_changed(self);
        }
        chrome::resize_edges(&ctx);
        // Requests raised while drawing are handled on the next frame.
        if !self.s.requests.is_empty() {
            ctx.request_repaint();
        }
        let _ = ToastKind::Info;
    }
}

/// How a remote clipboard write is announced: control characters made visible and the line
/// count stated, so `ls\ncurl … | sh` can't pass for a harmless `ls`.
fn clipboard_summary(text: &str) -> String {
    let lines = text.lines().count();
    let shown: String = text.chars().take(60).flat_map(|c| if c.is_control() { c.escape_debug().collect::<Vec<_>>() } else { vec![c] }).collect();
    let more = if text.chars().count() > 60 { "…" } else { "" };
    if lines > 1 { format!("{shown}{more} ({lines} lines)") } else { format!("{shown}{more}") }
}

#[cfg(test)]
mod clipboard_tests {
    #[test]
    fn remote_clipboard_toast_shows_hidden_lines() {
        assert_eq!(super::clipboard_summary("ls"), "ls");
        assert_eq!(super::clipboard_summary("ls\ncurl x|sh\n"), "ls\\ncurl x|sh\\n (2 lines)");
        assert!(super::clipboard_summary("a\x0fb").contains("\\u{f}"));
    }
}
