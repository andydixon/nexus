//! Port forwards dialog for a live SSH session: list, stop, add.

use crate::chrome::modal;
use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{Align, Key, Layout, RichText};
use egui_phosphor::regular as icon;
use nexus_core::models::{ForwardDirection, PortForward, new_id};
use parking_lot::Mutex;
use std::sync::Arc;

struct Form {
    remote: bool,
    bind_host: String,
    bind_port: String,
    dest_host: String,
    dest_port: String,
    label: String,
}

impl Default for Form {
    fn default() -> Form {
        Form {
            remote: false,
            bind_host: "127.0.0.1".into(),
            bind_port: String::new(),
            dest_host: "localhost".into(),
            dest_port: String::new(),
            label: String::new(),
        }
    }
}

#[derive(Default)]
pub struct Forwards {
    session: Option<String>,
    form: Form,
    /// Worker results: true = a forward started, false = one stopped.
    done: Arc<Mutex<Vec<bool>>>,
}

fn port(s: &str) -> Option<i64> {
    s.trim().parse::<u16>().ok().filter(|&p| p > 0).map(i64::from)
}

impl Forwards {
    pub fn open(&mut self, session: &str) {
        self.session = Some(session.into());
    }

    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        for started in std::mem::take(&mut *self.done.lock()) {
            if started {
                s.success("Forward started");
                self.form.bind_port.clear();
                self.form.dest_port.clear();
                self.form.label.clear();
            } else {
                s.info("Forward stopped");
            }
        }
        let Some(sid) = self.session.clone() else {
            return;
        };
        // A lock and a clone in the client; no network.
        let list = match s.svc.sessions.forwards(&sid) {
            Ok(l) => l,
            Err(_) => {
                s.error("Port forwarding is only available for SSH sessions");
                self.session = None;
                return;
            }
        };
        let t = Tokens::get(ctx);
        let mut keep = !ctx.input(|i| i.key_pressed(Key::Escape));
        let mut stop = None;
        let mut start = false;
        let f = &mut self.form;
        modal(ctx, "port-forwards", 600.0, |ui| {
            ui.horizontal(|ui| {
                ui.label(RichText::new(format!("{}  Port Forwards", icon::ARROWS_LEFT_RIGHT)).font(theme::semibold(15.0)));
                ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                    if widgets::icon_button(ui, icon::X, "Close").clicked() {
                        keep = false;
                    }
                });
            });
            ui.add_space(4.0);
            if list.is_empty() {
                widgets::hint(ui, "No active port forwards");
            }
            for fwd in &list {
                ui.horizontal(|ui| {
                    let local = fwd.direction == ForwardDirection::Local;
                    widgets::chip(ui, if local { "L" } else { "R" }, if local { t.accent } else { t.warning });
                    let or = |s: &str, d: &str| {
                        if s.is_empty() { d.to_string() } else { s.to_string() }
                    };
                    let text = format!("{}:{} → {}:{}", or(&fwd.bind_host, "127.0.0.1"), fwd.bind_port, or(&fwd.dest_host, "localhost"), fwd.dest_port);
                    ui.label(RichText::new(text).font(theme::mono(12.0)));
                    if !fwd.label.is_empty() {
                        ui.label(RichText::new(&fwd.label).size(11.5).color(t.text_faint));
                    }
                    ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                        if ui.add(egui::Button::new(RichText::new(format!("{}  Stop", icon::STOP)).color(t.accent_text)).fill(t.danger)).clicked() {
                            stop = Some(fwd.id.clone());
                        }
                    });
                });
            }
            ui.separator();
            widgets::section(ui, "Add new forward");
            ui.horizontal(|ui| {
                egui::ComboBox::from_id_salt("pf-dir").width(96.0).selected_text(if f.remote { "Remote ←" } else { "Local →" }).show_ui(ui, |ui| {
                    ui.selectable_value(&mut f.remote, false, "Local →");
                    ui.selectable_value(&mut f.remote, true, "Remote ←");
                });
                ui.add(egui::TextEdit::singleline(&mut f.bind_host).hint_text("127.0.0.1").desired_width(110.0));
                ui.add(egui::TextEdit::singleline(&mut f.bind_port).hint_text("Port").desired_width(52.0));
                ui.label(RichText::new(icon::ARROW_RIGHT).color(t.text_faint));
                ui.add(egui::TextEdit::singleline(&mut f.dest_host).hint_text("localhost").desired_width(110.0));
                ui.add(egui::TextEdit::singleline(&mut f.dest_port).hint_text("Port").desired_width(52.0));
            });
            ui.horizontal(|ui| {
                ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                    start = widgets::primary(ui, icon::PLUS, "Start Forward").clicked();
                    ui.add(egui::TextEdit::singleline(&mut f.label).hint_text("Label (optional)").desired_width(f32::INFINITY));
                });
            });
        });

        if let Some(id) = stop {
            let (svc, sid, done) = (s.svc.clone(), sid.clone(), self.done.clone());
            s.spawn(move || svc.sessions.stop_forward(&sid, &id).map_err(|e| format!("Failed to stop: {e}").into()), move |_| done.lock().push(false));
        }
        if start {
            let f = &self.form;
            match (port(&f.bind_port), port(&f.dest_port)) {
                (Some(bind_port), Some(dest_port)) => {
                    let or = |s: &str, d: &str| {
                        if s.trim().is_empty() { d.to_string() } else { s.trim().to_string() }
                    };
                    let rule = PortForward {
                        id: new_id(),
                        direction: if f.remote { ForwardDirection::Remote } else { ForwardDirection::Local },
                        bind_host: or(&f.bind_host, "127.0.0.1"),
                        bind_port,
                        dest_host: or(&f.dest_host, "localhost"),
                        dest_port,
                        enabled: true,
                        label: f.label.trim().into(),
                    };
                    let (svc, done) = (s.svc.clone(), self.done.clone());
                    s.spawn(
                        move || svc.sessions.start_forward(&sid, rule).map_err(|e| format!("Failed to start forward: {e}").into()),
                        move |_| done.lock().push(true),
                    );
                }
                _ => s.error("Bind port and destination port are required"),
            }
        }
        if !keep {
            self.session = None;
        }
    }
}
