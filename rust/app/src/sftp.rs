//! SFTP browser (right dock), the file-drop dialog and the remote directory picker.
//! Every SFTP call blocks on the network, so it runs on a worker thread; results come back
//! through `inbox` and are applied on the UI thread at the start of the next frame.

use crate::chrome::modal;
use crate::state::{Request, Shared};
use crate::theme::{self, Tokens};
use crate::views::View;
use crate::widgets;
use egui::{Align, Key, Layout, Modifiers, PointerButton, RichText, Sense, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::clients::sftp::{self as rsftp, FileEntry, Progress};
use nexus_core::keyfiles::safe_sftp_upload_name;
use nexus_core::models::Protocol;
use nexus_core::service::Service;
use parking_lot::Mutex;
use std::collections::{BTreeMap, HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::Arc;

/// Worker results, applied on the UI thread.
enum Done {
    Listed {
        session: String,
        path: String,
        files: Result<Vec<FileEntry>, String>,
    },
    Picker {
        session: String,
        result: Result<(String, Vec<FileEntry>, bool), String>,
    },
    Refresh(String),
    TransferEnded {
        session: String,
        path: String,
    },
    /// Always shown.
    Toast(String),
    /// Shown only while the panel for `session` is open (otherwise per-file toasts cover it).
    Summary {
        session: String,
        text: String,
    },
}

#[derive(Clone)]
struct Inbox(Arc<Mutex<Vec<Done>>>, egui::Context);

impl Inbox {
    fn push(&self, d: Done) {
        self.0.lock().push(d);
        self.1.request_repaint();
    }
}

#[derive(Default)]
struct Panel {
    cwd: String,
    files: Vec<FileEntry>,
    loading: bool,
    error: Option<String>,
    /// Selected paths.
    selected: HashSet<String>,
    /// Index into `files` for Shift-range selection.
    anchor: Option<usize>,
}

struct Transfer {
    session: String,
    path: String,
    upload: bool,
    transferred: u64,
    total: u64,
}

enum InputKind {
    Mkdir,
    Rename(FileEntry),
    GoTo,
}

struct Picker {
    session: String,
    files: Vec<PathBuf>,
    cwd: String,
    entries: Vec<FileEntry>,
    cwd_writable: bool,
    filter: String,
    /// Index into the visible (filtered) entries.
    cursor: Option<usize>,
    loading: bool,
    error: Option<String>,
    focused: bool,
}

enum Dialog {
    Input { session: String, kind: InputKind, value: String },
    Delete { session: String, entries: Vec<FileEntry> },
    Drop { session: String, paths: Vec<PathBuf>, ssh: bool },
    Picker(Picker),
}

#[derive(Default)]
pub struct Sftp {
    open_for: Option<String>,
    panels: HashMap<String, Panel>,
    transfers: Vec<Transfer>,
    dialog: Option<Dialog>,
    /// Profile id (or host:port for quick connects) → bookmarked paths. Loaded on first use.
    bookmarks: Option<BTreeMap<String, Vec<String>>>,
    inbox: Arc<Mutex<Vec<Done>>>,
    /// Where a browser opened from a terminal should start: the directory its shell reported.
    start_at: HashMap<String, String>,
}

// --- path helpers ---------------------------------------------------------------------------

fn parent(p: &str) -> String {
    let t = p.trim_end_matches('/');
    match t.rfind('/') {
        Some(i) if i > 0 => t[..i].to_string(),
        _ => "/".into(),
    }
}

/// "/a/b" → [("/", "/"), ("a", "/a"), ("b", "/a/b")]
fn segments(p: &str) -> Vec<(String, String)> {
    let mut out = vec![("/".to_string(), "/".to_string())];
    let mut acc = String::new();
    for part in p.split('/').filter(|s| !s.is_empty()) {
        acc = format!("{acc}/{part}");
        out.push((part.to_string(), acc.clone()));
    }
    out
}

/// Permission checks from the mode string, as the web build did (any r/w bit counts).
fn can_read(mode: &str) -> bool {
    mode.is_empty() || mode.contains('r')
}

fn can_write(mode: &str) -> bool {
    mode.is_empty() || mode.contains('w')
}

fn file_icon(e: &FileEntry) -> &'static str {
    if e.is_dir {
        return icon::FOLDER;
    }
    let ext = e.name.rsplit('.').next().unwrap_or("").to_lowercase();
    match ext.as_str() {
        "jpg" | "jpeg" | "png" | "gif" | "svg" | "webp" => icon::FILE_IMAGE,
        "mp3" | "wav" | "ogg" | "flac" => icon::FILE_AUDIO,
        "mp4" | "avi" | "mkv" | "mov" => icon::FILE_VIDEO,
        "zip" | "tar" | "gz" | "bz2" | "xz" | "7z" => icon::FILE_ZIP,
        "sh" | "bash" | "zsh" => icon::TERMINAL,
        "py" | "js" | "ts" | "go" | "rs" | "c" | "h" | "cpp" | "java" => icon::FILE_CODE,
        "md" | "txt" | "log" | "csv" => icon::FILE_TEXT,
        "json" | "yaml" | "yml" | "toml" | "xml" => icon::GEAR,
        "key" | "pem" | "crt" | "pub" => icon::KEY,
        _ => icon::FILE,
    }
}

fn base_name(p: &str) -> &str {
    p.rsplit('/').next().filter(|s| !s.is_empty()).unwrap_or(p)
}

fn local_name(p: &Path) -> String {
    p.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default()
}

fn modified(ms: i64) -> String {
    use chrono::TimeZone;
    chrono::Local.timestamp_millis_opt(ms).single().map(|t| t.format("%Y-%m-%d %H:%M:%S").to_string()).unwrap_or_default()
}

fn danger(ui: &mut Ui, label: &str) -> egui::Response {
    let t = Tokens::get(ui.ctx());
    ui.add(egui::Button::new(RichText::new(label).color(t.accent_text)).fill(t.danger))
}

fn breadcrumb(ui: &mut Ui, path: &str) -> Option<String> {
    let t = Tokens::get(ui.ctx());
    let mut go = None;
    ui.horizontal_wrapped(|ui| {
        ui.spacing_mut().item_spacing.x = 2.0;
        for (i, (label, p)) in segments(path).into_iter().enumerate() {
            if i > 1 {
                ui.label(RichText::new(icon::CARET_RIGHT).size(10.0).color(t.text_faint));
            }
            let last = p == path || (path.is_empty() && i == 0);
            let text = RichText::new(label).size(12.0).color(if last { t.text } else { t.text_dim });
            if ui.add(egui::Button::new(text).frame(false)).on_hover_text(&p).clicked() {
                go = Some(p);
            }
        }
    });
    go
}

fn state_line(ui: &mut Ui, loading: bool, error: Option<&str>) -> bool {
    let t = Tokens::get(ui.ctx());
    let mut retry = false;
    ui.vertical_centered(|ui| {
        ui.add_space(28.0);
        if loading {
            ui.add(egui::Spinner::new());
            ui.label(RichText::new("Loading…").color(t.text_dim));
        } else if let Some(e) = error {
            ui.label(RichText::new(icon::WARNING).size(28.0).color(t.warning));
            ui.label(RichText::new(e).color(t.text_dim).size(12.0));
            retry = ui.button(format!("{}  Retry", icon::ARROWS_CLOCKWISE)).clicked();
        }
    });
    retry
}

impl Sftp {
    /// Opens (or closes) the browser for `session`; `at` is the shell's reported directory, which
    /// the browser goes to when it opens.
    pub fn toggle(&mut self, session: &str, at: Option<String>) {
        if self.open_for.as_deref() == Some(session) {
            self.close(session);
        } else {
            self.open_for = Some(session.into());
            if let Some(dir) = at.filter(|d| d.starts_with('/')) {
                self.start_at.insert(session.into(), dir);
            }
        }
    }

    /// The session whose browser is showing in the dock, if any.
    pub fn open_for(&self) -> Option<&str> {
        self.open_for.as_deref()
    }

    pub fn close(&mut self, session: &str) {
        if self.open_for.as_deref() == Some(session) {
            self.open_for = None;
        }
        self.panels.remove(session);
    }

    pub fn on_progress(&mut self, session: &str, p: Progress) {
        let i = match self.transfers.iter().position(|t| t.session == session && t.path == p.path) {
            Some(i) => i,
            None if p.done => return,
            None => {
                self.transfers.push(Transfer { session: session.into(), path: p.path.clone(), upload: false, transferred: 0, total: 0 });
                self.transfers.len() - 1
            }
        };
        let tr = &mut self.transfers[i];
        tr.transferred = p.transferred;
        tr.total = p.total;
        if p.done {
            let tr = self.transfers.remove(i);
            if self.open_for.as_deref() != Some(session) {
                let verb = if tr.upload { "↑ Uploaded" } else { "↓ Downloaded" };
                self.inbox.lock().push(Done::Toast(format!("{verb} {}", base_name(&tr.path))));
            }
        }
    }

    /// Files dropped on a terminal: offer "paste contents" or (SSH) "upload to a directory".
    pub fn drop_files(&mut self, s: &mut Shared, session: &str, paths: Vec<PathBuf>) {
        let ssh = s.pane(session).is_some_and(|p| p.info.protocol == Protocol::Ssh);
        self.dialog = Some(Dialog::Drop { session: session.into(), paths, ssh });
    }

    // --- workers --------------------------------------------------------------------------

    fn work(&self, s: &Shared, f: impl FnOnce(&Service, &Inbox) + Send + 'static) {
        let (svc, inbox) = (s.svc.clone(), Inbox(self.inbox.clone(), s.ctx.clone()));
        std::thread::spawn(move || f(&svc, &inbox));
    }

    fn drain(&mut self, s: &mut Shared) {
        let items = std::mem::take(&mut *self.inbox.lock());
        for d in items {
            match d {
                Done::Listed { session, path, files } => {
                    if let Some(p) = self.panels.get_mut(&session) {
                        p.loading = false;
                        match files {
                            Ok(f) => {
                                p.cwd = path;
                                p.files = f;
                                p.error = None;
                            }
                            Err(e) => p.error = Some(e),
                        }
                    }
                }
                Done::Picker { session, result } => {
                    if let Some(Dialog::Picker(pk)) = &mut self.dialog
                        && pk.session == session
                    {
                        pk.loading = false;
                        match result {
                            Ok((cwd, files, writable)) => {
                                pk.cwd = cwd;
                                pk.entries = files.into_iter().filter(|e| e.is_dir).collect();
                                pk.cwd_writable = writable;
                                pk.cursor = None;
                                pk.error = None;
                            }
                            Err(e) => pk.error = Some(e),
                        }
                    }
                }
                Done::Refresh(session) => {
                    if self.open_for.as_deref() == Some(session.as_str())
                        && let Some(cwd) = self.panels.get(&session).map(|p| p.cwd.clone())
                    {
                        self.navigate(s, &session, Some(cwd));
                    }
                }
                Done::TransferEnded { session, path } => self.transfers.retain(|t| !(t.session == session && t.path == path)),
                Done::Toast(text) => s.success(text),
                Done::Summary { session, text } => {
                    if self.open_for.as_deref() == Some(session.as_str()) {
                        s.success(text);
                    }
                }
            }
        }
    }

    /// Lists `path` (None: the session's cwd, falling back to the SFTP home).
    fn navigate(&mut self, s: &Shared, session: &str, path: Option<String>) {
        let p = self.panels.entry(session.into()).or_default();
        if p.loading {
            return;
        }
        p.loading = true;
        p.selected.clear();
        p.anchor = None;
        let sid = session.to_string();
        self.work(s, move |svc, out| {
            let path = path.filter(|p| !p.is_empty()).map(Ok).unwrap_or_else(|| start_dir(svc, &sid));
            let (path, files) = match path {
                Ok(p) => {
                    let f = svc.sftp_list(&sid, &p).map_err(|e| e.to_string());
                    (p, f)
                }
                Err(e) => (String::new(), Err(e)),
            };
            out.push(Done::Listed { session: sid, path, files });
        });
    }

    fn refresh(&mut self, s: &Shared, session: &str) {
        let cwd = self.panels.get(session).map(|p| p.cwd.clone()).filter(|c| !c.is_empty());
        self.navigate(s, session, cwd);
    }

    /// Uploads local files into `dir`, one after another; stops at the first failure.
    fn upload(&mut self, s: &Shared, session: &str, dir: &str, files: Vec<PathBuf>) {
        if files.is_empty() {
            return;
        }
        for f in &files {
            if let Ok(name) = safe_sftp_upload_name(&local_name(f)) {
                self.transfers.push(Transfer { session: session.into(), path: rsftp::join(dir, &name), upload: true, transferred: 0, total: 0 });
            }
        }
        let (sid, dir) = (session.to_string(), dir.to_string());
        self.work(s, move |svc, out| {
            let n = files.len();
            for f in &files {
                let name = local_name(f);
                let remote = safe_sftp_upload_name(&name).map(|n| rsftp::join(&dir, &n)).unwrap_or_default();
                let r = svc.sftp_upload_path(&sid, &dir, f);
                out.push(Done::TransferEnded { session: sid.clone(), path: remote });
                out.push(Done::Refresh(sid.clone()));
                if let Err(e) = r {
                    // Drop the queued entries that will never start.
                    for g in &files {
                        if let Ok(n) = safe_sftp_upload_name(&local_name(g)) {
                            out.push(Done::TransferEnded { session: sid.clone(), path: rsftp::join(&dir, &n) });
                        }
                    }
                    crate::app::post_error(format!("Upload failed for {name}: {e}"));
                    return;
                }
            }
            let text = if n == 1 { format!("Uploaded {}", local_name(&files[0])) } else { format!("Uploaded {n} files") };
            out.push(Done::Summary { session: sid, text });
        });
    }

    fn download(&mut self, s: &Shared, session: &str, files: Vec<FileEntry>) {
        let targets: Vec<(FileEntry, PathBuf)> = if files.len() == 1 {
            // The name comes from the server: keep only a safe base name for the dialog's default.
            let suggested = safe_sftp_upload_name(&files[0].name).unwrap_or_else(|_| "download".into());
            let Some(p) = rfd::FileDialog::new().set_file_name(suggested).save_file() else {
                return;
            };
            vec![(files[0].clone(), p)]
        } else {
            let Some(dir) = rfd::FileDialog::new().set_title("Download to folder").pick_folder() else {
                return;
            };
            files.into_iter().filter_map(|e| safe_sftp_upload_name(&e.name).ok().map(|n| (e, dir.join(n)))).collect()
        };
        for (e, _) in &targets {
            self.transfers.push(Transfer { session: session.into(), path: e.path.clone(), upload: false, transferred: 0, total: e.size });
        }
        let sid = session.to_string();
        self.work(s, move |svc, out| {
            let n = targets.len();
            let mut failed = 0;
            for (e, local) in &targets {
                let r = svc.sessions.sftp_download(&sid, &e.path, local);
                out.push(Done::TransferEnded { session: sid.clone(), path: e.path.clone() });
                if let Err(err) = r {
                    failed += 1;
                    crate::app::post_error(format!("Download failed: {} — {err}", e.name));
                }
            }
            if failed == 0 {
                let text = if n == 1 { format!("Downloaded {}", targets[0].0.name) } else { format!("All {n} files downloaded") };
                out.push(Done::Summary { session: sid, text });
            }
        });
    }

    fn delete(&mut self, s: &Shared, session: &str, entries: Vec<FileEntry>) {
        let sid = session.to_string();
        self.work(s, move |svc, out| {
            let mut failed = 0;
            for e in &entries {
                if let Err(err) = svc.sftp_remove(&sid, &e.path) {
                    failed += 1;
                    crate::app::post_error(format!("Delete failed: {} — {err}", e.name));
                }
            }
            if failed == 0 {
                let text = match entries.as_slice() {
                    [e] => format!("{} deleted", if e.is_dir { "Directory" } else { "File" }),
                    _ => format!("Deleted {} items", entries.len()),
                };
                out.push(Done::Summary { session: sid.clone(), text });
            }
            out.push(Done::Refresh(sid));
        });
    }

    /// Runs a single SFTP operation, then toasts `ok` and refreshes the listing.
    fn op(
        &mut self,
        s: &Shared,
        session: &str,
        ok: &'static str,
        what: &'static str,
        f: impl FnOnce(&Service, &str) -> nexus_core::Result<()> + Send + 'static,
    ) {
        let sid = session.to_string();
        self.work(s, move |svc, out| {
            match f(svc, &sid) {
                Ok(()) => out.push(Done::Summary { session: sid.clone(), text: ok.into() }),
                Err(e) => crate::app::post_error(format!("{what} failed: {e}")),
            }
            out.push(Done::Refresh(sid));
        });
    }

    // --- bookmarks ------------------------------------------------------------------------

    fn bookmarks_path(s: &Shared) -> PathBuf {
        s.svc.config.data_dir.join("sftp-bookmarks.json")
    }

    fn bookmarks(&mut self, s: &Shared) -> &mut BTreeMap<String, Vec<String>> {
        self.bookmarks.get_or_insert_with(|| std::fs::read(Self::bookmarks_path(s)).ok().and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or_default())
    }

    fn save_bookmarks(&mut self, s: &mut Shared) {
        let json = serde_json::to_vec_pretty(self.bookmarks(s)).unwrap_or_default();
        if let Err(e) = std::fs::write(Self::bookmarks_path(s), json) {
            s.error(format!("Couldn't save bookmarks: {e}"));
        }
    }

    fn bookmark_key(s: &Shared, session: &str) -> Option<String> {
        let i = &s.pane(session)?.info;
        Some(if i.profile_id.is_empty() { format!("{}:{}", i.host, i.port) } else { i.profile_id.clone() })
    }

    // --- dock panel -----------------------------------------------------------------------

    /// The dock panel.
    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        self.drain(s);
        let Some(session) = self.open_for.clone() else {
            return;
        };
        if let Some(dir) = self.start_at.remove(&session) {
            if self.panels.get(&session).is_none_or(|p| p.cwd != dir) {
                self.navigate(s, &session, Some(dir));
            }
        } else if !self.panels.contains_key(&session) {
            self.navigate(s, &session, None);
        }
        let t = Tokens::get(ui.ctx());
        ui.style_mut().interaction.selectable_labels = false;
        let title = s.pane(&session).map(|p| p.info.tab_name.clone()).unwrap_or_default();
        let bm_key = Self::bookmark_key(s, &session);
        let marks = bm_key.as_ref().and_then(|k| self.bookmarks(s).get(k).cloned()).unwrap_or_default();

        enum Act {
            Nav(String),
            Refresh,
            Close,
            Upload,
            Mkdir,
            GoTo,
            AddMark,
            DelMark(usize),
            Download(Vec<FileEntry>),
            Rename(FileEntry),
            Delete(Vec<FileEntry>),
        }
        let mut act = None;

        // Header: title + toolbar.
        ui.horizontal(|ui| {
            ui.label(RichText::new(icon::FOLDER_OPEN).size(15.0).color(t.accent));
            ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                ui.spacing_mut().item_spacing.x = 2.0;
                if widgets::icon_button(ui, icon::X, "Close").clicked() {
                    act = Some(Act::Close);
                }
                if widgets::icon_button(ui, icon::ARROWS_CLOCKWISE, "Refresh").clicked() {
                    act = Some(Act::Refresh);
                }
                if widgets::icon_button(ui, icon::UPLOAD_SIMPLE, "Upload files").clicked() {
                    act = Some(Act::Upload);
                }
                if widgets::icon_button(ui, icon::FOLDER_PLUS, "New folder").clicked() {
                    act = Some(Act::Mkdir);
                }
                let star = widgets::icon_button(ui, icon::STAR, "Bookmarks");
                egui::Popup::menu(&star).show(|ui| {
                    ui.set_min_width(220.0);
                    widgets::section(ui, "Bookmarks");
                    if marks.is_empty() {
                        widgets::hint(ui, "No bookmarks yet.");
                    }
                    for (i, m) in marks.iter().enumerate() {
                        ui.horizontal(|ui| {
                            if widgets::icon_button(ui, icon::X, "Remove bookmark").clicked() {
                                act = Some(Act::DelMark(i));
                            }
                            if ui.add(egui::Button::new(RichText::new(m).font(theme::mono(12.0))).frame(false)).clicked() {
                                act = Some(Act::Nav(m.clone()));
                                ui.close();
                            }
                        });
                    }
                    ui.separator();
                    if ui.button(format!("{}  Bookmark current", icon::STAR)).clicked() {
                        act = Some(Act::AddMark);
                        ui.close();
                    }
                });
                if widgets::icon_button(ui, icon::NAVIGATION_ARROW, "Go to path").clicked() {
                    act = Some(Act::GoTo);
                }
                ui.with_layout(Layout::left_to_right(Align::Center), |ui| {
                    ui.add(egui::Label::new(RichText::new(&title).font(theme::semibold(13.0))).truncate());
                });
            });
        });

        let Some(p) = self.panels.get(&session) else {
            return;
        };
        if let Some(go) = breadcrumb(ui, &p.cwd) {
            act = Some(Act::Nav(go));
        }
        ui.separator();

        // Transfer queue.
        let mine: Vec<&Transfer> = self.transfers.iter().filter(|x| x.session == session).collect();
        if !mine.is_empty() {
            widgets::section(ui, &format!("Transfers ({})", mine.len()));
            egui::ScrollArea::vertical().id_salt("sftp-transfers").max_height(120.0).show(ui, |ui| {
                for x in mine {
                    let frac = if x.total > 0 { (x.transferred as f32 / x.total as f32).min(1.0) } else { 0.0 };
                    ui.horizontal(|ui| {
                        let arrow = if x.upload { icon::ARROW_UP } else { icon::DOWNLOAD_SIMPLE };
                        ui.label(RichText::new(arrow).color(t.accent));
                        ui.vertical(|ui| {
                            ui.add(egui::Label::new(base_name(&x.path)).truncate()).on_hover_text(&x.path);
                            ui.add(egui::ProgressBar::new(frac).desired_height(4.0).fill(t.accent));
                            let total = if x.total > 0 { widgets::human_size(x.total) } else { "?".into() };
                            ui.label(
                                RichText::new(format!("{} / {total} ({:.0}%)", widgets::human_size(x.transferred), frac * 100.0))
                                    .size(11.0)
                                    .color(t.text_faint),
                            );
                        });
                    });
                }
            });
            ui.separator();
        }

        // File list.
        let sel: Vec<FileEntry> = p.files.iter().filter(|f| p.selected.contains(&f.path)).cloned().collect();
        let bar_h = if sel.is_empty() { 0.0 } else { 34.0 };
        let list_h = (ui.available_height() - bar_h).max(60.0);
        let has_parent = !p.cwd.is_empty() && p.cwd != "/";
        let off = has_parent as usize;
        let mut pick = None;
        ui.allocate_ui(vec2(ui.available_width(), list_h), |ui| {
            if p.loading || p.error.is_some() {
                if state_line(ui, p.loading, p.error.as_deref()) {
                    act = Some(Act::Refresh);
                }
                return;
            }
            if p.files.is_empty() {
                if has_parent && ui.add(egui::Button::new(format!("{}  ..", icon::ARROW_BEND_UP_LEFT)).frame(false)).double_clicked() {
                    act = Some(Act::Nav(parent(&p.cwd)));
                }
                widgets::empty(ui, icon::FOLDER_OPEN, "Empty directory", "");
                return;
            }
            let clicked = widgets::rows(ui, &format!("sftp-{session}"), p.files.len() + off, 24.0, None, |ui, i| {
                let r = ui.max_rect();
                if i < off {
                    ui.label(RichText::new(icon::ARROW_BEND_UP_LEFT).size(14.0).color(t.text_dim));
                    ui.label(RichText::new("..").color(t.text_dim));
                    return;
                }
                let e = &p.files[i - off];
                if p.selected.contains(&e.path) {
                    ui.painter().rect_filled(r.expand2(vec2(6.0, 0.0)), 3.0, t.row_selected);
                }
                ui.painter().text(
                    r.left_center() + vec2(8.0, 0.0),
                    egui::Align2::CENTER_CENTER,
                    file_icon(e),
                    egui::FontId::proportional(14.0),
                    if e.is_dir { t.accent } else { t.text_dim },
                );
                let meta_w = if e.is_dir { 0.0 } else { 72.0 };
                let nr = egui::Rect::from_min_max(egui::pos2(r.min.x + 22.0, r.min.y), egui::pos2(r.max.x - meta_w - 4.0, r.max.y));
                ui.scope_builder(egui::UiBuilder::new().max_rect(nr).layout(Layout::left_to_right(Align::Center)), |ui| {
                    ui.add(egui::Label::new(&e.name).truncate());
                });
                if !e.is_dir {
                    let mr = egui::Rect::from_min_max(egui::pos2(r.max.x - meta_w, r.min.y), r.max);
                    ui.painter().text(
                        mr.right_center(),
                        egui::Align2::RIGHT_CENTER,
                        widgets::human_size(e.size),
                        egui::FontId::proportional(11.5),
                        t.text_faint,
                    );
                    if e.modified_at > 0 {
                        ui.interact(mr, ui.id().with(("meta", i)), Sense::hover()).on_hover_text(modified(e.modified_at));
                    }
                }
            });
            if let Some(i) = clicked {
                let dbl = ui.input(|inp| inp.pointer.button_double_clicked(PointerButton::Primary));
                if i < off {
                    if dbl {
                        act = Some(Act::Nav(parent(&p.cwd)));
                    }
                } else if dbl && p.files[i - off].is_dir {
                    act = Some(Act::Nav(p.files[i - off].path.clone()));
                } else if !dbl {
                    pick = Some((i - off, ui.input(|inp| inp.modifiers)));
                }
            }
        });
        if let Some((fi, m)) = pick
            && let Some(p) = self.panels.get_mut(&session)
        {
            let path = p.files[fi].path.clone();
            if m.command {
                if !p.selected.remove(&path) {
                    p.selected.insert(path);
                }
                p.anchor = Some(fi);
            } else if m.shift
                && let Some(a) = p.anchor
            {
                let (lo, hi) = (a.min(fi), a.max(fi));
                p.selected = p.files[lo..=hi].iter().map(|f| f.path.clone()).collect();
            } else {
                let only = p.selected.len() == 1 && p.selected.contains(&path);
                p.selected.clear();
                if !only {
                    p.selected.insert(path);
                }
                p.anchor = Some(fi);
            }
        }

        // Selection actions.
        if !sel.is_empty() {
            ui.separator();
            ui.horizontal(|ui| {
                let files: Vec<FileEntry> = sel.iter().filter(|e| !e.is_dir).cloned().collect();
                let label = if sel.len() == 1 { sel[0].name.clone() } else { format!("{} items selected", sel.len()) };
                let tip = sel.iter().map(|e| e.path.as_str()).collect::<Vec<_>>().join("\n");
                ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                    let n = if sel.len() == 1 { String::new() } else { format!(" ({})", sel.len()) };
                    if danger(ui, &format!("{}  Delete{n}", icon::TRASH)).clicked() {
                        act = Some(Act::Delete(sel.clone()));
                    }
                    if sel.len() == 1 && widgets::button(ui, icon::PENCIL_SIMPLE, "Rename").clicked() {
                        act = Some(Act::Rename(sel[0].clone()));
                    }
                    if !files.is_empty() {
                        let n = if files.len() == 1 { String::new() } else { format!(" ({})", files.len()) };
                        if widgets::primary(ui, icon::DOWNLOAD_SIMPLE, &format!("Download{n}")).clicked() {
                            act = Some(Act::Download(files));
                        }
                    }
                    ui.with_layout(Layout::left_to_right(Align::Center), |ui| {
                        ui.add(egui::Label::new(RichText::new(label).color(t.text_dim)).truncate()).on_hover_text(tip);
                    });
                });
            });
        }

        let cwd = self.panels.get(&session).map(|p| p.cwd.clone()).unwrap_or_default();
        match act {
            None => {}
            Some(Act::Nav(path)) => self.navigate(s, &session, Some(path)),
            Some(Act::Refresh) => self.refresh(s, &session),
            Some(Act::Close) => self.close(&session),
            Some(Act::Upload) => {
                if let Some(files) = rfd::FileDialog::new().set_title("Upload files").pick_files() {
                    self.upload(s, &session, &cwd, files);
                }
            }
            Some(Act::Mkdir) => self.dialog = Some(Dialog::Input { session, kind: InputKind::Mkdir, value: String::new() }),
            Some(Act::GoTo) => self.dialog = Some(Dialog::Input { session, kind: InputKind::GoTo, value: cwd }),
            Some(Act::Rename(e)) => self.dialog = Some(Dialog::Input { session, value: e.name.clone(), kind: InputKind::Rename(e) }),
            Some(Act::Delete(entries)) => self.dialog = Some(Dialog::Delete { session, entries }),
            Some(Act::Download(files)) => self.download(s, &session, files),
            Some(Act::AddMark) => {
                if let Some(k) = bm_key
                    && !cwd.is_empty()
                {
                    let list = self.bookmarks(s).entry(k).or_default();
                    if list.contains(&cwd) {
                        s.info("Already bookmarked");
                    } else {
                        list.push(cwd);
                        self.save_bookmarks(s);
                    }
                }
            }
            Some(Act::DelMark(i)) => {
                if let Some(k) = bm_key
                    && let Some(list) = self.bookmarks(s).get_mut(&k)
                    && i < list.len()
                {
                    list.remove(i);
                    if list.is_empty() {
                        self.bookmarks(s).remove(&k);
                    }
                    self.save_bookmarks(s);
                }
            }
        }
    }

    // --- dialogs --------------------------------------------------------------------------

    /// Modal dialogs (file drop, directory picker), drawn every frame.
    pub fn dialogs(&mut self, ctx: &egui::Context, s: &mut Shared) {
        self.drain(s);
        let Some(mut d) = self.dialog.take() else {
            return;
        };
        let t = Tokens::get(ctx);
        let esc = crate::chrome::modal_key(ctx, Key::Escape);
        let enter = crate::chrome::modal_key(ctx, Key::Enter);
        let mut keep = !esc;
        match &mut d {
            Dialog::Input { session, kind, value } => {
                let (title, hint, ok) = match kind {
                    InputKind::Mkdir => ("New Folder", "Folder name", "Create"),
                    InputKind::Rename(_) => ("Rename", "New name", "Rename"),
                    InputKind::GoTo => ("Go to Path", "/path/to/directory", "Navigate"),
                };
                let mut go = enter;
                modal(ctx, "sftp-input", 360.0, |ui| {
                    ui.label(RichText::new(title).font(theme::semibold(15.0)));
                    ui.add(egui::TextEdit::singleline(value).hint_text(hint).desired_width(f32::INFINITY)).request_focus();
                    ui.add_space(6.0);
                    ui.horizontal(|ui| {
                        go |= widgets::primary(ui, icon::CHECK, ok).clicked();
                        if ui.button("Cancel").clicked() {
                            keep = false;
                        }
                    });
                });
                let v = value.trim().to_string();
                if go && keep {
                    keep = false;
                    let cwd = self.panels.get(session.as_str()).map(|p| p.cwd.clone()).unwrap_or_default();
                    match kind {
                        _ if v.is_empty() => {}
                        InputKind::GoTo => self.navigate(s, session, Some(v)),
                        InputKind::Mkdir => {
                            let path = rsftp::join(&cwd, &v);
                            self.op(s, session, "Directory created", "mkdir", move |svc, sid| svc.sftp_mkdir(sid, &path));
                        }
                        InputKind::Rename(e) if v != e.name => {
                            let (from, to) = (e.path.clone(), rsftp::join(&parent(&e.path), &v));
                            self.op(s, session, "Renamed", "Rename", move |svc, sid| svc.sftp_rename(sid, &from, &to));
                        }
                        InputKind::Rename(_) => {}
                    }
                }
            }
            Dialog::Delete { session, entries } => {
                let (title, what, ok) = match entries.as_slice() {
                    [e] => (format!("Delete {}", if e.is_dir { "Folder" } else { "File" }), e.name.clone(), "Delete"),
                    _ => (format!("Delete {} Items", entries.len()), format!("{} selected items", entries.len()), "Delete All"),
                };
                let mut go = false;
                modal(ctx, "sftp-delete", 380.0, |ui| {
                    ui.label(RichText::new(format!("{}  {title}", icon::TRASH)).font(theme::semibold(15.0)));
                    ui.label(format!("Permanently delete {what}?"));
                    ui.add_space(6.0);
                    ui.horizontal(|ui| {
                        go = danger(ui, ok).clicked();
                        if ui.button("Cancel").clicked() {
                            keep = false;
                        }
                    });
                });
                if go {
                    keep = false;
                    self.delete(s, session, std::mem::take(entries));
                }
            }
            Dialog::Drop { session, paths, ssh } => {
                let title = match paths.as_slice() {
                    [p] => {
                        let size = std::fs::metadata(p).ok().filter(|m| m.is_file()).map(|m| format!(" ({})", widgets::human_size(m.len())));
                        format!("{}{}", local_name(p), size.unwrap_or_default())
                    }
                    _ => format!("{} files", paths.len()),
                };
                let (mut paste, mut upload) = (false, false);
                modal(ctx, "sftp-drop", 420.0, |ui| {
                    ui.label(RichText::new(format!("{}  File Drop", icon::FILE_ARROW_UP)).font(theme::semibold(15.0)));
                    ui.label(RichText::new(title).strong());
                    ui.label(
                        RichText::new(format!("Choose what to do with the dropped {}.", if paths.len() == 1 { "file" } else { "files" })).color(t.text_dim),
                    );
                    ui.add_space(8.0);
                    ui.horizontal(|ui| {
                        upload = ui
                            .add_enabled_ui(*ssh, |ui| widgets::primary(ui, icon::UPLOAD_SIMPLE, "Upload to current directory"))
                            .inner
                            .on_disabled_hover_text("Upload is only available for SSH sessions.")
                            .clicked();
                        paste = widgets::button(ui, icon::CLIPBOARD_TEXT, "Paste contents").clicked();
                        if ui.button("Cancel").clicked() {
                            keep = false;
                        }
                    });
                });
                if paste {
                    keep = false;
                    paste_files(s, session, paths);
                } else if upload {
                    let start = self.panels.get(session.as_str()).map(|p| p.cwd.clone()).filter(|c| !c.is_empty());
                    let pk = Picker {
                        session: session.clone(),
                        files: std::mem::take(paths),
                        cwd: start.clone().unwrap_or_default(),
                        entries: vec![],
                        cwd_writable: true,
                        filter: String::new(),
                        cursor: None,
                        loading: false,
                        error: None,
                        focused: false,
                    };
                    d = Dialog::Picker(pk);
                    if let Dialog::Picker(pk) = &mut d {
                        self.picker_load(s, pk, start);
                    }
                }
            }
            Dialog::Picker(pk) => {
                if let Some(dir) = self.picker_ui(ctx, s, pk, &mut keep) {
                    keep = false;
                    let files = std::mem::take(&mut pk.files);
                    if files.len() > 1 {
                        s.info(format!("Uploading {} files…", files.len()));
                    }
                    let session = pk.session.clone();
                    self.upload(s, &session, &dir, files);
                }
            }
        }
        if keep && self.dialog.is_none() {
            self.dialog = Some(d);
        }
    }

    fn picker_load(&self, s: &Shared, pk: &mut Picker, path: Option<String>) {
        if pk.loading {
            return;
        }
        pk.loading = true;
        let sid = pk.session.clone();
        self.work(s, move |svc, out| {
            let result = (|| -> Result<_, String> {
                let path = match path.filter(|p| !p.is_empty()) {
                    Some(p) => p,
                    None => start_dir(svc, &sid)?,
                };
                let files = svc.sftp_list(&sid, &path).map_err(|e| e.to_string())?;
                let sftp = svc.sessions.ssh(&sid).and_then(|c| c.sftp()).map_err(|e| e.to_string())?;
                let st = rsftp::stat(&sftp, &path).map_err(|e| e.to_string())?;
                Ok((path, files, can_write(&st.mode)))
            })();
            out.push(Done::Picker { session: sid, result });
        });
    }

    /// Draws the directory picker; returns the chosen directory.
    fn picker_ui(&self, ctx: &egui::Context, s: &Shared, pk: &mut Picker, keep: &mut bool) -> Option<String> {
        let t = Tokens::get(ctx);
        let filter = pk.filter.trim().to_lowercase();
        let visible: Vec<usize> = (0..pk.entries.len()).filter(|&i| pk.entries[i].name.to_lowercase().contains(&filter)).collect();
        pk.cursor = pk.cursor.filter(|&c| c < visible.len());
        let sel = pk.cursor.map(|c| &pk.entries[visible[c]]);
        let (target, writable) = match sel {
            Some(e) => (e.path.clone(), can_write(&e.mode)),
            None => (pk.cwd.clone(), pk.cwd_writable),
        };
        let ready = !pk.loading && pk.error.is_none();
        let mut chosen = None;
        let mut load: Option<Option<String>> = None;

        // Keyboard: ↑↓ move (skipping unreadable folders), Enter uses, Backspace goes up.
        if ready {
            let (down, up, back) = ctx
                .input_mut(|i| (i.consume_key(Modifiers::NONE, Key::ArrowDown), i.consume_key(Modifiers::NONE, Key::ArrowUp), i.key_pressed(Key::Backspace)));
            let delta: isize = if down {
                1
            } else if up {
                -1
            } else {
                0
            };
            if delta != 0 && !visible.is_empty() {
                let n = visible.len() as isize;
                let mut idx = pk.cursor.map_or(if delta > 0 { -1 } else { n }, |c| c as isize);
                for _ in 0..n {
                    idx = (idx + delta).rem_euclid(n);
                    if can_read(&pk.entries[visible[idx as usize]].mode) {
                        pk.cursor = Some(idx as usize);
                        break;
                    }
                }
            }
            if crate::chrome::modal_key(ctx, Key::Enter) && writable && !target.is_empty() {
                chosen = Some(target.clone());
            }
            if back && pk.filter.is_empty() && !pk.cwd.is_empty() && pk.cwd != "/" {
                load = Some(Some(parent(&pk.cwd)));
            }
        }

        modal(ctx, "sftp-picker", 580.0, |ui| {
            ui.style_mut().interaction.selectable_labels = false;
            ui.horizontal(|ui| {
                ui.label(RichText::new(format!("{}  Select Upload Directory", icon::FOLDER_OPEN)).font(theme::semibold(15.0)));
                ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                    if widgets::icon_button(ui, icon::X, "Close").clicked() {
                        *keep = false;
                    }
                });
            });
            if let Some(p) = breadcrumb(ui, &pk.cwd) {
                load = Some(Some(p));
            }
            ui.horizontal(|ui| {
                let r = ui.add(egui::TextEdit::singleline(&mut pk.filter).hint_text("Filter folders").desired_width(260.0));
                if !pk.focused {
                    r.request_focus();
                    pk.focused = true;
                }
                if r.changed() {
                    pk.cursor = None;
                }
                if widgets::button(ui, icon::ARROW_UP, "Up").clicked() && !pk.cwd.is_empty() {
                    load = Some(Some(parent(&pk.cwd)));
                }
                if widgets::button(ui, icon::ARROWS_CLOCKWISE, "Refresh").clicked() {
                    load = Some(Some(pk.cwd.clone()));
                }
            });
            ui.separator();
            let has_parent = !pk.cwd.is_empty() && pk.cwd != "/";
            let off = has_parent as usize;
            ui.allocate_ui(vec2(ui.available_width(), 300.0), |ui| {
                ui.set_min_height(300.0);
                if !ready {
                    if state_line(ui, pk.loading, pk.error.as_deref()) {
                        load = Some(Some(pk.cwd.clone()));
                    }
                    return;
                }
                if visible.is_empty() && !has_parent {
                    widgets::empty(ui, icon::FOLDER_DASHED, "No folders found", "");
                    return;
                }
                let n = visible.len() + off;
                let clicked = widgets::rows(ui, "sftp-picker-rows", n.max(1), 24.0, pk.cursor.map(|c| c + off), |ui, i| {
                    if i < off {
                        ui.label(RichText::new(icon::ARROW_BEND_UP_LEFT).size(14.0).color(t.text_dim));
                        ui.label(RichText::new("..").color(t.text_dim));
                        return;
                    }
                    let Some(&ei) = visible.get(i - off) else {
                        widgets::hint(ui, "No folders found");
                        return;
                    };
                    let e = &pk.entries[ei];
                    let (r, w) = (can_read(&e.mode), can_write(&e.mode));
                    let dim = if r { t.text } else { t.text_faint };
                    ui.label(RichText::new(icon::FOLDER).size(14.0).color(if r { t.accent } else { t.text_faint }));
                    ui.label(RichText::new(&e.name).color(dim));
                    ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                        if !r {
                            widgets::chip(ui, "NO READ", t.danger);
                        } else if !w {
                            widgets::chip(ui, "READ ONLY", t.warning);
                        }
                    });
                });
                if let Some(i) = clicked {
                    let dbl = ui.input(|inp| inp.pointer.button_double_clicked(PointerButton::Primary));
                    if i < off {
                        if dbl {
                            load = Some(Some(parent(&pk.cwd)));
                        }
                    } else if let Some(&ei) = visible.get(i - off)
                        && can_read(&pk.entries[ei].mode)
                    {
                        if dbl {
                            load = Some(Some(pk.entries[ei].path.clone()));
                        } else {
                            pk.cursor = Some(i - off);
                        }
                    }
                }
            });
            ui.separator();
            ui.horizontal(|ui| {
                let suffix = if sel.is_none() && !pk.cwd_writable { " (read only)" } else { "" };
                ui.add(egui::Label::new(RichText::new(format!("{target}{suffix}")).font(theme::mono(11.5)).color(t.text_dim)).truncate())
                    .on_hover_text(&target);
                let status = match (sel.is_some(), writable) {
                    (_, true) => "",
                    (true, false) => "No write permission",
                    (false, false) => "Current folder is read only",
                };
                if !status.is_empty() {
                    ui.label(RichText::new(status).size(11.5).color(t.warning));
                }
            });
            ui.horizontal(|ui| {
                widgets::chip(ui, "READ ONLY", t.warning);
                widgets::chip(ui, "NO READ", t.danger);
                ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                    let ok = ui
                        .add_enabled_ui(ready && writable && !target.is_empty(), |ui| widgets::primary(ui, icon::CHECK, "Use this folder"))
                        .inner
                        .on_disabled_hover_text("No write permission");
                    if ok.clicked() {
                        chosen = Some(target.clone());
                    }
                    if ui.button("Cancel").clicked() {
                        *keep = false;
                    }
                });
            });
        });
        if let Some(path) = load {
            self.picker_load(s, pk, path);
        }
        chosen
    }
}

/// The directory a browser starts in: the shell's cwd, else the SFTP home.
fn start_dir(svc: &Service, sid: &str) -> Result<String, String> {
    match svc.sessions.cwd(sid) {
        Ok(c) if c.trim().starts_with('/') => Ok(c.trim().to_string()),
        _ => svc.sftp_pwd(sid).map_err(|e| e.to_string()),
    }
}

/// "Paste contents": the dropped text files, joined by newlines, as a terminal paste.
fn paste_files(s: &mut Shared, session: &str, paths: &[PathBuf]) {
    let mut combined = String::new();
    for p in paths {
        match std::fs::read_to_string(p) {
            Ok(text) if !text.is_empty() => {
                if !combined.is_empty() {
                    combined.push('\n');
                }
                combined.push_str(&text);
            }
            Ok(_) => {}
            Err(_) => {
                s.error("Failed to read file contents.");
                return;
            }
        }
    }
    if combined.is_empty() {
        return;
    }
    if let Some(View::Terminal(term)) = s.panes.get_mut(session).map(|p| &mut p.view) {
        let mut out = crate::terminal::TermResponse::default();
        // The drop was already confirmed in its own dialog.
        term.paste_now(&combined, &mut out);
        s.request(Request::Write(session.into(), out.input));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn paths() {
        assert_eq!(parent("/a/b/"), "/a");
        assert_eq!(parent("/a"), "/");
        assert_eq!(parent("/"), "/");
        let segs: Vec<String> = segments("/home/me").into_iter().map(|s| s.1).collect();
        assert_eq!(segs, ["/", "/home", "/home/me"]);
        assert!(can_read("dr-x------") && !can_write("dr-x------"));
        assert!(!can_read("d-wx--x--x") && can_write(""));
    }
}
