// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Window chrome: custom title bar, status bar, command palette, credential and host-key
//! prompts, the main window's modal dialogs, toasts and resize edges.

use crate::app::{App, Dialog, InputThen, Prompt};
use crate::state::{Request, SettingsTab, ToastKind, keys};
use crate::theme::{self, ThemeKind, Tokens};
use crate::views::View;
use crate::widgets;
use egui::{Align2, Color32, Key, Rect, RichText, Sense, Stroke, Ui, ViewportCommand, pos2, vec2};
use egui_phosphor::regular as icon;
use nexus_core::models::{Protocol, SessionState};
use nexus_core::sessions::HostKeyDecision;

pub const LOGO_PNG: &[u8] = include_bytes!("../assets/icon/icon-256.png");

/// The Nexus mark, uploaded once.
pub fn logo(ctx: &egui::Context) -> egui::TextureHandle {
    let id = egui::Id::new("nexus-logo");
    if let Some(t) = ctx.data(|d| d.get_temp::<egui::TextureHandle>(id)) {
        return t;
    }
    let img = image::load_from_memory(LOGO_PNG).map(|i| i.to_rgba8()).unwrap_or_default();
    let color = egui::ColorImage::from_rgba_unmultiplied([img.width() as usize, img.height() as usize], img.as_raw());
    let opts = egui::TextureOptions { mipmap_mode: Some(egui::TextureFilter::Linear), ..egui::TextureOptions::LINEAR };
    let tex = ctx.load_texture("nexus-logo", color, opts);
    ctx.data_mut(|d| d.insert_temp(id, tex.clone()));
    tex
}

fn brand(ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    ui.add(egui::Image::new(&logo(ui.ctx())).fit_to_exact_size(vec2(22.0, 22.0)));
    ui.label(RichText::new("Nexus").font(theme::semibold(13.5)).color(t.accent));
    ui.add_space(-6.0);
    ui.label(RichText::new("Terminal").font(theme::semibold(13.5)));
}

fn item(ui: &mut Ui, glyph: &str, label: &str, shortcut: &str) -> bool {
    let clicked = ui.add(egui::Button::new(format!("{glyph}  {label}")).shortcut_text(keys(shortcut)).min_size(vec2(230.0, 0.0))).clicked();
    if clicked {
        ui.close();
    }
    clicked
}

pub fn title_bar(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    let bar = ui.max_rect();
    let drag = ui.interact(bar, ui.id().with("titlebar"), Sense::click_and_drag());
    if drag.drag_started() {
        ui.ctx().send_viewport_cmd(ViewportCommand::StartDrag);
    }
    if drag.double_clicked() {
        let max = ui.ctx().input(|i| i.viewport().maximized.unwrap_or(false));
        ui.ctx().send_viewport_cmd(ViewportCommand::Maximized(!max));
    }
    let mut menus_right = bar.left();
    ui.horizontal_centered(|ui| {
        ui.add_space(10.0);
        brand(ui);
        ui.add_space(8.0);
        egui::MenuBar::new().ui(ui, |ui| {
            ui.menu_button("File", |ui| {
                if item(ui, icon::PLUS_CIRCLE, "New connection…", "Mod+N") {
                    app.s.request(Request::NewProfile { protocol: None, folder: String::new() });
                }
                if item(ui, icon::LIGHTNING, "Quick connect…", "Mod+Shift+Q") {
                    app.quick.open();
                }
                if item(ui, icon::TERMINAL_WINDOW, "Local terminal", "") {
                    app.connect_local();
                }
                ui.separator();
                if item(ui, icon::DOWNLOAD_SIMPLE, "Import profiles…", "") {
                    app.s.request(Request::ImportEncrypted);
                }
                if item(ui, icon::UPLOAD_SIMPLE, "Export profiles…", "") {
                    app.s.request(Request::ExportEncrypted(vec![]));
                }
                if item(ui, icon::FILE_ARROW_DOWN, "Import ~/.ssh/config", "") {
                    match app.s.svc.import_ssh_config(None, "SSH Config") {
                        Ok(n) => {
                            app.s.success(crate::settings::ssh_import_summary(n));
                            app.s.reload_profiles();
                        }
                        Err(e) => app.s.error(format!("Import failed: {e}")),
                    }
                }
                ui.separator();
                if item(ui, icon::GEAR, "Settings", "Mod+,") {
                    app.settings.open(SettingsTab::General);
                }
                if item(ui, icon::SIGN_OUT, "Quit", "") {
                    ui.ctx().send_viewport_cmd(ViewportCommand::Close);
                }
            });
            ui.menu_button("Session", |ui| {
                let focused = app.s.focused_session();
                let ssh = focused.as_deref().and_then(|f| app.s.pane(f)).is_some_and(|p| p.info.protocol == Protocol::Ssh);
                if item(ui, icon::MAGNIFYING_GLASS, "Find in terminal", "Mod+F") {
                    app.open_find();
                }
                if item(ui, icon::SQUARE_SPLIT_HORIZONTAL, "Split right", "Mod+Shift+D") {
                    crate::splits::split_focused(app, crate::splits::Dir::Right);
                }
                if item(ui, icon::SQUARE_SPLIT_VERTICAL, "Split down", "Mod+Shift+E") {
                    crate::splits::split_focused(app, crate::splits::Dir::Down);
                }
                ui.separator();
                if item(ui, icon::BROADCAST, if app.s.broadcast { "Stop broadcast" } else { "Broadcast input" }, "Mod+Shift+B") {
                    app.toggle_broadcast();
                }
                if item(ui, icon::USERS_THREE, "Broadcast targets…", "") {
                    app.pick_broadcast_targets();
                }
                if item(ui, icon::RECORD, "Toggle recording", "Mod+Shift+R")
                    && let Some(f) = focused.clone()
                {
                    app.toggle_recording(&f);
                }
                if item(ui, icon::CODE, "Snippets", "Mod+Shift+S") {
                    app.snippets.open();
                }
                if item(ui, icon::LIGHTNING_A, "Trigger rules", "") {
                    app.triggers.open();
                }
                if item(ui, icon::FILM_STRIP, "Recordings", "") {
                    app.recordings.open();
                }
                ui.separator();
                ui.add_enabled_ui(ssh, |ui| {
                    if item(ui, icon::FOLDER_OPEN, "SFTP browser", "")
                        && let Some(f) = focused.clone()
                    {
                        app.s.request(crate::state::Request::Sftp(f));
                    }
                    if item(ui, icon::ARROWS_LEFT_RIGHT, "Port forwards", "")
                        && let Some(f) = focused.clone()
                    {
                        app.forwards.open(&f);
                    }
                });
                if item(ui, icon::KEY, "SSH key manager", "") {
                    app.keys.open();
                }
            });
            ui.menu_button("View", |ui| {
                if item(ui, icon::COMMAND, "Command palette", "Mod+K") {
                    app.palette = Some(Palette::default());
                }
                if item(ui, icon::CORNERS_OUT, "Full screen", "F11") {
                    let full = ui.ctx().input(|i| i.viewport().fullscreen.unwrap_or(false));
                    ui.ctx().send_viewport_cmd(ViewportCommand::Fullscreen(!full));
                }
                ui.separator();
                for k in ThemeKind::ALL {
                    let mark = if k == app.theme { icon::CHECK } else { " " };
                    if item(ui, mark, k.label(), "") {
                        set_theme(app, k);
                    }
                }
                ui.menu_button(format!("{}  Terminal colours", icon::PALETTE), |ui| {
                    for th in crate::terminal_themes::THEMES {
                        let mark = if th.id == app.s.prefs.terminal_theme { icon::CHECK } else { " " };
                        if item(ui, mark, th.label, "") {
                            app.s.prefs.terminal_theme = th.id.into();
                            let _ = app.s.svc.save_preferences(&app.s.prefs);
                        }
                    }
                });
            });
            ui.menu_button("Help", |ui| {
                if item(ui, icon::BOOK_OPEN, "Help", "") {
                    app.settings.open(SettingsTab::Help);
                }
                if item(ui, icon::INFO, "About Nexus Terminal", "") {
                    app.settings.open(SettingsTab::About);
                }
            });
        });
        menus_right = ui.min_rect().right();
    });

    let mut x = bar.right() - 3.0 * 44.0;
    window_controls(ui, Rect::from_min_max(pos2(x, bar.top()), bar.max));
    x -= 34.0;
    let (next, glyph) = if app.theme == ThemeKind::Light { (ThemeKind::Pro, icon::MOON) } else { (ThemeKind::Light, icon::SUN) };
    let tr = Rect::from_center_size(pos2(x + 14.0, bar.center().y), vec2(24.0, 24.0));
    if widgets::icon_button(&mut ui.new_child(egui::UiBuilder::new().max_rect(tr)), glyph, "Toggle light / dark").clicked() {
        set_theme(app, next);
    }
    let w = (bar.width() * 0.22).clamp(140.0, 300.0);
    x -= w + 8.0;
    let r = Rect::from_min_size(pos2(x, bar.center().y - 12.0), vec2(w, 24.0));
    let resp = ui.interact(r, ui.id().with("palette-launch"), Sense::click());
    ui.painter().rect(r, 6.0, if resp.hovered() { t.hover } else { t.field }, Stroke::new(1.0, t.field_border), egui::StrokeKind::Inside);
    ui.painter().text(r.left_center() + vec2(9.0, 0.0), Align2::LEFT_CENTER, icon::MAGNIFYING_GLASS, egui::FontId::proportional(13.0), t.text_faint);
    let show_keys = w > 250.0;
    let clip = Rect::from_min_max(r.min, pos2(r.right() - if show_keys { 52.0 } else { 4.0 }, r.bottom()));
    let hint = if w > 290.0 { "Search connections and commands" } else { "Search" };
    ui.painter().with_clip_rect(clip).text(r.left_center() + vec2(28.0, 0.0), Align2::LEFT_CENTER, hint, egui::FontId::proportional(12.0), t.text_faint);
    if show_keys {
        ui.painter().text(r.right_center() - vec2(8.0, 0.0), Align2::RIGHT_CENTER, keys("Mod+K").replace('+', " "), theme::semibold(10.5), t.text_faint);
    }
    if resp.clicked() {
        app.palette = Some(Palette::default());
    }
    if let Some(p) = app.s.active.as_deref().and_then(|a| app.s.pane(a)) {
        let title = format!("{} — {}", p.info.tab_name, p.info.host);
        let gap = Rect::from_min_max(pos2(menus_right + 16.0, bar.top()), pos2(x - 16.0, bar.bottom()));
        if gap.width() > 60.0 {
            ui.painter().with_clip_rect(gap).text(gap.center(), Align2::CENTER_CENTER, title, egui::FontId::proportional(12.0), t.text_dim);
        }
    }
}

pub fn set_theme(app: &mut App, k: ThemeKind) {
    app.theme = k;
    theme::apply(&app.s.ctx, k);
    app.s.prefs.theme = k.id().into();
    let _ = app.s.svc.save_preferences(&app.s.prefs);
}

fn window_controls(ui: &mut Ui, area: Rect) {
    let t = Tokens::get(ui.ctx());
    let ctx = ui.ctx().clone();
    let max = ctx.input(|i| i.viewport().maximized.unwrap_or(false));
    for (k, (glyph, close)) in [(icon::MINUS, false), (if max { icon::CORNERS_IN } else { icon::SQUARE }, false), (icon::X, true)].into_iter().enumerate() {
        let r = Rect::from_min_size(pos2(area.left() + k as f32 * 44.0, area.top()), vec2(44.0, area.height()));
        let resp = ui.interact(r, ui.id().with(("winctl", k)), Sense::click());
        if resp.hovered() {
            ui.painter().rect_filled(r, 0.0, if close { Color32::from_rgb(196, 43, 28) } else { t.hover });
        }
        let c = if close && resp.hovered() { Color32::WHITE } else { t.text_dim };
        ui.painter().text(r.center(), Align2::CENTER_CENTER, glyph, egui::FontId::proportional(14.0), c);
        if resp.clicked() {
            match k {
                0 => minimize_window(&ctx),
                1 => ctx.send_viewport_cmd(ViewportCommand::Maximized(!max)),
                _ => ctx.send_viewport_cmd(ViewportCommand::Close),
            }
        }
    }
}

/// `ViewportCommand::Minimized(true)` can leave eframe unresponsive after restore on macOS.
/// Hiding the app follows the normal macOS app lifecycle and avoids that minimize event path.
fn minimize_window(_ctx: &egui::Context) {
    #[cfg(target_os = "macos")]
    {
        if let Some(mtm) = objc2::MainThreadMarker::new() {
            objc2_app_kit::NSApplication::sharedApplication(mtm).hide(None);
        }
    }
    #[cfg(not(target_os = "macos"))]
    _ctx.send_viewport_cmd(ViewportCommand::Minimized(true));
}

/// Invisible window edges that resize the undecorated window.
pub fn resize_edges(ctx: &egui::Context) {
    use egui::{CursorIcon, ResizeDirection as D};
    if ctx.input(|i| i.viewport().maximized.unwrap_or(false) || i.viewport().fullscreen.unwrap_or(false)) {
        return;
    }
    let r = ctx.content_rect();
    let e = 5.0;
    let zones = [
        (Rect::from_min_max(r.min, r.min + vec2(12.0, 12.0)), D::NorthWest, CursorIcon::ResizeNorthWest),
        (Rect::from_min_max(pos2(r.right() - 12.0, r.top()), pos2(r.right(), r.top() + 12.0)), D::NorthEast, CursorIcon::ResizeNorthEast),
        (Rect::from_min_max(pos2(r.left(), r.bottom() - 12.0), pos2(r.left() + 12.0, r.bottom())), D::SouthWest, CursorIcon::ResizeSouthWest),
        (Rect::from_min_max(r.max - vec2(12.0, 12.0), r.max), D::SouthEast, CursorIcon::ResizeSouthEast),
        (Rect::from_min_max(r.min, pos2(r.right(), r.top() + e)), D::North, CursorIcon::ResizeVertical),
        (Rect::from_min_max(pos2(r.left(), r.bottom() - e), r.max), D::South, CursorIcon::ResizeVertical),
        (Rect::from_min_max(r.min, pos2(r.left() + e, r.bottom())), D::West, CursorIcon::ResizeHorizontal),
        (Rect::from_min_max(pos2(r.right() - e, r.top()), r.max), D::East, CursorIcon::ResizeHorizontal),
    ];
    let Some(p) = ctx.input(|i| i.pointer.hover_pos()) else {
        return;
    };
    for (zone, dir, cursor) in zones {
        if zone.contains(p) {
            ctx.set_cursor_icon(cursor);
            if ctx.input(|i| i.pointer.primary_pressed()) {
                ctx.send_viewport_cmd(ViewportCommand::BeginResize(dir));
            }
            return;
        }
    }
}

// --- status bar -----------------------------------------------------------------------------

pub fn state_color(t: &Tokens, s: SessionState) -> Color32 {
    match s {
        SessionState::Connected => t.success,
        SessionState::Connecting | SessionState::Reconnecting => t.warning,
        SessionState::Error => t.danger,
        SessionState::Disconnected => t.text_faint,
    }
}

pub fn state_label(s: SessionState) -> &'static str {
    match s {
        SessionState::Connecting => "Connecting",
        SessionState::Connected => "Connected",
        SessionState::Disconnected => "Disconnected",
        SessionState::Reconnecting => "Reconnecting",
        SessionState::Error => "Error",
    }
}

pub fn status_bar(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    latency_tick(app);
    ui.horizontal_centered(|ui| {
        ui.spacing_mut().item_spacing.x = 14.0;
        ui.add_space(8.0);
        let dim = |s: String| RichText::new(s).size(11.5).color(t.text_dim);
        let active = app.s.active.clone();
        match active.as_deref().and_then(|a| app.s.pane(a)) {
            Some(p) => {
                let st = app.s.session_state(&p.info.id).unwrap_or(p.info.state);
                ui.label(RichText::new("●").color(state_color(&t, st)).size(10.0));
                ui.add_space(-10.0);
                ui.label(dim(state_label(st).into()));
                ui.label(RichText::new(crate::views::protocol_badge(p.info.protocol)).size(11.0).color(crate::views::protocol_color(p.info.protocol)));
                if p.recording {
                    ui.label(RichText::new(format!("{} REC", icon::RECORD)).size(11.0).color(t.danger));
                }
            }
            None => {
                ui.label(dim("No active session".into()));
            }
        }
        if let Some((sid, r)) = &app.latency
            && active.as_deref() == Some(sid)
            && let Ok(ms) = r
        {
            let c = if *ms < 100 {
                t.success
            } else if *ms < 250 {
                t.warning
            } else {
                t.danger
            };
            ui.label(RichText::new(format!("{ms} ms")).size(11.5).color(c));
        }
        if app.s.broadcast {
            ui.label(RichText::new(format!("{} BROADCAST", icon::BROADCAST)).size(11.0).color(t.warning));
        }
        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
            ui.add_space(8.0);
            let vault = app.s.svc.secrets.vault.is_unlocked();
            let keychain = app.s.svc.secrets.keychain_ok;
            let label = if vault { format!("{} Vault unlocked", icon::LOCK_OPEN) } else { format!("{} Vault locked", icon::LOCK) };
            if ui
                .add(egui::Label::new(dim(label)).sense(Sense::click()))
                .on_hover_text(if keychain {
                    "Saved credentials use the OS keychain; the vault is the fallback."
                } else {
                    "No OS keychain available: saved credentials use the encrypted vault."
                })
                .clicked()
            {
                app.settings.open(SettingsTab::Security);
            }
            let total = app.s.panes.len();
            let connected = app.s.panes.keys().filter(|id| app.s.session_state(id) == Some(SessionState::Connected)).count();
            ui.label(dim(format!("{connected}/{total} sessions")));
            if let Some(u) = &app.update
                && u.available
            {
                let txt = format!(
                    "{} Update available{}",
                    icon::ARROW_CIRCLE_UP,
                    if u.latest_version.is_empty() { String::new() } else { format!(" ({})", u.latest_version) }
                );
                if ui.add(egui::Label::new(RichText::new(txt).size(11.5).color(t.accent)).sense(Sense::click())).clicked() {
                    app.settings.open(SettingsTab::About);
                }
            }
        });
    });
}

static LATENCY: parking_lot::Mutex<Option<(String, Result<i64, String>)>> = parking_lot::Mutex::new(None);

/// Pings the active SSH session every 10 s for the status-bar latency.
fn latency_tick(app: &mut App) {
    let id = app.s.active.clone();
    let ssh = id.as_deref().and_then(|a| app.s.pane(a)).is_some_and(|p| p.info.protocol == Protocol::Ssh);
    if let Some(r) = LATENCY.lock().take() {
        app.latency = Some(r);
    }
    let Some(id) = id.filter(|_| ssh) else { return };
    if app.s.session_state(&id) != Some(SessionState::Connected) {
        return;
    }
    let last = app.s.ctx.data(|d| d.get_temp::<(String, f64)>(egui::Id::new("nexus-latency-at")));
    let due = last.as_ref().is_none_or(|(sid, at)| sid != &id || app.s.now - at > 10.0);
    // A failed ping stops pinging that session, as before.
    let failed = matches!(&app.latency, Some((sid, Err(_))) if sid == &id);
    if !due || failed {
        app.s.ctx.request_repaint_after(std::time::Duration::from_secs(10));
        return;
    }
    app.s.ctx.data_mut(|d| d.insert_temp(egui::Id::new("nexus-latency-at"), (id.clone(), app.s.now)));
    let (svc, ctx) = (app.s.svc.clone(), app.s.ctx.clone());
    std::thread::spawn(move || {
        let r = svc.sessions.ping(&id).map_err(|e| e.to_string());
        *LATENCY.lock() = Some((id, r));
        ctx.request_repaint();
    });
}

// --- palette --------------------------------------------------------------------------------

#[derive(Default)]
pub struct Palette {
    pub query: String,
    pub sel: usize,
}

#[derive(Clone)]
enum Cmd {
    Connect(String),
    NewProfile(Protocol),
    Quick,
    Local,
    CloseTab,
    NextTab,
    PrevTab,
    Tab(String),
    Settings,
    Import,
    Export,
    ExportJson,
    ImportJson,
    Lock,
    Unlock,
    Keys,
    Triggers,
    Snippets,
    Recordings,
    SplitRight,
    SplitDown,
    Clear,
    Reset,
    Broadcast,
    Theme(ThemeKind),
    About,
    Fullscreen,
    Sftp(String),
}

/// Fuzzy subsequence match with bonuses for consecutive and leading hits (utils.js fuzzyMatch).
pub fn fuzzy_score(text: &str, query: &str) -> Option<i32> {
    if query.is_empty() {
        return Some(1);
    }
    let text: Vec<char> = text.to_lowercase().chars().collect();
    let (mut idx, mut score, mut bonus) = (0usize, 0i32, 0i32);
    for qc in query.to_lowercase().chars() {
        let found = (idx..text.len()).find(|&i| text[i] == qc)?;
        bonus = if found == idx { bonus + 1 } else { 0 };
        score += 1 + bonus + if found == 0 { 2 } else { 0 };
        idx = found + 1;
    }
    Some(score)
}

fn commands(app: &App) -> Vec<(Cmd, String, &'static str, &'static str, String)> {
    let mut v: Vec<(Cmd, String, &'static str, &'static str, String)> = vec![];
    for p in &app.s.profiles {
        let name = if p.display_name.is_empty() { &p.host } else { &p.display_name };
        v.push((Cmd::Connect(p.id.clone()), format!("Connect to {name}"), "Connections", icon::PLUGS_CONNECTED, String::new()));
    }
    for (pr, label) in [
        (Protocol::Ssh, "New SSH Connection"),
        (Protocol::Telnet, "New Telnet Connection"),
        (Protocol::Raw, "New RAW Connection"),
        (Protocol::Serial, "New Serial Connection"),
    ] {
        v.push((Cmd::NewProfile(pr), label.into(), "Actions", icon::PLUS_CIRCLE, if pr == Protocol::Ssh { keys("Mod+N") } else { String::new() }));
    }
    v.push((Cmd::Quick, "Quick Connect…".into(), "Actions", icon::LIGHTNING, keys("Mod+Shift+Q")));
    v.push((Cmd::Local, "Open Local Terminal".into(), "Actions", icon::TERMINAL_WINDOW, String::new()));
    v.push((Cmd::Broadcast, "Toggle Broadcast Mode".into(), "Actions", icon::BROADCAST, keys("Mod+Shift+B")));
    v.push((Cmd::Snippets, "Snippet Library".into(), "Actions", icon::CODE, keys("Mod+Shift+S")));
    v.push((Cmd::Recordings, "Recordings".into(), "Actions", icon::FILM_STRIP, String::new()));
    if !app.s.tabs.is_empty() {
        v.push((Cmd::CloseTab, "Close Tab".into(), "Tabs", icon::X, keys("Mod+W")));
        v.push((Cmd::NextTab, "Next Tab".into(), "Tabs", icon::ARROW_RIGHT, keys("Mod+Tab")));
        v.push((Cmd::PrevTab, "Previous Tab".into(), "Tabs", icon::ARROW_LEFT, keys("Mod+Shift+Tab")));
        for t in &app.s.tabs {
            if let Some(p) = app.s.pane(&t.id) {
                v.push((Cmd::Tab(t.id.clone()), format!("Switch to {}", p.info.tab_name), "Tabs", icon::BROWSER, String::new()));
            }
        }
    }
    v.push((Cmd::Settings, "Settings".into(), "Application", icon::GEAR, keys("Mod+,")));
    v.push((Cmd::Import, "Import Profiles".into(), "Application", icon::DOWNLOAD_SIMPLE, String::new()));
    v.push((Cmd::Export, "Export Profiles".into(), "Application", icon::UPLOAD_SIMPLE, String::new()));
    v.push((Cmd::ImportJson, "Import Profiles (JSON)".into(), "Application", icon::FILE_ARROW_DOWN, String::new()));
    v.push((Cmd::ExportJson, "Export Profiles (JSON)".into(), "Application", icon::FILE_ARROW_UP, String::new()));
    for k in ThemeKind::ALL {
        v.push((Cmd::Theme(k), format!("Theme: {}", k.label()), "Application", icon::PALETTE, String::new()));
    }
    v.push((Cmd::Fullscreen, "Toggle Full Screen".into(), "Application", icon::CORNERS_OUT, "F11".into()));
    v.push((Cmd::About, "About Nexus Terminal".into(), "Application", icon::INFO, String::new()));
    if app.s.svc.secrets.vault.is_unlocked() {
        v.push((Cmd::Lock, "Lock Secret Store".into(), "Security", icon::LOCK, String::new()));
    } else {
        v.push((Cmd::Unlock, "Unlock Secret Store".into(), "Security", icon::LOCK_OPEN, String::new()));
    }
    v.push((Cmd::Keys, "SSH Key Manager".into(), "Security", icon::KEY, String::new()));
    v.push((Cmd::Triggers, "Trigger Rules".into(), "Terminal", icon::LIGHTNING_A, String::new()));
    if app.s.focused_session().and_then(|f| app.s.pane(&f)).is_some_and(|p| matches!(p.view, View::Terminal(_))) {
        v.push((Cmd::SplitRight, "Split Pane Right".into(), "Terminal", icon::SQUARE_SPLIT_HORIZONTAL, keys("Mod+Shift+D")));
        v.push((Cmd::SplitDown, "Split Pane Down".into(), "Terminal", icon::SQUARE_SPLIT_VERTICAL, keys("Mod+Shift+E")));
        if let Some(id) = app.s.focused_session().filter(|f| app.s.pane(f).is_some_and(|p| p.info.protocol == Protocol::Ssh)) {
            v.push((Cmd::Sftp(id), "SFTP Browser".into(), "Terminal", icon::FOLDER_OPEN, String::new()));
        }
        v.push((Cmd::Clear, "Clear Terminal".into(), "Terminal", icon::ERASER, String::new()));
        v.push((Cmd::Reset, "Reset Terminal".into(), "Terminal", icon::ARROW_COUNTER_CLOCKWISE, String::new()));
    }
    v
}

/// Runs the palette command with this label (case-insensitive); false when there is none.
pub fn run_labelled(app: &mut App, ctx: &egui::Context, label: &str) -> bool {
    match commands(app).into_iter().find(|c| c.1.eq_ignore_ascii_case(label)) {
        Some(c) => {
            run(app, ctx, c.0);
            true
        }
        None => false,
    }
}

fn run(app: &mut App, ctx: &egui::Context, c: Cmd) {
    match c {
        Cmd::Sftp(id) => app.s.request(Request::Sftp(id)),
        Cmd::Connect(id) => app.connect_profile(&id, false),
        Cmd::NewProfile(p) => app.s.request(Request::NewProfile { protocol: Some(p), folder: String::new() }),
        Cmd::Quick => app.quick.open(),
        Cmd::Local => app.connect_local(),
        Cmd::CloseTab => {
            if let Some(id) = app.s.focused_session() {
                app.request_close(&id);
            }
        }
        Cmd::NextTab | Cmd::PrevTab => {
            let order = crate::tabs::ordered(&app.s);
            if let Some(cur) = app.s.active.as_ref().and_then(|a| order.iter().position(|x| x == a)) {
                let n = order.len() as i32;
                let next = (cur as i32 + if matches!(c, Cmd::NextTab) { 1 } else { -1 }).rem_euclid(n) as usize;
                let id = order[next].clone();
                app.activate(&id);
            }
        }
        Cmd::Tab(id) => app.activate(&id),
        Cmd::Settings => app.settings.open(SettingsTab::General),
        Cmd::Import => app.s.request(Request::ImportEncrypted),
        Cmd::Export => app.s.request(Request::ExportEncrypted(vec![])),
        Cmd::ExportJson => app.transfer.export_json(),
        Cmd::ImportJson => app.transfer.import_json(),
        Cmd::Lock => {
            app.s.svc.secrets.vault.lock();
            app.s.info("Secret store locked");
        }
        Cmd::Unlock => app.settings.open(SettingsTab::Security),
        Cmd::Keys => app.keys.open(),
        Cmd::Triggers => app.triggers.open(),
        Cmd::Snippets => app.snippets.open(),
        Cmd::Recordings => app.recordings.open(),
        Cmd::SplitRight => crate::splits::split_focused(app, crate::splits::Dir::Right),
        Cmd::SplitDown => crate::splits::split_focused(app, crate::splits::Dir::Down),
        Cmd::Clear | Cmd::Reset => {
            if let Some(id) = app.s.focused_session() {
                app.terminal_menu(&id, if matches!(c, Cmd::Clear) { crate::terminal::MenuAction::Clear } else { crate::terminal::MenuAction::Reset });
            }
        }
        Cmd::Broadcast => app.toggle_broadcast(),
        Cmd::Theme(k) => set_theme(app, k),
        Cmd::About => app.settings.open(SettingsTab::About),
        Cmd::Fullscreen => {
            let full = ctx.input(|i| i.viewport().fullscreen.unwrap_or(false));
            ctx.send_viewport_cmd(ViewportCommand::Fullscreen(!full));
        }
    }
}

pub fn modal_backdrop(ctx: &egui::Context) {
    egui::Area::new("backdrop".into()).order(egui::Order::Middle).fixed_pos(pos2(0.0, 0.0)).show(ctx, |ui| {
        let r = ctx.content_rect();
        ui.painter().rect_filled(r, 0.0, Color32::from_black_alpha(90));
        ui.allocate_rect(r, Sense::click());
    });
}

pub fn palette(app: &mut App, ctx: &egui::Context) {
    let Some(mut pal) = app.palette.take() else {
        return;
    };
    let t = Tokens::get(ctx);
    let mut matches: Vec<_> = commands(app).into_iter().filter_map(|c| fuzzy_score(&format!("{} {}", c.1, c.2), pal.query.trim()).map(|s| (s, c))).collect();
    if !pal.query.trim().is_empty() {
        matches.sort_by_key(|m| std::cmp::Reverse(m.0));
    }
    let matches: Vec<_> = matches.into_iter().map(|(_, c)| c).collect();
    pal.sel = pal.sel.min(matches.len().saturating_sub(1));
    let mut chosen = None;
    let mut close = crate::chrome::modal_key(ctx, Key::Escape);
    if ctx.input(|i| i.key_pressed(Key::ArrowDown)) {
        pal.sel = (pal.sel + 1).min(matches.len().saturating_sub(1));
    }
    if ctx.input(|i| i.key_pressed(Key::ArrowUp)) {
        pal.sel = pal.sel.saturating_sub(1);
    }
    if crate::chrome::modal_key(ctx, Key::Enter) {
        chosen = matches.get(pal.sel).map(|m| m.0.clone());
    }
    modal_backdrop(ctx);
    egui::Area::new("palette".into()).order(egui::Order::Foreground).anchor(Align2::CENTER_TOP, vec2(0.0, 80.0)).show(ctx, |ui| {
        egui::Frame::popup(ui.style()).corner_radius(10).inner_margin(8).show(ui, |ui| {
            ui.set_width(580.0);
            ui.horizontal(|ui| {
                ui.label(RichText::new(icon::MAGNIFYING_GLASS).size(16.0).color(t.text_faint));
                let r = ui.add(
                    egui::TextEdit::singleline(&mut pal.query)
                        .hint_text("Connect, switch tab, or run a command…")
                        .frame(egui::Frame::NONE)
                        .desired_width(f32::INFINITY)
                        .font(egui::FontId::proportional(15.0)),
                );
                r.request_focus();
                if r.changed() {
                    pal.sel = 0;
                }
            });
            ui.separator();
            egui::ScrollArea::vertical().max_height(400.0).show(ui, |ui| {
                let mut last = "";
                for (i, (cmd, label, section, glyph, keys)) in matches.iter().enumerate() {
                    if *section != last {
                        last = section;
                        ui.label(RichText::new(section.to_uppercase()).font(theme::semibold(10.0)).color(t.text_faint));
                    }
                    let (r, resp) = ui.allocate_exact_size(vec2(ui.available_width(), 28.0), Sense::click());
                    if i == pal.sel {
                        ui.painter().rect_filled(r, 6.0, t.accent_soft);
                        ui.scroll_to_rect(r, None);
                    } else if resp.hovered() {
                        ui.painter().rect_filled(r, 6.0, t.hover);
                    }
                    ui.painter().text(r.left_center() + vec2(12.0, 0.0), Align2::LEFT_CENTER, *glyph, egui::FontId::proportional(15.0), t.accent);
                    ui.painter().text(r.left_center() + vec2(36.0, 0.0), Align2::LEFT_CENTER, label, egui::FontId::proportional(13.0), t.text);
                    if !keys.is_empty() {
                        ui.painter().text(r.right_center() - vec2(10.0, 0.0), Align2::RIGHT_CENTER, keys, theme::semibold(10.5), t.text_faint);
                    }
                    if resp.clicked() {
                        chosen = Some(cmd.clone());
                    }
                }
                if matches.is_empty() {
                    widgets::hint(ui, "No matching command.");
                }
            });
        });
    });
    if let Some(c) = chosen {
        close = true;
        run(app, ctx, c);
    }
    if !close {
        app.palette = Some(pal);
    }
}

// --- prompts --------------------------------------------------------------------------------

pub fn modal(ctx: &egui::Context, id: &str, width: f32, body: impl FnOnce(&mut Ui)) {
    modal_backdrop(ctx);
    egui::Area::new(egui::Id::new(id)).order(egui::Order::Foreground).anchor(Align2::CENTER_CENTER, vec2(0.0, -40.0)).show(ctx, |ui| {
        egui::Frame::popup(ui.style()).corner_radius(10).inner_margin(16).show(ui, |ui| {
            ui.set_width(width);
            body(ui);
        });
    });
}

/// Set each frame while a credential or host-key prompt is on screen.
const PROMPT_OPEN: &str = "nexus-prompt-open";

/// Enter/Escape for any modal except the prompts: while a prompt is showing it takes those keys,
/// so one key press can't also confirm or dismiss the dialog underneath.
pub fn modal_key(ctx: &egui::Context, key: Key) -> bool {
    !ctx.data(|d| d.get_temp::<bool>(egui::Id::new(PROMPT_OPEN)).unwrap_or(false)) && ctx.input(|i| i.key_pressed(key))
}

pub fn prompts(app: &mut App, ctx: &egui::Context) {
    let Some(p) = app.prompts.front_mut() else {
        return;
    };
    let t = Tokens::get(ctx);
    let esc = ctx.input(|i| i.key_pressed(Key::Escape));
    let enter = ctx.input(|i| i.key_pressed(Key::Enter));
    let mut done = false;
    match p {
        Prompt::Secret { session, title, message, value, reply } => {
            let who = app.s.pane(session).map(|p| format!("{} ({})", p.info.tab_name, p.info.host));
            modal(ctx, "secret-prompt", 380.0, |ui| {
                ui.label(RichText::new(format!("{}  {title}", icon::KEY)).font(theme::semibold(15.0)));
                if let Some(w) = &who {
                    ui.label(RichText::new(w).color(t.text_dim));
                }
                ui.add_space(4.0);
                ui.label(message.as_str());
                let r = ui.add(egui::TextEdit::singleline(&mut **value).password(true).desired_width(f32::INFINITY));
                r.request_focus();
                ui.add_space(6.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::CHECK, "OK").clicked() || enter {
                        let _ = reply.send(std::mem::take(&mut **value));
                        done = true;
                    }
                    if ui.button("Cancel").clicked() || esc {
                        done = true; // dropping the sender cancels
                    }
                });
            });
        }
        Prompt::HostKey { host, key_type, fingerprint, stored, reply } => {
            modal(ctx, "hostkey-prompt", 480.0, |ui| {
                let mut decide = |d: HostKeyDecision| {
                    let _ = reply.send(d);
                    done = true;
                };
                match stored {
                    Some(old) => {
                        ui.label(RichText::new(format!("{}  Host key changed", icon::SHIELD_WARNING)).font(theme::semibold(15.0)).color(t.danger));
                        ui.add(
                            egui::Label::new(format!(
                                "The key {host} presented does not match the one on record. This happens when a server is reinstalled, but it is also what an interception attack looks like. Only continue if you can confirm the new fingerprint with the server's administrator."
                            ))
                            .wrap(),
                        );
                        ui.add_space(4.0);
                        widgets::kv(ui, "hk", &[("Key type", key_type.clone()), ("Stored", old.clone()), ("Received", fingerprint.clone())]);
                        ui.add_space(6.0);
                        ui.horizontal(|ui| {
                            if widgets::primary(ui, icon::X, "Reject").clicked() || esc {
                                decide(HostKeyDecision::Reject);
                            }
                            if ui.button(format!("{}  Accept Once", icon::CHECK)).on_hover_text("Connect this time; keep the stored key unchanged").clicked() {
                                decide(HostKeyDecision::AcceptOnce);
                            }
                            if ui
                                .add(
                                    egui::Button::new(RichText::new(format!("{}  Accept & Update", icon::ARROWS_CLOCKWISE)).color(t.accent_text))
                                        .fill(t.danger),
                                )
                                .on_hover_text("Connect and replace the stored key with this one")
                                .clicked()
                            {
                                decide(HostKeyDecision::AcceptAndSave);
                            }
                        });
                    }
                    None => {
                        ui.label(RichText::new(format!("{}  Verify host key", icon::SHIELD_WARNING)).font(theme::semibold(15.0)));
                        ui.label(format!("The authenticity of {host} can't be established."));
                        ui.add_space(4.0);
                        widgets::kv(ui, "hk", &[("Key type", key_type.clone()), ("Fingerprint", fingerprint.clone())]);
                        widgets::hint(ui, "Accept only if this matches the fingerprint the server's administrator gave you.");
                        ui.add_space(6.0);
                        ui.horizontal(|ui| {
                            if widgets::primary(ui, icon::CHECK, "Accept & Save").clicked() {
                                decide(HostKeyDecision::AcceptAndSave);
                            }
                            if ui.button(format!("{}  Accept Once", icon::CHECK)).on_hover_text("Connect without recording the key").clicked() {
                                decide(HostKeyDecision::AcceptOnce);
                            }
                            if ui.button("Reject").clicked() || esc {
                                decide(HostKeyDecision::Reject);
                            }
                        });
                    }
                }
            });
        }
    }
    if done {
        app.prompts.pop_front();
    }
}

// --- dialogs --------------------------------------------------------------------------------

pub fn dialogs(app: &mut App, ctx: &egui::Context) {
    let Some(mut d) = app.dialog.take() else {
        return;
    };
    let t = Tokens::get(ctx);
    let esc = crate::chrome::modal_key(ctx, Key::Escape);
    let mut keep = !esc;
    match &mut d {
        Dialog::Reuse { profile, existing } => {
            let name = app.s.profile(profile).map(|p| p.display_name.clone()).unwrap_or_default();
            modal(ctx, "reuse", 400.0, |ui| {
                ui.label(RichText::new("Connection Already Open").font(theme::semibold(15.0)));
                ui.label(format!("{name} already has an open session."));
                ui.add_space(8.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::BROWSER, "Use Existing").clicked() {
                        let id = existing.clone();
                        app.activate(&id);
                        keep = false;
                    }
                    if ui.button("New Connection").clicked() {
                        let id = profile.clone();
                        app.connect_profile(&id, true);
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::DirectVnc { profile } => {
            modal(ctx, "direct-vnc", 420.0, |ui| {
                ui.label(RichText::new(format!("{}  Unencrypted VNC", icon::WARNING)).font(theme::semibold(15.0)).color(t.warning));
                ui.label("This VNC connection isn't tunnelled over SSH. VNC traffic (including keystrokes and screen contents) is sent unencrypted. Enable the SSH tunnel in the profile to protect it.");
                ui.add_space(8.0);
                ui.horizontal(|ui| {
                    if ui.button("Connect Anyway").clicked() {
                        let id = profile.clone();
                        app.connect_profile(&id, true);
                        keep = false;
                    }
                    if ui.button("Edit Profile").clicked() {
                        app.s.request(Request::EditProfile(profile.clone()));
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::ConfirmClose { session } => {
            let name = app.s.pane(session).map(|p| p.info.tab_name.clone()).unwrap_or_default();
            modal(ctx, "confirm-close", 360.0, |ui| {
                ui.label(RichText::new("Close session?").font(theme::semibold(15.0)));
                ui.label(format!("{name} is still connected."));
                ui.add_space(8.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::X, "Close").clicked() || crate::chrome::modal_key(ctx, Key::Enter) {
                        let id = session.clone();
                        app.close_session(&id);
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::ConfirmDisconnectMany { sessions } => {
            modal(ctx, "confirm-many", 360.0, |ui| {
                ui.label(RichText::new("Disconnect sessions?").font(theme::semibold(15.0)));
                ui.label(format!("{} open session(s) will be closed.", sessions.len()));
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::PLUGS, "Disconnect").clicked() {
                        for id in sessions.clone() {
                            app.close_session(&id);
                        }
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::Rename { session, name } => {
            modal(ctx, "rename", 340.0, |ui| {
                ui.label(RichText::new("Rename tab").font(theme::semibold(15.0)));
                let r = ui.add(egui::TextEdit::singleline(name).desired_width(f32::INFINITY));
                r.request_focus();
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::CHECK, "Rename").clicked() || crate::chrome::modal_key(ctx, Key::Enter) {
                        let n = name.trim().to_string();
                        if !n.is_empty() {
                            let _ = app.s.svc.sessions.rename(session, &n);
                            if let Some(p) = app.s.pane_mut(session) {
                                p.info.tab_name = n;
                            }
                        }
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::Broadcast { picks } => {
            modal(ctx, "broadcast", 360.0, |ui| {
                ui.label(RichText::new("Broadcast Targets").font(theme::semibold(15.0)));
                widgets::hint(ui, "Select which sessions should receive broadcast input:");
                for (_, label, on) in picks.iter_mut() {
                    ui.checkbox(on, label.as_str());
                }
                ui.add_space(6.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::BROADCAST, "Apply & Enable").clicked() {
                        let sel: std::collections::HashSet<String> = picks.iter().filter(|p| p.2).map(|p| p.0.clone()).collect();
                        if sel.is_empty() {
                            app.s.error("Select at least one target session");
                        } else {
                            let all = sel.len() == picks.len();
                            app.s.broadcast_targets = if all { None } else { Some(sel.clone()) };
                            if !app.s.broadcast {
                                app.toggle_broadcast();
                            } else {
                                app.s.info(if all { "Broadcasting to all sessions".to_string() } else { format!("Broadcasting to {} sessions", sel.len()) });
                            }
                            keep = false;
                        }
                    }
                    if ui.button("Select All").clicked() {
                        for p in picks.iter_mut() {
                            p.2 = true;
                        }
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::ConfirmPaste { session, text } => {
            let lines = text.lines().count();
            // Control characters other than newline/tab act on their own; show them.
            let preview: String = text
                .lines()
                .take(12)
                .map(|l| l.chars().flat_map(|c| if c.is_control() && c != '\t' { c.escape_debug().collect::<Vec<_>>() } else { vec![c] }).collect::<String>())
                .collect::<Vec<_>>()
                .join("\n");
            modal(ctx, "confirm-paste", 480.0, |ui| {
                ui.label(RichText::new("Paste multiple lines?").font(theme::semibold(15.0)));
                widgets::hint(ui, &format!("{lines} line(s) will run as commands as soon as they are pasted:"));
                egui::ScrollArea::vertical().max_height(220.0).show(ui, |ui| {
                    ui.add(egui::Label::new(RichText::new(preview).monospace()).wrap());
                });
                if lines > 12 {
                    widgets::hint(ui, &format!("… and {} more", lines - 12));
                }
                ui.add_space(6.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::CHECK, "Paste").clicked() {
                        if let Some(View::Terminal(t)) = app.s.panes.get_mut(session.as_str()).map(|p| &mut p.view) {
                            let mut out = crate::terminal::TermResponse::default();
                            t.paste_now(text, &mut out);
                            let id = session.clone();
                            app.send_input(&id, &out.input);
                        }
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() || crate::chrome::modal_key(ctx, Key::Escape) {
                        keep = false;
                    }
                });
            });
        }
        Dialog::Guided { session, title, body, known_hosts } => {
            let label = app.s.pane(session).map(|p| p.info.tab_name.clone()).unwrap_or_else(|| "Session".into());
            let profile_id = app.s.pane(session).map(|p| p.info.profile_id.clone()).unwrap_or_default();
            modal(ctx, "guided", 440.0, |ui| {
                ui.label(RichText::new(title.as_str()).font(theme::semibold(15.0)).color(t.danger));
                ui.label(RichText::new(label).strong());
                ui.add(egui::Label::new(body.as_str()).wrap());
                ui.add_space(8.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::ARROW_CLOCKWISE, "Retry").clicked() {
                        let id = session.clone();
                        app.reconnect(&id);
                        keep = false;
                    }
                    if !profile_id.is_empty() && ui.button("Edit Profile").clicked() {
                        app.s.request(crate::state::Request::EditProfile(profile_id.clone()));
                        keep = false;
                    }
                    if *known_hosts && ui.button("Known Hosts").clicked() {
                        app.settings.open(SettingsTab::KnownHosts);
                        keep = false;
                    }
                    if ui.button("Close").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::Delete { profiles } => {
            let names: Vec<String> = profiles.iter().filter_map(|id| app.s.profile(id)).map(|p| p.display_name.clone()).collect();
            modal(ctx, "delete", 380.0, |ui| {
                ui.label(RichText::new("Delete connection?").font(theme::semibold(15.0)));
                ui.label(if names.len() == 1 {
                    format!("\"{}\" will be deleted, with its saved credentials.", names[0])
                } else {
                    format!("{} connections will be deleted.", names.len())
                });
                ui.add_space(8.0);
                ui.horizontal(|ui| {
                    if ui.add(egui::Button::new(RichText::new(format!("{}  Delete", icon::TRASH)).color(Color32::WHITE)).fill(t.danger)).clicked() {
                        for id in profiles.clone() {
                            if let Err(e) = app.s.svc.delete_profile(&id) {
                                app.s.error(e.to_string());
                            }
                        }
                        app.s.reload_profiles();
                        app.sidebar.selected.clear();
                        keep = false;
                    }
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
        }
        Dialog::Input { title, label, value, then } => {
            let mut submit = false;
            modal(ctx, "input", 360.0, |ui| {
                ui.label(RichText::new(title.as_str()).font(theme::semibold(15.0)));
                ui.label(label.as_str());
                let r = ui.add(egui::TextEdit::singleline(value).desired_width(f32::INFINITY));
                r.request_focus();
                ui.horizontal(|ui| {
                    submit = widgets::primary(ui, icon::CHECK, "OK").clicked() || crate::chrome::modal_key(ctx, Key::Enter);
                    if ui.button("Cancel").clicked() {
                        keep = false;
                    }
                });
            });
            if submit {
                keep = false;
                let v = value.trim().to_string();
                match then {
                    InputThen::Tags { profiles, replace } => {
                        let tags: Vec<String> = v.split(',').map(|x| x.trim().to_string()).filter(|x| !x.is_empty()).collect();
                        for id in profiles.iter() {
                            if let Some(mut p) = app.s.profile(id).cloned() {
                                if *replace {
                                    p.tags = tags.clone();
                                } else {
                                    for tg in &tags {
                                        if !p.tags.contains(tg) {
                                            p.tags.push(tg.clone());
                                        }
                                    }
                                }
                                let _ = app.s.svc.update_profile(p);
                            }
                        }
                    }
                    InputThen::MoveGroup { profiles } => {
                        for id in profiles.iter() {
                            if let Some(mut p) = app.s.profile(id).cloned() {
                                p.folder = v.clone();
                                let _ = app.s.svc.update_profile(p);
                            }
                        }
                    }
                }
                app.s.reload_profiles();
            }
        }
    }
    if keep && app.dialog.is_none() {
        app.dialog = Some(d);
    }
}

// --- toasts ---------------------------------------------------------------------------------

pub fn toasts(app: &mut App, ctx: &egui::Context) {
    let t = Tokens::get(ctx);
    let now = app.s.now;
    app.s.toasts.retain(|x| x.until > now);
    if app.s.toasts.is_empty() {
        return;
    }
    ctx.request_repaint_after(std::time::Duration::from_millis(250));
    let mut dismiss = None;
    egui::Area::new("toasts".into()).order(egui::Order::Tooltip).anchor(Align2::RIGHT_TOP, vec2(-16.0, 48.0)).show(ctx, |ui| {
        for (i, toast) in app.s.toasts.iter().enumerate() {
            egui::Frame::popup(ui.style()).corner_radius(8).inner_margin(egui::Margin::symmetric(12, 8)).show(ui, |ui| {
                ui.set_max_width(420.0);
                ui.horizontal_top(|ui| {
                    let (g, c) = match toast.kind {
                        ToastKind::Error => (icon::WARNING_CIRCLE, t.danger),
                        ToastKind::Warning => (icon::WARNING, t.warning),
                        ToastKind::Success => (icon::CHECK_CIRCLE, t.success),
                        ToastKind::Info => (icon::INFO, t.accent),
                    };
                    ui.label(RichText::new(g).color(c).size(15.0));
                    ui.add(egui::Label::new(&toast.text).wrap());
                    if ui.add(egui::Button::new(RichText::new(icon::X).size(11.0)).frame(false)).clicked() {
                        dismiss = Some(i);
                    }
                });
            });
            ui.add_space(6.0);
        }
    });
    if let Some(i) = dismiss {
        app.s.toasts.remove(i);
    }
}

#[cfg(test)]
mod modal_key_tests {
    use super::*;

    #[test]
    fn an_open_prompt_takes_enter_and_escape() {
        let ctx = egui::Context::default();
        let press = |key| egui::RawInput {
            events: vec![egui::Event::Key { key, physical_key: None, pressed: true, repeat: false, modifiers: Default::default() }],
            ..Default::default()
        };
        for (prompt_open, want) in [(false, true), (true, false)] {
            let mut got = None;
            let mut out = ctx.run_ui(press(Key::Enter), |ui| {
                ui.ctx().data_mut(|d| d.insert_temp(egui::Id::new(PROMPT_OPEN), prompt_open));
                got = Some(modal_key(ui.ctx(), Key::Enter));
            });
            out.textures_delta.clear();
            assert_eq!(got, Some(want), "prompt open: {prompt_open}");
        }
    }
}
