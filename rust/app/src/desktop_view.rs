//! Remote desktop viewer for VNC and RDP: the remote framebuffer as a texture, with mouse,
//! keyboard and clipboard input. Input is expressed in X keysyms and VNC pointer masks (VNC's own
//! terms); `Remote` translates them for RDP. Toolbar after the web build's `vnc.js` (status,
//! Ctrl+Alt+Del, clipboard), plus refresh and a scale toggle.

use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{Event, Key, Modifiers, Pos2, Rect, RichText, Sense, TextureHandle, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::clients::rdp::{self, MouseButton, MousePosition, Operation, Scancode, WheelRotations};
use nexus_core::clients::vnc::{self, keysym_for};
use nexus_core::models::{Profile, Protocol};
use std::sync::Arc;

const XK_SHIFT: u32 = 0xffe1;
const XK_CONTROL: u32 = 0xffe3;
const XK_ALT: u32 = 0xffe9;
const XK_SUPER: u32 = 0xffeb;
const XK_DELETE: u32 = 0xffff;
/// Wheel distance (points) per remote scroll notch for pixel-precise wheels.
const NOTCH: f32 = 40.0;

/// The connected client a view drives.
#[derive(Clone)]
enum Remote {
    Vnc(Arc<vnc::Client>),
    Rdp(Arc<rdp::Client>),
}

impl Remote {
    fn framebuffer(&self) -> Arc<parking_lot::Mutex<vnc::Framebuffer>> {
        match self {
            Remote::Vnc(c) => c.framebuffer(),
            Remote::Rdp(c) => c.framebuffer(),
        }
    }

    /// `held` is the modifier keysyms down on the remote side (for RDP shortcuts).
    fn key(&self, sym: u32, down: bool, held: &[u32]) {
        match self {
            Remote::Vnc(c) => {
                let _ = c.key(sym, down);
            }
            Remote::Rdp(c) => {
                if let Some(op) = rdp_key(sym, down, held.iter().any(|&k| matches!(k, XK_CONTROL | XK_ALT | XK_SUPER))) {
                    c.input(vec![op]);
                }
            }
        }
    }

    /// Bits 0–2 left, middle, right; 3/4 a wheel notch up/down (VNC's pointer mask).
    fn pointer(&self, x: u16, y: u16, mask: u8) {
        match self {
            Remote::Vnc(c) => {
                let _ = c.pointer(x, y, mask);
            }
            Remote::Rdp(c) => {
                let mut ops = vec![Operation::MouseMove(MousePosition { x, y })];
                for (bit, b) in [(0, MouseButton::Left), (1, MouseButton::Middle), (2, MouseButton::Right)] {
                    ops.push(if mask & 1 << bit != 0 { Operation::MouseButtonPressed(b) } else { Operation::MouseButtonReleased(b) });
                }
                for (bit, units) in [(3, 120), (4, -120)] {
                    if mask & 1 << bit != 0 {
                        ops.push(Operation::WheelRotations(WheelRotations { is_vertical: true, rotation_units: units }));
                    }
                }
                c.input(ops);
            }
        }
    }
}

/// An X keysym as an RDP key operation: named keys (and letters/digits pressed with Ctrl, Alt or
/// Win, so shortcuts work) as US-layout scancodes, everything else as a Unicode key.
fn rdp_key(sym: u32, down: bool, chord: bool) -> Option<Operation> {
    let sc = |ext: bool, code: u8| {
        Some(if down { Operation::KeyPressed(Scancode::from_u8(ext, code)) } else { Operation::KeyReleased(Scancode::from_u8(ext, code)) })
    };
    let named = match sym {
        0xff1b => (false, 0x01),
        0xff08 => (false, 0x0e),
        0xff09 => (false, 0x0f),
        0xff0d => (false, 0x1c),
        0xffe1 => (false, 0x2a),
        0xffe2 => (false, 0x36),
        0xffe3 => (false, 0x1d),
        0xffe4 => (true, 0x1d),
        0xffe9 => (false, 0x38),
        0xffea | 0xfe03 => (true, 0x38),
        0xffeb => (true, 0x5b),
        0xffec => (true, 0x5c),
        0xffe5 => (false, 0x3a),
        0xff50 => (true, 0x47),
        0xff51 => (true, 0x4b),
        0xff52 => (true, 0x48),
        0xff53 => (true, 0x4d),
        0xff54 => (true, 0x50),
        0xff55 => (true, 0x49),
        0xff56 => (true, 0x51),
        0xff57 => (true, 0x4f),
        0xff63 => (true, 0x52),
        0xffff => (true, 0x53),
        f @ 0xffbe..=0xffc7 => (false, 0x3b + (f - 0xffbe) as u8),
        0xffc8 => (false, 0x57),
        0xffc9 => (false, 0x58),
        _ => (false, 0),
    };
    if named.1 != 0 {
        return sc(named.0, named.1);
    }
    let ch = char::from_u32(if sym >= 0x0100_0000 { sym - 0x0100_0000 } else { sym })?;
    if chord {
        const LETTERS: &[u8; 26] = b"\x1e\x30\x2e\x20\x12\x21\x22\x23\x17\x24\x25\x26\x32\x31\x18\x19\x10\x13\x1f\x14\x16\x2f\x11\x2d\x15\x2c";
        let lower = ch.to_ascii_lowercase();
        match lower {
            'a'..='z' => return sc(false, LETTERS[(lower as u8 - b'a') as usize]),
            '1'..='9' => return sc(false, lower as u8 - b'1' + 0x02),
            '0' => return sc(false, 0x0b),
            ' ' => return sc(false, 0x39),
            _ => {}
        }
    }
    Some(if down { Operation::UnicodeKeyPressed(ch) } else { Operation::UnicodeKeyReleased(ch) })
}

pub struct DesktopView {
    pub session: String,
    protocol: Protocol,
    view_only: bool,
    scale_to_fit: bool,
    /// RDP: ask the server to match the tab's size.
    dynamic_resolution: bool,
    /// Last tab size reported (pixels) and when it changed, for debounced RDP resizes.
    reported: ([u32; 2], [u32; 2], f64),
    ready: bool,
    client: Option<Remote>,
    texture: Option<TextureHandle>,
    fb_size: [usize; 2],
    name: String,
    /// Modifier keysyms currently held on the remote side.
    held: Vec<u32>,
    buttons: u8,
    last_pos: (u16, u16),
    wheel: f32,
    had_focus: bool,
}

/// Keysym for a non-printing key (arrows, F-keys, Enter…); printable keys arrive as `Event::Text`.
fn named_keysym(key: Key) -> Option<u32> {
    let name = key.name();
    if name.chars().count() == 1 || key == Key::Space {
        return None;
    }
    keysym_for(name, None)
}

/// Keysym for a printable key pressed with Ctrl/Alt/Cmd (egui sends no text for those).
fn chord_keysym(key: Key, shift: bool) -> Option<u32> {
    if key == Key::Space {
        return Some(0x20);
    }
    let mut chars = key.symbol_or_name().chars();
    let c = chars.next()?;
    if chars.next().is_some() {
        return None;
    }
    keysym_for("", Some(if shift { c.to_ascii_uppercase() } else { c.to_ascii_lowercase() }))
}

/// Modifier keysyms that should be held for `m`.
fn modifier_syms(m: Modifiers) -> Vec<u32> {
    let mut v = vec![];
    if m.shift {
        v.push(XK_SHIFT);
    }
    if m.ctrl {
        v.push(XK_CONTROL);
    }
    if m.alt {
        v.push(XK_ALT);
    }
    if m.mac_cmd {
        v.push(XK_SUPER);
    }
    v
}

/// Vertical wheel movement this frame in notches (positive = up).
fn wheel_notches(events: &[Event]) -> f32 {
    events
        .iter()
        .map(|e| match e {
            Event::MouseWheel { unit, delta, .. } => match unit {
                egui::MouseWheelUnit::Point => delta.y / NOTCH,
                egui::MouseWheelUnit::Line => delta.y,
                egui::MouseWheelUnit::Page => delta.y * 5.0,
            },
            _ => 0.0,
        })
        .sum()
}

/// Pointer button mask: bit 0 left, 1 middle, 2 right.
fn button_mask(left: bool, middle: bool, right: bool) -> u8 {
    left as u8 | (middle as u8) << 1 | (right as u8) << 2
}

/// A screen position over the drawn image → framebuffer coordinates (clamped).
fn to_remote(p: Pos2, img: Rect, fb: [usize; 2]) -> (u16, u16) {
    let sx = fb[0] as f32 / img.width().max(1.0);
    let sy = fb[1] as f32 / img.height().max(1.0);
    let x = ((p.x - img.left()) * sx).clamp(0.0, fb[0].saturating_sub(1) as f32);
    let y = ((p.y - img.top()) * sy).clamp(0.0, fb[1].saturating_sub(1) as f32);
    (x as u16, y as u16)
}

impl DesktopView {
    pub fn new(session: &str, profile: &Profile) -> DesktopView {
        let (view_only, scale_to_fit, dynamic_resolution) = match (&profile.vnc, &profile.rdp) {
            (Some(v), _) if profile.protocol == Protocol::Vnc => (v.view_only, v.scale_to_fit, false),
            (_, Some(r)) => (false, r.scale_to_fit, r.dynamic_resolution && r.width == 0),
            _ => (false, true, false),
        };
        DesktopView {
            session: session.into(),
            protocol: profile.protocol,
            view_only,
            scale_to_fit,
            dynamic_resolution,
            reported: ([0, 0], [0, 0], 0.0),
            ready: false,
            client: None,
            texture: None,
            fb_size: [0, 0],
            name: String::new(),
            held: vec![],
            buttons: 0,
            last_pos: (u16::MAX, u16::MAX),
            wheel: 0.0,
            had_focus: false,
        }
    }

    /// Called on `Event::DesktopReady` (the client is connected; the view fetches it next frame).
    pub fn on_ready(&mut self) {
        self.ready = true;
        self.client = None;
    }

    fn key(&self, sym: u32, down: bool) {
        if let Some(c) = &self.client {
            c.key(sym, down, &self.held);
        }
    }

    fn tap(&self, syms: &[u32]) {
        syms.iter().for_each(|&k| self.key(k, true));
        syms.iter().rev().for_each(|&k| self.key(k, false));
    }

    fn sync_modifiers(&mut self, want: Vec<u32>) {
        for k in self.held.clone() {
            if !want.contains(&k) {
                self.key(k, false);
            }
        }
        for &k in &want {
            if !self.held.contains(&k) {
                self.key(k, true);
            }
        }
        self.held = want;
    }

    fn release_all(&mut self) {
        self.sync_modifiers(vec![]);
        if self.buttons != 0
            && let Some(c) = &self.client
        {
            c.pointer(self.last_pos.0, self.last_pos.1, 0);
        }
        self.buttons = 0;
    }

    /// Makes `text` the remote desktop's clipboard contents.
    fn paste(&self, s: &mut Shared, text: &str) {
        match &self.client {
            Some(Remote::Vnc(c)) => {
                if let Err(e) = c.send_clipboard(text) {
                    s.error(format!("Clipboard: {e}"));
                }
            }
            Some(Remote::Rdp(c)) => c.set_clipboard(text.to_string()),
            None => {}
        }
    }

    /// RDP with dynamic resolution: report the tab's pixel size once it has settled.
    fn report_size(&mut self, ui: &Ui, s: &Shared, area: Rect) {
        if self.protocol != Protocol::Rdp || !self.dynamic_resolution {
            return;
        }
        let ppp = ui.ctx().pixels_per_point();
        let size = [(area.width() * ppp) as u32 & !1, (area.height() * ppp) as u32];
        let now = ui.input(|i| i.time);
        if size != self.reported.1 {
            self.reported.1 = size;
            self.reported.2 = now;
        }
        // ponytail: 0.4 s debounce; each resize is a server-side desktop reconfiguration.
        if size != self.reported.0 && now - self.reported.2 > 0.4 && size[0] >= 200 && size[1] >= 200 {
            self.reported.0 = size;
            let _ = s.svc.sessions.resize(&self.session, size[0].min(8192) as u16, size[1].min(8192) as u16);
        } else if size != self.reported.0 {
            ui.ctx().request_repaint_after(std::time::Duration::from_millis(450));
        }
    }

    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        if self.ready && self.client.is_none() {
            self.client = match self.protocol {
                Protocol::Rdp => s.svc.sessions.rdp(&self.session).ok().map(Remote::Rdp),
                _ => s.svc.sessions.vnc(&self.session).ok().map(Remote::Vnc),
            };
            if let Some(Remote::Vnc(c)) = &self.client {
                self.name = c.desktop_name();
            }
        }
        self.upload(ui);
        self.toolbar(ui, s);

        let t = Tokens::get(ui.ctx());
        let area = ui.available_rect_before_wrap();
        self.report_size(ui, s, area);
        ui.painter().rect_filled(area, 0.0, t.canvas);
        let Some(tex) = self.texture.clone().filter(|_| self.ready) else {
            ui.allocate_rect(area, Sense::hover());
            let mut c = ui.new_child(egui::UiBuilder::new().max_rect(area));
            c.vertical_centered(|ui| {
                ui.add_space(area.height() / 2.0 - 30.0);
                ui.add(egui::Spinner::new().size(22.0));
                ui.label(RichText::new(if self.ready { "Waiting for the first frame…" } else { "Connecting…" }).color(t.text_dim));
            });
            return;
        };
        let fb = self.fb_size;
        let size = vec2(fb[0] as f32, fb[1] as f32);
        if self.scale_to_fit {
            ui.allocate_rect(area, Sense::hover());
            let scale = (area.width() / size.x).min(area.height() / size.y);
            let img = Rect::from_center_size(area.center(), size * scale);
            self.draw(ui, s, &tex, img);
        } else {
            let mut c = ui.new_child(egui::UiBuilder::new().max_rect(area));
            egui::ScrollArea::both().id_salt(("desktop", &self.session)).auto_shrink([false, false]).show(&mut c, |ui| {
                let (img, _) = ui.allocate_exact_size(size, Sense::hover());
                self.draw(ui, s, &tex, img);
            });
            ui.allocate_rect(area, Sense::hover());
        }
    }

    /// Uploads the framebuffer when the client marked it dirty.
    fn upload(&mut self, ui: &Ui) {
        let Some(c) = &self.client else { return };
        let fb = c.framebuffer();
        let mut fb = fb.lock();
        if !fb.dirty || fb.width == 0 || fb.height == 0 {
            return;
        }
        let size = [fb.width as usize, fb.height as usize];
        // Alpha is always 255, so the buffer is already premultiplied.
        let img = egui::ColorImage::from_rgba_premultiplied(size, &fb.rgba);
        fb.dirty = false;
        drop(fb);
        self.fb_size = size;
        match &mut self.texture {
            Some(t) => t.set(img, egui::TextureOptions::LINEAR),
            None => self.texture = Some(ui.ctx().load_texture(format!("desktop-{}", self.session), img, egui::TextureOptions::LINEAR)),
        }
    }

    fn toolbar(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        egui::Frame::new().fill(t.chrome).inner_margin(egui::Margin::symmetric(8, 5)).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal(|ui| {
                let (dot, status) = match (self.ready, self.name.is_empty()) {
                    (false, _) => (t.warning, "Connecting…".to_string()),
                    (true, true) => (t.success, "Connected".to_string()),
                    (true, false) => (t.success, format!("Connected — {}", self.name)),
                };
                ui.label(RichText::new("●").color(dot).size(10.0));
                ui.label(RichText::new(status).font(theme::semibold(12.5)));
                if self.fb_size[0] > 0 {
                    ui.label(RichText::new(format!("{}×{}", self.fb_size[0], self.fb_size[1])).color(t.text_faint).size(11.5));
                }
                if self.view_only {
                    widgets::chip(ui, "VIEW ONLY", t.warning).on_hover_text("Input is not sent to the remote desktop");
                }
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    ui.add_enabled_ui(self.ready, |ui| {
                        let (g, tip) = if self.scale_to_fit {
                            (icon::CORNERS_IN, "Scaled to fit (click for 1:1)")
                        } else {
                            (icon::CORNERS_OUT, "1:1 (click to scale to fit)")
                        };
                        if widgets::icon_button(ui, g, tip).clicked() {
                            self.scale_to_fit = !self.scale_to_fit;
                        }
                        if let Some(Remote::Vnc(c)) = &self.client
                            && widgets::icon_button(ui, icon::ARROW_CLOCKWISE, "Refresh the screen").clicked()
                        {
                            let _ = c.request_full_update();
                        }
                        if !self.view_only {
                            if widgets::button(ui, icon::CLIPBOARD_TEXT, "Paste Clipboard")
                                .on_hover_text("Send the local clipboard to the remote desktop")
                                .clicked()
                            {
                                match arboard::Clipboard::new().and_then(|mut c| c.get_text()) {
                                    Ok(text) if !text.is_empty() => {
                                        self.paste(s, &text);
                                        s.info("Clipboard sent");
                                    }
                                    _ => s.warn("The clipboard has no text"),
                                }
                            }
                            if widgets::button(ui, icon::KEYBOARD, "Ctrl+Alt+Del").clicked() {
                                self.tap(&[XK_CONTROL, XK_ALT, XK_DELETE]);
                            }
                        }
                    });
                });
            });
        });
    }

    fn draw(&mut self, ui: &mut Ui, s: &mut Shared, tex: &TextureHandle, img: Rect) {
        ui.painter().image(tex.id(), img, Rect::from_min_max(Pos2::ZERO, Pos2::new(1.0, 1.0)), egui::Color32::WHITE);
        let id = egui::Id::new(("desktop-input", &self.session));
        let resp = ui.interact(img, id, Sense::click_and_drag());
        if resp.clicked() || resp.drag_started() || resp.secondary_clicked() || resp.middle_clicked() {
            resp.request_focus();
        }
        let focused = resp.has_focus();
        // RDP shares the clipboard both ways: offer the local text whenever the desktop gains focus.
        if focused
            && !self.had_focus
            && let Some(Remote::Rdp(c)) = &self.client
            && let Ok(text) = arboard::Clipboard::new().and_then(|mut c| c.get_text())
            && !text.is_empty()
        {
            c.set_clipboard(text);
        }
        if focused {
            ui.painter().rect_stroke(img, 0.0, egui::Stroke::new(1.0, Tokens::get(ui.ctx()).accent.gamma_multiply(0.6)), egui::StrokeKind::Outside);
            ui.memory_mut(|m| m.set_focus_lock_filter(id, egui::EventFilter { tab: true, horizontal_arrows: true, vertical_arrows: true, escape: true }));
        } else if self.had_focus {
            self.release_all();
        }
        self.had_focus = focused;
        if self.view_only || self.client.is_none() {
            return;
        }
        self.pointer(ui, &resp, img);
        if focused {
            self.keyboard(ui, s);
        }
    }

    fn pointer(&mut self, ui: &Ui, resp: &egui::Response, img: Rect) {
        let Some(c) = self.client.clone() else { return };
        let (pos, left, middle, right, scroll) = ui.input(|i| {
            let p = &i.pointer;
            (p.latest_pos(), p.primary_down(), p.middle_down(), p.secondary_down(), wheel_notches(&i.events))
        });
        let Some(pos) = pos else { return };
        // Only while hovering the image, or while a drag that began on it continues.
        if !resp.hovered() && !resp.dragged() && self.buttons == 0 {
            return;
        }
        let buttons = if resp.hovered() || resp.dragged() || self.buttons != 0 { button_mask(left, middle, right) } else { 0 };
        let at = to_remote(pos, img, self.fb_size);
        if at != self.last_pos || buttons != self.buttons {
            c.pointer(at.0, at.1, buttons);
            self.last_pos = at;
            self.buttons = buttons;
        }
        if resp.hovered() && scroll != 0.0 {
            self.wheel += scroll;
            while self.wheel.abs() >= 1.0 {
                let bit = if self.wheel > 0.0 { 1 << 3 } else { 1 << 4 };
                c.pointer(at.0, at.1, buttons | bit);
                c.pointer(at.0, at.1, buttons);
                self.wheel -= self.wheel.signum();
            }
        }
    }

    fn keyboard(&mut self, ui: &Ui, s: &mut Shared) {
        let (events, mods) = ui.input(|i| (i.events.clone(), i.modifiers));
        self.sync_modifiers(modifier_syms(mods));
        for e in events {
            match e {
                Event::Key { key, pressed, modifiers, .. } => {
                    let sym = named_keysym(key)
                        .or_else(|| (modifiers.ctrl || modifiers.alt || modifiers.mac_cmd).then(|| chord_keysym(key, modifiers.shift)).flatten());
                    if let Some(sym) = sym {
                        self.key(sym, pressed);
                    }
                }
                Event::Text(text) => {
                    for ch in text.chars() {
                        if let Some(sym) = keysym_for("", Some(ch)) {
                            self.tap(&[sym]);
                        }
                    }
                }
                // egui turns Ctrl+C/X/V into these; forward the keystroke (and the text for paste).
                Event::Copy => self.tap(&[u32::from(b'c')]),
                Event::Cut => self.tap(&[u32::from(b'x')]),
                Event::Paste(text) => {
                    self.paste(s, &text);
                    self.tap(&[u32::from(b'v')]);
                }
                _ => {}
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keys_map_to_keysyms() {
        assert_eq!(named_keysym(Key::Enter), Some(0xff0d));
        assert_eq!(named_keysym(Key::ArrowLeft), Some(0xff51));
        assert_eq!(named_keysym(Key::F5), Some(0xffc2));
        assert_eq!(named_keysym(Key::A), None);
        assert_eq!(named_keysym(Key::Space), None);
        assert_eq!(named_keysym(Key::Comma), None);
        assert_eq!(chord_keysym(Key::A, false), Some(u32::from(b'a')));
        assert_eq!(chord_keysym(Key::A, true), Some(u32::from(b'A')));
        assert_eq!(chord_keysym(Key::Num1, false), Some(u32::from(b'1')));
        assert_eq!(chord_keysym(Key::Space, false), Some(0x20));
        assert_eq!(modifier_syms(Modifiers::CTRL | Modifiers::SHIFT), vec![XK_SHIFT, XK_CONTROL]);
    }

    #[test]
    fn keysyms_map_to_rdp_input() {
        let sc = |ext, code| format!("{:?}", Some(Operation::KeyPressed(Scancode::from_u8(ext, code))));
        let k = |sym, down, chord| format!("{:?}", rdp_key(sym, down, chord));
        assert_eq!(k(0xff0d, true, false), sc(false, 0x1c), "Enter");
        assert_eq!(k(0xff51, true, false), sc(true, 0x4b), "Left is extended");
        assert_eq!(k(0xffc9, true, false), sc(false, 0x58), "F12");
        assert_eq!(k(XK_DELETE, true, false), sc(true, 0x53));
        assert_eq!(k(u32::from(b'c'), true, false), format!("{:?}", Some(Operation::UnicodeKeyPressed('c'))), "typed text is Unicode");
        assert_eq!(k(u32::from(b'c'), true, true), sc(false, 0x2e), "Ctrl+C sends the C scancode");
        assert_eq!(k(u32::from(b'1'), false, true), format!("{:?}", Some(Operation::KeyReleased(Scancode::from_u8(false, 0x02)))));
        assert_eq!(k(0x0100_20ac, true, false), format!("{:?}", Some(Operation::UnicodeKeyPressed('€'))));
    }

    #[test]
    fn pointer_mapping() {
        assert_eq!(button_mask(true, false, true), 0b101);
        let img = Rect::from_min_size(Pos2::new(100.0, 50.0), vec2(400.0, 300.0));
        assert_eq!(to_remote(Pos2::new(100.0, 50.0), img, [800, 600]), (0, 0));
        assert_eq!(to_remote(Pos2::new(300.0, 200.0), img, [800, 600]), (400, 300));
        assert_eq!(to_remote(Pos2::new(900.0, 900.0), img, [800, 600]), (799, 599));
    }
}
