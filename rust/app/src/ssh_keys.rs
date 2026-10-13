// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! SSH Key Manager window: key pairs in ~/.ssh — copy public key, delete, generate (in-process, OpenSSH format).

use crate::settings::{Job, poll};
use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{RichText, vec2};
use egui_phosphor::regular as icon;
use nexus_core::service::SshKeyInfo;

struct Generate {
    name: String,
    kind: &'static str,
    bits: u32,
    pass: String,
    confirm: String,
}

#[derive(Default)]
pub struct SshKeys {
    open: bool,
    /// Reload the list on the next frame.
    stale: bool,
    keys: Vec<SshKeyInfo>,
    load_error: Option<String>,
    confirm_delete: Option<String>,
    form: Option<Generate>,
    job: Option<Job<Result<String, String>>>,
}

impl SshKeys {
    pub fn open(&mut self) {
        self.open = true;
        self.stale = true;
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        match poll(&mut self.job) {
            Some(Ok(name)) => {
                s.success(format!("Key \"{name}\" generated successfully"));
                self.form = None;
                self.stale = true;
            }
            Some(Err(e)) => s.error(format!("Generation failed: {e}")),
            None => {}
        }
        if !self.open {
            return;
        }
        if std::mem::take(&mut self.stale) {
            match s.svc.ssh_key_pairs() {
                Ok(k) => (self.keys, self.load_error) = (k, None),
                Err(e) => self.load_error = Some(e.to_string()),
            }
        }
        let t = Tokens::get(ctx);
        let mut open = true;
        egui::Window::new(RichText::new(format!("{}  SSH Key Manager", icon::KEY)).font(theme::semibold(14.0)))
            .id(egui::Id::new("ssh-keys-window"))
            .open(&mut open)
            .collapsible(false)
            .pivot(egui::Align2::CENTER_CENTER)
            .default_pos(ctx.content_rect().center())
            .resizable(true)
            .default_size([620.0, 460.0])
            .min_size([480.0, 300.0])
            .show(ctx, |ui| {
                widgets::section(ui, "Key pairs in ~/.ssh");
                if let Some(e) = &self.load_error {
                    ui.label(RichText::new(format!("Failed to load keys: {e}")).color(t.danger));
                } else if self.keys.is_empty() {
                    widgets::empty(ui, icon::KEY, "No SSH keys found", "Generate a key pair below.");
                }
                let list_h = if self.form.is_some() { 150.0 } else { (ui.available_height() - 40.0).max(120.0) };
                egui::ScrollArea::vertical().id_salt("keys").max_height(list_h).auto_shrink([false, true]).show(ui, |ui| {
                    for k in &self.keys {
                        egui::Frame::new().fill(t.field).corner_radius(t.radius).inner_margin(8).show(ui, |ui| {
                            ui.set_width(ui.available_width());
                            ui.horizontal(|ui| {
                                ui.label(RichText::new(&k.name).font(theme::semibold(13.0)));
                                widgets::chip(ui, &k.kind.to_uppercase(), t.accent);
                                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                                    if widgets::icon_button(ui, icon::TRASH, "Delete").clicked() {
                                        self.confirm_delete = Some(k.name.clone());
                                    }
                                    if widgets::icon_button(ui, icon::COPY, "Copy public key").clicked() {
                                        ui.ctx().copy_text(k.public_key.clone());
                                        s.success("Public key copied to clipboard");
                                    }
                                });
                            });
                            ui.add(egui::Label::new(RichText::new(&k.public_key).font(theme::mono(11.0)).color(t.text_dim)).truncate())
                                .on_hover_text(&k.public_key);
                        });
                        ui.add_space(2.0);
                    }
                });
                ui.add_space(6.0);
                match &mut self.form {
                    None => {
                        if widgets::primary(ui, icon::PLUS, "Generate New Key").clicked() {
                            self.form = Some(Generate { name: String::new(), kind: "ed25519", bits: 4096, pass: String::new(), confirm: String::new() });
                        }
                    }
                    Some(f) => {
                        if generate_form(ui, f, self.job.is_some()) {
                            let name = f.name.trim().to_string();
                            if name.is_empty() {
                                s.error("Key name is required");
                            } else if f.pass != f.confirm {
                                s.error("Passphrases do not match");
                            } else {
                                let (svc, kind, bits, pass) = (s.svc.clone(), f.kind, f.bits, f.pass.clone());
                                self.job =
                                    Some(Job::spawn(ctx, move || svc.generate_ssh_key(&name, kind, bits, &pass).map(|_| name).map_err(|e| e.to_string())));
                            }
                        }
                        if self.job.is_none() && ui.button("Cancel").clicked() {
                            self.form = None;
                        }
                    }
                }
            });
        self.open = open;
        self.delete_dialog(ctx, s);
    }

    fn delete_dialog(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(name) = self.confirm_delete.clone() else {
            return;
        };
        let t = Tokens::get(ctx);
        let mut done = false;
        let r = egui::Modal::new(egui::Id::new("delete-key")).show(ctx, |ui| {
            ui.set_width(360.0);
            ui.label(RichText::new("Delete Key").font(theme::semibold(15.0)));
            ui.label(format!("Delete key pair \"{name}\"? This cannot be undone."));
            ui.add_space(8.0);
            ui.horizontal(|ui| {
                if ui.add(egui::Button::new(RichText::new(format!("{}  Delete", icon::TRASH)).color(t.accent_text)).fill(t.danger)).clicked() {
                    match s.svc.delete_ssh_key(&name) {
                        Ok(()) => s.success(format!("Key \"{name}\" deleted")),
                        Err(e) => s.error(format!("Delete failed: {e}")),
                    }
                    self.stale = true;
                    done = true;
                }
                if ui.button("Cancel").clicked() {
                    done = true;
                }
            });
        });
        if done || r.should_close() {
            self.confirm_delete = None;
        }
    }
}

/// The generate form; true when Generate is clicked.
fn generate_form(ui: &mut egui::Ui, f: &mut Generate, busy: bool) -> bool {
    widgets::section(ui, "Generate new key");
    let mut go = false;
    egui::Grid::new("gen-key").num_columns(2).spacing(vec2(14.0, 8.0)).show(ui, |ui| {
        ui.label("Key name");
        ui.add(egui::TextEdit::singleline(&mut f.name).hint_text(format!("id_{}_nexus", f.kind)).desired_width(260.0));
        ui.end_row();
        ui.label("Key type");
        let label = |k: &str| match k {
            "rsa" => "RSA",
            "ecdsa" => "ECDSA",
            _ => "Ed25519 (recommended)",
        };
        egui::ComboBox::from_id_salt("key-type").selected_text(label(f.kind)).width(260.0).show_ui(ui, |ui| {
            for k in ["ed25519", "rsa", "ecdsa"] {
                ui.selectable_value(&mut f.kind, k, label(k));
            }
        });
        ui.end_row();
        if f.kind == "rsa" {
            ui.label("Key size");
            ui.horizontal(|ui| {
                ui.selectable_value(&mut f.bits, 4096, "4096");
                ui.selectable_value(&mut f.bits, 2048, "2048");
            });
            ui.end_row();
        }
        ui.label("Passphrase");
        ui.add(egui::TextEdit::singleline(&mut f.pass).password(true).hint_text("Leave blank for no passphrase").desired_width(260.0));
        ui.end_row();
        ui.label("Confirm");
        ui.add(egui::TextEdit::singleline(&mut f.confirm).password(true).desired_width(260.0));
        ui.end_row();
    });
    ui.add_space(4.0);
    ui.horizontal(|ui| {
        if busy {
            ui.spinner();
            ui.label("Generating…");
        } else {
            go = widgets::primary(ui, icon::KEY, "Generate").clicked();
        }
    });
    go
}
