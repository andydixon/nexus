//! IRC client view: room tabs, message log, composer and room/user lists. Port of `irc.js`;
//! lines are `ircformat::format_event` spans drawn in the terminal theme's colours.

use crate::state::Shared;
use crate::terminal_themes::TermTheme;
use crate::theme::Tokens;
use crate::widgets;
use egui::text::LayoutJob;
use egui::{Color32, FontId, Galley, Rect, RichText, Sense, Stroke, TextFormat, Ui, pos2, vec2};
use egui_phosphor::regular as icon;
use nexus_core::clients::irc::{self, RoomInfo};
use nexus_core::ircformat::{self, Span};
use nexus_core::models::Profile;
use std::collections::{HashMap, HashSet, VecDeque};
use std::sync::Arc;

const SERVER: &str = "server";
const MAX_LINES: usize = 2000;
const SIDEBAR_W: f32 = 230.0;

struct Line {
    spans: Vec<Span>,
    /// Laid-out line for the wrap width and theme it was built with.
    galley: Option<(f32, &'static str, Arc<Galley>)>,
}

struct Target {
    name: String,
    lines: VecDeque<Line>,
}

/// Where an event goes (irc.js `handleEvent`).
#[derive(Debug, PartialEq)]
enum Route {
    Rooms,
    Users(String),
    RoomList,
    Line(String),
}

fn route(e: &irc::Event) -> Route {
    match e.kind.as_str() {
        "rooms" => Route::Rooms,
        "users" => Route::Users(e.target.clone()),
        "room-list" => Route::RoomList,
        _ => Route::Line(if e.target.is_empty() { SERVER.into() } else { e.target.clone() }),
    }
}

fn is_room(t: &str) -> bool {
    t.starts_with(['#', '&', '+', '!'])
}

fn display_nick(u: &str) -> &str {
    u.trim_start_matches(['~', '&', '@', '%', '+'])
}

/// Tab completion of the last word: `@pre` → `@nick `, `pre` → `nick ` (first match, case-insensitive).
fn complete(input: &str, users: &[String]) -> Option<String> {
    let start = input.rfind(char::is_whitespace).map_or(0, |i| i + 1);
    let word = &input[start..];
    let (at, prefix) = match word.strip_prefix('@') {
        Some(p) => ("@", p),
        None => ("", word),
    };
    if prefix.contains('@') || (at.is_empty() && prefix.is_empty()) {
        return None;
    }
    let lower = prefix.to_lowercase();
    let nick = users.iter().map(|u| display_nick(u)).find(|n| n.to_lowercase().starts_with(&lower))?;
    Some(format!("{}{at}{nick} ", &input[..start]))
}

fn time(ms: i64) -> String {
    use chrono::{Local, TimeZone};
    let d = if ms > 0 { Local.timestamp_millis_opt(ms).single() } else { None }.unwrap_or_else(Local::now);
    d.format("%H:%M:%S").to_string()
}

/// xterm-256 palette: the theme's 16 colours, the 6×6×6 cube, then the grey ramp.
fn xterm(i: u8, theme: &TermTheme) -> Color32 {
    let i = i as usize;
    if i < 16 {
        let [r, g, b, _] = theme.ansi[i];
        return Color32::from_rgb(r, g, b);
    }
    if i < 232 {
        let i = i - 16;
        let v = |x: usize| if x == 0 { 0 } else { (55 + x * 40) as u8 };
        return Color32::from_rgb(v(i / 36), v((i / 6) % 6), v(i % 6));
    }
    let g = (8 + (i - 232) * 10) as u8;
    Color32::from_rgb(g, g, g)
}

fn rgb4(c: [u8; 4]) -> Color32 {
    Color32::from_rgb(c[0], c[1], c[2])
}

fn layout(spans: &[Span], theme: &TermTheme, font: &FontId, wrap: f32) -> LayoutJob {
    let mut job = LayoutJob { wrap: egui::text::TextWrapping { max_width: wrap, ..Default::default() }, ..Default::default() };
    let (dfg, dbg) = (rgb4(theme.foreground), Color32::TRANSPARENT);
    for s in spans {
        let st = s.style;
        let mut fg = st.fg_rgb.map(|[r, g, b]| Color32::from_rgb(r, g, b)).or(st.fg.map(|i| xterm(i, theme))).unwrap_or(dfg);
        let mut bg = st.bg_rgb.map(|[r, g, b]| Color32::from_rgb(r, g, b)).or(st.bg.map(|i| xterm(i, theme))).unwrap_or(dbg);
        if st.reverse {
            (fg, bg) = (if bg == Color32::TRANSPARENT { rgb4(theme.background) } else { bg }, fg);
        }
        if st.dim {
            fg = fg.gamma_multiply(0.65);
        }
        let line = Stroke::new(1.0, fg);
        job.append(
            &s.text,
            0.0,
            TextFormat {
                font_id: font.clone(),
                color: fg,
                background: bg,
                italics: st.italic,
                underline: if st.underline { line } else { Stroke::NONE },
                strikethrough: if st.strike { line } else { Stroke::NONE },
                ..Default::default()
            },
        );
    }
    job
}

pub struct IrcView {
    pub session: String,
    targets: Vec<Target>,
    active: String,
    unread: HashSet<String>,
    users: HashMap<String, Vec<String>>,
    joined: Vec<RoomInfo>,
    room_list: Vec<RoomInfo>,
    listing: bool,
    room_filter: String,
    join_input: String,
    input: String,
    refreshed: bool,
    focus_composer: bool,
    font_size: f32,
}

impl IrcView {
    pub fn new(session: &str, profile: &Profile) -> IrcView {
        let mut v = IrcView {
            session: session.into(),
            targets: vec![],
            active: SERVER.into(),
            unread: HashSet::new(),
            users: HashMap::new(),
            joined: vec![],
            room_list: vec![],
            listing: false,
            room_filter: String::new(),
            join_input: String::new(),
            input: String::new(),
            refreshed: false,
            focus_composer: true,
            font_size: if profile.terminal_font_size > 0 { profile.terminal_font_size as f32 } else { 13.0 },
        };
        v.append(SERVER, &irc::Event { kind: "system".into(), text: "Connecting...".into(), ..Default::default() });
        v
    }

    fn target(&mut self, name: &str) -> &mut Target {
        let name = if name.is_empty() { SERVER } else { name };
        let i = match self.targets.iter().position(|t| t.name == name) {
            Some(i) => i,
            None => {
                self.targets.push(Target { name: name.into(), lines: VecDeque::new() });
                self.targets.len() - 1
            }
        };
        &mut self.targets[i]
    }

    fn append(&mut self, target: &str, e: &irc::Event) {
        let spans = ircformat::format_event(e, &time(e.timestamp));
        let t = self.target(target);
        t.lines.push_back(Line { spans, galley: None });
        while t.lines.len() > MAX_LINES {
            t.lines.pop_front();
        }
    }

    pub fn on_event(&mut self, event: irc::Event) {
        match route(&event) {
            Route::Rooms => {
                // Show the first room once auto-join lands, rather than leaving the server log up.
                let first = self.joined.is_empty() && self.active == SERVER;
                self.joined = event.rooms;
                if first && let Some(r) = self.joined.first().map(|r| r.name.clone()) {
                    self.set_active(&r);
                }
                for r in self.joined.clone() {
                    self.target(&r.name);
                }
            }
            Route::Users(target) => {
                if !target.is_empty() {
                    self.users.insert(target, event.users);
                }
            }
            Route::RoomList => {
                self.room_list = event.rooms;
                self.listing = false;
            }
            Route::Line(target) => {
                self.append(&target, &event);
                if target != self.active {
                    self.unread.insert(target);
                }
            }
        }
    }

    fn set_active(&mut self, target: &str) {
        self.target(target);
        self.active = target.into();
        self.unread.remove(target);
        self.focus_composer = true;
    }

    fn send(&mut self, s: &mut Shared) {
        let text = std::mem::take(&mut self.input);
        if text.trim().is_empty() {
            return;
        }
        let r = s.svc.sessions.irc(&self.session).and_then(|c| c.handle_input(&self.active, &text));
        if let Err(e) = r {
            s.error(e.to_string());
            let active = self.active.clone();
            self.append(&active, &irc::Event { kind: "error".into(), text: e.to_string(), ..Default::default() });
        }
    }

    fn join(&mut self, s: &mut Shared, room: &str) {
        if let Err(e) = s.svc.sessions.irc(&self.session).and_then(|c| c.join(room)) {
            s.error(format!("Join failed: {e}"));
        }
    }

    fn insert_mention(&mut self, nick: &str) {
        if !self.input.is_empty() && !self.input.ends_with(char::is_whitespace) {
            self.input.push(' ');
        }
        self.input.push_str(&format!("@{nick} "));
        self.focus_composer = true;
    }

    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        if !self.refreshed {
            self.refreshed = true;
            if let Ok(c) = s.svc.sessions.irc(&self.session) {
                self.on_event(irc::Event { kind: "rooms".into(), rooms: c.rooms(), ..Default::default() });
            }
        }
        let t = Tokens::get(ui.ctx());
        let full = ui.available_rect_before_wrap();
        let side_w = SIDEBAR_W.min(full.width() * 0.4);
        let main = Rect::from_min_max(full.min, pos2(full.right() - side_w, full.bottom()));
        let side = Rect::from_min_max(pos2(main.right(), full.top()), full.max);
        ui.allocate_rect(full, Sense::hover());

        let tabs_h = 32.0;
        let comp_h = 40.0;
        let tabs = Rect::from_min_max(main.min, pos2(main.right(), main.top() + tabs_h));
        let log = Rect::from_min_max(pos2(main.left(), tabs.bottom()), pos2(main.right(), main.bottom() - comp_h));
        let comp = Rect::from_min_max(pos2(main.left(), log.bottom()), main.max);

        ui.painter().rect_filled(tabs, 0.0, t.tab_strip);
        let mut c = ui.new_child(egui::UiBuilder::new().max_rect(tabs.shrink2(vec2(6.0, 4.0))).layout(egui::Layout::left_to_right(egui::Align::Center)));
        self.tabs(&mut c, s);

        let theme = crate::terminal_themes::get(&s.prefs.terminal_theme);
        ui.painter().rect_filled(log, 0.0, rgb4(theme.background));
        let mut c = ui.new_child(egui::UiBuilder::new().max_rect(log.shrink2(vec2(10.0, 6.0))));
        self.log(&mut c, theme);

        ui.painter().rect_filled(comp, 0.0, t.chrome);
        let mut c = ui.new_child(egui::UiBuilder::new().max_rect(comp.shrink2(vec2(8.0, 6.0))).layout(egui::Layout::left_to_right(egui::Align::Center)));
        self.composer(&mut c, s);

        ui.painter().rect_filled(side, 0.0, t.dock);
        ui.painter().line_segment([side.left_top(), side.left_bottom()], Stroke::new(1.0, t.separator));
        let mut c = ui.new_child(egui::UiBuilder::new().max_rect(side.shrink2(vec2(10.0, 8.0))));
        self.sidebar(&mut c, s);
    }

    fn tabs(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        egui::ScrollArea::horizontal().id_salt("irc-tabs").show(ui, |ui| {
            ui.horizontal(|ui| {
                let names: Vec<String> = self.targets.iter().map(|t| t.name.clone()).collect();
                for name in names {
                    let label = if name == SERVER { format!("{} Server", icon::HARD_DRIVES) } else { name.clone() };
                    let unread = self.unread.contains(&name);
                    let text = if unread { RichText::new(format!("{label} •")).color(t.accent) } else { RichText::new(label) };
                    let r = ui.selectable_label(self.active == name, text);
                    if r.clicked() {
                        self.set_active(&name);
                    }
                    if is_room(&name) {
                        r.context_menu(|ui| {
                            if ui.button(format!("{}  Leave {name}", icon::SIGN_OUT)).clicked()
                                && let Err(e) = s.svc.sessions.irc(&self.session).and_then(|c| c.part(&name))
                            {
                                s.error(format!("Part failed: {e}"));
                            }
                        });
                    }
                }
            });
        });
    }

    fn log(&mut self, ui: &mut Ui, theme: &'static TermTheme) {
        let font = FontId::monospace(self.font_size);
        let wrap = ui.available_width().max(100.0);
        let Some(target) = self.targets.iter_mut().find(|t| t.name == self.active) else {
            return;
        };
        // Lay out lines once per width/theme; the galley cache keeps scrolling cheap.
        let mut tops = Vec::with_capacity(target.lines.len());
        let mut y = 0.0;
        for line in target.lines.iter_mut() {
            let stale = line.galley.as_ref().is_none_or(|(w, id, _)| *w != wrap || *id != theme.id);
            if stale {
                let g = ui.fonts_mut(|f| f.layout_job(layout(&line.spans, theme, &font, wrap)));
                line.galley = Some((wrap, theme.id, g));
            }
            tops.push(y);
            y += line.galley.as_ref().map_or(0.0, |(_, _, g)| g.size().y) + 1.0;
        }
        let total = y;
        egui::ScrollArea::vertical().id_salt(("irc-log", &self.active)).auto_shrink([false, false]).stick_to_bottom(true).show_viewport(ui, |ui, vp| {
            let (rect, _) = ui.allocate_exact_size(vec2(wrap, total), Sense::hover());
            let first = tops.partition_point(|&top| top < vp.min.y).saturating_sub(1);
            for (i, line) in target.lines.iter().enumerate().skip(first) {
                if tops[i] > vp.max.y {
                    break;
                }
                if let Some((_, _, g)) = &line.galley {
                    ui.painter().galley(pos2(rect.left(), rect.top() + tops[i]), g.clone(), rgb4(theme.foreground));
                }
            }
        });
    }

    fn composer(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        let id = egui::Id::new(("irc-composer", &self.session));
        // Tab completes nicks instead of moving focus.
        if ui.memory(|m| m.has_focus(id)) && ui.input_mut(|i| i.consume_key(egui::Modifiers::NONE, egui::Key::Tab)) && is_room(&self.active) {
            let users = self.users.get(&self.active).cloned().unwrap_or_default();
            if let Some(done) = complete(&self.input, &users) {
                self.input = done;
                self.move_cursor_to_end(ui, id);
            }
        }
        let mut send = false;
        ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
            send |= ui.add(egui::Button::new(RichText::new(format!("{}  Send", icon::PAPER_PLANE_RIGHT)).color(t.accent_text)).fill(t.accent)).clicked();
            let hint = if self.active == SERVER { "/command or /msg <nick> <message>".to_string() } else { format!("Message {} or /command", self.active) };
            let r = ui.add(egui::TextEdit::singleline(&mut self.input).id(id).hint_text(hint).desired_width(ui.available_width()));
            if r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter)) {
                send = true;
                self.focus_composer = true;
            }
        });
        if send {
            self.send(s);
        }
        if std::mem::take(&mut self.focus_composer) {
            ui.memory_mut(|m| m.request_focus(id));
            self.move_cursor_to_end(ui, id);
        }
    }

    fn move_cursor_to_end(&self, ui: &Ui, id: egui::Id) {
        if let Some(mut st) = egui::TextEdit::load_state(ui.ctx(), id) {
            let end = egui::text::CCursor::new(self.input.chars().count());
            st.cursor.set_char_range(Some(egui::text::CCursorRange::one(end)));
            st.store(ui.ctx(), id);
        }
    }

    fn sidebar(&mut self, ui: &mut Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        let total_h = ui.available_height();

        widgets::section(ui, "Rooms");
        let mut join = false;
        ui.horizontal(|ui| {
            let r = ui.add(egui::TextEdit::singleline(&mut self.join_input).hint_text("#room").desired_width(ui.available_width() - 52.0));
            join |= r.lost_focus() && ui.input(|i| i.key_pressed(egui::Key::Enter));
            join |= ui.button("Join").clicked();
        });
        if join && !self.join_input.trim().is_empty() {
            let room = self.join_input.trim().to_string();
            self.join(s, &room);
            self.join_input.clear();
        }
        let rows_h = 22.0;
        if self.joined.is_empty() {
            widgets::hint(ui, "No joined rooms");
        } else {
            let joined = self.joined.clone();
            let h = (joined.len() as f32 * (rows_h + 2.0)).min(total_h * 0.22);
            ui.allocate_ui(vec2(ui.available_width(), h), |ui| {
                let sel = joined.iter().position(|r| r.name == self.active);
                let clicked = widgets::rows(ui, "irc-joined", joined.len(), rows_h, sel, |ui, i| {
                    let r = &joined[i];
                    ui.label(&r.name);
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        ui.label(RichText::new(r.users.to_string()).color(t.text_faint).size(11.0));
                    });
                    let tip = ircformat::plain_text(if r.topic.is_empty() { &r.name } else { &r.topic });
                    ui.interact(ui.max_rect(), ui.id().with("tip"), Sense::hover()).on_hover_text(tip);
                });
                if let Some(i) = clicked {
                    self.set_active(&joined[i].name);
                }
            });
        }

        ui.add_space(6.0);
        ui.horizontal(|ui| {
            widgets::section(ui, "Room List");
            ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                if ui.small_button(format!("{} List", icon::LIST_BULLETS)).clicked() {
                    self.room_list.clear();
                    self.listing = true;
                    if let Err(e) = s.svc.sessions.irc(&self.session).and_then(|c| c.list_rooms()) {
                        self.listing = false;
                        s.error(format!("Room list failed: {e}"));
                    }
                }
            });
        });
        ui.add(egui::TextEdit::singleline(&mut self.room_filter).hint_text("Filter rooms").desired_width(ui.available_width()));
        let filter = self.room_filter.to_lowercase();
        let listed: Vec<&RoomInfo> =
            self.room_list.iter().filter(|r| filter.is_empty() || format!("{} {}", r.name, r.topic).to_lowercase().contains(&filter)).collect();
        let list_h = total_h * 0.3;
        let mut join_room = None;
        if listed.is_empty() {
            widgets::hint(ui, if self.listing { "Loading rooms..." } else { "No rooms loaded" });
        } else {
            ui.allocate_ui(vec2(ui.available_width(), list_h), |ui| {
                let clicked = widgets::rows(ui, "irc-room-list", listed.len(), rows_h, None, |ui, i| {
                    let r = listed[i];
                    ui.label(&r.name);
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        ui.label(RichText::new(r.users.to_string()).color(t.text_faint).size(11.0));
                        ui.label(RichText::new(ircformat::plain_text(&r.topic)).color(t.text_faint).size(11.0));
                    });
                    ui.interact(ui.max_rect(), ui.id().with("tip"), Sense::hover())
                        .on_hover_text(format!("{}\nClick to join", ircformat::plain_text(if r.topic.is_empty() { &r.name } else { &r.topic })));
                });
                join_room = clicked.map(|i| listed[i].name.clone());
            });
        }
        if let Some(room) = join_room {
            self.join(s, &room);
        }

        ui.add_space(6.0);
        let users = if is_room(&self.active) { self.users.get(&self.active).cloned() } else { None };
        widgets::section(ui, &format!("Users{}", users.as_ref().map(|u| format!(" ({})", u.len())).unwrap_or_default()));
        match users {
            _ if !is_room(&self.active) => widgets::hint(ui, "No room selected"),
            Some(u) if !u.is_empty() => {
                let clicked = widgets::rows(ui, "irc-users", u.len(), rows_h, None, |ui, i| {
                    let nick = display_nick(&u[i]);
                    ui.label(RichText::new(nick).color(xterm(ircformat::nick_colour(nick), crate::terminal_themes::get(&s.prefs.terminal_theme))));
                    ui.interact(ui.max_rect(), ui.id().with("tip"), Sense::hover()).on_hover_text(format!("{}\nClick to mention", u[i]));
                });
                if let Some(i) = clicked {
                    self.insert_mention(display_nick(&u[i]));
                }
            }
            _ => widgets::hint(ui, "No users yet"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(kind: &str, target: &str) -> irc::Event {
        irc::Event { kind: kind.into(), target: target.into(), text: "hi".into(), ..Default::default() }
    }

    #[test]
    fn routes_like_irc_js() {
        assert_eq!(route(&ev("rooms", "")), Route::Rooms);
        assert_eq!(route(&ev("users", "#a")), Route::Users("#a".into()));
        assert_eq!(route(&ev("room-list", "")), Route::RoomList);
        assert_eq!(route(&ev("message", "#a")), Route::Line("#a".into()));
        assert_eq!(route(&ev("quit", "")), Route::Line(SERVER.into()));
    }

    #[test]
    fn events_fill_targets_and_unread() {
        let mut v = IrcView::new("s", &Profile::new(nexus_core::models::Protocol::Irc));
        v.on_event(ev("message", "#rust"));
        v.on_event(ev("nick", ""));
        assert_eq!(v.targets.iter().map(|t| t.name.as_str()).collect::<Vec<_>>(), [SERVER, "#rust"]);
        assert!(v.unread.contains("#rust") && !v.unread.contains(SERVER));
        assert_eq!(v.targets[0].lines.len(), 2); // "Connecting..." + nick
        v.set_active("#rust");
        assert!(v.unread.is_empty());
        for _ in 0..MAX_LINES + 5 {
            v.on_event(ev("message", "#rust"));
        }
        assert_eq!(v.targets[1].lines.len(), MAX_LINES);
    }

    #[test]
    fn nick_completion() {
        let users = vec!["@Alice".to_string(), "+bob".to_string()];
        assert_eq!(complete("hi @al", &users).as_deref(), Some("hi @Alice "));
        assert_eq!(complete("bo", &users).as_deref(), Some("bob "));
        assert_eq!(complete("hi ", &users), None);
        assert_eq!(complete("x @zz", &users), None);
        assert_eq!(display_nick("~&@%+nick"), "nick");
    }

    #[test]
    fn palette() {
        let th = crate::terminal_themes::get("charcoal");
        assert_eq!(xterm(16, th), Color32::from_rgb(0, 0, 0));
        assert_eq!(xterm(231, th), Color32::from_rgb(255, 255, 255));
        assert_eq!(xterm(244, th), Color32::from_rgb(128, 128, 128));
    }
}
