//! The connection sidebar: Local Machine, Favourites, Recent, folders, Ungrouped; fuzzy search;
//! click to connect, Ctrl/Cmd-click to multi-select, double-click to edit; bulk actions.

use crate::app::{App, Dialog, InputThen};
use crate::chrome::fuzzy_score;
use crate::state::Request;
use crate::theme::{self, Tokens};
use crate::views::{protocol_badge, protocol_color};
use crate::widgets;
use egui::{Align2, Rect, RichText, Sense, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::models::Profile;
use std::collections::HashSet;

#[derive(Default)]
pub struct Sidebar {
    pub search: String,
    pub collapsed: HashSet<String>,
    pub selected: HashSet<String>,
}

const RECENT: usize = 5;

pub fn ui(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    ui.add_space(8.0);
    ui.horizontal(|ui| {
        ui.add_space(8.0);
        ui.label(RichText::new(icon::MAGNIFYING_GLASS).color(t.text_faint));
        ui.add(egui::TextEdit::singleline(&mut app.sidebar.search).hint_text("Search connections").desired_width(ui.available_width() - 10.0));
    });
    ui.add_space(4.0);

    // Footer actions, pinned to the bottom of the sidebar.
    egui::Panel::bottom("sidebar-footer").exact_size(36.0).frame(egui::Frame::NONE.fill(t.dock)).show(ui, |ui| {
        ui.horizontal_centered(|ui| {
            ui.add_space(6.0);
            if widgets::icon_button(ui, icon::PLUS, "New connection (Ctrl/Cmd+N)").clicked() {
                app.s.request(Request::NewProfile { protocol: None, folder: String::new() });
            }
            if widgets::icon_button(ui, icon::COMMAND, "Command palette (Ctrl/Cmd+K)").clicked() {
                app.palette = Some(Default::default());
            }
            if widgets::icon_button(ui, icon::DOWNLOAD_SIMPLE, "Import profiles").clicked() {
                app.s.request(Request::ImportEncrypted);
            }
            if widgets::icon_button(ui, icon::UPLOAD_SIMPLE, "Export profiles").clicked() {
                app.s.request(Request::ExportEncrypted(vec![]));
            }
            if widgets::icon_button(ui, icon::FILM_SLATE, "Recordings").clicked() {
                app.recordings.open();
            }
            if widgets::icon_button(ui, icon::GEAR, "Settings (Ctrl/Cmd+,)").clicked() {
                app.settings.open(crate::state::SettingsTab::General);
            }
        });
    });
    if !app.sidebar.selected.is_empty() {
        egui::Panel::bottom("sidebar-bulk").frame(egui::Frame::NONE.fill(t.dock).inner_margin(6)).show(ui, |ui| bulk_bar(app, ui));
    }
    egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| list(app, ui));
}

fn list(app: &mut App, ui: &mut Ui) {
    let q = app.sidebar.search.trim().to_string();
    // Search: one ranked list.
    if !q.is_empty() {
        let mut hits: Vec<(i32, Profile)> = app
            .s
            .profiles
            .iter()
            .filter_map(|p| {
                let fields = format!("{} {} {} {}", p.display_name, p.host, p.tags.join(" "), p.protocol.id());
                fuzzy_score(&fields, &q).map(|s| (s, p.clone()))
            })
            .collect();
        hits.sort_by_key(|h| std::cmp::Reverse(h.0));
        if hits.is_empty() {
            widgets::empty(ui, icon::MAGNIFYING_GLASS, "No matches", "Try a host, name, tag or protocol.");
        }
        for (_, p) in hits {
            row(app, ui, &p, "search");
        }
        return;
    }

    local_row(app, ui);
    let profiles = app.s.profiles.clone();
    let favs: Vec<&Profile> = profiles.iter().filter(|p| p.favourite).collect();
    if !favs.is_empty() {
        section(app, ui, "★ Favourites", "favourites", &favs);
    }
    let mut recent: Vec<&Profile> = profiles.iter().filter(|p| p.last_connected.is_some()).collect();
    recent.sort_by_key(|p| std::cmp::Reverse(p.last_connected));
    recent.truncate(RECENT);
    if !recent.is_empty() {
        section(app, ui, "Recent", "recent", &recent);
    }
    let mut folders: std::collections::BTreeMap<String, Vec<&Profile>> = Default::default();
    let mut ungrouped = vec![];
    for p in &profiles {
        let f = p.folder.trim();
        if f.is_empty() {
            ungrouped.push(p);
        } else {
            folders.entry(f.to_string()).or_default().push(p);
        }
    }
    let mut names: Vec<&String> = folders.keys().collect();
    names.sort_by_key(|a| a.to_lowercase());
    let has_groups = !names.is_empty();
    for f in names {
        section(app, ui, f, &format!("group:{f}"), &folders[f]);
    }
    if !ungrouped.is_empty() {
        section(app, ui, if has_groups { "Ungrouped" } else { "All Connections" }, "ungrouped", &ungrouped);
    }
    if profiles.is_empty() {
        widgets::empty(ui, icon::PLUGS, "No connections yet", "Create one with the + button below.");
        ui.vertical_centered(|ui| {
            if widgets::primary(ui, icon::PLUS, "New connection").clicked() {
                app.s.request(Request::NewProfile { protocol: None, folder: String::new() });
            }
        });
    }
}

fn header(app: &mut App, ui: &mut Ui, title: &str, key: &str, count: usize) -> bool {
    let t = Tokens::get(ui.ctx());
    let folded = app.sidebar.collapsed.contains(key);
    let (r, resp) = ui.allocate_exact_size(vec2(ui.available_width(), 22.0), Sense::click());
    let c = if resp.hovered() { t.text_dim } else { t.text_faint };
    let caret = if folded { icon::CARET_RIGHT } else { icon::CARET_DOWN };
    ui.painter().text(r.left_center() + vec2(10.0, 0.0), Align2::LEFT_CENTER, caret, egui::FontId::proportional(10.0), c);
    ui.painter().text(r.left_center() + vec2(24.0, 0.0), Align2::LEFT_CENTER, title.to_uppercase(), theme::semibold(10.5), c);
    ui.painter().text(r.right_center() - vec2(10.0, 0.0), Align2::RIGHT_CENTER, count.to_string(), egui::FontId::proportional(10.5), t.text_faint);
    if resp.clicked() {
        if folded {
            app.sidebar.collapsed.remove(key);
        } else {
            app.sidebar.collapsed.insert(key.into());
        }
    }
    // Right-click a folder to add a connection to it.
    if let Some(folder) = key.strip_prefix("group:") {
        resp.context_menu(|ui| {
            if ui.button(format!("{}  New connection in {folder}", icon::PLUS)).clicked() {
                app.s.request(Request::NewProfile { protocol: None, folder: folder.into() });
                ui.close();
            }
        });
    }
    !folded
}

fn section(app: &mut App, ui: &mut Ui, title: &str, key: &str, list: &[&Profile]) {
    ui.add_space(4.0);
    if header(app, ui, title, key, list.len()) {
        for p in list {
            row(app, ui, p, key);
        }
    }
}

fn local_row(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    let (r, resp) = ui.allocate_exact_size(vec2(ui.available_width(), 30.0), Sense::click());
    let r = r.shrink2(vec2(6.0, 1.0));
    if resp.hovered() {
        ui.painter().rect_filled(r, 6.0, t.hover);
    }
    ui.painter().text(
        r.left_center() + vec2(10.0, 0.0),
        Align2::LEFT_CENTER,
        icon::TERMINAL_WINDOW,
        egui::FontId::proportional(15.0),
        crate::views::protocol_color(nexus_core::models::Protocol::Local),
    );
    ui.painter().text(r.left_center() + vec2(32.0, 0.0), Align2::LEFT_CENTER, "Local Machine", egui::FontId::proportional(13.0), t.text);
    if resp.on_hover_text("Open a terminal on this computer").clicked() {
        app.connect_local();
    }
}

fn row(app: &mut App, ui: &mut Ui, p: &Profile, section: &str) {
    let t = Tokens::get(ui.ctx());
    let selected = app.sidebar.selected.contains(&p.id);
    let (r, resp) = ui.allocate_exact_size(vec2(ui.available_width(), 34.0), Sense::click());
    let r = r.shrink2(vec2(6.0, 1.0));
    if selected {
        ui.painter().rect_filled(r, 6.0, t.row_selected);
    } else if resp.hovered() {
        ui.painter().rect_filled(r, 6.0, t.hover);
    }
    let live = app.s.panes.values().any(|pn| pn.info.profile_id == p.id);
    let dot = Rect::from_center_size(r.left_center() + vec2(12.0, 0.0), vec2(8.0, 8.0));
    let col = protocol_color(p.protocol);
    if live {
        ui.painter().circle_filled(dot.center(), 4.0, col);
    } else {
        ui.painter().circle_stroke(dot.center(), 3.5, egui::Stroke::new(1.5, col));
    }
    let name = if p.display_name.is_empty() { &p.host } else { &p.display_name };
    let text_r = Rect::from_min_max(r.min + vec2(24.0, 0.0), r.max - vec2(46.0, 0.0));
    let painter = ui.painter().with_clip_rect(text_r);
    painter.text(text_r.left_top() + vec2(0.0, 3.0), Align2::LEFT_TOP, name, egui::FontId::proportional(13.0), t.text);
    let sub = if p.username.is_empty() || p.protocol == nexus_core::models::Protocol::Serial { p.host.clone() } else { format!("{}@{}", p.username, p.host) };
    painter.text(text_r.left_bottom() - vec2(0.0, 3.0), Align2::LEFT_BOTTOM, sub, egui::FontId::proportional(11.0), t.text_faint);
    ui.painter().text(r.right_center() - vec2(8.0, 0.0), Align2::RIGHT_CENTER, protocol_badge(p.protocol), theme::semibold(9.0), col);
    if p.favourite {
        ui.painter().text(r.right_center() - vec2(36.0, 0.0), Align2::RIGHT_CENTER, "★", egui::FontId::proportional(11.0), t.warning);
    }
    let tip = format!(
        "{}\n{} {}:{}{}",
        name,
        protocol_badge(p.protocol),
        p.host,
        p.port,
        if p.description.is_empty() { String::new() } else { format!("\n{}", p.description) }
    );
    let resp = resp.on_hover_text(tip);
    let multi = ui.input(|i| i.modifiers.command);
    if resp.double_clicked() {
        app.s.request(Request::EditProfile(p.id.clone()));
    } else if resp.clicked() {
        if multi {
            if !app.sidebar.selected.remove(&p.id) {
                app.sidebar.selected.insert(p.id.clone());
            }
        } else {
            app.sidebar.selected.clear();
            app.connect_profile(&p.id, false);
        }
    }
    resp.context_menu(|ui| {
        let _ = section;
        if ui.button(format!("{}  Connect", icon::PLUGS_CONNECTED)).clicked() {
            app.connect_profile(&p.id, false);
            ui.close();
        }
        if ui.button(format!("{}  Edit", icon::PENCIL_SIMPLE)).clicked() {
            app.s.request(Request::EditProfile(p.id.clone()));
            ui.close();
        }
        if ui.button(format!("{}  Duplicate", icon::COPY)).clicked() {
            match app.s.svc.duplicate_profile(&p.id) {
                Ok(d) => app.s.success(format!("Created {}", d.display_name)),
                Err(e) => app.s.error(e.to_string()),
            }
            app.s.reload_profiles();
            ui.close();
        }
        let fav = if p.favourite { "Remove from Favourites" } else { "Add to Favourites" };
        if ui.button(format!("{}  {fav}", icon::STAR)).clicked() {
            let _ = app.s.svc.set_favourite(&p.id, !p.favourite);
            app.s.reload_profiles();
            ui.close();
        }
        if ui.button(format!("{}  Export…", icon::UPLOAD_SIMPLE)).clicked() {
            app.s.request(Request::ExportEncrypted(vec![p.id.clone()]));
            ui.close();
        }
        ui.separator();
        if ui.button(RichText::new(format!("{}  Delete", icon::TRASH)).color(t.danger)).clicked() {
            app.dialog = Some(Dialog::Delete { profiles: vec![p.id.clone()] });
            ui.close();
        }
    });
}

fn bulk_bar(app: &mut App, ui: &mut Ui) {
    let t = Tokens::get(ui.ctx());
    let ids: Vec<String> = app.sidebar.selected.iter().cloned().collect();
    egui::Frame::NONE.fill(t.card).corner_radius(6).inner_margin(6).show(ui, |ui| {
        ui.label(RichText::new(format!("{} selected", ids.len())).size(11.5).color(t.text_dim));
        ui.horizontal_wrapped(|ui| {
            if ui.small_button(format!("{} Connect all", icon::PLUGS_CONNECTED)).clicked() {
                for id in &ids {
                    app.connect_profile(id, true);
                }
                app.sidebar.selected.clear();
            }
            if ui.small_button(format!("{} Disconnect", icon::PLUGS)).clicked() {
                let sessions: Vec<String> = app.s.panes.values().filter(|p| ids.contains(&p.info.profile_id)).map(|p| p.info.id.clone()).collect();
                if sessions.is_empty() {
                    app.s.info("None of the selected connections are open");
                } else if app.s.prefs.confirm_on_disconnect {
                    app.dialog = Some(Dialog::ConfirmDisconnectMany { sessions });
                } else {
                    for s in sessions {
                        app.close_session(&s);
                    }
                }
            }
            ui.menu_button(format!("{} Tags", icon::TAG), |ui| {
                if ui.button("Add tags…").clicked() {
                    app.dialog = Some(Dialog::Input {
                        title: "Add tags".into(),
                        label: "Comma-separated tags to add".into(),
                        value: String::new(),
                        then: InputThen::Tags { profiles: ids.clone(), replace: false },
                    });
                    ui.close();
                }
                if ui.button("Replace tags…").clicked() {
                    app.dialog = Some(Dialog::Input {
                        title: "Replace tags".into(),
                        label: "Comma-separated tags".into(),
                        value: String::new(),
                        then: InputThen::Tags { profiles: ids.clone(), replace: true },
                    });
                    ui.close();
                }
            });
            if ui.small_button(format!("{} Move group", icon::FOLDER)).clicked() {
                app.dialog = Some(Dialog::Input {
                    title: "Move to group".into(),
                    label: "Group name (empty for Ungrouped)".into(),
                    value: String::new(),
                    then: InputThen::MoveGroup { profiles: ids.clone() },
                });
            }
            if ui.small_button(format!("{} Delete", icon::TRASH)).clicked() {
                app.dialog = Some(Dialog::Delete { profiles: ids.clone() });
            }
            if ui.small_button("Clear").clicked() {
                app.sidebar.selected.clear();
            }
        });
    });
}
