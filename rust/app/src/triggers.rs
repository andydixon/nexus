//! Trigger rules: match terminal output lines and notify / highlight / beep (port of triggers.js).
//! Rules live in `triggers.json` in the data directory (the web build's localStorage rules are
//! not migrated).

use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use egui::RichText;
use egui_phosphor::regular as icon;
use regex::{Regex, RegexBuilder};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::LazyLock;

const ACTIONS: [(&str, &str); 4] = [("notify", "Notify"), ("highlight", "Highlight tab"), ("both", "Notify + Highlight"), ("sound", "Sound")];

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Rule {
    pub name: String,
    pub pattern: String,
    pub is_regex: bool,
    pub action: String,
    pub case_sensitive: bool,
    pub enabled: bool,
    #[serde(skip)]
    re: Option<Regex>,
}

impl Rule {
    fn compile(&mut self) -> Result<(), regex::Error> {
        self.re = None;
        if self.is_regex {
            self.re = Some(RegexBuilder::new(&self.pattern).case_insensitive(!self.case_sensitive).build()?);
        }
        Ok(())
    }

    /// Whether `line` (already ANSI-stripped) matches. An invalid regex never matches.
    pub fn matches(&self, line: &str) -> bool {
        if self.is_regex {
            self.re.as_ref().is_some_and(|r| r.is_match(line))
        } else if self.case_sensitive {
            line.contains(&self.pattern)
        } else {
            line.to_lowercase().contains(&self.pattern.to_lowercase())
        }
    }
}

/// Strips CSI sequences exactly as the JS did (`/\x1b\[[0-9;]*[a-zA-Z]/g`).
pub fn strip_ansi(line: &str) -> std::borrow::Cow<'_, str> {
    static RE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\x1b\[[0-9;]*[a-zA-Z]").unwrap());
    RE.replace_all(line, "")
}

/// Appends `data` to the session buffer and returns every completed line; the trailing partial
/// line stays buffered.
pub fn complete_lines(buf: &mut Vec<u8>, data: &[u8]) -> Vec<String> {
    buf.extend_from_slice(data);
    let Some(last) = buf.iter().rposition(|&b| b == b'\n') else {
        // ponytail: a line with no newline is capped at 64 KiB (keeps the tail) so a stream
        // without newlines can't grow forever.
        if buf.len() > 64 * 1024 {
            buf.drain(..buf.len() - 64 * 1024);
        }
        return vec![];
    };
    let rest = buf.split_off(last + 1);
    let done = std::mem::replace(buf, rest);
    done[..done.len() - 1].split(|&b| b == b'\n').map(|l| String::from_utf8_lossy(l).into_owned()).collect()
}

enum Form {
    New(Rule),
    Edit(usize, Rule),
}

#[derive(Default)]
pub struct Triggers {
    open: bool,
    rules: Option<Vec<Rule>>,
    buffers: HashMap<String, Vec<u8>>,
    form: Option<Form>,
}

fn path(s: &Shared) -> PathBuf {
    s.svc.config.data_dir.join("triggers.json")
}

impl Triggers {
    pub fn open(&mut self) {
        self.open = true;
    }

    fn rules(&mut self, s: &Shared) -> &mut Vec<Rule> {
        self.rules.get_or_insert_with(|| {
            let mut v: Vec<Rule> = std::fs::read(path(s)).ok().and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or_default();
            v.iter_mut().for_each(|r| drop(r.compile()));
            v
        })
    }

    fn save(&mut self, s: &mut Shared) {
        let json = serde_json::to_vec_pretty(self.rules(s)).unwrap_or_default();
        if let Err(e) = std::fs::write(path(s), json) {
            s.error(format!("Failed to save trigger rules: {e}"));
        }
    }

    /// Feeds session output; fires matching rules (toasts, highlight request, bell).
    /// Returns true if the tab should be highlighted.
    pub fn check(&mut self, s: &mut Shared, session: &str, data: &[u8]) -> bool {
        if self.rules(s).is_empty() {
            return false;
        }
        let lines = complete_lines(self.buffers.entry(session.to_owned()).or_default(), data);
        let mut highlight = false;
        let label = s
            .pane(session)
            .map(|p| if p.info.tab_name.is_empty() { p.info.profile_name.clone() } else { p.info.tab_name.clone() })
            .unwrap_or_else(|| session.to_owned());
        let mut fired: Vec<(String, String)> = vec![];
        for line in &lines {
            let clean = strip_ansi(line);
            for r in self.rules(s).iter().filter(|r| r.enabled && r.matches(&clean)) {
                let name = if r.name.is_empty() { r.pattern.clone() } else { r.name.clone() };
                fired.push((r.action.clone(), name));
            }
        }
        for (action, name) in fired {
            let msg = format!("[{label}] Trigger: {name}");
            match action.as_str() {
                "notify" => s.info(msg),
                "highlight" => highlight = true,
                "both" => {
                    s.info(msg);
                    highlight = true;
                }
                "sound" => {
                    s.ctx.send_viewport_cmd(egui::ViewportCommand::RequestUserAttention(egui::UserAttentionType::Informational));
                    s.info(format!("{} {msg}", icon::BELL_RINGING));
                }
                _ => {}
            }
        }
        highlight
    }

    pub fn cleanup(&mut self, session: &str) {
        self.buffers.remove(session);
    }

    pub fn ui(&mut self, ctx: &egui::Context, s: &mut Shared) {
        if !self.open {
            return;
        }
        let mut open = self.open;
        egui::Window::new(format!("{}  Trigger Rules", icon::LIGHTNING))
            .id(egui::Id::new("triggers"))
            .open(&mut open)
            .collapsible(false)
            .default_size([620.0, 380.0])
            .show(ctx, |ui| {
                widgets::hint(ui, "Rules match against terminal output and fire actions when a pattern is detected.");
                ui.add_space(4.0);
                if self.form.is_some() {
                    self.form_ui(ui, s);
                } else {
                    self.list_ui(ui, s);
                }
            });
        self.open = open;
    }

    fn list_ui(&mut self, ui: &mut egui::Ui, s: &mut Shared) {
        let t = Tokens::get(ui.ctx());
        if widgets::primary(ui, icon::PLUS, "Add Rule").clicked() {
            self.form = Some(Form::New(Rule { action: "notify".into(), enabled: true, ..Default::default() }));
        }
        ui.add_space(4.0);
        let (mut changed, mut edit, mut delete) = (false, None, None);
        let rules = self.rules(s);
        if rules.is_empty() {
            widgets::empty(ui, icon::LIGHTNING, "No trigger rules defined", "Add a rule to watch terminal output");
            return;
        }
        egui::ScrollArea::vertical().auto_shrink([false, true]).show(ui, |ui| {
            ui.style_mut().wrap_mode = Some(egui::TextWrapMode::Extend);
            egui::Grid::new("trigger-rules").num_columns(4).striped(true).spacing([12.0, 6.0]).show(ui, |ui| {
                for (i, r) in rules.iter_mut().enumerate() {
                    changed |= ui.checkbox(&mut r.enabled, "").on_hover_text("Enabled").changed();
                    ui.vertical(|ui| {
                        ui.label(RichText::new(if r.name.is_empty() { &r.pattern } else { &r.name }).font(theme::semibold(13.0)));
                        let action = ACTIONS.iter().find(|a| a.0 == r.action).map_or(r.action.as_str(), |a| a.1);
                        let meta = format!(
                            "{} · {action} · {}",
                            if r.is_regex { "Regex" } else { "Text" },
                            if r.case_sensitive { "Case-sensitive" } else { "Case-insensitive" }
                        );
                        ui.label(RichText::new(meta).size(11.5).color(t.text_dim));
                    });
                    ui.label(RichText::new(&r.pattern).font(theme::mono(12.0)).color(if r.is_regex && r.re.is_none() { t.danger } else { t.text }));
                    ui.horizontal(|ui| {
                        if widgets::icon_button(ui, icon::PENCIL_SIMPLE, "Edit").clicked() {
                            edit = Some(i);
                        }
                        if widgets::icon_button(ui, icon::TRASH, "Delete").clicked() {
                            delete = Some(i);
                        }
                    });
                    ui.end_row();
                }
            });
        });
        if let Some(i) = delete {
            rules.remove(i);
            changed = true;
        } else if let Some(i) = edit {
            let rule = rules[i].clone();
            self.form = Some(Form::Edit(i, rule));
        }
        if changed {
            self.save(s);
        }
    }

    fn form_ui(&mut self, ui: &mut egui::Ui, s: &mut Shared) {
        let Some(form) = &mut self.form else { return };
        let (is_new, r) = match form {
            Form::New(r) => (true, r),
            Form::Edit(_, r) => (false, r),
        };
        egui::Grid::new("trigger-form").num_columns(2).spacing([10.0, 8.0]).show(ui, |ui| {
            ui.label("Name (optional)");
            ui.add(egui::TextEdit::singleline(&mut r.name).hint_text("e.g. Error alert").desired_width(320.0));
            ui.end_row();
            ui.label("Pattern");
            ui.add(egui::TextEdit::singleline(&mut r.pattern).hint_text("e.g. ERROR|FATAL|panic").font(theme::mono(12.5)).desired_width(320.0));
            ui.end_row();
            ui.label("Match type");
            egui::ComboBox::from_id_salt("trigger-type").selected_text(if r.is_regex { "Regex" } else { "Plain text" }).show_ui(ui, |ui| {
                ui.selectable_value(&mut r.is_regex, false, "Plain text");
                ui.selectable_value(&mut r.is_regex, true, "Regex");
            });
            ui.end_row();
            ui.label("Action");
            let cur = ACTIONS.iter().find(|a| a.0 == r.action).map_or("Notify", |a| a.1);
            egui::ComboBox::from_id_salt("trigger-action").selected_text(cur).show_ui(ui, |ui| {
                for (id, label) in ACTIONS {
                    if ui.selectable_label(r.action == id, label).clicked() {
                        r.action = id.into();
                    }
                }
            });
            ui.end_row();
            ui.label("");
            ui.checkbox(&mut r.case_sensitive, "Case-sensitive");
            ui.end_row();
        });
        ui.add_space(8.0);
        let (mut save, mut cancel) = (false, false);
        ui.horizontal(|ui| {
            save = widgets::primary(ui, icon::CHECK, if is_new { "Add" } else { "Save" }).clicked();
            cancel = ui.button("Cancel").clicked();
        });
        if cancel {
            self.form = None;
        } else if save {
            let mut rule = r.clone();
            rule.name = rule.name.trim().to_owned();
            rule.pattern = rule.pattern.trim().to_owned();
            if rule.pattern.is_empty() {
                return s.error("Pattern is required");
            }
            if let Err(e) = rule.compile() {
                return s.error(format!("Invalid regex: {e}"));
            }
            let form = self.form.take();
            let rules = self.rules(s);
            match form {
                Some(Form::Edit(i, _)) if i < rules.len() => rules[i] = rule,
                _ => rules.push(rule),
            }
            self.save(s);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_buffering() {
        let mut b = vec![];
        assert!(complete_lines(&mut b, b"par").is_empty());
        assert_eq!(complete_lines(&mut b, b"tial\nnext\n\nlast"), ["partial", "next", ""]);
        assert_eq!(b, b"last");
        assert_eq!(complete_lines(&mut b, b"\n"), ["last"]);
        assert!(b.is_empty());
        // A UTF-8 char split across chunks survives.
        let e = "é".as_bytes();
        complete_lines(&mut b, &e[..1]);
        assert_eq!(complete_lines(&mut b, &[e[1], b'\n']), ["é"]);
    }

    #[test]
    fn ansi_strip_and_match() {
        assert_eq!(strip_ansi("\x1b[1;31mERROR\x1b[0m: disk\x1b[K"), "ERROR: disk");
        // Only CSI with digits/semicolons is stripped, as in the JS.
        assert_eq!(strip_ansi("\x1b[?25lx\x1b]0;t\x07"), "\x1b[?25lx\x1b]0;t\x07");
        let mut r = Rule { pattern: "error|fatal".into(), is_regex: true, enabled: true, ..Default::default() };
        r.compile().unwrap();
        assert!(r.matches(&strip_ansi("\x1b[31mFATAL\x1b[0m")));
        r.case_sensitive = true;
        r.compile().unwrap();
        assert!(!r.matches("FATAL"));
        let r = Rule { pattern: "Disk Full".into(), ..Default::default() };
        assert!(r.matches("warning: disk full"));
        let mut bad = Rule { pattern: "(".into(), is_regex: true, ..Default::default() };
        assert!(bad.compile().is_err());
        assert!(!bad.matches("("));
    }
}
