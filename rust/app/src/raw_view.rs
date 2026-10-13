// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! RAW TCP viewer: text / hex / split views of the capture, pause, clear, save, and a send bar.
//! Port of the web build's `raw.js`. Sent data comes back through `Event::RawData` ("send"), so
//! the echo of a sent entry needs no local bookkeeping.

use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::text::LayoutJob;
use egui::{Color32, FontId, RichText, Sense, TextFormat, Ui, pos2, vec2};
use egui_phosphor::regular as icon;
use nexus_core::clients::raw::format_hex_dump;
use nexus_core::clients::raw::to_display_encoding;
use nexus_core::models::{PayloadPreset, Profile};
use std::collections::VecDeque;

/// Entries kept in the viewer (the core keeps its own, larger capture history).
const MAX_ENTRIES: usize = 5000;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    Text,
    Hex,
    Split,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Format {
    Text,
    Hex,
    Escaped,
}

impl Format {
    fn label(self) -> &'static str {
        match self {
            Format::Text => "Text",
            Format::Hex => "Hex",
            Format::Escaped => "Escaped",
        }
    }
    fn from_id(s: &str) -> Format {
        match s {
            "hex" => Format::Hex,
            "escaped" => Format::Escaped,
            _ => Format::Text,
        }
    }
}

struct Entry {
    seq: u64,
    send: bool,
    timestamp: i64,
    data: Vec<u8>,
}

/// One display row. `header` rows carry the direction/time line of a hex or split entry.
#[derive(Debug, PartialEq)]
struct Row {
    seq: u64,
    send: bool,
    header: bool,
    left: String,
    /// The hex dump column in split mode.
    right: String,
}

pub struct RawView {
    pub session: String,
    show_timestamps: bool,
    show_direction: bool,
    encoding: String,
    presets: Vec<PayloadPreset>,
    mode: Mode,
    paused: bool,
    entries: VecDeque<Entry>,
    next_seq: u64,
    rows: VecDeque<Row>,
    /// Widest row in characters (for horizontal scrolling).
    widest: usize,
    tx_bytes: u64,
    rx_bytes: u64,
    format: Format,
    input: String,
}

impl RawView {
    pub fn new(session: &str, profile: &Profile) -> RawView {
        let raw = profile.raw.clone().unwrap_or_default();
        RawView {
            session: session.into(),
            show_timestamps: raw.show_timestamps,
            show_direction: raw.show_direction,
            encoding: raw.encoding,
            presets: raw.payload_presets,
            mode: Mode::Text,
            paused: false,
            entries: VecDeque::new(),
            next_seq: 0,
            rows: VecDeque::new(),
            widest: 0,
            tx_bytes: 0,
            rx_bytes: 0,
            format: Format::Text,
            input: String::new(),
        }
    }

    /// Data from `Event::RawData` (direction "send"/"recv", unix ms timestamp).
    pub fn on_data(&mut self, data: &[u8], direction: &str, timestamp: i64) {
        let send = direction == "send";
        if send {
            self.tx_bytes += data.len() as u64;
        } else {
            self.rx_bytes += data.len() as u64;
        }
        let entry = Entry { seq: self.next_seq, send, timestamp, data: data.to_vec() };
        self.next_seq += 1;
        if !self.paused {
            let rows = self.rows_for(&entry);
            self.push_rows(rows);
        }
        self.entries.push_back(entry);
        while self.entries.len() > MAX_ENTRIES {
            self.entries.pop_front();
        }
        // A paused display stays frozen; it is rebuilt on resume.
        if !self.paused
            && let Some(first) = self.entries.front().map(|e| e.seq)
        {
            while self.rows.front().is_some_and(|r| r.seq < first) {
                self.rows.pop_front();
            }
        }
    }

    fn push_rows(&mut self, rows: Vec<Row>) {
        for r in rows {
            self.widest = self.widest.max(r.left.chars().count() + r.right.chars().count());
            self.rows.push_back(r);
        }
    }

    fn rebuild(&mut self) {
        self.rows.clear();
        self.widest = 0;
        let rows: Vec<Row> = self.entries.iter().flat_map(|e| self.rows_for(e)).collect();
        self.push_rows(rows);
    }

    fn prefix(&self, e: &Entry) -> String {
        let mut p = String::new();
        if self.show_timestamps {
            p.push_str(&format!("[{}] ", format_time(e.timestamp)));
        }
        if self.show_direction {
            p.push_str(if e.send { "[TX] " } else { "[RX] " });
        }
        p
    }

    fn rows_for(&self, e: &Entry) -> Vec<Row> {
        let row = |header: bool, left: String, right: String| Row { seq: e.seq, send: e.send, header, left, right };
        match self.mode {
            Mode::Text => {
                let prefix = self.prefix(e);
                let pad = " ".repeat(prefix.chars().count());
                text_lines(&to_display_encoding(&e.data, &self.encoding))
                    .into_iter()
                    .enumerate()
                    .map(|(i, l)| row(false, format!("{}{l}", if i == 0 { &prefix } else { &pad }), String::new()))
                    .collect()
            }
            Mode::Hex => {
                let mut out = Vec::new();
                let prefix = self.prefix(e);
                if !prefix.is_empty() {
                    out.push(row(true, format!("{}· {} bytes", prefix, e.data.len()), String::new()));
                }
                out.extend(format_hex_dump(&e.data).lines().map(|l| row(false, l.to_string(), String::new())));
                out
            }
            Mode::Split => {
                let mut head = String::new();
                if self.show_timestamps {
                    head.push_str(&format_time(e.timestamp));
                    head.push(' ');
                }
                head.push_str(if e.send { "▲ TX" } else { "▼ RX" });
                let mut out = vec![row(true, format!("{head} · {} bytes", e.data.len()), String::new())];
                let text = text_lines(&to_display_encoding(&e.data, &self.encoding));
                let dump = format_hex_dump(&e.data);
                let hex: Vec<&str> = dump.lines().collect();
                for i in 0..text.len().max(hex.len()) {
                    out.push(row(false, text.get(i).cloned().unwrap_or_default(), hex.get(i).map(|s| s.to_string()).unwrap_or_default()));
                }
                out
            }
        }
    }

    fn set_mode(&mut self, mode: Mode) {
        if self.mode != mode {
            self.mode = mode;
            self.rebuild();
        }
    }

    fn send(&mut self, s: &mut Shared) {
        if self.input.is_empty() {
            return;
        }
        let m = &s.svc.sessions;
        let r = match self.format {
            Format::Text => m.raw_write_text(&self.session, &self.input),
            Format::Hex => m.raw_write_hex(&self.session, &self.input),
            Format::Escaped => m.raw_write_escaped(&self.session, &self.input),
        };
        match r {
            Ok(()) => self.input.clear(),
            Err(e) => s.error(format!("Send failed: {e}")),
        }
    }

    fn save(&mut self, s: &mut Shared) {
        if self.entries.is_empty() {
            s.warn("Nothing to save");
            return;
        }
        let Some(path) = rfd::FileDialog::new().set_file_name("capture.json").add_filter("JSON", &["json"]).save_file() else {
            return;
        };
        match s.svc.sessions.raw_save_capture(&self.session, &path) {
            Ok(()) => s.success("Capture saved"),
            Err(e) => s.error(format!("Save failed: {e}")),
        }
    }

    fn clear(&mut self, s: &mut Shared) {
        self.entries.clear();
        self.rows.clear();
        self.widest = 0;
        self.tx_bytes = 0;
        self.rx_bytes = 0;
        let _ = s.svc.sessions.raw_clear_history(&self.session);
    }

    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        egui::Frame::new().fill(t.chrome).inner_margin(egui::Margin::symmetric(8, 5)).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal(|ui| self.toolbar(ui, s));
        });
        let send_h = 40.0;
        let h = (ui.available_height() - send_h).max(40.0);
        let (rect, _) = ui.allocate_exact_size(vec2(ui.available_width(), h), Sense::hover());
        ui.painter().rect_filled(rect, 0.0, t.canvas);
        let mut child = ui.new_child(egui::UiBuilder::new().max_rect(rect.shrink2(vec2(8.0, 4.0))));
        self.display(&mut child);
        egui::Frame::new().fill(t.chrome).inner_margin(egui::Margin::symmetric(8, 6)).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal(|ui| self.send_bar(ui, s));
        });
    }

    fn toolbar(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        for (m, label, glyph) in [(Mode::Text, "Text", icon::TEXT_AA), (Mode::Hex, "Hex", icon::HASH), (Mode::Split, "Split", icon::COLUMNS)] {
            if ui.selectable_label(self.mode == m, format!("{glyph} {label}")).clicked() {
                self.set_mode(m);
            }
        }
        ui.separator();
        let (g, label) = if self.paused { (icon::PLAY, "Resume") } else { (icon::PAUSE, "Pause") };
        if widgets::button(ui, g, label).clicked() {
            self.paused = !self.paused;
            if !self.paused {
                self.rebuild();
            }
        }
        if widgets::button(ui, icon::TRASH, "Clear").clicked() {
            self.clear(s);
        }
        if widgets::button(ui, icon::FLOPPY_DISK, "Save").clicked() {
            self.save(s);
        }
        if self.paused {
            widgets::chip(ui, "PAUSED", t.warning);
        }
        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
            let total = self.tx_bytes + self.rx_bytes;
            ui.label(RichText::new(widgets::human_size(total)).font(theme::semibold(12.0)));
            ui.label(RichText::new(format!("{} RX {}", icon::ARROW_DOWN, widgets::human_size(self.rx_bytes))).color(t.text_dim).size(11.5));
            ui.label(RichText::new(format!("{} TX {}", icon::ARROW_UP, widgets::human_size(self.tx_bytes))).color(t.accent).size(11.5));
            ui.label(RichText::new(format!("{} entries", widgets::group(self.entries.len() as u64))).color(t.text_faint).size(11.5));
        });
    }

    fn display(&mut self, ui: &mut Ui) {
        let t = Tokens::get(ui.ctx());
        if self.rows.is_empty() {
            widgets::empty(ui, icon::BROADCAST, if self.paused { "Paused" } else { "No data yet" }, "Received and sent bytes appear here");
            return;
        }
        let font = theme::mono(12.5);
        let (cw, rh) = ui.fonts_mut(|f| (f.glyph_width(&font, '0'), f.row_height(&font)));
        let split = self.mode == Mode::Split;
        let avail = ui.available_width();
        // Split mode: the text column takes what the hex dump (77 chars) leaves, at least 30 chars.
        let hex_w = 79.0 * cw;
        let left_w = if split { (avail - hex_w - 12.0).max(30.0 * cw) } else { 0.0 };
        let width = if split { left_w + 12.0 + hex_w } else { self.widest as f32 * cw + 16.0 };
        ui.spacing_mut().item_spacing.y = 0.0;
        egui::ScrollArea::both().auto_shrink([false, false]).stick_to_bottom(true).show_rows(ui, rh, self.rows.len(), |ui, range| {
            for i in range {
                let row = &self.rows[i];
                let (rect, _) = ui.allocate_exact_size(vec2(width.max(avail), rh), Sense::hover());
                let fg = if row.send { t.accent } else { t.byte_print };
                let p = ui.painter();
                if row.header {
                    p.rect_filled(rect, 0.0, t.text.gamma_multiply(0.04));
                    p.text(rect.left_center(), egui::Align2::LEFT_CENTER, &row.left, font.clone(), if row.send { t.accent } else { t.text_dim });
                    continue;
                }
                match self.mode {
                    Mode::Text => {
                        p.text(rect.left_center(), egui::Align2::LEFT_CENTER, &row.left, font.clone(), fg);
                    }
                    Mode::Hex => {
                        let g = ui.fonts_mut(|f| f.layout_job(hex_job(&row.left, &font, &t, fg)));
                        p.galley(pos2(rect.left(), rect.top()), g, fg);
                    }
                    Mode::Split => {
                        let clip = egui::Rect::from_min_size(rect.min, vec2(left_w, rh)).intersect(ui.clip_rect());
                        p.with_clip_rect(clip).text(rect.left_center(), egui::Align2::LEFT_CENTER, &row.left, font.clone(), fg);
                        let x = rect.left() + left_w + 6.0;
                        p.line_segment([pos2(x, rect.top()), pos2(x, rect.bottom())], egui::Stroke::new(1.0, t.separator));
                        if !row.right.is_empty() {
                            let g = ui.fonts_mut(|f| f.layout_job(hex_job(&row.right, &font, &t, fg)));
                            p.galley(pos2(x + 6.0, rect.top()), g, fg);
                        }
                    }
                }
            }
        });
    }

    fn send_bar(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        egui::ComboBox::from_id_salt(("raw-format", &self.session)).width(84.0).selected_text(self.format.label()).show_ui(ui, |ui| {
            for f in [Format::Text, Format::Hex, Format::Escaped] {
                ui.selectable_value(&mut self.format, f, f.label());
            }
        });
        // Right-hand controls are laid out first so the input takes the remaining width.
        let mut send = false;
        let mut insert: Option<&str> = None;
        let mut focus = false;
        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
            send |= ui.add(egui::Button::new(RichText::new(format!("{}  Send", icon::PAPER_PLANE_RIGHT)).color(t.accent_text)).fill(t.accent)).clicked();
            if !self.presets.is_empty() {
                egui::ComboBox::from_id_salt(("raw-presets", &self.session)).width(110.0).selected_text("Presets…").show_ui(ui, |ui| {
                    for p in &self.presets {
                        if ui.selectable_label(false, &p.name).clicked() {
                            self.input = p.payload.clone();
                            self.format = Format::from_id(&p.format);
                            focus = true;
                        }
                    }
                });
            }
            for (label, esc) in [("EOT", "\\x04"), ("ETX", "\\x03"), ("LF", "\\n"), ("CR", "\\r"), ("NUL", "\\x00")] {
                if ui.small_button(label).on_hover_text(esc).clicked() {
                    insert = Some(esc);
                }
            }
            let r = ui.add(
                egui::TextEdit::singleline(&mut self.input)
                    .id_salt(("raw-input", &self.session))
                    .font(theme::mono(12.5))
                    .hint_text("Type data to send…")
                    .desired_width(ui.available_width()),
            );
            if r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter)) {
                send = true;
                focus = true;
            }
            if let Some(esc) = insert {
                self.insert_byte(esc);
                focus = true;
            }
            if focus {
                r.request_focus();
            }
        });
        if send {
            self.send(s);
        }
    }

    /// Appends a quick byte in the current format (Text switches to Escaped so it isn't sent literally).
    fn insert_byte(&mut self, esc: &str) {
        match self.format {
            Format::Hex => {
                if !self.input.is_empty() && !self.input.ends_with(' ') {
                    self.input.push(' ');
                }
                self.input.push_str(&esc_to_hex(esc));
            }
            Format::Text | Format::Escaped => {
                self.format = Format::Escaped;
                self.input.push_str(esc);
            }
        }
    }
}

/// "\\x04" / "\\n" → "04" / "0a".
fn esc_to_hex(esc: &str) -> String {
    nexus_core::clients::raw::parse_escaped(esc).map(|b| nexus_core::clients::raw::format_hex(&b)).unwrap_or_default()
}

/// Splits decoded text into display lines: CRLF/LF break lines, other control characters show as '·'.
fn text_lines(s: &str) -> Vec<String> {
    let s = s.strip_suffix('\n').unwrap_or(s);
    s.split('\n')
        .map(|l| {
            l.trim_end_matches('\r')
                .chars()
                .map(|c| match c {
                    '\t' => "    ".to_string(),
                    c if c.is_control() => "·".to_string(),
                    c => c.to_string(),
                })
                .collect()
        })
        .collect()
}

/// A hex dump line coloured by column: faint offset, bytes, dim ASCII.
fn hex_job(line: &str, font: &FontId, t: &Tokens, fg: Color32) -> LayoutJob {
    let mut job = LayoutJob::default();
    let fmt = |c: Color32| TextFormat { font_id: font.clone(), color: c, ..Default::default() };
    let (offset, rest) = line.split_at(line.len().min(10));
    let (bytes, ascii) = rest.find('|').map(|i| rest.split_at(i)).unwrap_or((rest, ""));
    job.append(offset, 0.0, fmt(t.text_faint));
    job.append(bytes, 0.0, fmt(fg));
    job.append(ascii, 0.0, fmt(t.text_dim));
    job
}

/// HH:MM:SS for today, otherwise "Mon DD HH:MM:SS" (the web build's formatTimestamp).
fn format_time(ms: i64) -> String {
    use chrono::{Local, TimeZone};
    let Some(d) = Local.timestamp_millis_opt(ms).single() else {
        return String::new();
    };
    if d.date_naive() == Local::now().date_naive() { d.format("%H:%M:%S").to_string() } else { d.format("%b %d %H:%M:%S").to_string() }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn view() -> RawView {
        let mut p = Profile::new(nexus_core::models::Protocol::Raw);
        p.raw = Some(nexus_core::models::RawConfig { show_direction: true, ..Default::default() });
        RawView::new("s", &p)
    }

    #[test]
    fn text_rows_decode_utf8_and_prefix_direction() {
        let mut v = view();
        v.on_data("héllo\r\nwörld\n".as_bytes(), "recv", 0);
        v.on_data(b"ping\x01", "send", 0);
        let rows: Vec<&str> = v.rows.iter().map(|r| r.left.as_str()).collect();
        assert_eq!(rows, ["[RX] héllo", "     wörld", "[TX] ping·"]);
        assert_eq!((v.rx_bytes, v.tx_bytes), (15, 5));
    }

    #[test]
    fn hex_and_split_rows() {
        let mut v = view();
        v.on_data(&[0x41; 20], "recv", 0);
        v.set_mode(Mode::Hex);
        assert_eq!(v.rows.len(), 3); // header + 2 dump lines
        assert!(v.rows[1].left.starts_with("00000000  41 41"));
        v.set_mode(Mode::Split);
        assert!(v.rows[0].header && v.rows[0].left.contains("▼ RX"));
        assert_eq!(v.rows[1].left, "A".repeat(20));
        assert!(v.rows[2].right.starts_with("00000010"));
    }

    #[test]
    fn pause_freezes_and_buffer_caps() {
        let mut v = view();
        v.paused = true;
        for _ in 0..MAX_ENTRIES + 10 {
            v.on_data(b"x", "recv", 0);
        }
        assert!(v.rows.is_empty());
        assert_eq!(v.entries.len(), MAX_ENTRIES);
        v.paused = false;
        v.rebuild();
        assert_eq!(v.rows.len(), MAX_ENTRIES);
        v.on_data(b"y", "recv", 0);
        assert_eq!(v.rows.len(), MAX_ENTRIES);
        assert_eq!(v.rows.back().unwrap().left, "[RX] y");
    }

    #[test]
    fn quick_bytes() {
        assert_eq!(esc_to_hex("\\x04"), "04");
        assert_eq!(esc_to_hex("\\n"), "0a");
        let mut v = view();
        v.insert_byte("\\r");
        assert!(v.format == Format::Escaped && v.input == "\\r");
        v.format = Format::Hex;
        v.input = "41".into();
        v.insert_byte("\\x00");
        assert_eq!(v.input, "41 00");
    }
}
