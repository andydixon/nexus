//! Snippet library window and the {{variable}} prompt (port of snippets.js).

use crate::state::{Request, Shared};
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{Key, RichText};
use egui_phosphor::regular as icon;
use nexus_core::models::{SessionState, Snippet};
use regex::Regex;
use std::collections::BTreeMap;
use std::sync::LazyLock;

static VAR_RE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\{\{([^}]+)\}\}").unwrap());

/// Replaces built-ins (`hostname`, `date`, `time`, `timestamp`, case-insensitive) and returns the
/// remaining user variable names, de-duplicated in order of appearance.
pub fn expand_builtins(cmd: &str, hostname: &str) -> (String, Vec<String>) {
    let now = chrono::Local::now();
    let mut vars: Vec<String> = vec![];
    let out = VAR_RE.replace_all(cmd, |c: &regex::Captures| {
        let name = c[1].trim();
        let v = match name.to_lowercase().as_str() {
            "hostname" => Some(if hostname.is_empty() { "unknown".to_owned() } else { hostname.to_owned() }),
            // Same formats as JS: toISOString() date (UTC), toTimeString() HH:MM:SS (local), epoch secs.
            "date" => Some(now.with_timezone(&chrono::Utc).format("%Y-%m-%d").to_string()),
            "time" => Some(now.format("%H:%M:%S").to_string()),
            "timestamp" => Some(now.timestamp().to_string()),
            _ => None,
        };
        v.unwrap_or_else(|| {
            if !vars.iter().any(|x| x == name) {
                vars.push(name.to_owned());
            }
            c[0].to_owned()
        })
    });
    (out.into_owned(), vars)
}

/// Fills user variables (`vars[i]` ← `values[i]`) left by `expand_builtins`.
pub fn fill(resolved: &str, vars: &[String], values: &[String]) -> String {
    VAR_RE
        .replace_all(resolved, |c: &regex::Captures| match vars.iter().position(|v| v == c[1].trim()) {
            Some(i) => values.get(i).cloned().unwrap_or_default(),
            None => c[0].to_owned(),
        })
        .into_owned()
}

enum Pane {
    Empty(&'static str),
    Detail(String),
    Edit(Snippet),
}

struct VarForm {
    resolved: String,
    vars: Vec<String>,
    values: Vec<String>,
}

pub struct Snippets {
    open: bool,
    list: Vec<Snippet>,
    search: String,
    pane: Pane,
    confirm_delete: Option<String>,
    vars: Option<VarForm>,
    loaded: bool,
}

impl Default for Snippets {
    fn default() -> Self {
        Snippets {
            open: false,
            list: vec![],
            search: String::new(),
            pane: Pane::Empty("Select a snippet or create a new one"),
            confirm_delete: None,
            vars: None,
            loaded: false,
        }
    }
}

impl Snippets {
    pub fn open(&mut self) {
        self.open = true;
        self.loaded = false;
        self.pane = Pane::Empty("Select a snippet or create a new one");
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        self.var_form(ctx, s);
        self.delete_dialog(ctx, s);
        if !self.open {
            return;
        }
        if !self.loaded {
            self.list = s.svc.snippets();
            self.loaded = true;
        }
        let mut open = self.open;
        egui::Window::new(format!("{}  Snippets", icon::CODE))
            .id(egui::Id::new("snippets"))
            .pivot(egui::Align2::CENTER_CENTER)
            .default_pos(ctx.content_rect().center())
            .open(&mut open)
            .collapsible(false)
            .default_size([720.0, 440.0])
            .show(ctx, |ui| {
                ui.horizontal_top(|ui| {
                    let h = ui.available_height().max(400.0);
                    ui.allocate_ui(egui::vec2(300.0, h), |ui| {
                        ui.set_width(300.0);
                        ui.vertical(|ui| self.list_ui(ui));
                    });
                    ui.separator();
                    ui.vertical(|ui| {
                        ui.set_min_height(h);
                        self.pane_ui(ui, s);
                    });
                });
            });
        self.open = open;
    }

    fn list_ui(&mut self, ui: &mut egui::Ui) {
        let t = Tokens::get(ui.ctx());
        ui.horizontal(|ui| {
            ui.add(egui::TextEdit::singleline(&mut self.search).hint_text("Search snippets…").desired_width(200.0));
            if widgets::primary(ui, icon::PLUS, "New").clicked() {
                self.pane = Pane::Edit(blank());
            }
        });
        let q = self.search.trim().to_lowercase();
        let mut groups: BTreeMap<&str, Vec<&Snippet>> = BTreeMap::new();
        for sn in &self.list {
            if q.is_empty() || [&sn.name, &sn.command, &sn.description].iter().any(|f| f.to_lowercase().contains(&q)) {
                groups.entry(sn.folder.as_str()).or_default().push(sn);
            }
        }
        if groups.is_empty() {
            widgets::empty(ui, icon::CODE, "No snippets found", if q.is_empty() { "Create one with New" } else { "Try another search" });
            return;
        }
        let selected = match &self.pane {
            Pane::Detail(id) => Some(id.clone()),
            _ => None,
        };
        let mut pick = None;
        egui::ScrollArea::vertical().id_salt("snippet-list").auto_shrink([false, false]).show(ui, |ui| {
            // Ungrouped ("") sorts first, folders alphabetically after.
            for (folder, items) in groups {
                if !folder.is_empty() {
                    widgets::section(ui, &format!("{}  {folder}", icon::FOLDER));
                }
                for sn in items {
                    let sel = selected.as_deref() == Some(sn.id.as_str());
                    let r = egui::Frame::NONE
                        .fill(if sel { t.row_selected } else { egui::Color32::TRANSPARENT })
                        .corner_radius(t.radius)
                        .inner_margin(egui::Margin::symmetric(6, 4))
                        .show(ui, |ui| {
                            ui.set_width(ui.available_width());
                            ui.style_mut().wrap_mode = Some(egui::TextWrapMode::Truncate);
                            ui.label(RichText::new(&sn.name).font(theme::semibold(13.0)));
                            ui.label(RichText::new(&sn.command).font(theme::mono(11.5)).color(t.text_dim));
                        })
                        .response
                        .interact(egui::Sense::click());
                    if r.hovered() && !sel {
                        ui.painter().rect_filled(r.rect, t.radius, t.hover.gamma_multiply(0.4));
                    }
                    if r.clicked() {
                        pick = Some(sn.id.clone());
                    }
                }
            }
        });
        if let Some(id) = pick {
            self.pane = Pane::Detail(id);
        }
    }

    fn pane_ui(&mut self, ui: &mut egui::Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        match &mut self.pane {
            Pane::Empty(msg) => {
                widgets::empty(ui, icon::TERMINAL_WINDOW, msg, "Snippets are sent to the active session");
            }
            Pane::Detail(id) => {
                let Some(sn) = self.list.iter().find(|x| &x.id == id).cloned() else {
                    self.pane = Pane::Empty("Select a snippet or create a new one");
                    return;
                };
                ui.label(RichText::new(&sn.name).font(theme::semibold(15.0)));
                if !sn.description.is_empty() {
                    ui.label(RichText::new(&sn.description).color(t.text_dim));
                }
                if !sn.folder.is_empty() {
                    widgets::chip(ui, &sn.folder, t.accent);
                }
                egui::Frame::NONE.fill(t.field).corner_radius(t.radius).inner_margin(10).show(ui, |ui| {
                    ui.set_width(ui.available_width());
                    egui::ScrollArea::vertical().max_height(220.0).show(ui, |ui| {
                        ui.add(egui::Label::new(RichText::new(&sn.command).font(theme::mono(12.5))).wrap());
                    });
                });
                ui.add_space(6.0);
                ui.horizontal(|ui| {
                    if widgets::primary(ui, icon::PAPER_PLANE_RIGHT, "Send to Session").clicked() {
                        self.send(s, &sn.command);
                    }
                    if widgets::button(ui, icon::PENCIL_SIMPLE, "Edit").clicked() {
                        self.pane = Pane::Edit(sn.clone());
                    }
                    if widgets::button(ui, icon::TRASH, "Delete").clicked() {
                        self.confirm_delete = Some(sn.id.clone());
                    }
                });
            }
            Pane::Edit(ed) => {
                let is_new = ed.id.is_empty();
                ui.label(RichText::new(if is_new { "New Snippet" } else { "Edit Snippet" }).font(theme::semibold(14.0)));
                egui::Grid::new("snippet-editor").num_columns(2).spacing([10.0, 6.0]).show(ui, |ui| {
                    ui.label("Name");
                    ui.add(egui::TextEdit::singleline(&mut ed.name).desired_width(f32::INFINITY));
                    ui.end_row();
                    ui.label("Description");
                    ui.add(egui::TextEdit::singleline(&mut ed.description).desired_width(f32::INFINITY));
                    ui.end_row();
                    ui.label("Command");
                    ui.add(egui::TextEdit::multiline(&mut ed.command).font(theme::mono(12.5)).desired_rows(8).desired_width(f32::INFINITY));
                    ui.end_row();
                    ui.label("Folder");
                    ui.add(egui::TextEdit::singleline(&mut ed.folder).hint_text("e.g. Networking, Debugging").desired_width(f32::INFINITY));
                    ui.end_row();
                });
                widgets::hint(ui, "Variables: {{hostname}} {{date}} {{time}} {{timestamp}}, or {{anything}} to be asked when sending.");
                ui.add_space(6.0);
                let (mut save, mut cancel) = (false, false);
                ui.horizontal(|ui| {
                    save = widgets::primary(ui, icon::FLOPPY_DISK, "Save").clicked();
                    cancel = ui.button("Cancel").clicked();
                });
                if cancel {
                    self.pane = if is_new { Pane::Empty("Select a snippet or create a new one") } else { Pane::Detail(ed.id.clone()) };
                } else if save {
                    let mut sn = ed.clone();
                    sn.name = sn.name.trim().to_owned();
                    sn.description = sn.description.trim().to_owned();
                    sn.folder = sn.folder.trim().to_owned();
                    if sn.name.is_empty() {
                        s.error("Name is required");
                    } else if sn.command.is_empty() {
                        s.error("Command is required");
                    } else {
                        match s.svc.save_snippet(sn) {
                            Ok(saved) => {
                                s.info(if is_new { "Snippet created" } else { "Snippet updated" });
                                self.list = s.svc.snippets();
                                self.pane = Pane::Detail(saved.id);
                            }
                            Err(e) => s.error(format!("Failed to save snippet: {e}")),
                        }
                    }
                }
            }
        }
    }

    /// Resolves variables then types the command (plus newline) into the focused session.
    fn send(&mut self, s: &mut Shared, command: &str) {
        let Some(id) = s.focused_session() else {
            return s.error("No active session");
        };
        if s.session_state(&id) != Some(SessionState::Connected) {
            return s.error("Session is not connected");
        }
        let host = s.pane(&id).map(|p| p.info.host.clone()).unwrap_or_default();
        let (resolved, vars) = expand_builtins(command, &host);
        if vars.is_empty() {
            send_text(s, resolved);
        } else {
            let values = vec![String::new(); vars.len()];
            self.vars = Some(VarForm { resolved, vars, values });
        }
    }

    fn var_form(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(f) = &mut self.vars else { return };
        let (mut send, mut cancel) = (false, crate::chrome::modal_key(ctx, Key::Escape));
        crate::chrome::modal(ctx, "snippet-vars", 360.0, |ui| {
            ui.label(RichText::new("Snippet Variables").font(theme::semibold(15.0)));
            ui.add_space(4.0);
            for (i, (name, value)) in f.vars.iter().zip(f.values.iter_mut()).enumerate() {
                ui.label(name);
                let r = ui.add(egui::TextEdit::singleline(value).hint_text(format!("Enter value for {name}")).desired_width(f32::INFINITY));
                if i == 0 && !ui.ctx().memory(|m| m.focused().is_some()) {
                    r.request_focus();
                }
            }
            ui.add_space(8.0);
            ui.horizontal(|ui| {
                send = widgets::primary(ui, icon::PAPER_PLANE_RIGHT, "Send").clicked() || crate::chrome::modal_key(ui.ctx(), Key::Enter);
                cancel |= ui.button("Cancel").clicked();
            });
        });
        if send {
            let text = fill(&f.resolved, &f.vars, &f.values);
            self.vars = None;
            send_text(s, text);
        } else if cancel {
            self.vars = None;
        }
    }

    fn delete_dialog(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(id) = self.confirm_delete.clone() else {
            return;
        };
        let name = self.list.iter().find(|x| x.id == id).map(|x| x.name.clone()).unwrap_or_default();
        let (mut yes, mut no) = (false, crate::chrome::modal_key(ctx, Key::Escape));
        crate::chrome::modal(ctx, "snippet-delete", 360.0, |ui| {
            ui.label(RichText::new("Delete snippet?").font(theme::semibold(15.0)));
            ui.label(format!("\u{201c}{name}\u{201d} will be permanently deleted."));
            ui.add_space(8.0);
            ui.horizontal(|ui| {
                yes = widgets::primary(ui, icon::TRASH, "Delete").clicked();
                no |= ui.button("Cancel").clicked();
            });
        });
        if yes {
            self.confirm_delete = None;
            match s.svc.delete_snippet(&id) {
                Ok(()) => {
                    self.list.retain(|x| x.id != id);
                    self.pane = Pane::Empty("Snippet deleted");
                }
                Err(e) => s.error(format!("Failed to delete: {e}")),
            }
        } else if no {
            self.confirm_delete = None;
        }
    }
}

fn blank() -> Snippet {
    let now = nexus_core::models::now();
    Snippet {
        id: String::new(),
        name: String::new(),
        command: String::new(),
        description: String::new(),
        folder: String::new(),
        tags: vec![],
        created_at: now,
        updated_at: now,
    }
}

fn send_text(s: &mut Shared, text: String) {
    s.request(Request::SendToActive(text + "\n"));
    s.info("Snippet sent");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn builtins_and_prompted_vars() {
        let (out, vars) = expand_builtins("ssh {{ HOSTNAME }} {{user}} {{user}} {{port}} at {{timestamp}}", "box");
        assert!(out.starts_with("ssh box {{user}} {{user}} {{port}} at "));
        assert!(out.rsplit(' ').next().unwrap().parse::<i64>().is_ok());
        assert_eq!(vars, ["user", "port"]);
        assert_eq!(fill(&out, &vars, &["root".into(), "22".into()]).split(" at ").next().unwrap(), "ssh box root root 22");
        let (out, _) = expand_builtins("{{hostname}} {{date}} {{time}}", "");
        let parts: Vec<&str> = out.split(' ').collect();
        assert_eq!(parts[0], "unknown");
        assert!(chrono::NaiveDate::parse_from_str(parts[1], "%Y-%m-%d").is_ok());
        assert!(chrono::NaiveTime::parse_from_str(parts[2], "%H:%M:%S").is_ok());
        assert_eq!(expand_builtins("echo {not} {{}}", "h"), ("echo {not} {{}}".into(), vec![]));
    }
}
