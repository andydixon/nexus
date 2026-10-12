//! Recordings browser and playback window (port of the RecordingManager in recordings.js).

use crate::state::Shared;
use crate::terminal::{CursorStyle, TermView};
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::{Key, RichText};
use egui_phosphor::regular as icon;
use nexus_core::recording::{CleanupResult, EVENT_OUTPUT, Event, Header, RecordingMeta, StorageInfo};
use std::sync::mpsc::{Receiver, channel};

const AGES: [(u32, &str); 4] = [(30, "Older than 30 days"), (7, "Older than 7 days"), (90, "Older than 90 days"), (0, "Ignore age")];
const SIZES: [(u64, &str); 3] = [(1 << 30, "Keep under 1 GB"), (5 << 30, "Keep under 5 GB"), (0, "Ignore size")];
const SPEEDS: [f64; 5] = [0.5, 1.0, 2.0, 5.0, 10.0];

/// m:ss, as the JS `_formatSeconds`.
pub fn mmss(secs: f64) -> String {
    if secs.is_nan() || secs <= 0.0 {
        return "0:00".into();
    }
    let s = secs.floor() as u64;
    format!("{}:{:02}", s / 60, s % 60)
}

enum Loaded {
    List(Vec<RecordingMeta>, StorageInfo),
    Cleaned(CleanupResult),
    Recording(String, Header, Vec<Event>),
}

struct Playback {
    title: String,
    term: TermView,
    events: Vec<Event>,
    total: f64,
    /// Virtual time played so far, and the next event to feed.
    pos: f64,
    next: usize,
    playing: bool,
    speed: f64,
}

impl Playback {
    fn feed_until(&mut self, t: f64) {
        while let Some(e) = self.events.get(self.next).filter(|e| e.elapsed <= t) {
            if e.kind == EVENT_OUTPUT {
                self.term.feed(e.data.as_bytes());
            }
            self.next += 1;
        }
    }

    /// Replays from the start up to `t`.
    fn seek(&mut self, t: f64) {
        self.term.reset();
        self.next = 0;
        self.pos = t.clamp(0.0, self.total);
        self.feed_until(self.pos);
    }

    fn play(&mut self) {
        if self.next >= self.events.len() {
            self.seek(0.0);
        }
        self.playing = true;
    }

    fn tick(&mut self, ctx: &egui::Context) {
        if !self.playing {
            return;
        }
        let dt = ctx.input(|i| i.stable_dt).min(0.25) as f64;
        self.pos = (self.pos + dt * self.speed).min(self.total);
        self.feed_until(self.pos);
        if self.next >= self.events.len() {
            self.playing = false;
            self.pos = self.total;
        } else {
            let wait = (self.events[self.next].elapsed - self.pos) / self.speed;
            ctx.request_repaint_after(std::time::Duration::from_secs_f64(wait.clamp(0.0, 0.25)));
        }
    }
}

pub struct Recordings {
    open: bool,
    list: Vec<RecordingMeta>,
    info: Option<StorageInfo>,
    loading: bool,
    rx: Option<Receiver<Loaded>>,
    age: u32,
    size: u64,
    status: String,
    confirm_delete: Option<RecordingMeta>,
    playback: Option<Playback>,
}

impl Default for Recordings {
    fn default() -> Self {
        Recordings {
            open: false,
            list: vec![],
            info: None,
            loading: false,
            rx: None,
            age: 30,
            size: 1 << 30,
            status: String::new(),
            confirm_delete: None,
            playback: None,
        }
    }
}

impl Recordings {
    pub fn open(&mut self) {
        self.open = true;
        self.status.clear();
        self.info = None;
    }

    /// Runs `f` on a worker thread; its result is picked up by `poll`.
    fn spawn(&mut self, s: &Shared, f: impl FnOnce(&nexus_core::recording::Store) -> nexus_core::Result<Loaded> + Send + 'static) {
        let (tx, rx) = channel();
        let store = s.svc.sessions.recordings().clone();
        self.rx = Some(rx);
        self.loading = true;
        s.spawn(move || f(&store), move |v| drop(tx.send(v)));
    }

    fn reload(&mut self, s: &Shared) {
        self.spawn(s, |st| Ok(Loaded::List(st.list()?, st.storage_info()?)));
    }

    fn poll(&mut self, s: &mut Shared) {
        let Some(rx) = &self.rx else { return };
        let got = match rx.try_recv() {
            Ok(v) => v,
            Err(std::sync::mpsc::TryRecvError::Empty) => return,
            Err(_) => {
                // Worker failed (error already toasted).
                self.rx = None;
                self.loading = false;
                return;
            }
        };
        self.rx = None;
        self.loading = false;
        match got {
            Loaded::List(list, info) => {
                self.list = list;
                self.info = Some(info);
            }
            Loaded::Cleaned(r) => {
                self.status = format!("Deleted {}; freed {}.", r.deleted, widgets::human_size(r.freed_bytes));
                self.reload(s);
            }
            Loaded::Recording(title, header, events) => {
                if events.is_empty() {
                    return s.info("Recording is empty — nothing to play back.");
                }
                let total = events.last().map_or(1.0, |e| e.elapsed).max(f64::EPSILON);
                let title = if header.title.is_empty() { title } else { header.title };
                let mut p =
                    Playback { title, term: TermView::new(10_000, 13.0, CursorStyle::Block), events, total, pos: 0.0, next: 0, playing: false, speed: 1.0 };
                p.play(); // autoplay
                self.playback = Some(p);
            }
        }
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        self.poll(s);
        self.playback_ui(ctx, s);
        self.delete_dialog(ctx, s);
        if !self.open {
            return;
        }
        if self.info.is_none() && !self.loading {
            self.reload(s);
        }
        let mut open = self.open;
        egui::Window::new(format!("{}  Session Recordings", icon::RECORD))
            .id(egui::Id::new("recordings"))
            .pivot(egui::Align2::CENTER_CENTER)
            .default_pos(ctx.content_rect().center())
            .open(&mut open)
            .collapsible(false)
            .default_size([640.0, 440.0])
            .show(ctx, |ui| self.browser(ui, s));
        self.open = open;
    }

    fn browser(&mut self, ui: &mut egui::Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        ui.horizontal(|ui| {
            let (count, total) = self.info.as_ref().map_or((self.list.len(), 0), |i| (i.count, i.total_bytes));
            ui.label(RichText::new(format!("{count} recording{}", if count == 1 { "" } else { "s" })).font(theme::semibold(13.0)));
            ui.label(RichText::new(widgets::human_size(total)).color(t.text_dim));
            if self.loading {
                ui.spinner();
            }
        });
        ui.horizontal(|ui| {
            egui::ComboBox::from_id_salt("rec-age").selected_text(AGES.iter().find(|a| a.0 == self.age).map_or("", |a| a.1)).show_ui(ui, |ui| {
                for (v, l) in AGES {
                    ui.selectable_value(&mut self.age, v, l);
                }
            });
            egui::ComboBox::from_id_salt("rec-size").selected_text(SIZES.iter().find(|a| a.0 == self.size).map_or("", |a| a.1)).show_ui(ui, |ui| {
                for (v, l) in SIZES {
                    ui.selectable_value(&mut self.size, v, l);
                }
            });
            if ui.add_enabled(!self.loading, egui::Button::new(format!("{}  Clean up", icon::BROOM))).clicked() {
                let (age, size) = (self.age, self.size);
                self.status.clear();
                self.spawn(s, move |st| Ok(Loaded::Cleaned(st.cleanup(age, size)?)));
            }
        });
        if !self.status.is_empty() {
            widgets::hint(ui, &self.status);
        }
        ui.separator();
        if self.list.is_empty() {
            if !self.loading {
                widgets::empty(ui, icon::RECORD, "No recordings yet", "Press Ctrl/Cmd+Shift+R in a terminal, or use Session → Toggle recording.");
            }
            return;
        }
        let (mut play, mut delete) = (None, None);
        widgets::rows(ui, "rec-list", self.list.len(), 44.0, None, |ui, i| {
            let r = &self.list[i];
            ui.vertical(|ui| {
                ui.set_width(ui.available_width() - 64.0);
                ui.label(RichText::new(if r.title.is_empty() { &r.id } else { &r.title }).font(theme::semibold(13.0)));
                let date = r.created_at.with_timezone(&chrono::Local).format("%Y-%m-%d %H:%M");
                ui.label(
                    RichText::new(format!("{date} · {} · {} · {}×{}", mmss(r.duration), widgets::human_size(r.size), r.width, r.height))
                        .size(11.5)
                        .color(t.text_dim),
                );
            });
            if widgets::icon_button(ui, icon::PLAY, "Play").clicked() {
                play = Some(i);
            }
            if widgets::icon_button(ui, icon::TRASH, "Delete").clicked() {
                delete = Some(i);
            }
        });
        if let Some(i) = play {
            let (id, title) = (self.list[i].id.clone(), self.list[i].title.clone());
            self.open = false;
            self.spawn(s, move |st| {
                let (h, ev) = st.get(&id)?;
                Ok(Loaded::Recording(title, h, ev))
            });
        }
        if let Some(i) = delete {
            self.confirm_delete = Some(self.list[i].clone());
        }
    }

    fn delete_dialog(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(r) = &self.confirm_delete else {
            return;
        };
        let (mut yes, mut no) = (false, ctx.input(|i| i.key_pressed(Key::Escape)));
        let name = if r.title.is_empty() { r.id.clone() } else { r.title.clone() };
        crate::chrome::modal(ctx, "rec-delete", 380.0, |ui| {
            ui.label(RichText::new("Delete recording?").font(theme::semibold(15.0)));
            ui.label(format!("\u{201c}{name}\u{201d} will be permanently deleted."));
            ui.add_space(8.0);
            ui.horizontal(|ui| {
                yes = widgets::primary(ui, icon::TRASH, "Delete").clicked();
                no |= ui.button("Cancel").clicked();
            });
        });
        if yes {
            let id = self.confirm_delete.take().unwrap().id;
            match s.svc.sessions.recordings().delete(&id) {
                Ok(()) => {
                    s.success("Recording deleted");
                    self.list.retain(|m| m.id != id);
                    self.reload(s);
                }
                Err(e) => s.error(format!("Failed to delete recording: {e}")),
            }
        } else if no {
            self.confirm_delete = None;
        }
    }

    fn playback_ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        let Some(p) = &mut self.playback else { return };
        p.tick(ctx);
        let t = Tokens::get(ctx);
        let theme = crate::terminal_themes::get(&s.prefs.terminal_theme);
        let mut open = true;
        egui::Window::new(format!("{}  {}", icon::PLAY_CIRCLE, p.title))
            .id(egui::Id::new("rec-playback"))
            .pivot(egui::Align2::CENTER_CENTER)
            .default_pos(ctx.content_rect().center())
            .open(&mut open)
            .collapsible(false)
            .default_size([820.0, 520.0])
            .show(ctx, |ui| {
                egui::Panel::bottom("rec-controls").frame(egui::Frame::NONE.inner_margin(egui::Margin::symmetric(0, 6))).show(ui, |ui| {
                    ui.horizontal(|ui| {
                        let (glyph, tip) = if p.playing { (icon::PAUSE, "Pause") } else { (icon::PLAY, "Play") };
                        if widgets::icon_button(ui, glyph, tip).clicked() {
                            if p.playing { p.playing = false } else { p.play() }
                        }
                        if widgets::icon_button(ui, icon::ARROW_COUNTER_CLOCKWISE, "Restart").clicked() {
                            p.playing = false;
                            p.seek(0.0);
                        }
                        let label = format!("{} / {}", mmss(p.pos), mmss(p.total));
                        let lw = 120.0;
                        let mut pos = p.pos;
                        ui.spacing_mut().slider_width = (ui.available_width() - lw - 90.0).max(80.0);
                        if ui.add(egui::Slider::new(&mut pos, 0.0..=p.total).show_value(false)).changed() {
                            p.seek(pos);
                        }
                        ui.add_sized([lw, 20.0], egui::Label::new(RichText::new(label).font(theme::mono(12.0)).color(t.text_dim)));
                        egui::ComboBox::from_id_salt("rec-speed").width(60.0).selected_text(format!("{}×", p.speed)).show_ui(ui, |ui| {
                            for v in SPEEDS {
                                ui.selectable_value(&mut p.speed, v, format!("{v}×"));
                            }
                        });
                    });
                });
                egui::Frame::NONE
                    .fill(egui::Color32::from_rgba_unmultiplied(theme.background[0], theme.background[1], theme.background[2], 255))
                    .corner_radius(t.radius)
                    .show(ui, |ui| {
                        ui.set_min_size(ui.available_size());
                        // Read-only: keyboard input and terminal replies are dropped.
                        let _ = p.term.ui(ui, theme, false);
                    });
            });
        if !open {
            self.playback = None;
        }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn mmss() {
        assert_eq!(super::mmss(0.0), "0:00");
        assert_eq!(super::mmss(f64::NAN), "0:00");
        assert_eq!(super::mmss(59.9), "0:59");
        assert_eq!(super::mmss(125.0), "2:05");
    }
}
