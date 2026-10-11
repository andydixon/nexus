//! Split panes: a binary tree of sessions inside one tab. A new split starts as a placeholder
//! pane offering Local Machine and the saved connections; ✕ on a pane promotes its sibling.

use crate::app::App;
use crate::state::Shared;
use crate::theme::Tokens;
use crate::views::protocol_color;
use crate::widgets;
use egui::{Align2, Rect, RichText, Sense, Ui, pos2, vec2};
use egui_phosphor::regular as icon;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Dir {
    /// Side by side (Split Right).
    Right,
    /// Stacked (Split Down).
    Down,
}

/// A tab's layout. Leaf `None` is the placeholder pane showing the connection picker.
#[derive(Debug)]
pub enum Split {
    Leaf(Option<String>),
    Node { dir: Dir, ratio: f32, a: Box<Split>, b: Box<Split> },
}

const MIN_PANE: f32 = 100.0;

impl Split {
    pub fn sessions(&self) -> Vec<String> {
        match self {
            Split::Leaf(Some(s)) => vec![s.clone()],
            Split::Leaf(None) => vec![],
            Split::Node { a, b, .. } => {
                let mut v = a.sessions();
                v.extend(b.sessions());
                v
            }
        }
    }

    pub fn rename(&mut self, from: &str, to: &str) {
        match self {
            Split::Leaf(Some(s)) if s == from => *s = to.into(),
            Split::Node { a, b, .. } => {
                a.rename(from, to);
                b.rename(from, to);
            }
            _ => {}
        }
    }

    fn at(&mut self, path: &[bool]) -> &mut Split {
        match (self, path.split_first()) {
            (Split::Node { a, b, .. }, Some((side, rest))) => if *side { b.at(rest) } else { a.at(rest) },
            (s, _) => s,
        }
    }

    /// Splits the leaf holding `session` (or the first placeholder when `session` is None).
    fn split_leaf(&mut self, session: Option<&str>, dir: Dir) -> bool {
        match self {
            Split::Leaf(s) if s.as_deref() == session => {
                let old = std::mem::replace(self, Split::Leaf(None));
                *self = Split::Node { dir, ratio: 0.5, a: Box::new(old), b: Box::new(Split::Leaf(None)) };
                true
            }
            Split::Node { a, b, .. } => a.split_leaf(session, dir) || b.split_leaf(session, dir),
            _ => false,
        }
    }

    /// Removes the leaf at `path`, replacing its parent with the sibling.
    fn remove_at(&mut self, path: &[bool]) {
        let Some((last, parent)) = path.split_last() else {
            *self = Split::Leaf(None);
            return;
        };
        let node = self.at(parent);
        if let Split::Node { a, b, .. } = std::mem::replace(node, Split::Leaf(None)) {
            *node = if *last { *a } else { *b };
        }
    }

    fn find(&self, session: &str, path: &mut Vec<bool>) -> bool {
        match self {
            Split::Leaf(Some(s)) => s == session,
            Split::Leaf(None) => false,
            Split::Node { a, b, .. } => {
                path.push(false);
                if a.find(session, path) {
                    return true;
                }
                path.pop();
                path.push(true);
                if b.find(session, path) {
                    return true;
                }
                path.pop();
                false
            }
        }
    }

    fn leaves(&self, rect: Rect, path: &mut Vec<bool>, out: &mut Vec<(Vec<bool>, Option<String>, Rect)>, dividers: &mut Vec<(Vec<bool>, Dir, Rect, Rect)>) {
        match self {
            Split::Leaf(s) => out.push((path.clone(), s.clone(), rect)),
            Split::Node { dir, ratio, a, b } => {
                let (ra, rb, div) = divide(rect, *dir, *ratio);
                dividers.push((path.clone(), *dir, div, rect));
                path.push(false);
                a.leaves(ra, path, out, dividers);
                path.pop();
                path.push(true);
                b.leaves(rb, path, out, dividers);
                path.pop();
            }
        }
    }
}

fn divide(r: Rect, dir: Dir, ratio: f32) -> (Rect, Rect, Rect) {
    const GAP: f32 = 4.0;
    match dir {
        Dir::Right => {
            let x = r.left() + (r.width() - GAP) * ratio;
            (Rect::from_min_max(r.min, pos2(x, r.bottom())), Rect::from_min_max(pos2(x + GAP, r.top()), r.max), Rect::from_min_max(pos2(x, r.top()), pos2(x + GAP, r.bottom())))
        }
        Dir::Down => {
            let y = r.top() + (r.height() - GAP) * ratio;
            (Rect::from_min_max(r.min, pos2(r.right(), y)), Rect::from_min_max(pos2(r.left(), y + GAP), r.max), Rect::from_min_max(pos2(r.left(), y), pos2(r.right(), y + GAP)))
        }
    }
}

/// Splits the focused pane of the active tab.
pub fn split_focused(app: &mut App, dir: Dir) {
    let Some(tab_id) = app.s.active.clone() else { return };
    let focused = app.s.focused_session().unwrap_or(tab_id.clone());
    let Some(tab) = app.s.tabs.iter_mut().find(|t| t.id == tab_id) else { return };
    match &mut tab.split {
        None => tab.split = Some(Split::Node { dir, ratio: 0.5, a: Box::new(Split::Leaf(Some(tab_id.clone()))), b: Box::new(Split::Leaf(None)) }),
        Some(sp) => {
            if !sp.split_leaf(Some(&focused), dir) {
                sp.split_leaf(Some(&tab_id), dir);
            }
        }
    }
}

/// Takes a split child out of its tab (after its session closed). The tab's own session can be
/// removed too: the next pane takes over the tab.
pub fn remove_session(s: &mut Shared, session: &str) {
    for i in 0..s.tabs.len() {
        let Some(sp) = &mut s.tabs[i].split else { continue };
        let mut path = vec![];
        if !sp.find(session, &mut path) {
            continue;
        }
        sp.remove_at(&path);
        let rest = sp.sessions();
        if let Split::Leaf(Some(only)) = sp {
            let only = only.clone();
            s.tabs[i].split = None;
            s.tabs[i].id = only;
        } else if s.tabs[i].id == session {
            match rest.first() {
                Some(next) => s.tabs[i].id = next.clone(),
                None => {
                    s.tabs.remove(i);
                }
            }
        }
        if s.active.as_deref() == Some(session) {
            s.active = s.tabs.get(i.min(s.tabs.len().saturating_sub(1))).map(|t| t.id.clone());
        }
        return;
    }
}

pub fn render(app: &mut App, ui: &mut Ui, tab_id: &str) {
    let t = Tokens::get(ui.ctx());
    let area = ui.available_rect_before_wrap();
    let (mut leaves, mut dividers) = (vec![], vec![]);
    if let Some(sp) = app.s.tab(tab_id).and_then(|tb| tb.split.as_ref()) {
        sp.leaves(area, &mut vec![], &mut leaves, &mut dividers);
    }
    // Dividers: drag to resize, never squeezing a pane under MIN_PANE.
    for (path, dir, div, total) in dividers {
        let resp = ui.interact(div.expand(2.0), ui.id().with(("divider", &path)), Sense::drag());
        let c = if resp.hovered() || resp.dragged() { t.accent } else { t.separator };
        ui.painter().rect_filled(div, 0.0, c);
        if resp.hovered() {
            ui.ctx().set_cursor_icon(if dir == Dir::Right { egui::CursorIcon::ResizeHorizontal } else { egui::CursorIcon::ResizeVertical });
        }
        if resp.dragged()
            && let Some(p) = resp.interact_pointer_pos()
        {
            let (pos, start, len) = match dir {
                Dir::Right => (p.x, total.left(), total.width()),
                Dir::Down => (p.y, total.top(), total.height()),
            };
            let min = (MIN_PANE / len).min(0.45);
            let ratio = ((pos - start) / len).clamp(min, 1.0 - min);
            if let Some(Split::Node { ratio: r, .. }) = app.s.tabs.iter_mut().find(|tb| tb.id == tab_id).and_then(|tb| tb.split.as_mut()).map(|sp| sp.at(&path)) {
                *r = ratio;
            }
        }
    }
    let mut close: Option<(Vec<bool>, Option<String>)> = None;
    for (path, session, rect) in leaves {
        let head = Rect::from_min_size(rect.min, vec2(rect.width(), 22.0));
        let body = Rect::from_min_max(pos2(rect.left(), head.bottom()), rect.max);
        let focused = session.is_some() && app.s.focused == session;
        ui.painter().rect_filled(head, 0.0, if focused { t.card } else { t.tab_strip });
        let title = session.as_deref().and_then(|s| app.s.pane(s)).map(|p| (p.info.tab_name.clone(), protocol_color(p.info.protocol))).unwrap_or(("New pane".into(), t.text_faint));
        ui.painter().circle_filled(head.left_center() + vec2(10.0, 0.0), 3.5, title.1);
        ui.painter().with_clip_rect(head.shrink2(vec2(24.0, 0.0))).text(head.left_center() + vec2(20.0, 0.0), Align2::LEFT_CENTER, title.0, egui::FontId::proportional(11.5), if focused { t.text } else { t.text_dim });
        let xr = Rect::from_center_size(head.right_center() - vec2(12.0, 0.0), vec2(16.0, 16.0));
        if widgets::icon_button(&mut ui.new_child(egui::UiBuilder::new().max_rect(xr)), icon::X, "Close pane").clicked() {
            close = Some((path.clone(), session.clone()));
        }
        match session {
            Some(id) => {
                if ui.interact(head, ui.id().with(("pane-head", &id)), Sense::click()).clicked() {
                    app.s.focused = Some(id.clone());
                }
                crate::tabs::pane(app, ui, &id, body);
            }
            None => picker(app, ui, tab_id, &path, body),
        }
    }
    if let Some((path, session)) = close {
        match session {
            Some(id) => app.close_session(&id),
            None => {
                if let Some(tb) = app.s.tabs.iter_mut().find(|tb| tb.id == tab_id)
                    && let Some(sp) = &mut tb.split
                {
                    sp.remove_at(&path);
                    if let Split::Leaf(Some(_)) = sp {
                        tb.split = None;
                    }
                }
            }
        }
    }
}

/// The placeholder pane: pick what to open here.
fn picker(app: &mut App, ui: &mut Ui, tab_id: &str, path: &[bool], rect: Rect) {
    let t = Tokens::get(ui.ctx());
    ui.painter().rect_filled(rect, 0.0, t.canvas);
    let mut child = ui.new_child(egui::UiBuilder::new().max_rect(rect.shrink(16.0)));
    let mut chosen: Option<Option<String>> = None; // Some(None) = local
    child.vertical_centered(|ui| {
        ui.label(RichText::new("Open in this pane").font(crate::theme::semibold(14.0)));
        ui.add_space(6.0);
        egui::ScrollArea::vertical().max_height(rect.height() - 60.0).show(ui, |ui| {
            if ui.button(format!("{}  Local Machine", icon::TERMINAL_WINDOW)).clicked() {
                chosen = Some(None);
            }
            for p in &app.s.profiles {
                if !p.protocol.is_terminal() {
                    continue;
                }
                let name = if p.display_name.is_empty() { &p.host } else { &p.display_name };
                if ui.button(RichText::new(format!("●  {name}")).color(t.text)).clicked() {
                    chosen = Some(Some(p.id.clone()));
                }
            }
        });
    });
    let Some(choice) = chosen else { return };
    let sid = match choice {
        None => app.s.svc.connect_local(),
        Some(pid) => match app.s.svc.connect_profile(&pid) {
            Ok(s) => s,
            Err(e) => {
                app.s.error(format!("Connection failed: {e}"));
                return;
            }
        },
    };
    // The pane may have been closed meanwhile; then drop the new session.
    let placed = app.s.tabs.iter_mut().find(|tb| tb.id == tab_id).and_then(|tb| tb.split.as_mut()).map(|sp| {
        let leaf = sp.at(path);
        if matches!(leaf, Split::Leaf(None)) {
            *leaf = Split::Leaf(Some(sid.clone()));
            true
        } else {
            false
        }
    });
    if placed == Some(true) {
        app.add_session(&sid, false);
        app.s.focused = Some(sid);
    } else {
        let _ = app.s.svc.sessions.disconnect(&sid);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn split_and_collapse() {
        let mut sp = Split::Node { dir: Dir::Right, ratio: 0.5, a: Box::new(Split::Leaf(Some("a".into()))), b: Box::new(Split::Leaf(None)) };
        assert!(sp.split_leaf(Some("a"), Dir::Down));
        assert_eq!(sp.sessions(), vec!["a".to_string()]);
        let mut path = vec![];
        assert!(sp.find("a", &mut path));
        assert_eq!(path, vec![false, false]);
        sp.remove_at(&path);
        assert!(sp.sessions().is_empty());
        sp.rename("a", "b");
    }
}
