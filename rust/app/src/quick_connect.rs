//! Quick Connect dialog: an ad-hoc session without saving a profile.

use crate::chrome::modal;
use crate::state::{Request, Shared};
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{ComboBox, DragValue, Key, RichText, TextEdit};
use egui_phosphor::regular as icon;
use nexus_core::clients::serial;
use nexus_core::models::{Profile, Protocol};

const PROTOCOLS: [Protocol; 5] = [Protocol::Ssh, Protocol::Telnet, Protocol::Raw, Protocol::Irc, Protocol::Serial];

pub struct QuickConnect {
    open: bool,
    focus: bool,
    protocol: Protocol,
    host: String,
    port: i64,
    username: String,
    nick: String,
    ports: Vec<String>,
}

impl Default for QuickConnect {
    fn default() -> Self {
        QuickConnect {
            open: false,
            focus: false,
            protocol: Protocol::Ssh,
            host: String::new(),
            port: 22,
            username: String::new(),
            nick: "nexus".into(),
            ports: vec![],
        }
    }
}

impl QuickConnect {
    pub fn open(&mut self) {
        self.open = true;
        self.focus = true;
        self.host.clear();
        if self.protocol == Protocol::Serial {
            self.ports = serial::list_ports();
        }
    }

    fn set_protocol(&mut self, p: Protocol) {
        self.protocol = p;
        self.port = Profile::new(p).port;
        if p == Protocol::Serial {
            self.ports = serial::list_ports();
        }
    }

    /// The profile to connect, or None when the host is empty.
    fn profile(&self) -> Option<Profile> {
        let host = self.host.trim();
        if host.is_empty() {
            return None;
        }
        let mut p = Profile::new(self.protocol);
        p.host = host.into();
        p.display_name = host.into();
        if self.protocol != Protocol::Serial {
            p.port = self.port;
        }
        if matches!(self.protocol, Protocol::Ssh | Protocol::Irc) {
            p.username = self.username.trim().into();
        }
        if let Some(c) = &mut p.irc
            && !self.nick.trim().is_empty()
        {
            c.nick = self.nick.trim().into();
        }
        Some(p)
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        if !self.open {
            return;
        }
        let t = Tokens::get(ctx);
        let (enter, esc) = ctx.input(|i| (i.key_pressed(Key::Enter), i.key_pressed(Key::Escape)));
        let mut connect = enter;
        let mut close = esc;
        let label = |ui: &mut egui::Ui, l: &str| ui.label(RichText::new(l).color(t.text_dim).size(12.0));
        modal(ctx, "quick-connect", 360.0, |ui| {
            ui.label(RichText::new(format!("{}  Quick Connect", icon::LIGHTNING)).font(theme::semibold(15.0)));
            ui.add_space(4.0);
            label(ui, "Protocol");
            let mut proto = self.protocol;
            ComboBox::from_id_salt("qc-protocol").width(ui.available_width()).selected_text(proto.label()).show_ui(ui, |ui| {
                for p in PROTOCOLS {
                    ui.selectable_value(&mut proto, p, p.label());
                }
            });
            if proto != self.protocol {
                self.set_protocol(proto);
            }
            let serial = self.protocol == Protocol::Serial;
            label(ui, if serial { "Device" } else { "Host" });
            let r = ui
                .horizontal(|ui| {
                    let w = if serial { ui.available_width() - 34.0 } else { ui.available_width() };
                    let r = ui
                        .add(TextEdit::singleline(&mut self.host).hint_text(if serial { "/dev/ttyUSB0 or COM3" } else { "e.g. 192.168.1.1" }).desired_width(w));
                    if serial {
                        ui.menu_button(icon::CARET_DOWN, |ui| {
                            if self.ports.is_empty() {
                                widgets::hint(ui, "No serial devices found.");
                            }
                            for p in &self.ports {
                                if ui.selectable_label(self.host == *p, p).clicked() {
                                    self.host = p.clone();
                                    ui.close();
                                }
                            }
                            if ui.button(format!("{}  Refresh", icon::ARROWS_CLOCKWISE)).clicked() {
                                self.ports = serial::list_ports();
                            }
                        });
                    }
                    r
                })
                .inner;
            if std::mem::take(&mut self.focus) {
                r.request_focus();
            }
            if !serial {
                label(ui, "Port");
                ui.add(DragValue::new(&mut self.port).range(1..=65535).speed(0.25));
            }
            if matches!(self.protocol, Protocol::Ssh | Protocol::Irc) {
                label(ui, "Username");
                ui.add(TextEdit::singleline(&mut self.username).hint_text("root").desired_width(f32::INFINITY));
            }
            if self.protocol == Protocol::Irc {
                label(ui, "Nick");
                ui.add(TextEdit::singleline(&mut self.nick).desired_width(f32::INFINITY));
            }
            ui.add_space(8.0);
            ui.horizontal(|ui| {
                if widgets::primary(ui, icon::PLUGS_CONNECTED, "Connect").clicked() {
                    connect = true;
                }
                if ui.button("Cancel").clicked() {
                    close = true;
                }
            });
        });
        if close {
            self.open = false;
        } else if connect {
            match self.profile() {
                Some(p) => {
                    self.open = false;
                    s.request(Request::ConnectQuick(p));
                }
                None => self.focus = true,
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn builds_profiles() {
        let mut q = QuickConnect::default();
        assert!(q.profile().is_none());
        q.host = " box ".into();
        q.username = "me".into();
        q.set_protocol(Protocol::Irc);
        q.nick = "andy".into();
        let p = q.profile().unwrap();
        assert_eq!((p.host.as_str(), p.port, p.username.as_str(), p.irc.unwrap().nick.as_str()), ("box", 6667, "me", "andy"));
        q.set_protocol(Protocol::Serial);
        let p = q.profile().unwrap();
        assert_eq!((p.port, p.username.as_str()), (0, ""));
    }
}
