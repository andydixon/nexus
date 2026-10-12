//! Shared widgets: section headers, buttons, chips, key/value grids, virtual lists, empty states.
//! Colours always come from `Tokens`.

use crate::theme::{self, Tokens};
use egui::{Align, Color32, Layout, Response, RichText, Sense, Ui, vec2};

/// Small uppercase section header with a hairline.
pub fn section(ui: &mut Ui, title: &str) {
    let t = Tokens::get(ui.ctx());
    ui.add_space(4.0);
    ui.label(RichText::new(title.to_uppercase()).font(theme::semibold(10.5)).color(t.text_faint).extra_letter_spacing(0.6));
    ui.add_space(1.0);
}

/// An icon + label button in the secondary style.
pub fn button(ui: &mut Ui, glyph: &str, label: &str) -> Response {
    ui.add(egui::Button::new(format!("{glyph}  {label}")))
}

/// Filled accent button for a panel's primary action.
pub fn primary(ui: &mut Ui, glyph: &str, label: &str) -> Response {
    let t = Tokens::get(ui.ctx());
    ui.add(egui::Button::new(RichText::new(format!("{glyph}  {label}")).color(t.accent_text)).fill(t.accent))
}

/// A square icon-only button with a tooltip.
pub fn icon_button(ui: &mut Ui, glyph: &str, tip: &str) -> Response {
    let t = Tokens::get(ui.ctx());
    let (rect, resp) = ui.allocate_exact_size(vec2(24.0, 24.0), Sense::click());
    if resp.hovered() {
        ui.painter().rect_filled(rect, t.radius, t.hover);
    }
    let c = if resp.hovered() { t.text } else { t.text_dim };
    ui.painter().text(rect.center(), egui::Align2::CENTER_CENTER, glyph, egui::FontId::proportional(15.0), c);
    resp.on_hover_text(tip)
}

pub fn chip(ui: &mut Ui, text: &str, color: Color32) -> Response {
    let font = theme::semibold(10.5);
    let galley = ui.painter().layout_no_wrap(text.to_owned(), font, color);
    let (rect, resp) = ui.allocate_exact_size(galley.size() + vec2(12.0, 4.0), Sense::hover());
    ui.painter().rect_filled(rect, 9.0, color.gamma_multiply(0.18));
    ui.painter().galley(rect.center() - galley.size() / 2.0, galley, color);
    resp
}

/// Two-column key/value grid; clicking a value copies it.
pub fn kv(ui: &mut Ui, id: &str, rows: &[(&str, String)]) {
    let t = Tokens::get(ui.ctx());
    egui::Grid::new(id).num_columns(2).spacing(vec2(12.0, 4.0)).striped(false).show(ui, |ui| {
        for (k, v) in rows {
            ui.label(RichText::new(*k).color(t.text_dim));
            let r = ui.add(egui::Label::new(RichText::new(v).font(theme::mono(12.0))).sense(Sense::click()).wrap());
            if r.on_hover_text("Click to copy").clicked() {
                ui.ctx().copy_text(v.clone());
            }
            ui.end_row();
        }
    });
}

/// A virtualised list of `n` rows of height `h`; `row` draws row `i` and returns whether it
/// was clicked. Hover and selection are painted for you.
pub fn rows(ui: &mut Ui, id: &str, n: usize, h: f32, selected: Option<usize>, mut row: impl FnMut(&mut Ui, usize)) -> Option<usize> {
    let t = Tokens::get(ui.ctx());
    let mut clicked = None;
    egui::ScrollArea::vertical().id_salt(id).auto_shrink([false, false]).show_rows(ui, h, n, |ui, range| {
        for i in range {
            let (rect, resp) = ui.allocate_exact_size(vec2(ui.available_width(), h), Sense::click());
            if selected == Some(i) {
                ui.painter().rect_filled(rect, 3.0, t.row_selected);
            } else if resp.hovered() {
                ui.painter().rect_filled(rect, 3.0, t.hover.gamma_multiply(0.6));
            } else if i % 2 == 1 {
                ui.painter().rect_filled(rect, 0.0, t.text.gamma_multiply(0.025));
            }
            let mut child = ui.new_child(egui::UiBuilder::new().max_rect(rect.shrink2(vec2(6.0, 0.0))).layout(Layout::left_to_right(Align::Center)));
            child.set_clip_rect(rect.intersect(ui.clip_rect()));
            // A row never grows: long text truncates (hover shows it in full where the panel adds it).
            child.style_mut().wrap_mode = Some(egui::TextWrapMode::Truncate);
            row(&mut child, i);
            if resp.clicked() {
                clicked = Some(i);
            }
        }
    });
    clicked
}

/// Centered empty state.
pub fn empty(ui: &mut Ui, glyph: &str, title: &str, hint: &str) {
    let t = Tokens::get(ui.ctx());
    ui.vertical_centered(|ui| {
        ui.add_space(28.0);
        ui.label(RichText::new(glyph).size(30.0).color(t.text_faint));
        ui.add_space(4.0);
        ui.label(RichText::new(title).font(theme::semibold(13.5)).color(t.text_dim));
        ui.label(RichText::new(hint).color(t.text_faint).size(12.0));
    });
}

/// Dim helper text.
pub fn hint(ui: &mut Ui, text: &str) {
    let t = Tokens::get(ui.ctx());
    ui.label(RichText::new(text).color(t.text_faint).size(11.5));
}

/// A small counter line ("1,234 results"), with an optional truncation note.
pub fn count(ui: &mut Ui, n: usize, noun: &str, capped: bool) {
    let t = Tokens::get(ui.ctx());
    let s = format!("{} {noun}{}", group(n as u64), if capped { " (capped)" } else { "" });
    ui.label(RichText::new(s).color(if capped { t.warning } else { t.text_dim }).size(11.5));
}

/// 1234567 → "1,234,567"
pub fn group(n: u64) -> String {
    let s = n.to_string();
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 && (s.len() - i).is_multiple_of(3) {
            out.push(',');
        }
        out.push(c);
    }
    out
}

/// Whether `url` is a plain web link. Links that arrive from the network (update manifests,
/// GitHub API responses) are only handed to the system opener when they are http(s), so a
/// hostile or spoofed server can't launch `file:`, `ssh:` or custom-scheme handlers.
pub fn is_web_url(url: &str) -> bool {
    let u = url.trim();
    (u.len() > 8 && u[..8].eq_ignore_ascii_case("https://")) || (u.len() > 7 && u[..7].eq_ignore_ascii_case("http://"))
}

/// Opens `url` in the browser if it is a web link; returns false (and opens nothing) otherwise.
pub fn open_web_url(ctx: &egui::Context, url: &str) -> bool {
    if !is_web_url(url) {
        tracing::warn!(url, "refusing to open a non-web URL");
        return false;
    }
    ctx.open_url(egui::OpenUrl::new_tab(url.trim()));
    true
}

pub fn human_size(n: u64) -> String {
    const U: [&str; 5] = ["B", "KiB", "MiB", "GiB", "TiB"];
    let mut v = n as f64;
    let mut i = 0;
    while v >= 1024.0 && i < 4 {
        v /= 1024.0;
        i += 1;
    }
    if i == 0 { format!("{n} B") } else { format!("{v:.1} {}", U[i]) }
}

#[cfg(test)]
mod tests {
    #[test]
    fn helpers() {
        assert_eq!(super::group(1234567), "1,234,567");
    }

    #[test]
    fn only_web_urls_are_opened() {
        for ok in ["https://dixon.cx/downloads/nexus/", "http://localhost:3000/x", " HTTPS://Example.com "] {
            assert!(super::is_web_url(ok), "{ok}");
        }
        for bad in ["", "https://", "file:///etc/passwd", "ssh://evil", "javascript:alert(1)", "ms-msdt:/id x", "ftp://host/", "//host/path"] {
            assert!(!super::is_web_url(bad), "{bad}");
        }
    }
}
