//! Profile export/import: encrypted `.nexus` (also reads `.link`) and plain JSON.

use crate::settings::{Job, poll};
use crate::state::{Request, Shared};
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{RichText, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::exportimport::EXTENSION;
use std::collections::HashSet;
use std::path::PathBuf;

const STRATEGIES: [(&str, &str); 3] = [("skip", "Skip existing"), ("overwrite", "Overwrite existing"), ("duplicate", "Create duplicates")];

enum Mode {
    /// `selected` is filled from `preselect` (or every profile) on the first frame.
    Export {
        preselect: Option<Vec<String>>,
        selected: HashSet<String>,
        pw: String,
        confirm: String,
    },
    Import {
        path: PathBuf,
        pw: String,
        strategy: &'static str,
        status: String,
    },
    ExportJson {
        include_secrets: bool,
    },
    ImportJson {
        path: PathBuf,
        strategy: &'static str,
    },
}

enum Done {
    Exported(usize, PathBuf),
    Imported(usize),
}

#[derive(Default)]
pub struct Transfer {
    mode: Option<Mode>,
    job: Option<Job<Result<Done, String>>>,
}

impl Transfer {
    /// Encrypted export of these profiles (all when empty): pick profiles, password, file.
    pub fn export_encrypted(&mut self, ids: Vec<String>) {
        self.mode = Some(Mode::Export { preselect: Some(ids), selected: HashSet::new(), pw: String::new(), confirm: String::new() });
    }

    pub fn import_encrypted(&mut self) {
        if let Some(path) =
            rfd::FileDialog::new().set_title("Import Profiles").add_filter("Nexus Export", &[EXTENSION, "link"]).add_filter("All files", &["*"]).pick_file()
        {
            self.mode = Some(Mode::Import { path, pw: String::new(), strategy: "skip", status: String::new() });
        }
    }

    pub fn export_json(&mut self) {
        self.mode = Some(Mode::ExportJson { include_secrets: false });
    }

    pub fn import_json(&mut self) {
        if let Some(path) = rfd::FileDialog::new().set_title("Import Profiles (JSON)").add_filter("JSON", &["json"]).pick_file() {
            self.mode = Some(Mode::ImportJson { path, strategy: "skip" });
        }
    }

    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        if let Some(r) = poll(&mut self.job) {
            match r {
                Ok(Done::Exported(n, path)) => {
                    s.success(format!("Exported {n} profile(s) to {}", path.display()));
                    self.mode = None;
                }
                Ok(Done::Imported(n)) => {
                    s.success(format!("Imported {n} profile(s)"));
                    s.request(Request::ReloadProfiles);
                    self.mode = None;
                }
                Err(e) => {
                    if let Some(Mode::Import { status, .. }) = &mut self.mode {
                        *status = e.split_whitespace().collect::<Vec<_>>().join(" ");
                        s.error("Import failed");
                    } else {
                        s.error(e);
                    }
                }
            }
        }
        let Some(mode) = &mut self.mode else { return };
        if let Mode::Export { preselect: pre @ Some(_), selected, .. } = mode {
            if s.profiles.is_empty() {
                s.info("No profiles to export.");
                self.mode = None;
                return;
            }
            let ids = pre.take().unwrap_or_default();
            *selected = if ids.is_empty() { s.profiles.iter().map(|p| p.id.clone()).collect() } else { ids.into_iter().collect() };
        }
        let busy = self.job.is_some();
        let mut close = false;
        let mut start: Option<Box<dyn FnOnce() -> Result<Done, String> + Send>> = None;
        let r = egui::Modal::new(egui::Id::new("transfer")).show(ctx, |ui| {
            let t = Tokens::get(ui.ctx());
            match mode {
                Mode::Export { selected, pw, confirm, .. } => {
                    ui.set_width(440.0);
                    title(ui, icon::EXPORT, "Export Profiles (Encrypted)");
                    ui.horizontal(|ui| {
                        ui.label(RichText::new("Select profiles to export").color(t.text_dim));
                        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                            if ui.small_button("None").clicked() {
                                selected.clear();
                            }
                            if ui.small_button("All").clicked() {
                                *selected = s.profiles.iter().map(|p| p.id.clone()).collect();
                            }
                        });
                    });
                    egui::Frame::new().fill(t.field).corner_radius(t.radius).inner_margin(6).show(ui, |ui| {
                        egui::ScrollArea::vertical().max_height(200.0).auto_shrink([false, true]).show(ui, |ui| {
                            for p in &s.profiles {
                                let mut on = selected.contains(&p.id);
                                let name = if p.display_name.is_empty() { &p.host } else { &p.display_name };
                                let label = format!("{name}  ({})", p.protocol.id());
                                if ui.checkbox(&mut on, label).changed() {
                                    if on {
                                        selected.insert(p.id.clone())
                                    } else {
                                        selected.remove(&p.id)
                                    };
                                }
                            }
                        });
                    });
                    ui.add_space(4.0);
                    ui.label("Export password");
                    ui.add(egui::TextEdit::singleline(pw).password(true).hint_text("Enter a strong password").desired_width(f32::INFINITY));
                    crate::settings::strength_meter(ui, pw);
                    widgets::hint(ui, "This password will be required to import the file.");
                    ui.label("Confirm password");
                    ui.add(egui::TextEdit::singleline(confirm).password(true).hint_text("Confirm password").desired_width(f32::INFINITY));
                    if buttons(ui, busy, icon::EXPORT, "Export", &mut close) {
                        if pw.is_empty() {
                            s.error("Please enter a password.");
                        } else if pw != confirm {
                            s.error("Passwords do not match.");
                        } else if selected.is_empty() {
                            s.error("Please select at least one profile.");
                        } else if let Some(path) = rfd::FileDialog::new()
                            .set_title("Export Profiles")
                            .set_file_name(format!("nexus-profiles.{EXTENSION}"))
                            .add_filter("Nexus Export", &[EXTENSION])
                            .save_file()
                        {
                            // Keep the sidebar's order.
                            let ids: Vec<String> = s.profiles.iter().filter(|p| selected.contains(&p.id)).map(|p| p.id.clone()).collect();
                            let (svc, pw) = (s.svc.clone(), pw.clone());
                            start = Some(Box::new(move || {
                                svc.export_encrypted(&ids, &pw, &path).map(|p| Done::Exported(ids.len(), p)).map_err(|e| format!("Export failed: {e}"))
                            }));
                        }
                    }
                }
                Mode::Import { path, pw, strategy, status } => {
                    ui.set_width(420.0);
                    title(ui, icon::DOWNLOAD_SIMPLE, "Import Profiles (Encrypted)");
                    file_line(ui, path);
                    ui.label("Decryption password");
                    let r = ui.add(egui::TextEdit::singleline(pw).password(true).hint_text("Enter the export password").desired_width(f32::INFINITY));
                    if pw.is_empty() && !busy {
                        r.request_focus();
                    }
                    let enter = r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter));
                    strategy_combo(ui, strategy);
                    if !status.is_empty() {
                        ui.label(RichText::new(status.as_str()).color(t.danger).size(12.0));
                    }
                    if buttons(ui, busy, icon::DOWNLOAD_SIMPLE, "Import", &mut close) || (enter && !busy) {
                        if pw.is_empty() {
                            *status = "Please enter the decryption password.".into();
                        } else {
                            status.clear();
                            let (svc, pw, path, strategy) = (s.svc.clone(), pw.clone(), path.clone(), *strategy);
                            start = Some(Box::new(move || svc.import_encrypted(&path, &pw, strategy).map(Done::Imported).map_err(|e| e.to_string())));
                        }
                    }
                }
                Mode::ExportJson { include_secrets } => {
                    ui.set_width(400.0);
                    title(ui, icon::FILE_ARROW_UP, "Export Profiles (JSON)");
                    ui.label(format!("Exports all {} profile(s) as plain JSON.", s.profiles.len()));
                    ui.checkbox(include_secrets, "Include secrets (proxy passwords, TOTP secrets)");
                    if *include_secrets {
                        ui.label(
                            RichText::new(format!("{}  The file will contain secrets in plain text. Prefer the encrypted export.", icon::WARNING))
                                .color(t.warning)
                                .size(12.0),
                        );
                    }
                    if buttons(ui, false, icon::FILE_ARROW_UP, "Export", &mut close)
                        && let Some(path) = rfd::FileDialog::new()
                            .set_title("Export Profiles (JSON)")
                            .set_file_name("nexus-profiles.json")
                            .add_filter("JSON", &["json"])
                            .save_file()
                    {
                        match s
                            .svc
                            .export_json(*include_secrets)
                            .map_err(|e| e.to_string())
                            .and_then(|j| nexus_core::secrets::write_private(&path, j.as_bytes()).map_err(|e| e.to_string()))
                        {
                            Ok(()) => {
                                s.success(format!("Exported {} profile(s) to {}", s.profiles.len(), path.display()));
                                close = true;
                            }
                            Err(e) => s.error(format!("Export failed: {e}")),
                        }
                    }
                }
                Mode::ImportJson { path, strategy } => {
                    ui.set_width(400.0);
                    title(ui, icon::FILE_ARROW_DOWN, "Import Profiles (JSON)");
                    file_line(ui, path);
                    strategy_combo(ui, strategy);
                    if buttons(ui, false, icon::FILE_ARROW_DOWN, "Import", &mut close) {
                        match std::fs::read_to_string(&path).map_err(|e| e.to_string()).and_then(|j| s.svc.import_json(&j, strategy).map_err(|e| e.to_string()))
                        {
                            Ok(n) => {
                                s.success(format!("Imported {n} profile(s)"));
                                s.request(Request::ReloadProfiles);
                                close = true;
                            }
                            Err(e) => s.error(format!("Import failed: {e}")),
                        }
                    }
                }
            }
        });
        if let Some(f) = start {
            self.job = Some(Job::spawn(ctx, f));
        }
        if (close || r.should_close()) && self.job.is_none() {
            self.mode = None;
        }
    }
}

fn title(ui: &mut Ui, glyph: &str, text: &str) {
    ui.label(RichText::new(format!("{glyph}  {text}")).font(theme::semibold(15.0)));
    ui.add_space(4.0);
}

fn file_line(ui: &mut Ui, path: &std::path::Path) {
    let t = Tokens::get(ui.ctx());
    let name = path.file_name().map_or_else(|| path.display().to_string(), |n| n.to_string_lossy().into());
    ui.label(RichText::new(format!("{}  {name}", icon::FILE)).color(t.text_dim)).on_hover_text(path.display().to_string());
}

fn strategy_combo(ui: &mut Ui, strategy: &mut &'static str) {
    ui.horizontal(|ui| {
        ui.label("If a profile already exists");
        let cur = STRATEGIES.iter().find(|s| s.0 == *strategy).map_or("", |s| s.1);
        egui::ComboBox::from_id_salt("import-strategy").selected_text(cur).show_ui(ui, |ui| {
            for (id, label) in STRATEGIES {
                ui.selectable_value(strategy, id, label);
            }
        });
    });
}

/// Primary + Cancel row; true when the primary button is clicked.
fn buttons(ui: &mut Ui, busy: bool, glyph: &str, label: &str, close: &mut bool) -> bool {
    ui.add_space(8.0);
    let mut go = false;
    ui.horizontal(|ui| {
        ui.spacing_mut().item_spacing = vec2(8.0, 0.0);
        if busy {
            ui.spinner();
            ui.label("Working…");
        } else {
            go = widgets::primary(ui, glyph, label).clicked();
            if ui.button("Cancel").clicked() {
                *close = true;
            }
        }
    });
    go
}
