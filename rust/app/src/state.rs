//! State shared by the main window and every feature module: the service, cached profiles and
//! preferences, open tabs, toasts, and a request queue features use to ask the app to act.

use crate::views::View;
use nexus_core::models::{AppPreferences, Profile, Protocol, SessionInfo, SessionState};
use nexus_core::service::Service;
use std::collections::HashSet;
use std::sync::Arc;

/// Things a feature asks the main window to do (handled once per frame, in order).
#[allow(clippy::large_enum_variant)] // a per-frame queue; boxing buys nothing
pub enum Request {
    /// Merge profiles with the sync folder (manual, or after a change).
    SyncProfiles,
    /// Open the profile editor for a new profile (optionally pre-set protocol / folder).
    NewProfile {
        protocol: Option<Protocol>,
        folder: String,
    },
    EditProfile(String),
    Connect(String),
    ConnectQuick(Profile),
    /// Reload `Shared::profiles` from the database.
    ReloadProfiles,
    /// Re-read and apply preferences (theme, terminal theme, fonts…).
    ReloadPreferences,
    ExportEncrypted(Vec<String>),
    ImportEncrypted,
    Sftp(String),
    /// Type text into the active session (snippets).
    SendToActive(String),
    /// Bytes to a specific session.
    Write(String, Vec<u8>),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum SettingsTab {
    #[default]
    General,
    Terminal,
    Security,
    KnownHosts,
    Diagnostics,
    Help,
    About,
}

pub struct Toast {
    pub text: String,
    pub kind: ToastKind,
    pub until: f64,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum ToastKind {
    Info,
    Success,
    Warning,
    Error,
}

/// A session on screen (a tab's own session or a split pane) and how it's drawn.
pub struct Pane {
    pub info: SessionInfo,
    pub view: View,
    pub recording: bool,
    /// Local shell exited: Enter restarts it.
    pub local_exited: bool,
    /// Wall-clock time the session last connected.
    pub connected_since: Option<chrono::DateTime<chrono::Local>>,
    /// Bumped to make trigger highlights flash.
    pub highlight_until: f64,
    /// Serial DTR / RTS as last set (starts from the profile's on-connect settings).
    pub lines: (bool, bool),
}

/// A tab. Its session is `id`; `split` lays out extra panes (which have no tab of their own).
pub struct Tab {
    pub id: String,
    /// Output arrived while the tab wasn't active.
    pub activity: bool,
    pub split: Option<crate::splits::Split>,
}

pub struct Shared {
    pub svc: Arc<Service>,
    pub profiles: Vec<Profile>,
    pub prefs: AppPreferences,
    pub tabs: Vec<Tab>,
    /// Every on-screen session by id (tabs and split panes).
    pub panes: std::collections::HashMap<String, Pane>,
    /// Session id of the active tab.
    pub active: Option<String>,
    /// Session id of the focused pane inside the active tab (the tab itself or a split child).
    pub focused: Option<String>,
    pub toasts: Vec<Toast>,
    pub requests: Vec<Request>,
    pub broadcast: bool,
    /// None = every connected session.
    pub broadcast_targets: Option<HashSet<String>>,
    pub now: f64,
    pub ctx: egui::Context,
}

impl Shared {
    pub fn new(svc: Arc<Service>, ctx: egui::Context) -> Shared {
        let prefs = svc.preferences();
        let profiles = svc.profiles();
        Shared {
            svc,
            profiles,
            prefs,
            tabs: vec![],
            panes: Default::default(),
            active: None,
            focused: None,
            toasts: vec![],
            requests: vec![],
            broadcast: false,
            broadcast_targets: None,
            now: 0.0,
            ctx,
        }
    }

    pub fn request(&mut self, r: Request) {
        self.requests.push(r);
        self.ctx.request_repaint();
    }

    fn push(&mut self, text: impl Into<String>, kind: ToastKind) {
        let secs = if kind == ToastKind::Error { 6.0 } else { 4.0 };
        self.toasts.push(Toast { text: text.into(), kind, until: self.now + secs });
        self.ctx.request_repaint();
    }

    pub fn info(&mut self, text: impl Into<String>) {
        self.push(text, ToastKind::Info);
    }

    pub fn success(&mut self, text: impl Into<String>) {
        self.push(text, ToastKind::Success);
    }

    pub fn warn(&mut self, text: impl Into<String>) {
        self.push(text, ToastKind::Warning);
    }

    pub fn error(&mut self, text: impl Into<String>) {
        self.push(text, ToastKind::Error);
    }

    pub fn profile(&self, id: &str) -> Option<&Profile> {
        self.profiles.iter().find(|p| p.id == id)
    }

    pub fn reload_profiles(&mut self) {
        self.profiles = self.svc.profiles();
    }

    pub fn tab(&self, id: &str) -> Option<&Tab> {
        self.tabs.iter().find(|t| t.id == id)
    }

    pub fn pane(&self, id: &str) -> Option<&Pane> {
        self.panes.get(id)
    }

    pub fn pane_mut(&mut self, id: &str) -> Option<&mut Pane> {
        self.panes.get_mut(id)
    }

    /// The session keyboard actions apply to: the focused pane, else the active tab.
    pub fn focused_session(&self) -> Option<String> {
        self.focused.clone().or_else(|| self.active.clone())
    }

    /// Every session id on screen (tabs and split panes).
    pub fn all_sessions(&self) -> Vec<String> {
        self.panes.keys().cloned().collect()
    }

    pub fn session_state(&self, id: &str) -> Option<SessionState> {
        self.svc.sessions.info(id).map(|i| i.state)
    }

    /// Runs `f` on a worker thread and toasts its error, if any (for slow service calls).
    pub fn spawn<T: Send + 'static>(&self, f: impl FnOnce() -> nexus_core::Result<T> + Send + 'static, done: impl FnOnce(T) + Send + 'static) {
        let ctx = self.ctx.clone();
        std::thread::spawn(move || {
            match f() {
                Ok(v) => done(v),
                Err(e) => crate::app::post_error(e.to_string()),
            }
            ctx.request_repaint();
        });
    }
}

/// Formats a command-key shortcut for menus: "⌘K" on macOS, "Ctrl+K" elsewhere.
pub fn keys(s: &str) -> String {
    if cfg!(target_os = "macos") { s.replace("Mod+Shift+", "⇧⌘").replace("Mod+", "⌘") } else { s.replace("Mod+", "Ctrl+") }
}
