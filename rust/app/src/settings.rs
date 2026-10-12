//! Settings window: General, Terminal, Security, Known Hosts, Help, About. Also the
//! master-password dialog (strength meter + confirm) used to create or unlock the vault.

use crate::state::{Request, SettingsTab, Shared, keys};
use crate::theme::{self, ThemeKind, Tokens};
use crate::widgets;
use egui::text::{LayoutJob, TextFormat};
use egui::{RichText, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::models::{AppPreferences, KnownHost, Protocol};
use nexus_core::updater::UpdateInfo;
use parking_lot::Mutex;
use std::sync::Arc;

/// A result from a worker thread, polled each frame (`Some` while the work is in flight).
pub struct Job<T>(Arc<Mutex<Option<T>>>);

impl<T: Send + 'static> Job<T> {
    pub fn spawn(ctx: &egui::Context, f: impl FnOnce() -> T + Send + 'static) -> Self {
        let slot = Arc::new(Mutex::new(None));
        let (out, ctx) = (slot.clone(), ctx.clone());
        std::thread::spawn(move || {
            let v = f();
            *out.lock() = Some(v);
            ctx.request_repaint();
        });
        Job(slot)
    }

    pub fn take(&self) -> Option<T> {
        self.0.lock().take()
    }
}

/// Polls `job`; when it has finished, clears it and returns the result.
pub fn poll<T: Send + 'static>(job: &mut Option<Job<T>>) -> Option<T> {
    let v = job.as_ref()?.take()?;
    *job = None;
    Some(v)
}

/// Password strength 0–100 and its label, the same formula as the web build's `_measureStrength`.
pub fn strength(pw: &str) -> (u32, &'static str) {
    if pw.is_empty() {
        return (0, "");
    }
    let len = pw.chars().count() as u32;
    let mut score = (len * 3).min(40);
    let classes = [
        pw.chars().any(|c| c.is_ascii_lowercase()),
        pw.chars().any(|c| c.is_ascii_uppercase()),
        pw.chars().any(|c| c.is_ascii_digit()),
        pw.chars().any(|c| !c.is_ascii_alphanumeric()),
    ];
    score += classes.iter().filter(|b| **b).count() as u32 * 10;
    let unique = pw.chars().collect::<std::collections::HashSet<_>>().len() as u32;
    score += (unique * 20 / len).min(20);
    let score = score.min(100);
    let label = match score {
        0..25 => "Weak",
        25..50 => "Fair",
        50..75 => "Good",
        _ => "Strong",
    };
    (score, label)
}

/// Strength bar + label under a new-password field.
pub fn strength_meter(ui: &mut Ui, pw: &str) {
    let t = Tokens::get(ui.ctx());
    let (score, label) = strength(pw);
    let color = match label {
        "Weak" => t.danger,
        "Fair" => t.warning,
        "Good" => t.accent,
        _ => t.success,
    };
    ui.horizontal(|ui| {
        let (r, _) = ui.allocate_exact_size(vec2(ui.available_width() - 60.0, 6.0), egui::Sense::hover());
        ui.painter().rect_filled(r, 3.0, t.field);
        let mut fill = r;
        fill.set_width(r.width() * score as f32 / 100.0);
        ui.painter().rect_filled(fill, 3.0, color);
        ui.label(RichText::new(label).color(color).size(11.5));
    });
}

/// Create or unlock the vault.
struct MasterPrompt {
    new: bool,
    pw: String,
    confirm: String,
    error: String,
}

#[derive(Default)]
pub struct Settings {
    open: bool,
    tab: SettingsTab,
    /// Load the working copy from `Shared` on the next frame.
    fresh: bool,
    prefs: AppPreferences,
    theme: ThemeKind,
    known: Vec<KnownHost>,
    /// Add-CA form: (host pattern, public key), shown when Some.
    new_ca: Option<(String, String)>,
    default_shell: String,
    help_topic: usize,
    master: Option<MasterPrompt>,
    vault_job: Option<Job<Result<&'static str, String>>>,
    update_job: Option<Job<Result<UpdateInfo, String>>>,
    update: Option<Result<UpdateInfo, String>>,
}

impl Settings {
    pub fn open(&mut self, tab: SettingsTab) {
        self.open = true;
        self.fresh = true;
        self.tab = tab;
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        self.poll(s);
        if !self.open {
            return;
        }
        if std::mem::take(&mut self.fresh) {
            self.prefs = s.prefs.clone();
            self.theme = crate::app::theme_for(&self.prefs.theme);
            self.known = s.svc.known_hosts();
            self.default_shell = nexus_core::clients::local::default_shell();
        }
        let mut open = true;
        let mut close = false;
        egui::Window::new(RichText::new(format!("{}  Settings", icon::GEAR)).font(theme::semibold(14.0)))
            .id(egui::Id::new("settings-window"))
            .open(&mut open)
            .collapsible(false)
            .pivot(egui::Align2::CENTER_CENTER)
            .default_pos(ctx.content_rect().center())
            .resizable(true)
            .default_size([760.0, 540.0])
            .min_size([560.0, 380.0])
            .show(ctx, |ui| {
                self.tab_bar(ui);
                ui.separator();
                let footer = 40.0;
                let h = (ui.available_height() - footer).max(200.0);
                ui.allocate_ui(vec2(ui.available_width(), h), |ui| {
                    ui.set_min_height(h);
                    match self.tab {
                        SettingsTab::General => self.general(ui),
                        SettingsTab::Terminal => self.terminal(ui),
                        SettingsTab::Security => self.security(ui, s),
                        SettingsTab::KnownHosts => self.known_hosts(ui, s),
                        SettingsTab::Help => self.help(ui),
                        SettingsTab::About => self.about(ui, s),
                    }
                });
                ui.separator();
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    if widgets::primary(ui, icon::CHECK, "Save").clicked() {
                        self.save(s);
                        close = true;
                    }
                    if ui.button("Cancel").clicked() {
                        close = true;
                    }
                });
            });
        self.open = open && !close;
        self.master_dialog(ctx, s);
    }

    fn poll(&mut self, s: &mut Shared) {
        match poll(&mut self.vault_job) {
            Some(Ok(msg)) => s.success(msg),
            Some(Err(e)) => s.error(e),
            None => {}
        }
        if let Some(r) = poll(&mut self.update_job) {
            self.update = Some(r);
        }
    }

    fn save(&mut self, s: &mut Shared) {
        let mut p = self.prefs.clone();
        p.theme = self.theme.id().into();
        p.sidebar_width = p.sidebar_width.clamp(180, 500);
        p.default_font_size = p.default_font_size.clamp(8, 36);
        p.default_scrollback = p.default_scrollback.clamp(0, 100_000);
        p.local_shell = p.local_shell.trim().into();
        match s.svc.save_preferences(&p) {
            Ok(()) => {
                s.prefs = p;
                s.request(Request::ReloadPreferences);
                s.success("Settings saved");
            }
            Err(e) => s.error(format!("Failed to save settings: {e}")),
        }
    }

    fn tab_bar(&mut self, ui: &mut Ui) {
        let t = Tokens::get(ui.ctx());
        ui.horizontal(|ui| {
            for (tab, glyph, label) in [
                (SettingsTab::General, icon::SLIDERS_HORIZONTAL, "General"),
                (SettingsTab::Terminal, icon::TERMINAL_WINDOW, "Terminal"),
                (SettingsTab::Security, icon::SHIELD_CHECK, "Security"),
                (SettingsTab::KnownHosts, icon::FINGERPRINT, "Known Hosts"),
                (SettingsTab::Help, icon::BOOK_OPEN, "Help"),
                (SettingsTab::About, icon::INFO, "About"),
            ] {
                let on = self.tab == tab;
                let text = RichText::new(format!("{glyph}  {label}")).color(if on { t.text } else { t.text_dim });
                if ui.selectable_label(on, text).clicked() {
                    self.tab = tab;
                }
            }
        });
    }

    // --- General / Terminal ---------------------------------------------------------------

    fn general(&mut self, ui: &mut Ui) {
        let p = &mut self.prefs;
        form(ui, "general-form", |ui| {
            field(ui, "App theme");
            egui::ComboBox::from_id_salt("app-theme").selected_text(self.theme.label()).width(220.0).show_ui(ui, |ui| {
                for k in ThemeKind::ALL {
                    ui.selectable_value(&mut self.theme, k, k.label());
                }
            });
            ui.end_row();

            field(ui, "Default protocol");
            let cur = Protocol::from_id(&p.default_protocol).map_or("SSH", |x| x.label());
            egui::ComboBox::from_id_salt("default-proto").selected_text(cur).width(220.0).show_ui(ui, |ui| {
                for proto in Protocol::SAVED {
                    if ui.selectable_label(p.default_protocol == proto.id(), proto.label()).clicked() {
                        p.default_protocol = proto.id().into();
                    }
                }
            });
            ui.end_row();

            field(ui, "Confirm on close");
            ui.checkbox(&mut p.confirm_on_close, "Ask before closing a connected tab");
            ui.end_row();
            field(ui, "Confirm on disconnect");
            ui.checkbox(&mut p.confirm_on_disconnect, "Ask before disconnecting sessions");
            ui.end_row();
            field(ui, "Restore tabs");
            ui.checkbox(&mut p.restore_sessions, "Reopen last session's tabs and splits at launch");
            ui.end_row();
            field(ui, "Remote clipboard");
            ui.checkbox(&mut p.allow_remote_clipboard, "Let remote hosts set the clipboard (OSC 52, VNC)");
            ui.end_row();
            field(ui, "Multi-line paste");
            ui.checkbox(&mut p.confirm_multiline_paste, "Ask before pasting several lines into a terminal");
            ui.end_row();
            field(ui, "Updates");
            ui.checkbox(&mut p.check_for_updates, "Check for updates at launch");
            ui.end_row();
            field(ui, "Status bar");
            ui.checkbox(&mut p.show_status_bar, "Show status bar");
            ui.end_row();

            field(ui, "Sidebar width");
            ui.add(egui::DragValue::new(&mut p.sidebar_width).range(180..=500).suffix(" px"));
            ui.end_row();

            field(ui, "Local shell");
            ui.vertical(|ui| {
                ui.horizontal(|ui| {
                    ui.add(egui::TextEdit::singleline(&mut p.local_shell).hint_text(&self.default_shell).desired_width(300.0));
                    if widgets::button(ui, icon::FOLDER_OPEN, "Browse…").clicked()
                        && let Some(path) = rfd::FileDialog::new().set_title("Choose a shell").pick_file()
                    {
                        p.local_shell = path.to_string_lossy().into();
                    }
                });
                widgets::hint(ui, "Leave empty to use the system default");
            });
            ui.end_row();
        });
    }

    fn terminal(&mut self, ui: &mut Ui) {
        let p = &mut self.prefs;
        form(ui, "terminal-form", |ui| {
            field(ui, "Colour scheme");
            let cur = crate::terminal_themes::get(&p.terminal_theme).label;
            egui::ComboBox::from_id_salt("term-theme").selected_text(cur).width(220.0).show_ui(ui, |ui| {
                for th in crate::terminal_themes::THEMES {
                    if ui.selectable_label(p.terminal_theme == th.id, th.label).clicked() {
                        p.terminal_theme = th.id.into();
                    }
                }
            });
            ui.end_row();

            field(ui, "Font family");
            ui.add(egui::TextEdit::singleline(&mut p.default_font_family).hint_text("JetBrains Mono, Menlo, Monaco, Consolas, monospace").desired_width(300.0));
            ui.end_row();
            field(ui, "Font size");
            ui.add(egui::DragValue::new(&mut p.default_font_size).range(8..=36).suffix(" pt"));
            ui.end_row();
            field(ui, "Scrollback lines");
            ui.add(egui::DragValue::new(&mut p.default_scrollback).range(0..=100_000).speed(100));
            ui.end_row();

            field(ui, "Cursor style");
            let label = |c: &str| match c {
                "underline" => "Underline",
                "bar" => "Bar",
                _ => "Block",
            };
            egui::ComboBox::from_id_salt("cursor").selected_text(label(&p.default_cursor_style)).width(220.0).show_ui(ui, |ui| {
                for c in ["block", "underline", "bar"] {
                    if ui.selectable_label(p.default_cursor_style == c, label(c)).clicked() {
                        p.default_cursor_style = c.into();
                    }
                }
            });
            ui.end_row();
        });
        ui.add_space(6.0);
        widgets::hint(ui, "Font, size, scrollback and cursor are the defaults for new profiles; profiles can override them.");
    }

    // --- Security -------------------------------------------------------------------------

    fn security(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        let sec = &s.svc.secrets;
        let (init, unlocked) = (sec.vault.is_initialised(), sec.vault.is_unlocked());
        widgets::section(ui, "Credential storage");
        form(ui, "security-form", |ui| {
            field(ui, "OS keychain");
            if sec.keychain_ok {
                ui.label(RichText::new(format!("{}  Available", icon::CHECK_CIRCLE)).color(t.success));
            } else {
                ui.label(RichText::new(format!("{}  Unavailable", icon::WARNING_CIRCLE)).color(t.warning));
            }
            ui.end_row();
            field(ui, "Encrypted vault");
            let (text, color) = match (init, unlocked) {
                (false, _) => ("Not initialised", t.text_faint),
                (true, false) => ("Locked", t.text_dim),
                (true, true) => ("Unlocked", t.success),
            };
            ui.label(RichText::new(text).color(color));
            ui.end_row();
        });
        ui.add_space(4.0);
        widgets::hint(
            ui,
            if sec.keychain_ok {
                "Saved credentials go to the OS keychain first. The encrypted vault (AES-256-GCM, Argon2id key) is the fallback when the keychain refuses a write."
            } else {
                "The OS keychain isn't available, so saved credentials go to the encrypted vault (AES-256-GCM, Argon2id key). Unlock it to save or use credentials."
            },
        );
        ui.add_space(8.0);
        ui.horizontal(|ui| {
            if self.vault_job.is_some() {
                ui.spinner();
                ui.label("Working…");
            } else if !init {
                if widgets::primary(ui, icon::SHIELD_PLUS, "Initialise Secret Store").clicked() {
                    self.master = Some(MasterPrompt { new: true, pw: String::new(), confirm: String::new(), error: String::new() });
                }
            } else if !unlocked {
                if widgets::primary(ui, icon::LOCK_OPEN, "Unlock").clicked() {
                    self.master = Some(MasterPrompt { new: false, pw: String::new(), confirm: String::new(), error: String::new() });
                }
            } else if widgets::button(ui, icon::LOCK, "Lock").clicked() {
                s.svc.secrets.vault.lock();
                s.info("Secret store locked");
            }
        });
    }

    fn master_dialog(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(m) = &mut self.master else { return };
        let mut submit = false;
        let mut cancel = false;
        let r = egui::Modal::new(egui::Id::new("master-password")).show(ctx, |ui| {
            ui.set_width(380.0);
            let title = if m.new { "Create Master Password" } else { "Unlock Secret Store" };
            ui.label(RichText::new(format!("{}  {title}", icon::KEY)).font(theme::semibold(15.0)));
            ui.label(if m.new {
                "Choose a strong master password. This protects all saved credentials."
            } else {
                "Enter your master password to unlock the secret store."
            });
            ui.add_space(4.0);
            ui.label("Master password");
            let r = ui.add(egui::TextEdit::singleline(&mut m.pw).password(true).hint_text("Password…").desired_width(f32::INFINITY));
            if !r.has_focus() && m.pw.is_empty() && m.confirm.is_empty() {
                r.request_focus();
            }
            let mut enter = r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter));
            if m.new {
                strength_meter(ui, &m.pw);
                ui.label("Confirm password");
                let r = ui.add(egui::TextEdit::singleline(&mut m.confirm).password(true).hint_text("Confirm…").desired_width(f32::INFINITY));
                enter |= r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter));
            }
            if !m.error.is_empty() {
                ui.label(RichText::new(&m.error).color(Tokens::get(ui.ctx()).danger).size(12.0));
            }
            ui.add_space(6.0);
            ui.horizontal(|ui| {
                submit = widgets::primary(ui, icon::CHECK, if m.new { "Create" } else { "Unlock" }).clicked() || enter;
                cancel = ui.button("Cancel").clicked();
            });
        });
        if submit {
            if m.pw.is_empty() {
                m.error = "Enter a password.".into();
            } else if m.new && m.pw != m.confirm {
                m.error = "Passwords do not match.".into();
            } else {
                let (new, pw) = (m.new, std::mem::take(&mut m.pw));
                let svc = s.svc.clone();
                self.vault_job = Some(Job::spawn(ctx, move || {
                    let v = &svc.secrets.vault;
                    if new {
                        v.initialise(&pw).map(|_| "Secret store created").map_err(|e| format!("Failed to initialise: {e}"))
                    } else {
                        v.unlock(&pw).map(|_| "Secret store unlocked").map_err(|e| format!("Unlock failed: {e}"))
                    }
                }));
                cancel = true;
            }
        }
        if cancel || r.should_close() {
            self.master = None;
        }
    }

    // --- Known hosts ----------------------------------------------------------------------

    fn known_hosts(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        ui.horizontal(|ui| {
            if widgets::button(ui, icon::DOWNLOAD_SIMPLE, "Import ~/.ssh/known_hosts").clicked() {
                match s.svc.import_known_hosts(None) {
                    Ok((n, ca, skipped)) => {
                        let extra = if skipped > 0 { format!(" ({skipped} hashed or unsupported line(s) skipped)") } else { String::new() };
                        s.success(format!("Imported {n} host key(s) and {ca} certificate authorit{}{extra}", if ca == 1 { "y" } else { "ies" }));
                        self.known = s.svc.known_hosts();
                    }
                    Err(e) => s.error(format!("Import failed: {e}")),
                }
            }
            if widgets::button(ui, icon::UPLOAD_SIMPLE, "Export…").clicked()
                && let Some(path) = rfd::FileDialog::new().set_file_name("known_hosts").save_file()
            {
                match s.svc.export_known_hosts(&path) {
                    Ok(n) => s.success(format!("Wrote {n} line(s) to {}", path.display())),
                    Err(e) => s.error(format!("Export failed: {e}")),
                }
            }
            if widgets::button(ui, icon::SEAL_CHECK, "Trust a CA…").on_hover_text("Trust SSH host certificates signed by a certificate authority").clicked() {
                self.new_ca = Some(Default::default());
            }
        });
        let (mut done, mut added) = (false, false);
        if let Some((pattern, key)) = &mut self.new_ca {
            egui::Frame::group(ui.style()).show(ui, |ui| {
                ui.label(RichText::new("Trust an SSH certificate authority").font(theme::semibold(13.0)));
                widgets::hint(ui, "Servers whose host certificate this CA signed are trusted without a first-use prompt. Patterns use OpenSSH syntax: *.example.com, [host]:2222, !exclude.");
                ui.add(egui::TextEdit::singleline(pattern).hint_text("*.example.com").desired_width(320.0));
                ui.add(egui::TextEdit::multiline(key).hint_text("ssh-ed25519 AAAA… (the CA's public key)").desired_rows(2).desired_width(f32::INFINITY));
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::CHECK, "Trust").clicked() {
                        match s.svc.add_cert_authority(pattern, key) {
                            Ok(()) => {
                                s.success("Certificate authority trusted");
                                (done, added) = (true, true);
                            }
                            Err(e) => s.error(e.to_string()),
                        }
                    } else if ui.button("Cancel").clicked() {
                        done = true;
                    }
                });
            });
        }
        if done {
            self.new_ca = None;
        }
        if added {
            self.known = s.svc.known_hosts();
        }
        ui.add_space(6.0);
        if self.known.is_empty() {
            widgets::empty(ui, icon::FINGERPRINT, "No known hosts recorded", "Host keys are recorded the first time you connect over SSH.");
            return;
        }
        widgets::count(ui, self.known.len(), "known host(s)", false);
        let when = |d: &nexus_core::models::Time| {
            use chrono::Datelike;
            if d.year() <= 1 { "—".to_string() } else { d.with_timezone(&chrono::Local).format("%Y-%m-%d %H:%M").to_string() }
        };
        let mut remove = None;
        egui::ScrollArea::both().auto_shrink([false, false]).show(ui, |ui| {
            egui::Grid::new("known-hosts").num_columns(7).striped(true).spacing(vec2(14.0, 6.0)).show(ui, |ui| {
                for h in ["Host", "Port", "Key Type", "Fingerprint", "First Seen", "Last Seen", ""] {
                    ui.label(RichText::new(h).font(theme::semibold(11.5)).color(t.text_faint));
                }
                ui.end_row();
                for kh in &self.known {
                    ui.label(&kh.host);
                    ui.label(if kh.port == 0 { "—".to_string() } else { kh.port.to_string() });
                    ui.label(RichText::new(&kh.key_type).font(theme::mono(11.5)));
                    let short: String = kh.fingerprint.chars().take(24).collect();
                    let fp = ui.add(egui::Label::new(RichText::new(format!("{short}…")).font(theme::mono(11.5))).sense(egui::Sense::click()));
                    if fp.on_hover_text(format!("{}\nClick to copy", kh.fingerprint)).clicked() {
                        ui.ctx().copy_text(kh.fingerprint.clone());
                    }
                    ui.label(RichText::new(when(&kh.first_seen)).color(t.text_dim));
                    ui.label(RichText::new(when(&kh.last_seen)).color(t.text_dim));
                    if ui.button(RichText::new(format!("{}  Remove", icon::TRASH)).color(t.danger)).clicked() {
                        remove = Some(kh.id.clone());
                    }
                    ui.end_row();
                }
            });
        });
        if let Some(id) = remove {
            match s.svc.delete_known_host(&id) {
                Ok(()) => {
                    self.known.retain(|h| h.id != id);
                    s.success("Known host removed");
                }
                Err(e) => s.error(format!("Failed to remove: {e}")),
            }
        }
    }

    // --- Help -----------------------------------------------------------------------------

    fn help(&mut self, ui: &mut Ui) {
        let t = Tokens::get(ui.ctx());
        ui.horizontal_top(|ui| {
            ui.allocate_ui_with_layout(vec2(170.0, ui.available_height()), egui::Layout::top_down_justified(egui::Align::Min), |ui| {
                ui.set_width(170.0);
                for (i, (title, _)) in HELP.iter().enumerate() {
                    let on = self.help_topic == i;
                    if ui.selectable_label(on, RichText::new(*title).color(if on { t.text } else { t.text_dim })).clicked() {
                        self.help_topic = i;
                    }
                }
            });
            ui.separator();
            let (title, body) = HELP[self.help_topic.min(HELP.len() - 1)];
            ui.vertical(|ui| {
                egui::ScrollArea::vertical().id_salt(("help", self.help_topic)).auto_shrink([false, false]).show(ui, |ui| {
                    ui.label(RichText::new(title).font(theme::semibold(17.0)));
                    ui.add_space(4.0);
                    render_help(ui, body);
                });
            });
        });
    }

    // --- About ----------------------------------------------------------------------------

    fn about(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        ui.add_space(8.0);
        ui.label(RichText::new("Nexus Terminal").font(theme::semibold(22.0)));
        ui.label(RichText::new(format!("Version {}", nexus_core::service::VERSION)).color(t.text_dim));
        ui.label(RichText::new("Developed by Andy Dixon - dixon.cx/nexus").color(t.text_faint));
        ui.add_space(8.0);
        for f in [
            "SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminals",
            "AES-256-GCM encrypted credential vault",
            "Platform-native keychain integration",
            "SFTP file browser and transfer",
            "Session recording and playback",
        ] {
            ui.label(RichText::new(format!("{}  {f}", icon::CHECK)).color(t.text_dim));
        }
        ui.add_space(12.0);
        ui.horizontal(|ui| {
            if self.update_job.is_some() {
                ui.spinner();
                ui.label("Checking…");
            } else if widgets::primary(ui, icon::ARROW_CLOCKWISE, "Check for Updates").clicked() {
                let svc = s.svc.clone();
                self.update = None;
                self.update_job = Some(Job::spawn(ui.ctx(), move || svc.check_for_updates().map_err(|e| e.to_string())));
            }
            if widgets::button(ui, icon::DOWNLOAD_SIMPLE, "Import ~/.ssh/config").clicked() {
                match s.svc.import_ssh_config(None, "SSH Config") {
                    Ok(n) => {
                        s.success(ssh_import_summary(n));
                        s.request(Request::ReloadProfiles);
                    }
                    Err(e) => s.error(format!("SSH config import failed: {e}")),
                }
            }
        });
        ui.add_space(8.0);
        match &self.update {
            None => {}
            Some(Err(e)) => {
                ui.label(RichText::new(format!("Update check failed: {e}")).color(t.danger));
            }
            Some(Ok(info)) => {
                widgets::kv(ui, "update-kv", &[("Current", info.current_version.clone()), ("Latest", info.latest_version.clone())]);
                if info.available {
                    let name = if info.release_name.is_empty() { &info.latest_version } else { &info.release_name };
                    ui.label(RichText::new(format!("Update available: {name}")).color(t.accent));
                    if !info.release_notes.trim().is_empty() {
                        egui::Frame::new().fill(t.field).corner_radius(t.radius).inner_margin(8).show(ui, |ui| {
                            egui::ScrollArea::vertical().max_height(150.0).show(ui, |ui| {
                                ui.set_width(ui.available_width());
                                ui.label(RichText::new(info.release_notes.trim()).color(t.text_dim));
                            });
                        });
                    }
                    if widgets::is_web_url(&info.release_url)
                        && widgets::button(ui, icon::ARROW_SQUARE_OUT, "View release").clicked()
                        && !widgets::open_web_url(ui.ctx(), &info.release_url)
                    {
                        s.error("The release link is not a web address");
                    }
                } else {
                    ui.label(RichText::new("You are running the latest version.").color(t.success));
                }
            }
        }
    }
}

/// Two-column label/control grid.
fn form(ui: &mut Ui, id: &str, body: impl FnOnce(&mut Ui)) {
    egui::Grid::new(id).num_columns(2).spacing(vec2(18.0, 10.0)).min_col_width(150.0).show(ui, body);
}

fn field(ui: &mut Ui, label: &str) {
    let t = Tokens::get(ui.ctx());
    ui.label(RichText::new(label).color(t.text_dim));
}

/// Draws help text: `## heading`, `- bullet`, `1. step`, `> key :: description` (shortcut row),
/// blank-line separated paragraphs, inline `**bold**` and `` `code` ``. `{Mod+K}` becomes the
/// platform's shortcut ("Ctrl+K" or "⌘K").
fn render_help(ui: &mut Ui, text: &str) {
    let t = Tokens::get(ui.ctx());
    for line in text.lines().map(str::trim) {
        if line.is_empty() {
            ui.add_space(4.0);
        } else if let Some(h) = line.strip_prefix("## ") {
            ui.add_space(6.0);
            ui.label(RichText::new(h).font(theme::semibold(13.5)).color(t.text));
        } else if let Some(row) = line.strip_prefix("> ") {
            let (k, d) = row.split_once(" :: ").unwrap_or((row, ""));
            ui.horizontal(|ui| {
                ui.add_sized([150.0, 18.0], egui::Label::new(inline(&format!("`{}`", expand_keys(k)), &t)));
                ui.label(inline(d, &t));
            });
        } else if let Some(b) = line.strip_prefix("- ") {
            ui.horizontal_top(|ui| {
                ui.add_space(8.0);
                ui.label(RichText::new("•").color(t.text_faint));
                ui.add(egui::Label::new(inline(b, &t)).wrap());
            });
        } else if line.len() > 2 && line.as_bytes()[0].is_ascii_digit() && line[1..].starts_with(". ") {
            ui.horizontal_top(|ui| {
                ui.add_space(8.0);
                ui.label(RichText::new(&line[..2]).color(t.text_faint));
                ui.add(egui::Label::new(inline(&line[3..], &t)).wrap());
            });
        } else {
            ui.add(egui::Label::new(inline(line, &t)).wrap());
        }
    }
}

fn expand_keys(s: &str) -> String {
    let mut out = String::new();
    let mut rest = s;
    while let Some(i) = rest.find('{') {
        let Some(j) = rest[i..].find('}') else { break };
        out.push_str(&rest[..i]);
        out.push_str(&keys(&rest[i + 1..i + j]));
        rest = &rest[i + j + 1..];
    }
    out + rest
}

/// `**bold**` and `` `code` `` spans as a wrapped layout job; `{Mod+X}` keys render as code.
fn inline(s: &str, t: &Tokens) -> LayoutJob {
    let s = s.replace('{', "`{").replace('}', "}`");
    let s = expand_keys(&s);
    let mut job = LayoutJob::default();
    let (mut bold, mut code) = (false, false);
    let mut buf = String::new();
    let flush = |job: &mut LayoutJob, buf: &mut String, bold: bool, code: bool| {
        if buf.is_empty() {
            return;
        }
        let fmt = if code {
            TextFormat { font_id: theme::mono(12.0), color: t.text, background: t.field, ..Default::default() }
        } else if bold {
            TextFormat { font_id: theme::semibold(13.0), color: t.text, ..Default::default() }
        } else {
            TextFormat { font_id: egui::FontId::proportional(13.0), color: t.text_dim, ..Default::default() }
        };
        job.append(&std::mem::take(buf), 0.0, fmt);
    };
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '`' {
            flush(&mut job, &mut buf, bold, code);
            code = !code;
        } else if c == '*' && !code && chars.peek() == Some(&'*') {
            chars.next();
            flush(&mut job, &mut buf, bold, code);
            bold = !bold;
        } else {
            buf.push(c);
        }
    }
    flush(&mut job, &mut buf, bold, code);
    job
}

/// Help topics (the web build's, minus Leviathan, plus Serial).
const HELP: &[(&str, &str)] = &[
    (
        "Getting Started",
        "Welcome to **Nexus Terminal** — a desktop application for SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminal sessions.
## Creating Your First Connection
1. Click the **+** button at the top of the sidebar, or press {Mod+N}.
2. Choose a protocol (SSH, Telnet, RAW, Serial, VNC, IRC or GitHub).
3. Fill in the hostname, port, and any credentials required.
4. Optionally assign the connection to a **group** to keep your sidebar organised.
5. Click **Save**, then click the entry in the sidebar to connect.
## Quick Connect
Need to connect quickly without saving a profile? Press {Mod+Shift+Q} to open the quick connect dialog. Choose a protocol, enter the host and port, and you're connected immediately.
## Managing Connections
Right-click any connection in the sidebar to **edit**, **duplicate**, **favourite** or **delete** it. To move connections to another group, Ctrl/Cmd-click to select them and choose **Move Group** in the bulk-action bar. Use the search box to filter by name, host, tag or protocol.",
    ),
    (
        "SSH Connections",
        "SSH (Secure Shell) is the most common way to connect to remote servers. Nexus Terminal supports:
- **Password authentication** — enter your password when prompted, or save it in the OS keychain or encrypted vault.
- **Public key authentication** — point to your private key file (e.g. `~/.ssh/id_ed25519`). If the key has a passphrase, you will be prompted.
- **Keyboard-interactive** — for servers that use challenge-response (e.g. two-factor prompts).
- **SSH agent** — tick \"Use SSH Agent\" to authenticate via your system's SSH agent (e.g. `ssh-agent` or 1Password).
## TOTP / 2FA Auto-Fill
If your server requires a time-based one-time password (TOTP) during login, store the TOTP secret in the profile editor under the **TOTP / 2FA** section. When connecting, Nexus Terminal detects 2FA prompts (such as \"Verification code\" or \"OTP\") and automatically generates and submits a 6-digit code. Use the **Test** button to verify your secret before connecting.
## Jump Hosts (Bastions)
If you need to reach a server through an intermediate host, add one or more jump hosts in the connection editor. Nexus Terminal tunnels through each hop automatically.
## Port Forwarding
You can set up **local** and **remote** port forwards in the connection editor. These start automatically when the session connects.
## Proxy Support
Open the **Proxy** section in the profile editor to connect through a **SOCKS5** or **HTTP CONNECT** proxy, with optional username/password authentication. The proxy is used to reach the SSH server (or the first jump host if jump hosts are configured).
## SFTP File Browser
Once connected to an SSH server, click the folder icon on the tab to open the SFTP file browser. You can browse directories, upload, download, rename, and delete files from a side panel.
## SSH Agent Forwarding
Tick \"Agent Forwarding\" in the SSH settings to forward your local SSH agent to the remote server, so you can use your local keys when connecting onward from that server.
## Auto-Login Scripts
Define expect-style login scripts to automate interactions after connecting. In the **Login Script** section of the profile editor, add a sequence of **Wait for** (match text from the server) and **Send** (send text + newline) steps. For example:
1. Wait for: `login:`
2. Send: `admin`
3. Wait for: `Password:`
4. Send: `mysecretpassword`

Each wait step has a configurable timeout (default 10 seconds). Steps execute in order after the connection is established.
## Auto-Reconnect
Enable **Auto-Reconnect** in the Advanced section to reconnect automatically when the connection drops unexpectedly. Reconnection uses exponential backoff (1s, 2s, 4s, 8s… up to 30s). You can configure the maximum number of attempts (default 5).",
    ),
    (
        "Telnet Connections",
        "Telnet is an older, unencrypted protocol still used for network equipment, legacy systems, and some fun internet services.
- **Option negotiation** — Nexus Terminal handles standard Telnet options (echo, terminal type, window size) automatically.
- **Character encoding** — choose UTF-8, ASCII, or Latin-1 for the connection.
- **Local echo** — enable this if the remote host does not echo back what you type.
- **Line mode** — send data line-by-line (press Enter to send) rather than character-by-character.
- **Auto-login scripts** — the same expect-style send/wait sequences as SSH.
- **Auto-reconnect** — reconnect automatically with exponential backoff when the connection drops.

**Note:** Telnet sends everything in plain text. Avoid using it for anything sensitive over an untrusted network.",
    ),
    (
        "RAW TCP Connections",
        "RAW TCP lets you connect to any TCP port and exchange data directly — useful for debugging network services, testing APIs, or working with custom protocols.
- **Text view** — shows incoming data as readable text.
- **Hex view** — shows raw bytes in a hex dump format.
- **Split view** — text and hex side by side.
- **Payload presets** — save frequently-used commands or data packets for quick sending.
- **Line endings** — choose CR, LF, or CRLF to match the remote service.
- **Auto-reconnect** — reconnect automatically if the connection drops.",
    ),
    (
        "Serial Connections",
        "Serial profiles open a terminal on a local serial port — USB-serial adapters, console cables, development boards and network gear.
- **Device** — pick the port from the list (refresh after plugging in an adapter), e.g. `/dev/ttyUSB0`, `/dev/cu.usbserial-…` or `COM3`.
- **Line settings** — baud rate (default 115200), data bits, parity, stop bits and flow control (none, XON/XOFF, RTS/CTS).
- **Behaviour** — line ending, local echo, encoding, and whether DTR/RTS are raised on connect.
- **Tab controls** — right-click the tab to send a break or toggle DTR/RTS.
- **Auto-reconnect** — unplugging the adapter disconnects the session; it reopens the port when it comes back.

On Linux your user usually needs to be in the `dialout` (or `uucp`) group. On macOS use the `/dev/cu.*` device. A \"busy\" error means another program has the port open.",
    ),
    (
        "VNC Connections",
        "VNC profiles open a remote desktop viewer inside the Nexus Terminal workspace.
- **Password handling** — ask each time, use the session only, or save the password in the secret store.
- **View-only mode** — observe a desktop without sending mouse or keyboard input.
- **Scale-to-fit** — resize the remote desktop to fit the available workspace.
- **Shared session** — request a shared VNC session where the server supports it.
- **Quality** — tune image quality for slower links.
- **SSH tunnel** — carry VNC traffic through an SSH connection using password, public key, or SSH agent authentication.

Prefer **SSH tunnel** for VNC outside trusted networks. Direct VNC is supported for isolated or trusted networks, but many classic VNC deployments do not provide strong transport encryption.",
    ),
    (
        "GitHub Connections",
        "The GitHub connection type lets you browse your repositories without leaving Nexus Terminal.
## Setting Up
1. Create a new connection and choose **GitHub** as the protocol.
2. Enter your **Personal Access Token** (create one at GitHub → Settings → Developer settings → Personal access tokens). The token needs the `repo` scope.
3. Save and connect.
## What You'll See
Your repositories are listed grouped by organisation or owner, sorted by last commit date (newest first). The list refreshes automatically every few minutes.",
    ),
    (
        "Sessions & Tabs",
        "## Tab Management
- **Rename** — right-click a tab and choose \"Rename\".
- **Reorder** — drag tabs left or right to rearrange.
- **Pin** — right-click and pin a tab so it stays at the left of the bar.
- **Scroll** — use the mouse wheel or trackpad over the tab strip when many tabs are open.
- **Hover hints** — hover a tab to see its full name and connected-since time where available.
- **Close** — click the **×** on the tab, or press {Mod+W}.
## Split Panes
You can split the terminal into multiple panes:
- {Mod+Shift+D} — split vertically (side by side).
- {Mod+Shift+E} — split horizontally (top and bottom).

Drag the divider between panes to resize them.
## Broadcast Input
Press {Mod+Shift+B} to toggle **broadcast mode**. When active, everything you type is sent to **all connected sessions** simultaneously — perfect for running the same command across multiple servers.
## Session Recording
Press {Mod+Shift+R} to start recording a session. A red dot appears on the tab. Press the same shortcut again to stop. Browse and replay recordings from the Recordings window.
## Right-Click Context Menu
Right-click in the terminal for:
- **Copy** — copy selected text to the clipboard
- **Paste** — paste from the clipboard into the terminal
- **Select All** — select all terminal content
- **Find** — open the terminal search overlay
- **Send Snippet** — quickly send a saved command from the snippet library
- **Clear** / **Reset** — clear the terminal buffer",
    ),
    (
        "Power Features",
        "## Snippet Library
Press {Mod+Shift+S} to open the **snippet library**. Save frequently-used commands with a name, description, and optional folder. Click **Send to Session** to inject a command into the active terminal. You can also reach snippets from the right-click context menu.
## Terminal Search
Press {Mod+F} to open the search overlay on the active terminal:
- `Aa` — toggle case-sensitive matching
- `.*` — toggle regular expression mode
- A match counter showing current/total matches
- Move between matches with the arrows or Enter / Shift+Enter
## Quick Connect
Press {Mod+Shift+Q} to open a lightweight connect dialog and connect without creating a saved profile.
## Auto-Login Scripts
For SSH and Telnet connections you can define expect-style login scripts — a sequence of \"wait for text\" and \"send text\" steps that run automatically after connecting. Configure them in the **Login Script** section of the profile editor.
## Auto-Reconnect
Enable auto-reconnect in a profile's settings to reconnect automatically when a connection drops, using exponential backoff with a configurable maximum number of attempts.",
    ),
    (
        "Security & Credentials",
        "## How Your Passwords Are Stored
Nexus Terminal uses a **two-tier** approach to keep your credentials safe:
1. **OS keychain** (preferred) — your operating system's credential store (macOS Keychain, Windows Credential Manager, or Linux Secret Service). If available, it is used automatically.
2. **Encrypted vault** (fallback) — if the OS keychain is not available, Nexus Terminal keeps a local vault encrypted with AES-256-GCM. You set a master password to unlock it.
## Encryption Details
The vault uses **Argon2id** to derive the encryption key from your master password (128 MiB memory, making brute-force attacks very expensive). Each secret is individually encrypted and never stored in plain text.
## TOTP / 2FA Secrets
TOTP secrets for SSH two-factor authentication are stored alongside your other credentials — in the OS keychain or the encrypted vault. They are never saved in the profile database in plain text.
## Known Hosts
When you connect to an SSH server for the first time, Nexus Terminal records the server's fingerprint. If the fingerprint changes on a later connection, you'll be warned — this protects against man-in-the-middle attacks.

You can review and remove known hosts in **Settings → Known Hosts**, import or export them in OpenSSH known_hosts format, and trust SSH certificate authorities for host patterns.
## Encrypted Export
When exporting profiles, encrypted export (a `.nexus` file) protects the file with a password, using the same AES-256-GCM encryption as the vault.",
    ),
    (
        "Keyboard Shortcuts",
        "On macOS, use ⌘ (Cmd). On Windows and Linux, use Ctrl.
> {Mod+K} :: Open the command palette
> {Mod+N} :: New connection
> {Mod+W} :: Close the active tab
> {Mod+Tab} :: Next tab
> {Mod+Shift+Tab} :: Previous tab
> {Mod+1} – {Mod+9} :: Switch to tab by number
> {Mod+,} :: Open Settings
> {Mod+F} :: Search in terminal
> {Mod+Shift+B} :: Toggle broadcast input mode
> {Mod+Shift+D} :: Split pane vertically
> {Mod+Shift+E} :: Split pane horizontally
> {Mod+Shift+Q} :: Quick connect (no saved profile)
> {Mod+Shift+R} :: Toggle session recording
> {Mod+Shift+S} :: Open snippet library
> F11 :: Toggle fullscreen",
    ),
    (
        "Import & Export",
        "## Exporting Profiles
Use **Export (Encrypted)** from the sidebar menu or command palette. Choose which profiles to include and set a password to encrypt the file. Exported files use the `.nexus` extension. Plain **JSON** export is also available, optionally including secrets.
## Importing Profiles
Use **Import (Encrypted)**, select a `.nexus` file and enter the password used when exporting. Choose whether existing profiles are skipped, overwritten or duplicated. Imported profiles appear in your sidebar immediately.
## Importing from OpenSSH
If you have an existing `~/.ssh/config` file, go to **Settings → About** and click **Import ~/.ssh/config**. Nexus Terminal creates profiles from your SSH Host blocks, including hostnames, usernames, ports, identity files, and jump hosts.",
    ),
    (
        "Troubleshooting",
        "## I can't connect to my server
- Double-check the **hostname** and **port** — a common mistake is using port 22 for Telnet (which is usually 23) or vice versa.
- Make sure the server is reachable from your machine. Try `ping hostname` from another terminal.
- If using SSH keys, check that the key file path is correct and that the file permissions are not too open (should be `600` or `400`).
- If you're behind a firewall or VPN, ensure the relevant port is allowed through.
## SSH connection fails through a proxy
- Check the proxy host, port, and type (SOCKS5 or HTTP CONNECT) in the profile's **Proxy** section.
- If the proxy requires authentication, make sure the username and password are filled in.
- SOCKS5 defaults to port 1080; HTTP CONNECT defaults to port 8080.
- Some proxies block SSH traffic — check with your network administrator.
## TOTP/2FA code is rejected
- Ensure the TOTP secret is correct (base32-encoded, as shown during authenticator app setup).
- Check that your system clock is accurate — TOTP codes are only valid for 30-second windows.
- Use the **Test** button in the profile's TOTP section to verify the secret generates valid codes.
## Login script times out
- Each \"wait\" step has a configurable timeout (default 10 seconds). Increase it if the server takes longer.
- Check that the wait pattern matches exactly — it's a plain text substring match, not a regular expression.
- Make sure the server actually sends the expected text; prompts can differ by locale or configuration.
## Auto-reconnect keeps failing
- Auto-reconnect only works after a successful initial connection.
- If the server is genuinely unreachable, attempts stop after the configured maximum (default 5).
- Check the reconnection settings in the profile's Advanced section.
## Serial port won't open
- Check the device path and that the adapter is plugged in; refresh the device list in the editor.
- On Linux, add your user to the `dialout` (or `uucp`) group and log in again.
- Close any other program (screen, minicom, an IDE monitor) that has the port open.
## VNC will not connect
- Check the VNC host and port. The common port is `5900` plus the display number.
- If a password is required, check whether the profile is set to saved, session-only, or ask-every-time mode.
- If using an SSH tunnel, verify the tunnel host, username, authentication method, private key, and host-key policy.
- If using direct VNC, test from a trusted network and prefer SSH tunnelling on untrusted networks.
## The terminal looks garbled or the wrong size
- Try resizing the window — the terminal re-fits automatically.
- If you're using split panes, drag the divider to give the terminal more space.
- Some older servers don't support modern terminal features. Try a different terminal type or colour scheme.
## \"Host key verification failed\"
This means the server's fingerprint has changed since your last connection. This can happen if the server was reinstalled, but it could also indicate a security threat. If you're certain it's safe, go to **Settings → Known Hosts**, find the entry, and remove it. You'll be asked to accept the new key on your next connection.
## I forgot my vault master password
The vault is designed so that without the master password the stored credentials cannot be recovered. You would need to delete the vault and set up a new one. Your connection profiles (without saved passwords) remain intact.
## SFTP panel doesn't open
SFTP is only available for **SSH** connections. Make sure you are connected to an SSH session first, then click the folder icon on the tab.
## Export/import isn't working
Use the same password to import that was used to export. Encrypted `.nexus` files cannot be opened with the wrong password.",
    ),
    (
        "Fun Things to Try",
        "Nexus Terminal isn't just for work! Here are some entertaining things you can connect to:
## Star Wars in ASCII Art
Create a **Telnet** connection to `towel.blinkenlights.nl` on port **23**. Sit back and enjoy the original Star Wars film retold entirely in ASCII art.
## MapSCII — World Map in Your Terminal
Connect via **SSH** to `mapscii.me` on port **22**. Use the arrow keys to pan and `a`/`z` to zoom in and out of a fully interactive world map rendered in text.
## Telnet BBS Systems
Connect via **Telnet** to `particlesbbs.dyndns.org` on port **23** for a retro bulletin board system, complete with door games, message boards, and ASCII art.
## Fortune Cookies
Connect via **Telnet** to `djxmmx.net` on port **17** (the Quote of the Day protocol). You'll receive a random quote each time you connect — great for testing RAW TCP connections too.
## Test Your SSH Setup
Connect via **SSH** to `ssh.github.com` on port **443** (or `github.com` on port **22**) with your GitHub username. GitHub greets you by name if your key is set up correctly.
## RAW TCP Experiments
Create a **RAW TCP** connection to any HTTP server on port **80**, then type `GET / HTTP/1.0` followed by two blank lines to see the raw HTTP response.
## Broadcast Mode Fun
Open two or more sessions and press {Mod+Shift+B} to turn on broadcast mode. Everything you type goes to all sessions at once — try running `hostname` and watch them all respond!",
    ),
];

pub fn ssh_import_summary((n, skipped): (usize, usize)) -> String {
    match skipped {
        0 => format!("Imported {n} host(s) from ~/.ssh/config"),
        k => format!("Imported {n} host(s) from ~/.ssh/config; skipped {k} that couldn't be used as-is"),
    }
}
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strength_matches_web_formula() {
        assert_eq!(strength(""), (0, ""));
        // "abc": 9 + 10 + 20 = 39
        assert_eq!(strength("abc"), (39, "Fair"));
        // "aaaa": 12 + 10 + floor(1/4*20)=5 → 27
        assert_eq!(strength("aaaa"), (27, "Fair"));
        assert_eq!(strength("a"), (3 + 10 + 20, "Fair"));
        assert_eq!(strength("Tr0ub4dor&3xyz!"), (98, "Strong"));
        assert_eq!(strength("password1").1, "Good");
    }

    #[test]
    fn keys_expand() {
        assert_eq!(expand_keys("x {Mod+N} y"), format!("x {} y", keys("Mod+N")));
        assert!(HELP.iter().all(|(t, b)| !t.contains("Leviathan") && !b.contains("Leviathan") && !b.contains("drag connections")));
    }
}
