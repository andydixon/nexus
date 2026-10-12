//! Profile editor (right dock): create and edit every protocol's settings. A port of the
//! `profiles.js` drawer plus the Serial protocol. Switching protocol keeps the common fields
//! (including the group), and list editors keep unsaved edits.

use crate::state::{Request, Shared};
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{Align, Button, ComboBox, DragValue, Layout, Response, RichText, Stroke, TextEdit, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::clients::serial;
use nexus_core::keyfiles;
use nexus_core::models::*;
use nexus_core::secrets::SecretKind;
use nexus_core::validation::{ValidationError, validate_profile};
use parking_lot::Mutex;
use std::collections::{BTreeMap, HashMap};
use std::path::Path;
use std::sync::Arc;

type KeyChecks = HashMap<String, Result<&'static str, &'static str>>;

pub struct ProfileEditor {
    open: bool,
    is_new: bool,
    p: Profile,
    // Text buffers parsed on save.
    tags: String,
    env: String,
    rooms: String,
    /// Secrets typed into the editor, by session-manager kind ("password", "github-token", …);
    /// stored when the profile is saved.
    secrets: BTreeMap<SecretKind, String>,
    totp: String,
    totp_show: bool,
    totp_saved: bool,
    /// Text shown on the TOTP Test button until the given time.
    totp_flash: Option<(String, f64)>,
    keys: Vec<String>,
    folders: Vec<String>,
    ports: Vec<String>,
    key_checks: KeyChecks,
    /// Inline validation shows after the first Save/Test attempt.
    show_errors: bool,
    confirm_delete: bool,
    testing: bool,
    test: Arc<Mutex<Option<Result<u128, String>>>>,
}

impl Default for ProfileEditor {
    fn default() -> Self {
        ProfileEditor {
            open: false,
            is_new: true,
            p: Profile::new(Protocol::Ssh),
            tags: String::new(),
            env: String::new(),
            rooms: String::new(),
            secrets: BTreeMap::new(),
            totp: String::new(),
            totp_show: false,
            totp_saved: false,
            totp_flash: None,
            keys: vec![],
            folders: vec![],
            ports: vec![],
            key_checks: HashMap::new(),
            show_errors: false,
            confirm_delete: false,
            testing: false,
            test: Default::default(),
        }
    }
}

impl ProfileEditor {
    pub fn open_new(&mut self, s: &Shared, protocol: Option<Protocol>, folder: &str) {
        let proto = protocol.or_else(|| Protocol::from_id(&s.prefs.default_protocol)).filter(|p| *p != Protocol::Local).unwrap_or_default();
        let mut p = Profile::new(proto);
        p.folder = folder.to_string();
        self.load(s, p, true);
    }

    pub fn open_edit(&mut self, s: &Shared, id: &str) {
        if let Some(p) = s.profile(id) {
            self.load(s, p.clone(), false);
        }
    }

    pub fn is_open(&self) -> bool {
        self.open
    }

    fn load(&mut self, s: &Shared, mut p: Profile, is_new: bool) {
        prep(&mut p);
        self.tags = p.tags.join(", ");
        self.env = p.ssh.as_ref().map(|c| c.env_vars.iter().map(|(k, v)| format!("{k}={v}")).collect::<Vec<_>>().join("\n")).unwrap_or_default();
        self.rooms = p.irc.as_ref().map(|c| c.auto_join_rooms.join("\n")).unwrap_or_default();
        self.totp_saved = !is_new && s.svc.has_totp(&p.id);
        self.p = p;
        self.is_new = is_new;
        self.secrets.clear();
        self.totp.clear();
        self.totp_show = false;
        self.totp_flash = None;
        self.keys = keyfiles::list_ssh_keys().unwrap_or_default().iter().map(|k| k.display().to_string()).collect();
        let mut folders: Vec<String> = s.profiles.iter().map(|p| p.folder.trim().to_string()).filter(|f| !f.is_empty()).collect();
        folders.sort_by_key(|f| f.to_lowercase());
        folders.dedup();
        self.folders = folders;
        self.ports = serial::list_ports();
        self.key_checks.clear();
        self.show_errors = false;
        self.confirm_delete = false;
        self.open = true;
    }

    /// Keeps the common fields (folder included) and resets the port to the new protocol's
    /// default. Protocol configs edited earlier in this session are kept until save.
    fn switch(&mut self, proto: Protocol) {
        if self.p.protocol == proto {
            return;
        }
        let d = Profile::new(proto);
        let p = &mut self.p;
        p.protocol = proto;
        p.port = d.port;
        if proto == Protocol::Github && p.host.trim().is_empty() {
            p.host = d.host;
        }
        p.ssh = p.ssh.take().or(d.ssh);
        p.telnet = p.telnet.take().or(d.telnet);
        p.raw = p.raw.take().or(d.raw);
        p.github = p.github.take().or(d.github);
        p.vnc = p.vnc.take().or(d.vnc);
        p.irc = p.irc.take().or(d.irc);
        p.serial = p.serial.take().or(d.serial);
        prep(p);
    }

    /// The profile as it would be saved: buffers parsed, other protocols' configs dropped.
    fn collect(&self) -> Profile {
        let mut p = self.p.clone();
        let proto = p.protocol;
        if proto != Protocol::Ssh {
            p.ssh = None;
        }
        if proto != Protocol::Telnet {
            p.telnet = None;
        }
        if proto != Protocol::Raw {
            p.raw = None;
        }
        if proto != Protocol::Github {
            p.github = None;
        }
        if proto != Protocol::Vnc {
            p.vnc = None;
        }
        if proto != Protocol::Rdp {
            p.rdp = None;
        }
        if proto != Protocol::Irc {
            p.irc = None;
        }
        if proto != Protocol::Serial {
            p.serial = None;
        }
        p.folder = p.folder.trim().to_string();
        p.host = p.host.trim().to_string();
        p.tags = self.tags.split(',').map(str::trim).filter(|t| !t.is_empty()).map(String::from).collect();
        if let Some(c) = &mut p.ssh {
            c.env_vars = parse_env(&self.env);
            if c.proxy_type == "none" {
                c.proxy_type.clear();
            }
        }
        if let Some(c) = &mut p.irc {
            c.auto_join_rooms = self.rooms.split(['\n', ',']).map(str::trim).filter(|r| !r.is_empty()).map(String::from).collect();
        }
        p
    }

    fn validate(&self) -> Vec<ValidationError> {
        let mut p = self.collect();
        p.normalize();
        validate_profile(&p).err().map(|e| e.errors).unwrap_or_default()
    }

    /// Drawn inside the right dock while open.
    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        if let Some(r) = self.test.lock().take() {
            self.testing = false;
            match r {
                Ok(ms) => s.success(format!("Reachable — {ms} ms")),
                Err(e) => s.error(format!("Test failed: {e}")),
            }
        }
        ui.horizontal(|ui| {
            ui.label(RichText::new(if self.is_new { "New Connection" } else { "Edit Connection" }).font(theme::semibold(15.0)));
            ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                if widgets::icon_button(ui, icon::X, "Close").clicked() {
                    self.open = false;
                }
            });
        });
        ui.horizontal_wrapped(|ui| {
            for proto in Protocol::SAVED {
                if ui.selectable_label(self.p.protocol == proto, proto.label()).clicked() {
                    self.switch(proto);
                }
            }
        });
        ui.add_space(2.0);
        let errs = if self.show_errors { self.validate() } else { vec![] };
        egui::Panel::bottom("profile-editor-footer")
            .resizable(false)
            .frame(egui::Frame::NONE.fill(t.dock).inner_margin(egui::Margin::symmetric(0, 6)))
            .show(ui, |ui| self.footer(ui, s, &errs));
        egui::ScrollArea::vertical().auto_shrink([false, false]).show(ui, |ui| {
            ui.add_space(4.0);
            self.general(ui, &errs);
            match self.p.protocol {
                Protocol::Ssh => self.ssh(ui, s, &errs),
                Protocol::Telnet => self.telnet(ui),
                Protocol::Raw => self.raw(ui, &errs),
                Protocol::Github => self.github(ui, &errs),
                Protocol::Vnc => self.vnc(ui, &errs),
                Protocol::Rdp => self.rdp(ui, &errs),
                Protocol::Irc => self.irc(ui, &errs),
                Protocol::Serial => self.serial(ui, &errs),
                Protocol::Local => {}
            }
            if matches!(self.p.protocol, Protocol::Ssh | Protocol::Telnet | Protocol::Irc | Protocol::Serial) {
                self.terminal(ui, &errs);
            }
            let (p, tags) = (&mut self.p, &mut self.tags);
            section(ui, "Notes & Tags", |ui| {
                text(ui, "Tags", tags, "production, london, vpn");
                widgets::hint(ui, "Comma-separated tags for filtering");
                label(ui, "Notes");
                ui.add(TextEdit::multiline(&mut p.notes).hint_text("Free-form notes…").desired_rows(4).desired_width(f32::INFINITY));
            });
        });
    }

    fn footer(&mut self, ui: &mut Ui, s: &mut Shared, errs: &[ValidationError]) {
        let t = Tokens::get(ui.ctx());
        if !errs.is_empty() {
            let all = errs.iter().map(|e| e.message.clone()).collect::<Vec<_>>().join("\n");
            let n = errs.len();
            ui.label(
                RichText::new(format!("{}  {n} problem{} — see the highlighted fields", icon::WARNING_CIRCLE, if n == 1 { "" } else { "s" }))
                    .color(t.danger)
                    .size(12.0),
            )
            .on_hover_text(all);
        }
        if self.confirm_delete {
            ui.horizontal(|ui| {
                ui.label(RichText::new(format!("Delete \"{}\"? This can't be undone.", self.p.display_name)).color(t.danger));
            });
            ui.horizontal(|ui| {
                if ui.add(Button::new(RichText::new(format!("{}  Delete", icon::TRASH)).color(t.accent_text)).fill(t.danger)).clicked() {
                    match s.svc.delete_profile(&self.p.id) {
                        Ok(()) => {
                            s.success("Profile deleted");
                            s.request(Request::ReloadProfiles);
                            self.open = false;
                        }
                        Err(e) => s.error(format!("Failed to delete profile: {e}")),
                    }
                    self.confirm_delete = false;
                }
                if ui.button("Keep").clicked() {
                    self.confirm_delete = false;
                }
            });
            return;
        }
        ui.horizontal(|ui| {
            if !self.is_new && ui.add(Button::new(RichText::new(icon::TRASH).color(t.danger))).on_hover_text("Delete profile").clicked() {
                self.confirm_delete = true;
            }
            ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                let connect = if self.is_new { "Create & Connect" } else { "Save & Connect" };
                if widgets::primary(ui, icon::PLUGS_CONNECTED, connect).clicked() {
                    self.save(s, true);
                }
                if widgets::button(ui, icon::FLOPPY_DISK, if self.is_new { "Create" } else { "Save" }).clicked() {
                    self.save(s, false);
                }
                let can_test = !self.testing && self.p.protocol != Protocol::Serial;
                if ui.add_enabled(can_test, Button::new(format!("{}  {}", icon::PULSE, if self.testing { "Testing…" } else { "Test" }))).clicked() {
                    self.test_connection(s);
                }
                if ui.button("Cancel").clicked() {
                    self.open = false;
                }
            });
        });
    }

    fn save(&mut self, s: &mut Shared, and_connect: bool) {
        self.show_errors = true;
        let errs = self.validate();
        if let Some(e) = errs.first() {
            s.warn(format!("Please fix the highlighted fields ({}).", e.message));
            return;
        }
        let p = self.collect();
        let r = if self.is_new { s.svc.create_profile(p) } else { s.svc.update_profile(p) };
        match r {
            Err(e) => s.error(format!("Save failed: {e}")),
            Ok(saved) => {
                for (kind, v) in &self.secrets {
                    if !v.is_empty()
                        && let Err(e) = s.svc.set_profile_secret(&saved.id, *kind, v)
                    {
                        s.error(format!("Couldn't store the {} secret: {e}", kind.as_str()));
                    }
                }
                s.success(if self.is_new { "Profile created" } else { "Profile updated" });
                s.request(Request::ReloadProfiles);
                self.open = false;
                if and_connect {
                    s.request(Request::Connect(saved.id));
                }
            }
        }
    }

    fn test_connection(&mut self, s: &mut Shared) {
        self.show_errors = true;
        let p = self.collect();
        let (host, port) = match &p.vnc {
            Some(c) if p.protocol == Protocol::Vnc => (if c.vnc_host.trim().is_empty() { p.host.clone() } else { c.vnc_host.trim().to_string() }, c.vnc_port),
            _ => (p.host.clone(), p.port),
        };
        s.info(format!("Testing connection to {host}:{port}…"));
        self.testing = true;
        let (slot, svc, ctx) = (self.test.clone(), s.svc.clone(), s.ctx.clone());
        std::thread::spawn(move || {
            let r = svc.test_connection(&host, port).map_err(|e| e.to_string());
            *slot.lock() = Some(r);
            ctx.request_repaint();
        });
    }

    // --- sections ---------------------------------------------------------------------------

    fn general(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let Self { p, folders, ports, .. } = self;
        section(ui, "General", |ui| {
            text(ui, "Display Name", &mut p.display_name, "My Server");
            if p.protocol == Protocol::Serial {
                label(ui, "Device");
                let mut refresh = false;
                suggest(ui, &mut p.host, "/dev/ttyUSB0, /dev/cu.usbserial…, COM3", ports, |ui, _| {
                    refresh = widgets::icon_button(ui, icon::ARROWS_CLOCKWISE, "Refresh the device list").clicked();
                });
                if refresh {
                    *ports = serial::list_ports();
                }
                if ports.is_empty() {
                    widgets::hint(ui, "No serial devices found. Type a device path or plug one in and refresh.");
                }
                err(ui, errs, "host");
            } else {
                let hint = if p.protocol == Protocol::Github { "github.com" } else { "192.168.1.1 or example.com" };
                text(ui, "Host", &mut p.host, hint);
                err(ui, errs, "host");
                if p.protocol != Protocol::Github {
                    num(ui, "Port", &mut p.port, 1..=65535);
                    err(ui, errs, "port");
                    text(ui, "Username", &mut p.username, "root");
                    err(ui, errs, "username");
                }
            }
            label(ui, "Group");
            suggest(ui, &mut p.folder, "e.g. Production, Staging, Home Lab", folders, |_, _| {});
            widgets::hint(ui, "Choose an existing group or type a new name to create one");
            text(ui, "Description", &mut p.description, "Optional description");
            err(ui, errs, "displayName");
        });
    }

    fn ssh(&mut self, ui: &mut Ui, s: &mut Shared, errs: &[ValidationError]) {
        let Self { p, keys, key_checks, secrets, env, totp, totp_show, totp_saved, totp_flash, is_new, .. } = self;
        let id = p.id.clone();
        let c = p.ssh.get_or_insert_with(SshConfig::defaults);
        section(ui, "Authentication", |ui| {
            mode(ui, "Password Mode", &mut c.secret_mode);
            if c.secret_mode == SecretMode::Saved {
                secret(ui, "Password", secrets, SecretKind::Password, "Leave blank to keep the saved password");
            }
            label(ui, "Auth Methods");
            ui.horizontal_wrapped(|ui| {
                for (m, l) in [("publickey", "Public key"), ("password", "Password"), ("keyboard-interactive", "Keyboard-interactive")] {
                    let mut on = c.auth_methods.iter().any(|x| x == m);
                    if ui.checkbox(&mut on, l).changed() {
                        if on {
                            c.auth_methods.push(m.into());
                        } else {
                            c.auth_methods.retain(|x| x != m);
                        }
                    }
                }
            });
            err(ui, errs, "ssh.authMethods");
        });
        section(ui, "Key & Passphrase", |ui| {
            label(ui, "Private Key Path");
            key_path(ui, &mut c.private_key_path, keys, key_checks, "/Users/username/.ssh/id_ed25519");
            widgets::hint(ui, "Select a key from your .ssh folder or type a full path");
            mode(ui, "Passphrase Mode", &mut c.key_secret_mode);
            if c.key_secret_mode == SecretMode::Saved {
                secret(ui, "Passphrase", secrets, SecretKind::Passphrase, "Leave blank to keep the saved passphrase");
            }
        });
        section(ui, "Host Verification", |ui| {
            let opts: Vec<_> = HostKeyPolicy::ALL.iter().map(|k| (*k, k.label())).collect();
            combo(ui, "Host Key Policy", &mut c.host_key_policy, &opts);
        });
        section(ui, "Jump Hosts", |ui| {
            let n = c.jump_hosts.len();
            let mut act = None;
            for (i, jh) in c.jump_hosts.iter_mut().enumerate() {
                item(ui, |ui| {
                    rtl(ui, |ui| {
                        act = row_buttons(ui, i, n).or(act.take());
                        ui.add(TextEdit::singleline(&mut jh.username).hint_text("User").desired_width(ui.available_width() * 0.35));
                        ui.add(DragValue::new(&mut jh.port).range(1..=65535));
                        ui.add(TextEdit::singleline(&mut jh.host).hint_text("Host").desired_width(ui.available_width()));
                    });
                    key_path(ui, &mut jh.private_key_path, keys, key_checks, "Full key path (optional)");
                });
            }
            apply(&mut c.jump_hosts, act);
            list_errs(ui, errs, "ssh.jumpHosts");
            if ui.button(format!("{}  Add Jump Host", icon::PLUS)).clicked() {
                c.jump_hosts.push(JumpHost { port: 22, auth_method: "publickey".into(), secret_mode: SecretMode::AskEveryTime, ..Default::default() });
            }
        });
        section(ui, "Port Forwarding", |ui| {
            let n = c.port_forwards.len();
            let mut act = None;
            for (i, pf) in c.port_forwards.iter_mut().enumerate() {
                item(ui, |ui| {
                    ui.horizontal(|ui| {
                        let opts = [(ForwardDirection::Local, "Local →"), (ForwardDirection::Remote, "← Remote")];
                        ComboBox::from_id_salt(("pf-dir", i))
                            .width(84.0)
                            .selected_text(if pf.direction == ForwardDirection::Local { opts[0].1 } else { opts[1].1 })
                            .show_ui(ui, |ui| {
                                for (v, l) in opts {
                                    ui.selectable_value(&mut pf.direction, v, l);
                                }
                            });
                        ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                            act = row_buttons(ui, i, n).or(act.take());
                            ui.checkbox(&mut pf.enabled, "Active").on_hover_text("Start on connect");
                            ui.add(TextEdit::singleline(&mut pf.label).hint_text("Label (optional)").desired_width(ui.available_width()));
                        });
                    });
                    rtl(ui, |ui| {
                        // Two host fields share what the ports and arrow leave.
                        let w = ((ui.available_width() - 150.0) / 2.0).max(40.0);
                        ui.add(DragValue::new(&mut pf.dest_port).range(1..=65535));
                        ui.add(TextEdit::singleline(&mut pf.dest_host).hint_text("Dest host").desired_width(w));
                        ui.label("⟶");
                        ui.add(DragValue::new(&mut pf.bind_port).range(1..=65535));
                        ui.add(TextEdit::singleline(&mut pf.bind_host).hint_text("Bind host").desired_width(ui.available_width()));
                    });
                });
            }
            apply(&mut c.port_forwards, act);
            list_errs(ui, errs, "ssh.portForwards");
            if ui.button(format!("{}  Add Port Forward", icon::PLUS)).clicked() {
                c.port_forwards.push(PortForward {
                    id: new_id(),
                    bind_host: "127.0.0.1".into(),
                    bind_port: 1,
                    dest_port: 1,
                    enabled: true,
                    ..Default::default()
                });
            }
        });
        section(ui, "Startup", |ui| {
            text(ui, "Startup Command", &mut c.startup_command, "Optional command to run after connecting");
            label(ui, "Environment Variables");
            ui.add(
                TextEdit::multiline(env).hint_text("KEY=VALUE (one per line)").desired_rows(3).desired_width(f32::INFINITY).font(egui::TextStyle::Monospace),
            );
        });
        section(ui, "Login Script", |ui| login_script(ui, "ssh-login", &mut c.login_scripts));
        section(ui, "SSH Agent", |ui| {
            check(ui, "Use SSH Agent for Authentication", &mut c.agent_auth, "Authenticate using keys from the local SSH agent (ssh-agent)");
            check(ui, "Forward Agent to Remote", &mut c.agent_forwarding, "Allow the remote host to use your local SSH agent keys (equivalent to ssh -A)");
        });
        section(ui, "TOTP / 2FA", |ui| {
            widgets::hint(ui, "Configure a TOTP secret to auto-fill 2FA codes during SSH keyboard-interactive authentication. The secret is stored encrypted.");
            label(ui, "TOTP Secret");
            let hint = if *totp_saved { "••••••••• (secret saved)" } else { "Base32-encoded secret (e.g. from authenticator app setup)" };
            rtl(ui, |ui| {
                if widgets::icon_button(ui, if *totp_show { icon::EYE_SLASH } else { icon::EYE }, "Show/hide secret").clicked() {
                    *totp_show = !*totp_show;
                }
                ui.add(TextEdit::singleline(totp).password(!*totp_show).hint_text(hint).desired_width(ui.available_width()));
            });
            ui.horizontal(|ui| {
                let flash = totp_flash.as_ref().filter(|(_, until)| *until > s.now).map(|(t, _)| t.clone());
                if let Some((_, until)) = totp_flash {
                    ui.ctx().request_repaint_after(std::time::Duration::from_secs_f64((*until - s.now).max(0.0)));
                }
                let test = ui
                    .add(Button::new(RichText::new(flash.as_deref().unwrap_or("Test")).font(theme::mono(12.5))))
                    .on_hover_text("Generate a code with the saved secret");
                if test.clicked() {
                    *totp_flash = Some(match (*is_new, s.svc.totp_code(&id)) {
                        (false, Ok(code)) => (code, s.now + 5.0),
                        _ => ("Save first".into(), s.now + 2.0),
                    });
                }
                if ui.button("Save").on_hover_text("Encrypt and save the TOTP secret").clicked() {
                    let v = totp.trim().to_string();
                    if *is_new {
                        s.warn("Save the profile first, then set the TOTP secret.");
                    } else if v.is_empty() {
                        s.svc.delete_totp(&id);
                        *totp_saved = false;
                        s.info("TOTP secret removed.");
                    } else {
                        match s.svc.set_profile_secret(&id, SecretKind::Totp, &v) {
                            Ok(()) => {
                                totp.clear();
                                *totp_saved = true;
                                s.success("TOTP secret saved securely.");
                            }
                            Err(e) => s.error(format!("Failed to save TOTP secret: {e}")),
                        }
                    }
                }
                if ui.add_enabled(!*is_new, Button::new("Delete")).on_hover_text("Remove the TOTP secret").clicked() {
                    s.svc.delete_totp(&id);
                    *totp_saved = false;
                    s.info("TOTP secret removed.");
                }
            });
        });
        section(ui, "Proxy", |ui| {
            combo_s(ui, "Proxy Type", &mut c.proxy_type, &[("none", "None (direct connection)"), ("socks5", "SOCKS5"), ("http", "HTTP CONNECT")]);
            if c.proxy_type != "none" {
                text(ui, "Proxy Host", &mut c.proxy_host, "proxy.example.com");
                num(ui, "Proxy Port", &mut c.proxy_port, 1..=65535);
                text(ui, "Proxy Username", &mut c.proxy_username, "Optional");
                secret(ui, "Proxy Password", secrets, SecretKind::ProxyPassword, "Optional; leave blank to keep the saved password");
            }
        });
        section(ui, "Advanced", |ui| {
            text(ui, "Terminal Type", &mut c.terminal_type, DEFAULT_TERMINAL_TYPE);
            num(ui, "Keepalive Interval (s)", &mut c.keepalive_interval, 0..=86400);
            num(ui, "Max Missed Keepalives", &mut c.keepalive_count_max, 0..=1000);
            check(ui, "Compression", &mut c.compression, "");
            reconnect(ui, &mut c.auto_reconnect, &mut c.max_reconnect_tries);
        });
    }

    fn telnet(&mut self, ui: &mut Ui) {
        let c = self.p.telnet.get_or_insert_with(TelnetConfig::defaults);
        section(ui, "Login", |ui| {
            check(ui, "Local Echo", &mut c.local_echo, "");
            check(ui, "Line Mode", &mut c.line_mode, "");
        });
        section(ui, "Terminal & Encoding", |ui| {
            text(ui, "Terminal Type", &mut c.terminal_type, DEFAULT_TERMINAL_TYPE);
            combo_s(ui, "Encoding", &mut c.encoding, ENCODINGS);
        });
        section(ui, "Echo & Line Endings", |ui| line_ending(ui, &mut c.line_ending));
        section(ui, "Negotiation", |ui| check(ui, "Debug Negotiation", &mut c.negotiation_debug, ""));
        section(ui, "Login Script", |ui| login_script(ui, "telnet-login", &mut c.login_scripts));
        section(ui, "Reconnection", |ui| reconnect(ui, &mut c.auto_reconnect, &mut c.max_reconnect_tries));
    }

    fn raw(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let p = &mut self.p;
        let c = p.raw.get_or_insert_with(RawConfig::defaults);
        section(ui, "Encoding & Framing", |ui| {
            combo_s(ui, "Encoding", &mut c.encoding, &[("utf-8", "UTF-8"), ("ascii", "ASCII"), ("hex", "Hex")]);
            line_ending(ui, &mut c.line_ending);
        });
        section(ui, "Display", |ui| {
            check(ui, "Show Timestamps", &mut c.show_timestamps, "");
            check(ui, "Show Direction", &mut c.show_direction, "");
        });
        section(ui, "Payload Presets", |ui| {
            let n = c.payload_presets.len();
            let mut act = None;
            for (i, ps) in c.payload_presets.iter_mut().enumerate() {
                rtl(ui, |ui| {
                    act = row_buttons(ui, i, n).or(act.take());
                    ComboBox::from_id_salt(("preset-fmt", i)).width(76.0).selected_text(fmt_label(&ps.format)).show_ui(ui, |ui| {
                        for f in ["text", "hex", "escaped"] {
                            ui.selectable_value(&mut ps.format, f.to_string(), fmt_label(f));
                        }
                    });
                    let w = (ui.available_width() - ui.spacing().item_spacing.x) * 0.6;
                    ui.add(TextEdit::singleline(&mut ps.payload).hint_text("Payload").font(egui::TextStyle::Monospace).desired_width(w));
                    ui.add(TextEdit::singleline(&mut ps.name).hint_text("Name").desired_width(ui.available_width()));
                });
            }
            apply(&mut c.payload_presets, act);
            list_errs(ui, errs, "raw.payloadPresets");
            if ui.button(format!("{}  Add Preset", icon::PLUS)).clicked() {
                c.payload_presets.push(PayloadPreset { id: new_id(), format: "text".into(), ..Default::default() });
            }
        });
        section(ui, "Timeouts & Reconnect", |ui| {
            num(ui, "Connect Timeout (s)", &mut p.connect_timeout, 0..=3600);
            num(ui, "Read Timeout (s)", &mut p.read_timeout, 0..=3600);
            check(ui, "Auto-reconnect", &mut c.auto_reconnect, "");
        });
    }

    fn github(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let Self { p, secrets, .. } = self;
        let c = p.github.get_or_insert_with(GitHubConfig::defaults);
        section(ui, "API", |ui| {
            text(ui, "API Base URL", &mut c.base_url, "https://api.github.com");
            err(ui, errs, "github.baseURL");
            mode(ui, "Token Storage", &mut c.secret_mode);
            if c.secret_mode == SecretMode::Saved {
                secret(ui, "Personal Access Token", secrets, SecretKind::GithubToken, "Leave blank to keep the saved token");
            }
        });
        section(ui, "Refresh", |ui| {
            num(ui, "Refresh Interval (seconds)", &mut c.refresh_interval_secs, 60..=3600);
            err(ui, errs, "github.refreshIntervalSecs");
        });
    }

    fn vnc(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let Self { p, keys, key_checks, secrets, .. } = self;
        let c = p.vnc.get_or_insert_with(VncConfig::defaults);
        section(ui, "VNC Server", |ui| {
            text(ui, "VNC Host", &mut c.vnc_host, "192.168.1.100 (defaults to Host)");
            err(ui, errs, "vnc.vncHost");
            num(ui, "VNC Port", &mut c.vnc_port, 1..=65535);
            err(ui, errs, "vnc.vncPort");
            mode(ui, "Password Storage", &mut c.secret_mode);
            if c.secret_mode == SecretMode::Saved {
                secret(ui, "VNC Password", secrets, SecretKind::VncPassword, "Leave blank to keep the saved password");
            }
        });
        section(ui, "Display", |ui| {
            check(ui, "View Only", &mut c.view_only, "");
            check(ui, "Scale to Fit", &mut c.scale_to_fit, "");
            check(ui, "Shared Session", &mut c.shared_session, "");
            num(ui, "Quality (0-9)", &mut c.quality, 0..=9);
            err(ui, errs, "vnc.quality");
        });
        section(ui, "SSH Tunnel", |ui| {
            tunnel(
                ui,
                "vnc",
                Tunnel {
                    on: &mut c.ssh_tunnel,
                    host: &mut c.ssh_host,
                    port: &mut c.ssh_port,
                    user: &mut c.ssh_username,
                    auth: &mut c.ssh_auth_method,
                    key: &mut c.ssh_private_key,
                    agent: &mut c.ssh_agent_auth,
                    mode: &mut c.ssh_secret_mode,
                },
                keys,
                key_checks,
                secrets,
                errs,
            );
        });
    }

    fn rdp(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let Self { p, keys, key_checks, secrets, .. } = self;
        let c = p.rdp.get_or_insert_with(RdpConfig::defaults);
        section(ui, "RDP", |ui| {
            text(ui, "Domain", &mut c.domain, "Optional (e.g. CORP)");
            mode(ui, "Password Storage", &mut c.secret_mode);
            if c.secret_mode == SecretMode::Saved {
                secret(ui, "Password", secrets, SecretKind::RdpPassword, "Leave blank to keep the saved password");
            }
            let opts: Vec<_> = HostKeyPolicy::ALL.iter().map(|k| (*k, k.label())).collect();
            combo(ui, "Certificate Policy", &mut c.host_key_policy, &opts);
            check(
                ui,
                "Allow servers without NLA",
                &mut c.allow_tls_only,
                "Plain TLS logon, e.g. xrdp. Less secure: the session is set up before you authenticate.",
            );
        });
        section(ui, "Display", |ui| {
            check(ui, "Scale to Fit", &mut c.scale_to_fit, "");
            num(ui, "Width (0 = fit tab)", &mut c.width, 0..=8192);
            err(ui, errs, "rdp.width");
            num(ui, "Height (0 = fit tab)", &mut c.height, 0..=8192);
            err(ui, errs, "rdp.height");
            if c.width == 0 {
                check(ui, "Resize With Tab", &mut c.dynamic_resolution, "Ask the server to match the tab's size as it changes");
            }
        });
        section(ui, "Proxy", |ui| {
            combo_s(ui, "Proxy Type", &mut c.proxy_type, &[("none", "None (direct connection)"), ("socks5", "SOCKS5"), ("http", "HTTP CONNECT")]);
            err(ui, errs, "rdp.proxyType");
            if c.proxy_type != "none" {
                text(ui, "Proxy Host", &mut c.proxy_host, "proxy.example.com");
                err(ui, errs, "rdp.proxyHost");
                num(ui, "Proxy Port", &mut c.proxy_port, 0..=65535);
                text(ui, "Proxy Username", &mut c.proxy_username, "Optional");
                secret(ui, "Proxy Password", secrets, SecretKind::ProxyPassword, "Optional; leave blank to keep the saved password");
            }
        });
        section(ui, "SSH Tunnel", |ui| {
            tunnel(
                ui,
                "rdp",
                Tunnel {
                    on: &mut c.ssh_tunnel,
                    host: &mut c.ssh_host,
                    port: &mut c.ssh_port,
                    user: &mut c.ssh_username,
                    auth: &mut c.ssh_auth_method,
                    key: &mut c.ssh_private_key,
                    agent: &mut c.ssh_agent_auth,
                    mode: &mut c.ssh_secret_mode,
                },
                keys,
                key_checks,
                secrets,
                errs,
            );
        });
    }

    fn irc(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let Self { p, keys, key_checks, secrets, rooms, .. } = self;
        let port = &mut p.port;
        let c = p.irc.get_or_insert_with(IrcConfig::defaults);
        section(ui, "IRC", |ui| {
            text(ui, "Nick", &mut c.nick, "yourNick");
            err(ui, errs, "irc.nick");
            text(ui, "Real Name", &mut c.real_name, "Shown in WHOIS");
            err(ui, errs, "irc.realName");
            if ui.checkbox(&mut c.use_tls, "Use TLS").changed() {
                // Follow the protocol default unless the port was customised.
                match (c.use_tls, *port) {
                    (true, p) if p == i64::from(DEFAULT_IRC_PORT) => *port = DEFAULT_IRC_TLS_PORT.into(),
                    (false, p) if p == i64::from(DEFAULT_IRC_TLS_PORT) => *port = DEFAULT_IRC_PORT.into(),
                    _ => {}
                }
            }
            widgets::hint(ui, "Use port 6697 for most TLS IRC networks.");
            check(ui, "Server Password", &mut c.server_password_required, "Prompt for an IRC PASS secret before registration.");
            if c.server_password_required {
                mode(ui, "Server Password Storage", &mut c.server_password_mode);
                if c.server_password_mode == SecretMode::Saved {
                    secret(ui, "Server Password", secrets, SecretKind::IrcServerPassword, "Leave blank to keep the saved password");
                }
            }
        });
        section(ui, "NickServ", |ui| {
            check(ui, "Identify on Connect", &mut c.nick_serv_identify, "");
            if c.nick_serv_identify {
                text(ui, "Account", &mut c.nick_serv_account, "Optional if it matches your nick");
                err(ui, errs, "irc.nickServAccount");
                mode(ui, "Password Storage", &mut c.nick_serv_mode);
                if c.nick_serv_mode == SecretMode::Saved {
                    secret(ui, "NickServ Password", secrets, SecretKind::IrcNickServPassword, "Leave blank to keep the saved password");
                }
            }
        });
        section(ui, "Rooms", |ui| {
            label(ui, "Auto-Join Rooms");
            ui.add(TextEdit::multiline(rooms).hint_text("#general\n#ops").desired_rows(4).desired_width(f32::INFINITY));
            widgets::hint(ui, "One room per line or comma-separated.");
            let names: Vec<&str> = rooms.split(['\n', ',']).map(str::trim).filter(|r| !r.is_empty()).collect();
            for e in errs.iter().filter(|e| e.field.starts_with("irc.autoJoinRooms")) {
                let room = index_of(&e.field).and_then(|i| names.get(i)).copied().unwrap_or("");
                error_line(ui, &format!("{room}: {}", e.message));
            }
        });
        section(ui, "SSH Tunnel", |ui| {
            tunnel(
                ui,
                "irc",
                Tunnel {
                    on: &mut c.ssh_tunnel,
                    host: &mut c.ssh_host,
                    port: &mut c.ssh_port,
                    user: &mut c.ssh_username,
                    auth: &mut c.ssh_auth_method,
                    key: &mut c.ssh_private_key,
                    agent: &mut c.ssh_agent_auth,
                    mode: &mut c.ssh_secret_mode,
                },
                keys,
                key_checks,
                secrets,
                errs,
            );
        });
        section(ui, "Reconnection", |ui| reconnect(ui, &mut c.auto_reconnect, &mut c.max_reconnect_tries));
    }

    fn serial(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let c = self.p.serial.get_or_insert_with(SerialConfig::default);
        section(ui, "Line Settings", |ui| {
            label(ui, "Baud Rate");
            ui.horizontal(|ui| {
                ComboBox::from_id_salt("serial-baud")
                    .selected_text(if BAUDS.contains(&c.baud_rate) { c.baud_rate.to_string() } else { "Custom".into() })
                    .show_ui(ui, |ui| {
                        for b in BAUDS {
                            ui.selectable_value(&mut c.baud_rate, b, b.to_string());
                        }
                    });
                ui.add(DragValue::new(&mut c.baud_rate).range(1..=20_000_000)).on_hover_text("Any rate the adapter supports");
            });
            err(ui, errs, "serial.baudRate");
            combo(ui, "Data Bits", &mut c.data_bits, &[(5, "5"), (6, "6"), (7, "7"), (8, "8")]);
            err(ui, errs, "serial.dataBits");
            combo(ui, "Parity", &mut c.parity, &[(Parity::None, "None"), (Parity::Odd, "Odd"), (Parity::Even, "Even")]);
            combo(ui, "Stop Bits", &mut c.stop_bits, &[(1, "1"), (2, "2")]);
            err(ui, errs, "serial.stopBits");
            combo(
                ui,
                "Flow Control",
                &mut c.flow_control,
                &[(FlowControl::None, "None"), (FlowControl::Xonxoff, "XON/XOFF (software)"), (FlowControl::Rtscts, "RTS/CTS (hardware)")],
            );
        });
        section(ui, "Behaviour", |ui| {
            line_ending(ui, &mut c.line_ending);
            combo_s(ui, "Encoding", &mut c.encoding, ENCODINGS);
            check(ui, "Local Echo", &mut c.local_echo, "Show typed characters locally (for devices that don't echo)");
            check(ui, "Assert DTR on connect", &mut c.dtr_on_connect, "");
            check(ui, "Assert RTS on connect", &mut c.rts_on_connect, "");
            reconnect(ui, &mut c.auto_reconnect, &mut c.max_reconnect_tries);
            widgets::hint(ui, "Reconnects when the device is plugged back in.");
        });
    }

    fn terminal(&mut self, ui: &mut Ui, errs: &[ValidationError]) {
        let p = &mut self.p;
        section(ui, "Terminal", |ui| {
            text(ui, "Font Family", &mut p.terminal_font_family, DEFAULT_FONT_FAMILY);
            num(ui, "Font Size", &mut p.terminal_font_size, 8..=36);
            err(ui, errs, "terminalFontSize");
            num(ui, "Scrollback Lines", &mut p.terminal_scrollback, 0..=100_000);
            err(ui, errs, "terminalScrollback");
            combo_s(ui, "Cursor Style", &mut p.terminal_cursor_style, &[("block", "Block"), ("underline", "Underline"), ("bar", "Bar")]);
        });
    }
}

const ENCODINGS: &[(&str, &str)] = &[("utf-8", "UTF-8"), ("ascii", "ASCII"), ("iso-8859-1", "ISO 8859-1")];
const BAUDS: [u32; 14] = [300, 1200, 2400, 4800, 9600, 14400, 19200, 38400, 57600, 115200, 230400, 460800, 921600, 1_000_000];

/// Fills the zero values the JS form defaulted (`|| 5`, `|| 1080`…), so range-clamped inputs
/// start from the same numbers.
fn prep(p: &mut Profile) {
    fn tries(n: &mut i64) {
        if *n <= 0 {
            *n = 5;
        }
    }
    fn port(n: &mut i64, d: i64) {
        if *n <= 0 {
            *n = d;
        }
    }
    fn steps(v: &mut [LoginScriptStep]) {
        for s in v.iter_mut().filter(|s| s.kind == "wait" && s.timeout <= 0) {
            s.timeout = 10;
        }
    }
    if p.terminal_font_size == 0 {
        p.terminal_font_size = DEFAULT_FONT_SIZE;
    }
    if p.terminal_font_family.is_empty() {
        p.terminal_font_family = DEFAULT_FONT_FAMILY.into();
    }
    if p.terminal_cursor_style.is_empty() {
        p.terminal_cursor_style = DEFAULT_CURSOR_STYLE.into();
    }
    if let Some(c) = &mut p.ssh {
        tries(&mut c.max_reconnect_tries);
        port(&mut c.proxy_port, 1080);
        if c.proxy_type.is_empty() {
            c.proxy_type = "none".into();
        }
        for j in &mut c.jump_hosts {
            port(&mut j.port, 22);
        }
        steps(&mut c.login_scripts);
    }
    if let Some(c) = &mut p.telnet {
        tries(&mut c.max_reconnect_tries);
        steps(&mut c.login_scripts);
    }
    if let Some(c) = &mut p.irc {
        tries(&mut c.max_reconnect_tries);
        port(&mut c.ssh_port, 22);
    }
    if let Some(c) = &mut p.vnc {
        port(&mut c.vnc_port, DEFAULT_VNC_PORT.into());
        port(&mut c.ssh_port, 22);
    }
    if let Some(c) = &mut p.github
        && c.refresh_interval_secs == 0
    {
        c.refresh_interval_secs = 300;
    }
    if let Some(c) = &mut p.serial {
        tries(&mut c.max_reconnect_tries);
    }
}

fn parse_env(text: &str) -> BTreeMap<String, String> {
    text.lines().filter_map(|l| l.split_once('=')).map(|(k, v)| (k.trim().to_string(), v.trim().to_string())).filter(|(k, _)| !k.is_empty()).collect()
}

/// "ssh.jumpHosts[2].host" → 2
fn index_of(field: &str) -> Option<usize> {
    field.split_once('[')?.1.split_once(']')?.0.parse().ok()
}

// --- form widgets -----------------------------------------------------------------------------

/// A collapsible card with a section header.
fn section(ui: &mut Ui, title: &str, body: impl FnOnce(&mut Ui)) {
    let t = Tokens::get(ui.ctx());
    egui::Frame::NONE.fill(t.card).stroke(Stroke::new(1.0, t.card_border)).corner_radius(6).inner_margin(10).show(ui, |ui| {
        ui.set_width(ui.available_width());
        egui::CollapsingHeader::new(RichText::new(title.to_uppercase()).font(theme::semibold(10.5)).color(t.text_faint).extra_letter_spacing(0.6))
            .id_salt(("pe-section", title))
            .default_open(true)
            .show(ui, |ui| {
                ui.spacing_mut().item_spacing.y = 4.0;
                body(ui)
            });
    });
    ui.add_space(6.0);
}

/// A list entry (jump host, forward) on a faint background.
fn item(ui: &mut Ui, body: impl FnOnce(&mut Ui)) {
    let t = Tokens::get(ui.ctx());
    egui::Frame::NONE.fill(t.text.gamma_multiply(0.04)).corner_radius(4).inner_margin(6).show(ui, |ui| {
        ui.set_width(ui.available_width());
        body(ui)
    });
}

fn label(ui: &mut Ui, text: &str) {
    let t = Tokens::get(ui.ctx());
    ui.add_space(2.0);
    ui.label(RichText::new(text).color(t.text_dim).size(12.0));
}

fn error_line(ui: &mut Ui, msg: &str) {
    let t = Tokens::get(ui.ctx());
    ui.label(RichText::new(format!("{}  {msg}", icon::WARNING_CIRCLE)).color(t.danger).size(11.5));
}

fn err(ui: &mut Ui, errs: &[ValidationError], field: &str) {
    for e in errs.iter().filter(|e| e.field == field) {
        error_line(ui, &e.message);
    }
}

/// Errors for a list's rows ("ssh.jumpHosts[1].port" → "Row 2: …").
fn list_errs(ui: &mut Ui, errs: &[ValidationError], prefix: &str) {
    for e in errs.iter().filter(|e| e.field.starts_with(prefix)) {
        match index_of(&e.field) {
            Some(i) => error_line(ui, &format!("Row {}: {}", i + 1, e.message)),
            None => error_line(ui, &e.message),
        }
    }
}

fn text(ui: &mut Ui, l: &str, v: &mut String, hint: &str) -> Response {
    label(ui, l);
    ui.add(TextEdit::singleline(v).hint_text(hint).desired_width(f32::INFINITY))
}

fn num(ui: &mut Ui, l: &str, v: &mut i64, range: std::ops::RangeInclusive<i64>) {
    label(ui, l);
    ui.add(DragValue::new(v).range(range).speed(0.25));
}

fn check(ui: &mut Ui, l: &str, v: &mut bool, help: &str) {
    ui.checkbox(v, l);
    if !help.is_empty() {
        widgets::hint(ui, help);
    }
}

fn combo<T: PartialEq + Copy>(ui: &mut Ui, l: &str, v: &mut T, opts: &[(T, &str)]) {
    label(ui, l);
    let cur = opts.iter().find(|o| o.0 == *v).map_or("", |o| o.1);
    ComboBox::from_id_salt(("pe-combo", l)).selected_text(cur).width(ui.available_width().min(320.0)).show_ui(ui, |ui| {
        for (o, text) in opts {
            ui.selectable_value(v, *o, *text);
        }
    });
}

fn combo_s(ui: &mut Ui, l: &str, v: &mut String, opts: &[(&str, &str)]) {
    label(ui, l);
    let cur = opts.iter().find(|o| o.0 == v).map_or(v.clone(), |o| o.1.to_string());
    ComboBox::from_id_salt(("pe-combo", l)).selected_text(cur).width(ui.available_width().min(320.0)).show_ui(ui, |ui| {
        for (o, text) in opts {
            ui.selectable_value(v, o.to_string(), *text);
        }
    });
}

fn mode(ui: &mut Ui, l: &str, v: &mut SecretMode) {
    let opts: Vec<_> = SecretMode::ALL.iter().map(|m| (*m, m.label())).collect();
    combo(ui, l, v, &opts);
}

fn line_ending(ui: &mut Ui, v: &mut LineEnding) {
    let opts: Vec<_> = LineEnding::ALL.iter().map(|e| (*e, e.label())).collect();
    combo(ui, "Line Ending", v, &opts);
}

fn reconnect(ui: &mut Ui, on: &mut bool, tries: &mut i64) {
    check(ui, "Auto-Reconnect", on, "Automatically reconnect with exponential backoff when the connection drops");
    if *on {
        num(ui, "Max Reconnect Attempts", tries, 1..=20);
    }
}

/// A password field whose value is stored in the vault/keychain under `kind` on save.
fn secret(ui: &mut Ui, l: &str, secrets: &mut BTreeMap<SecretKind, String>, kind: SecretKind, hint: &str) {
    label(ui, l);
    ui.add(TextEdit::singleline(secrets.entry(kind).or_default()).password(true).hint_text(hint).desired_width(f32::INFINITY));
}

/// A text field with a drop-down of suggestions (an HTML datalist). `extra` draws trailing
/// buttons, right to left, so the field takes exactly the width they leave.
fn suggest(ui: &mut Ui, v: &mut String, hint: &str, opts: &[String], extra: impl FnOnce(&mut Ui, &mut String)) -> Response {
    rtl(ui, |ui| {
        extra(ui, v);
        ui.add_enabled_ui(!opts.is_empty(), |ui| {
            ui.menu_button(icon::CARET_DOWN, |ui| {
                egui::ScrollArea::vertical().max_height(280.0).show(ui, |ui| {
                    for o in opts {
                        if ui.selectable_label(v == o, o).clicked() {
                            *v = o.clone();
                            ui.close();
                        }
                    }
                });
            })
            .response
            .on_hover_text("Suggestions")
        });
        ui.add(TextEdit::singleline(v).hint_text(hint).desired_width(ui.available_width()))
    })
}

/// A row laid out right to left: add fixed-size widgets first, then one that fills the rest.
fn rtl<R>(ui: &mut Ui, body: impl FnOnce(&mut Ui) -> R) -> R {
    ui.horizontal(|ui| ui.with_layout(Layout::right_to_left(Align::Center), body).inner).inner
}

/// Key path with `~/.ssh` suggestions, Browse, and the JS checks once the field loses focus.
fn key_path(ui: &mut Ui, v: &mut String, keys: &[String], checks: &mut KeyChecks, hint: &str) {
    let r = suggest(ui, v, hint, keys, |ui, v| {
        if ui.button("Browse").clicked()
            && let Some(p) = rfd::FileDialog::new().set_title("Select SSH Private Key").pick_file()
        {
            *v = p.display().to_string();
        }
    });
    let path = v.trim();
    if path.is_empty() || r.has_focus() {
        return;
    }
    let t = Tokens::get(ui.ctx());
    match checks.entry(path.to_string()).or_insert_with(|| check_key(path)) {
        Ok(m) => ui.label(RichText::new(format!("{}  {m}", icon::CHECK_CIRCLE)).color(t.success).size(11.5)),
        Err(m) => ui.label(RichText::new(format!("{}  {m}", icon::WARNING_CIRCLE)).color(t.danger).size(11.5)),
    };
}

fn check_key(path: &str) -> Result<&'static str, &'static str> {
    if path.starts_with('~') {
        Err("Please use a full path (no ~).")
    } else if !Path::new(path).exists() {
        Err("Key file not found.")
    } else if !keyfiles::is_likely_private_key(Path::new(path)) {
        Err("File is not a recognised private key.")
    } else {
        Ok("Key verified.")
    }
}

enum Act {
    Up(usize),
    Down(usize),
    Remove(usize),
}

fn row_buttons(ui: &mut Ui, i: usize, n: usize) -> Option<Act> {
    let mut a = None;
    ui.horizontal(|ui| {
        ui.spacing_mut().item_spacing.x = 2.0;
        let mut order = [0, 1, 2];
        // Always ▲ ▼ ✕ on screen, also inside right-to-left rows.
        if ui.layout().prefer_right_to_left() {
            order.reverse();
        }
        for k in order {
            let (glyph, tip, enabled, act) = match k {
                0 => (icon::ARROW_UP, "Move up", i > 0, Act::Up(i)),
                1 => (icon::ARROW_DOWN, "Move down", i + 1 < n, Act::Down(i)),
                _ => (icon::X, "Remove", true, Act::Remove(i)),
            };
            if ui.add_enabled(enabled, Button::new(glyph).small()).on_hover_text(tip).clicked() {
                a = Some(act);
            }
        }
    });
    a
}

fn apply<T>(v: &mut Vec<T>, a: Option<Act>) {
    match a {
        Some(Act::Up(i)) if i > 0 && i < v.len() => v.swap(i - 1, i),
        Some(Act::Down(i)) if i + 1 < v.len() => v.swap(i, i + 1),
        Some(Act::Remove(i)) if i < v.len() => {
            v.remove(i);
        }
        _ => {}
    }
}

fn fmt_label(f: &str) -> &'static str {
    match f {
        "hex" => "Hex",
        "escaped" => "Escaped",
        _ => "Text",
    }
}

/// Expect-style steps: wait for text (timeout 1–120 s) or send text.
fn login_script(ui: &mut Ui, id: &str, steps: &mut Vec<LoginScriptStep>) {
    widgets::hint(ui, "Automate login sequences: wait for text from the server, then send a response. Steps execute in order after connection.");
    if steps.is_empty() {
        widgets::hint(ui, "No login script steps configured.");
    }
    let n = steps.len();
    let mut act = None;
    // Rows are fixed-width columns around a value field that fills the rest, so they line up.
    for (i, st) in steps.iter_mut().enumerate() {
        ui.horizontal(|ui| {
            let wait = st.kind == "wait";
            ComboBox::from_id_salt((id, i)).width(84.0).selected_text(if wait { "Wait for" } else { "Send" }).show_ui(ui, |ui| {
                ui.selectable_value(&mut st.kind, "wait".into(), "Wait for");
                ui.selectable_value(&mut st.kind, "send".into(), "Send");
            });
            if st.kind == "wait" && st.timeout <= 0 {
                st.timeout = 10;
            }
            ui.with_layout(Layout::right_to_left(Align::Center), |ui| {
                act = row_buttons(ui, i, n).or(act.take());
                if wait {
                    ui.add_sized(vec2(56.0, 22.0), DragValue::new(&mut st.timeout).range(1..=120).suffix(" s")).on_hover_text("Timeout (seconds)");
                } else {
                    ui.add_space(56.0 + ui.spacing().item_spacing.x);
                }
                ui.add(
                    TextEdit::singleline(&mut st.value).hint_text(if wait { "Text to match…" } else { "Text to send…" }).desired_width(ui.available_width()),
                );
            });
        });
    }
    apply(steps, act);
    ui.horizontal(|ui| {
        if ui.button(format!("{}  Wait", icon::PLUS)).clicked() {
            steps.push(LoginScriptStep { kind: "wait".into(), value: String::new(), timeout: 10 });
        }
        if ui.button(format!("{}  Send", icon::PLUS)).clicked() {
            steps.push(LoginScriptStep { kind: "send".into(), value: String::new(), timeout: 0 });
        }
    });
}

/// The SSH-tunnel fields VNC and IRC share.
struct Tunnel<'a> {
    on: &'a mut bool,
    host: &'a mut String,
    port: &'a mut i64,
    user: &'a mut String,
    auth: &'a mut String,
    key: &'a mut String,
    agent: &'a mut bool,
    mode: &'a mut SecretMode,
}

fn tunnel(ui: &mut Ui, kind: &str, c: Tunnel, keys: &[String], checks: &mut KeyChecks, secrets: &mut BTreeMap<SecretKind, String>, errs: &[ValidationError]) {
    check(ui, "Enable SSH Tunnel", c.on, "");
    if !*c.on {
        return;
    }
    text(ui, "SSH Host", c.host, "jump.example.com");
    err(ui, errs, &format!("{kind}.sshHost"));
    num(ui, "SSH Port", c.port, 1..=65535);
    err(ui, errs, &format!("{kind}.sshPort"));
    text(ui, "SSH Username", c.user, "admin");
    err(ui, errs, &format!("{kind}.sshUsername"));
    combo_s(ui, "SSH Auth Method", c.auth, &[("publickey", "Public Key"), ("password", "Password")]);
    err(ui, errs, &format!("{kind}.sshAuthMethod"));
    if c.auth != "password" {
        label(ui, "SSH Private Key Path");
        key_path(ui, c.key, keys, checks, "/Users/username/.ssh/id_ed25519");
        check(ui, "Use SSH Agent", c.agent, "");
    }
    mode(ui, "SSH Secret Storage", c.mode);
    if *c.mode == SecretMode::Saved {
        let (l, k) = match (kind, c.auth.as_str()) {
            ("vnc", "password") => ("SSH Password", SecretKind::VncSshPassword),
            ("vnc", _) => ("SSH Key Passphrase", SecretKind::VncSshPassphrase),
            ("rdp", "password") => ("SSH Password", SecretKind::RdpSshPassword),
            ("rdp", _) => ("SSH Key Passphrase", SecretKind::RdpSshPassphrase),
            (_, "password") => ("SSH Password", SecretKind::IrcSshPassword),
            _ => ("SSH Key Passphrase", SecretKind::IrcSshPassphrase),
        };
        secret(ui, l, secrets, k, "Leave blank to keep the saved secret");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_moves_and_parsing() {
        let mut v = vec![1, 2, 3];
        apply(&mut v, Some(Act::Up(2)));
        assert_eq!(v, [1, 3, 2]);
        apply(&mut v, Some(Act::Down(0)));
        assert_eq!(v, [3, 1, 2]);
        apply(&mut v, Some(Act::Remove(1)));
        assert_eq!(v, [3, 2]);
        apply(&mut v, Some(Act::Down(1))); // last row: no-op
        assert_eq!(v, [3, 2]);
        assert_eq!(index_of("ssh.jumpHosts[12].host"), Some(12));
        let env = parse_env("A=1\n =x\nB = two=2\nnoeq");
        assert_eq!(env.len(), 2);
        assert_eq!(env["B"], "two=2");
    }

    #[test]
    fn switching_protocol_keeps_common_fields() {
        let mut e = ProfileEditor::default();
        e.p.folder = "Prod".into();
        e.p.host = "box".into();
        e.p.display_name = "Box".into();
        e.switch(Protocol::Serial);
        assert_eq!((e.p.folder.as_str(), e.p.host.as_str(), e.p.port), ("Prod", "box", 0));
        assert!(e.p.serial.is_some());
        e.switch(Protocol::Irc);
        assert_eq!(e.p.port, 6667);
        let saved = e.collect();
        assert!(saved.ssh.is_none() && saved.serial.is_none() && saved.irc.is_some());
    }
}
