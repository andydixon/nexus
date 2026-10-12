//! The tab strip and the workspace under it: the active tab's session (or its split panes),
//! terminal find bar and connection overlays.

use crate::app::{App, Dialog};
use crate::chrome::{state_color, state_label};
use crate::state::{Request, Shared};
use crate::terminal::TermResponse;
use crate::theme::{self, Tokens};
use crate::views::{View, protocol_badge, protocol_color};
use crate::widgets;
use egui::{Align2, CornerRadius, Key, Rect, RichText, Sense, Stroke, Ui, pos2, vec2};
use egui_phosphor::regular as icon;
use nexus_core::models::{Protocol, SessionState};

#[derive(Default)]
pub struct TabsUi {
    pub find_focus: bool,
    drag: Option<String>,
}

/// Tab ids in display order: pinned first, then the rest, each in insertion order.
pub fn ordered(s: &Shared) -> Vec<String> {
    let pinned = |id: &str| s.pane(id).is_some_and(|p| p.info.pinned);
    let mut v: Vec<String> = s.tabs.iter().filter(|t| pinned(&t.id)).map(|t| t.id.clone()).collect();
    v.extend(s.tabs.iter().filter(|t| !pinned(&t.id)).map(|t| t.id.clone()));
    v
}

pub fn workspace(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    egui::Panel::top("tabstrip").exact_size(32.0).frame(egui::Frame::NONE.fill(t.dock)).show(ui, |ui| strip(app, ui));
    egui::CentralPanel::default().frame(egui::Frame::NONE.fill(t.canvas)).show(ui, |ui| {
        let Some(active) = app.s.active.clone().filter(|a| app.s.tab(a).is_some()) else {
            empty_state(app, ui);
            return;
        };
        let has_split = app.s.tab(&active).is_some_and(|tb| tb.split.is_some());
        if has_split {
            crate::splits::render(app, ui, &active);
        } else {
            let rect = ui.available_rect_before_wrap();
            pane(app, ui, &active, rect);
        }
    });
}

fn empty_state(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    let r = ui.max_rect();
    ui.scope_builder(egui::UiBuilder::new().max_rect(Rect::from_center_size(r.center(), vec2(420.0, 420.0))), |ui| {
        ui.vertical_centered(|ui| {
            ui.add(egui::Image::new(&crate::chrome::logo(ui.ctx())).fit_to_exact_size(vec2(84.0, 84.0)));
            ui.label(RichText::new("Nexus Terminal").font(theme::semibold(28.0)));
            ui.label(RichText::new("SSH · Telnet · RAW · Serial · VNC · RDP · IRC · GitHub").color(t.text_dim));
            ui.add_space(18.0);
            ui.horizontal(|ui| {
                ui.add_space((ui.available_width() - 330.0).max(0.0) / 2.0);
                if widgets::primary(ui, icon::PLUS, "New Connection").clicked() {
                    app.s.request(Request::NewProfile { protocol: None, folder: String::new() });
                }
                if widgets::button(ui, icon::LIGHTNING, "Quick Connect").clicked() {
                    app.quick.open();
                }
                if widgets::button(ui, icon::TERMINAL_WINDOW, "Local").clicked() {
                    app.connect_local();
                }
            });
            ui.add_space(16.0);
            let keys = [("Mod+K", "command palette"), ("Mod+N", "new connection"), ("Mod+Shift+Q", "quick connect"), ("Mod+,", "settings")];
            egui::Grid::new("welcome-keys").num_columns(2).spacing(vec2(10.0, 6.0)).show(ui, |ui| {
                for (k, v) in keys {
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| widgets::chip(ui, &crate::state::keys(k), t.text_dim));
                    ui.label(RichText::new(v).color(t.text_faint));
                    ui.end_row();
                }
            });
        });
    });
}

fn strip(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    let order = ordered(&app.s);
    let mut close = None;
    let mut activate = None;
    let mut menu_action: Option<(String, &'static str)> = None;
    let full = ui.max_rect();
    let right_w = 64.0;
    let strip_rect = Rect::from_min_max(full.min, pos2(full.right() - right_w, full.bottom()));
    let mut child = ui.new_child(egui::UiBuilder::new().max_rect(strip_rect));
    egui::ScrollArea::horizontal().id_salt("tabs-scroll").scroll_bar_visibility(egui::scroll_area::ScrollBarVisibility::AlwaysHidden).show(&mut child, |ui| {
        ui.horizontal(|ui| {
            ui.spacing_mut().item_spacing.x = 1.0;
            ui.add_space(4.0);
            for id in &order {
                let Some(p) = app.s.pane(id) else { continue };
                let p_info = p.info.clone();
                let (p_recording, p_since, lines) = (p.recording, p.connected_since, p.lines);
                let p = PaneInfo { info: p_info, recording: p_recording, connected_since: p_since };
                let tab_activity = app.s.tab(id).is_some_and(|tb| tb.activity);
                let active = app.s.active.as_deref() == Some(id.as_str());
                let state = app.s.session_state(id).unwrap_or(p.info.state);
                let label = p.info.tab_name.clone();
                let font = egui::FontId::proportional(12.5);
                let galley = ui.painter().layout_no_wrap(label.clone(), font, if active { t.text } else { t.text_dim });
                let badge = ui.painter().layout_no_wrap(protocol_badge(p.info.protocol).into(), theme::semibold(9.0), protocol_color(p.info.protocol));
                let sftp = p.info.protocol == Protocol::Ssh;
                let extra = if p.info.pinned { 16.0 } else { 0.0 } + if sftp { 20.0 } else { 0.0 };
                let w = (galley.size().x + badge.size().x + 66.0 + extra).min(280.0);
                let (r, resp) = ui.allocate_exact_size(vec2(w, 30.0), Sense::click_and_drag());
                let body = Rect::from_min_max(r.min + vec2(0.0, 3.0), r.max);
                if active {
                    ui.painter().rect_filled(body, CornerRadius { nw: 6, ne: 6, sw: 0, se: 0 }, t.canvas);
                    ui.painter().hline(body.x_range().shrink(6.0), body.top() + 0.5, Stroke::new(2.0, protocol_color(p.info.protocol)));
                    if active && resp.rect.min.x < strip_rect.min.x || resp.rect.max.x > strip_rect.max.x {
                        ui.scroll_to_rect(r, None);
                    }
                } else if resp.hovered() {
                    ui.painter().rect_filled(body, CornerRadius { nw: 6, ne: 6, sw: 0, se: 0 }, t.hover);
                }
                let mut x = body.left() + 9.0;
                ui.painter().circle_filled(pos2(x + 3.0, body.center().y), 3.5, state_color(&t, state));
                x += 12.0;
                let br = Rect::from_min_size(pos2(x, body.center().y - badge.size().y / 2.0 - 2.0), badge.size() + vec2(8.0, 4.0));
                ui.painter().rect_filled(br, 4.0, protocol_color(p.info.protocol).gamma_multiply(0.16));
                ui.painter().galley(br.min + vec2(4.0, 2.0), badge, t.text);
                x = br.right() + 7.0;
                if p.info.pinned {
                    ui.painter().text(pos2(x, body.center().y), Align2::LEFT_CENTER, icon::PUSH_PIN, egui::FontId::proportional(11.0), t.text_faint);
                    x += 16.0;
                }
                let right_controls = 24.0 + if sftp { 20.0 } else { 0.0 };
                let gr = Rect::from_min_max(pos2(x, body.top()), pos2(body.right() - right_controls, body.bottom()));
                ui.painter().with_clip_rect(gr).galley(pos2(x, body.center().y - galley.size().y / 2.0), galley, t.text);
                if tab_activity && !active {
                    ui.painter().circle_filled(pos2(body.right() - right_controls - 2.0, body.top() + 7.0), 3.0, t.accent);
                }
                if p.recording {
                    ui.painter().circle_filled(pos2(body.left() + 4.0, body.top() + 6.0), 3.0, t.danger);
                }
                if sftp {
                    let fr = Rect::from_center_size(pos2(body.right() - 33.0, body.center().y), vec2(16.0, 16.0));
                    let fresp = ui.interact(fr, ui.id().with(("sftp", id)), Sense::click());
                    let c = if app.sftp.open_for() == Some(id.as_str()) {
                        t.accent
                    } else if fresp.hovered() {
                        t.text
                    } else {
                        t.text_faint
                    };
                    ui.painter().text(fr.center(), Align2::CENTER_CENTER, icon::FOLDER_SIMPLE, egui::FontId::proportional(12.0), c);
                    if fresp.on_hover_text("SFTP browser").clicked() {
                        app.s.request(Request::Sftp(id.clone()));
                    }
                }
                let xr = Rect::from_center_size(pos2(body.right() - 13.0, body.center().y), vec2(16.0, 16.0));
                let xresp = ui.interact(xr, ui.id().with(("close", id)), Sense::click());
                if active || resp.hovered() || xresp.hovered() {
                    if xresp.hovered() {
                        ui.painter().rect_filled(xr, 3.0, t.hover);
                    }
                    ui.painter().text(xr.center(), Align2::CENTER_CENTER, icon::X, egui::FontId::proportional(11.0), t.text_dim);
                }
                // Drag to reorder.
                if resp.drag_started() {
                    app.tabs_ui.drag = Some(id.clone());
                }
                if let Some(d) = app.tabs_ui.drag.clone()
                    && d != *id
                    && resp.contains_pointer()
                    && ui.input(|i| i.pointer.primary_down())
                {
                    let from = app.s.tabs.iter().position(|x| x.id == d);
                    let to = app.s.tabs.iter().position(|x| x.id == *id);
                    if let (Some(f), Some(to)) = (from, to) {
                        let tb = app.s.tabs.remove(f);
                        app.s.tabs.insert(to, tb);
                    }
                }
                if xresp.clicked() {
                    close = Some(id.clone());
                } else if resp.clicked() {
                    activate = Some(id.clone());
                }
                let since = p.connected_since.map(|t| format!("\nConnected since {}", t.format("%H:%M:%S"))).unwrap_or_default();
                let tip = format!("{} — {}\n{}{since}", p.info.tab_name, p.info.host, state_label(state));
                let resp = resp.on_hover_text(tip);
                resp.context_menu(|ui| {
                    for (label, a, glyph) in [
                        ("Rename…", "rename", icon::PENCIL_SIMPLE),
                        ("Duplicate", "duplicate", icon::COPY),
                        (if p.info.pinned { "Unpin" } else { "Pin" }, "pin", icon::PUSH_PIN),
                        ("Move Left", "left", icon::ARROW_LEFT),
                        ("Move Right", "right", icon::ARROW_RIGHT),
                        ("Reconnect", "reconnect", icon::ARROW_CLOCKWISE),
                        ("Close", "close", icon::X),
                    ] {
                        if matches!(a, "left" | "reconnect" | "close") {
                            ui.separator();
                        }
                        if ui.button(format!("{glyph}  {label}")).clicked() {
                            menu_action = Some((id.clone(), a));
                            ui.close();
                        }
                    }
                    if matches!(p.info.protocol, Protocol::Telnet | Protocol::Serial) {
                        ui.separator();
                        if ui.button(format!("{}  Send Break", icon::LIGHTNING)).clicked() {
                            menu_action = Some((id.clone(), "break"));
                            ui.close();
                        }
                    }
                    if p.info.protocol == Protocol::Serial {
                        let (d, r) = lines;
                        if ui.button(format!("{}  DTR", if d { icon::CHECK_SQUARE } else { icon::SQUARE })).clicked() {
                            menu_action = Some((id.clone(), "dtr"));
                            ui.close();
                        }
                        if ui.button(format!("{}  RTS", if r { icon::CHECK_SQUARE } else { icon::SQUARE })).clicked() {
                            menu_action = Some((id.clone(), "rts"));
                            ui.close();
                        }
                    }
                });
            }
            if !ui.input(|i| i.pointer.primary_down()) {
                app.tabs_ui.drag = None;
            }
        });
    });
    // Right cluster: new tab and broadcast.
    let rr = Rect::from_min_max(pos2(full.right() - right_w, full.top()), full.max);
    let mut right = ui.new_child(egui::UiBuilder::new().max_rect(rr).layout(egui::Layout::left_to_right(egui::Align::Center)));
    right.add_space(6.0);
    if widgets::icon_button(&mut right, icon::PLUS, "New connection").clicked() {
        app.s.request(Request::NewProfile { protocol: None, folder: String::new() });
    }
    let b = widgets::icon_button(
        &mut right,
        icon::BROADCAST,
        if app.s.broadcast { "Broadcast ON — click to stop, right-click for targets" } else { "Broadcast input (right-click to pick targets)" },
    );
    if app.s.broadcast {
        right.painter().rect_stroke(b.rect, 4.0, Stroke::new(1.5, t.warning), egui::StrokeKind::Inside);
    }
    if b.clicked() {
        app.toggle_broadcast();
    }
    if b.secondary_clicked() {
        app.pick_broadcast_targets();
    }

    if let Some(id) = activate {
        app.activate(&id);
    }
    if let Some(id) = close {
        app.request_close(&id);
    }
    if let Some((id, a)) = menu_action {
        tab_action(app, &id, a);
    }
}

struct PaneInfo {
    info: nexus_core::models::SessionInfo,
    recording: bool,
    connected_since: Option<chrono::DateTime<chrono::Local>>,
}

fn tab_action(app: &mut App, id: &str, a: &str) {
    match a {
        "rename" => {
            let name = app.s.pane(id).map(|p| p.info.tab_name.clone()).unwrap_or_default();
            app.dialog = Some(Dialog::Rename { session: id.into(), name });
        }
        "duplicate" => {
            let Some(p) = app.s.pane(id) else { return };
            if p.info.protocol == Protocol::Local {
                app.connect_local();
            } else if let Some(prof) = app.s.svc.sessions.profile(id) {
                if app.s.profile(&prof.id).is_some() {
                    let pid = prof.id.clone();
                    app.connect_profile(&pid, true);
                } else {
                    app.s.request(Request::ConnectQuick(prof));
                }
            }
        }
        "pin" => {
            if let Some(p) = app.s.pane_mut(id) {
                p.info.pinned = !p.info.pinned;
                let pinned = p.info.pinned;
                let _ = app.s.svc.sessions.pin(id, pinned);
            }
        }
        "left" | "right" => {
            if let Some(i) = app.s.tabs.iter().position(|t| t.id == id) {
                let j = if a == "left" { i.checked_sub(1) } else { Some(i + 1).filter(|j| *j < app.s.tabs.len()) };
                if let Some(j) = j {
                    app.s.tabs.swap(i, j);
                }
            }
        }
        "reconnect" => app.reconnect(id),
        "break" => app.send_break(id),
        "dtr" => app.toggle_line(id, true),
        "rts" => app.toggle_line(id, false),
        "close" => app.request_close(id),
        _ => {}
    }
}

/// Draws one session into `rect`.
pub fn pane(app: &mut App, ui: &mut Ui, id: &str, rect: Rect) {
    let Some(mut pane) = app.s.panes.remove(id) else {
        return;
    };
    let t = Tokens::get(ui.ctx());
    let theme_id = app.s.prefs.terminal_theme.clone();
    let focused_tab = app.s.focused.as_deref() == Some(id) || (app.s.focused.is_none() && app.s.active.as_deref() == Some(id));
    let mut child = ui.new_child(egui::UiBuilder::new().max_rect(rect).id_salt(("pane", id)));
    child.set_clip_rect(rect.intersect(ui.clip_rect()));
    let ui = &mut child;
    let mut response = None;
    match &mut pane.view {
        View::Terminal(term) => {
            if term.find.is_some() {
                find_bar(app, ui, term);
            }
            let theme = crate::terminal_themes::get(&theme_id);
            term.confirm_paste = app.s.prefs.confirm_multiline_paste;
            let r = term.ui(ui, theme, focused_tab);
            if pane.highlight_until > app.s.now {
                ui.painter().rect_stroke(rect.shrink(1.0), 0.0, Stroke::new(2.0, t.warning), egui::StrokeKind::Inside);
                ui.ctx().request_repaint_after(std::time::Duration::from_millis(100));
            }
            response = Some(r);
        }
        View::Raw(v) => v.ui(ui, &mut app.s),
        View::Irc(v) => v.ui(ui, &mut app.s),
        View::Github(v) => v.ui(ui, &mut app.s),
        View::Desktop(v) => v.ui(ui, &mut app.s),
    }
    let state = app.s.session_state(id).unwrap_or(pane.info.state);
    let local_exited = pane.local_exited;
    let error = pane.info.error.clone();
    let proto = pane.info.protocol;
    app.s.panes.insert(id.into(), pane);
    if let Some(r) = response {
        terminal_response(app, id, r, local_exited);
    }
    overlay(app, ui, id, rect, state, &error, local_exited, proto);
}

fn terminal_response(app: &mut App, id: &str, r: TermResponse, local_exited: bool) {
    if r.focused && app.s.focused.as_deref() != Some(id) {
        app.s.focused = Some(id.into());
    }
    if let Some((c, rows)) = r.resized {
        let _ = app.s.svc.sessions.resize(id, c, rows);
    }
    if !r.reply.is_empty() {
        let _ = app.s.svc.sessions.write(id, &r.reply);
    }
    if local_exited {
        if r.input.iter().any(|b| *b == b'\r' || *b == b'\n') {
            let size = match app.s.pane(id).map(|p| &p.view) {
                Some(View::Terminal(t)) => t.size(),
                _ => (80, 24),
            };
            if let Err(e) = app.s.svc.sessions.restart_local(id, size.0, size.1) {
                app.s.error(e.to_string());
            } else if let Some(p) = app.s.pane_mut(id) {
                p.local_exited = false;
            }
        }
    } else if !r.input.is_empty() {
        app.send_input(id, &r.input);
    }
    if r.bell {
        app.s.ctx.request_repaint();
    }
    if let Some(text) = r.clipboard {
        app.remote_clipboard(&text);
    }
    if let Some(text) = r.confirm_paste {
        app.dialog = Some(crate::app::Dialog::ConfirmPaste { session: id.into(), text });
    }
    if let Some(a) = r.menu {
        app.terminal_menu(id, a);
    }
}

#[allow(clippy::too_many_arguments)]
fn overlay(app: &mut App, ui: &mut Ui, id: &str, rect: Rect, state: SessionState, error: &str, local_exited: bool, proto: Protocol) {
    let t = Tokens::get(ui.ctx());
    let show = match state {
        SessionState::Disconnected => !local_exited && proto != Protocol::Local,
        SessionState::Error => true,
        SessionState::Reconnecting => true,
        SessionState::Connecting => proto != Protocol::Local && !matches!(app.s.pane(id).map(|p| &p.view), Some(View::Terminal(_))),
        SessionState::Connected => false,
    };
    if !show {
        return;
    }
    let card = Rect::from_center_size(rect.center(), vec2(360.0_f32.min(rect.width() - 20.0), 150.0));
    ui.painter().rect_filled(rect, 0.0, t.canvas.gamma_multiply(0.55));
    let mut c = ui.new_child(egui::UiBuilder::new().max_rect(card));
    egui::Frame::popup(c.style()).corner_radius(10).inner_margin(14).show(&mut c, |ui| {
        ui.set_width(card.width() - 28.0);
        ui.vertical_centered(|ui| {
            let (g, title, col) = match state {
                SessionState::Reconnecting => (icon::ARROW_CLOCKWISE, "Reconnecting…", t.warning),
                SessionState::Connecting => (icon::PLUGS, "Connecting…", t.warning),
                SessionState::Error => (icon::WARNING_CIRCLE, "Connection error", t.danger),
                _ => (icon::PLUGS, "Disconnected", t.text_dim),
            };
            ui.label(RichText::new(format!("{g}  {title}")).font(theme::semibold(15.0)).color(col));
            if !error.is_empty() && state != SessionState::Reconnecting {
                ui.add(egui::Label::new(RichText::new(error).color(t.text_dim).size(12.0)).wrap());
            }
            ui.add_space(6.0);
            ui.horizontal(|ui| {
                ui.add_space((ui.available_width() - 200.0).max(0.0) / 2.0);
                if state == SessionState::Reconnecting {
                    if ui.button("Cancel").clicked() {
                        app.s.svc.sessions.cancel_reconnect(id);
                    }
                } else if state != SessionState::Connecting {
                    if widgets::primary(ui, icon::ARROW_CLOCKWISE, "Reconnect").clicked() {
                        app.reconnect(id);
                    }
                    if ui.button("Close").clicked() {
                        app.close_session(id);
                    }
                }
            });
        });
    });
}

fn find_bar(app: &mut App, ui: &mut Ui, term: &mut crate::terminal::TermView) {
    let t = Tokens::get(ui.ctx());
    let (cur, total) = term.find_counts();
    let mut close = false;
    let mut step = None;
    egui::Frame::NONE.fill(t.card).inner_margin(egui::Margin::symmetric(8, 4)).show(ui, |ui| {
        ui.horizontal(|ui| {
            let f = term.find.as_mut().unwrap();
            ui.label(RichText::new(icon::MAGNIFYING_GLASS).color(t.text_faint));
            let r = ui.add(egui::TextEdit::singleline(&mut f.query).hint_text("Find").desired_width(220.0));
            if app.tabs_ui.find_focus {
                r.request_focus();
                app.tabs_ui.find_focus = false;
            }
            if r.lost_focus() && ui.input(|i| i.key_pressed(Key::Enter)) {
                step = Some(!ui.input(|i| i.modifiers.shift));
                r.request_focus();
            }
            if r.has_focus() && ui.input(|i| i.key_pressed(Key::Escape)) {
                close = true;
            }
            ui.toggle_value(&mut f.case_sensitive, "Aa").on_hover_text("Match case");
            ui.toggle_value(&mut f.regex, ".*").on_hover_text("Regular expression");
            match &f.error {
                Some(e) => ui.label(RichText::new(e.lines().last().unwrap_or("bad regex")).color(t.danger).size(11.0)),
                None => ui.label(RichText::new(if f.query.is_empty() { String::new() } else { format!("{cur} of {total}") }).size(11.5).color(t.text_dim)),
            };
            if widgets::icon_button(ui, icon::ARROW_UP, "Previous (Shift+Enter)").clicked() {
                step = Some(false);
            }
            if widgets::icon_button(ui, icon::ARROW_DOWN, "Next (Enter)").clicked() {
                step = Some(true);
            }
            if widgets::icon_button(ui, icon::X, "Close (Esc)").clicked() {
                close = true;
            }
        });
    });
    if let Some(fwd) = step {
        term.find_step(fwd);
    }
    if close {
        term.find = None;
        term.request_focus();
    }
}
