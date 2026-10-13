// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Reopens the previous run's tabs and splits at launch: saved profiles reconnect, local shells
//! start again in the directory they were in. The layout is saved whenever it changes (not only on
//! exit), so it survives a crash. Quick Connect tabs aren't restored (they have no saved profile).

use crate::app::App;
use crate::splits::{Dir, Split};
use crate::state::Tab;
use nexus_core::models::Protocol;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

const KEY: &str = "session_layout";

#[derive(Serialize, Deserialize, PartialEq, Debug, Default)]
pub struct Saved {
    tabs: Vec<SavedTab>,
    active: usize,
}

#[derive(Serialize, Deserialize, PartialEq, Debug)]
struct SavedTab {
    name: String,
    pinned: bool,
    layout: Layout,
}

#[derive(Serialize, Deserialize, PartialEq, Debug)]
#[serde(tag = "kind", rename_all = "camelCase")]
enum Layout {
    Profile {
        id: String,
    },
    Local {
        shell: String,
        cwd: Option<PathBuf>,
    },
    /// A split pane still showing the connection picker, or a session that can't be restored.
    Empty,
    Split {
        down: bool,
        ratio: f32,
        a: Box<Layout>,
        b: Box<Layout>,
    },
}

fn session_layout(app: &App, id: &str) -> Layout {
    let Some(pane) = app.s.pane(id) else { return Layout::Empty };
    if pane.info.protocol == Protocol::Local {
        let (shell, cwd) = app.s.svc.sessions.local_state(id).unwrap_or_default();
        return Layout::Local { shell, cwd };
    }
    match app.s.profile(&pane.info.profile_id) {
        Some(p) => Layout::Profile { id: p.id.clone() },
        None => Layout::Empty,
    }
}

fn split_layout(app: &App, sp: &Split) -> Layout {
    match sp {
        Split::Leaf(Some(id)) => session_layout(app, id),
        Split::Leaf(None) => Layout::Empty,
        Split::Node { dir, ratio, a, b } => {
            Layout::Split { down: *dir == Dir::Down, ratio: *ratio, a: Box::new(split_layout(app, a)), b: Box::new(split_layout(app, b)) }
        }
    }
}

pub fn capture(app: &App) -> Saved {
    let tabs: Vec<SavedTab> = app
        .s
        .tabs
        .iter()
        .map(|t| {
            let info = app.s.pane(&t.id).map(|p| (p.info.tab_name.clone(), p.info.pinned)).unwrap_or_default();
            let layout = match &t.split {
                Some(sp) => split_layout(app, sp),
                None => session_layout(app, &t.id),
            };
            SavedTab { name: info.0, pinned: info.1, layout }
        })
        .filter(|t| t.layout != Layout::Empty)
        .collect();
    let active = app.s.tabs.iter().position(|t| Some(&t.id) == app.s.active.as_ref()).unwrap_or(0);
    Saved { active: active.min(tabs.len().saturating_sub(1)), tabs }
}

/// Saves the layout if it changed since the last save (called each frame; cheap for a few tabs).
pub fn save_if_changed(app: &mut App) {
    let Ok(json) = serde_json::to_string(&capture(app)) else { return };
    if app.saved_layout.as_deref() != Some(&json) {
        let _ = app.s.svc.store.set_setting(KEY, &json);
        app.saved_layout = Some(json);
    }
}

/// Opens one session for a layout leaf; returns its id.
fn open(app: &mut App, l: &Layout) -> Option<String> {
    let id = match l {
        Layout::Profile { id } => app.s.svc.connect_profile(id).ok()?,
        Layout::Local { shell, cwd } => app.s.svc.connect_local_in(shell, cwd.clone()),
        _ => return None,
    };
    app.add_session(&id, false);
    Some(id)
}

fn build(app: &mut App, l: &Layout) -> Split {
    match l {
        Layout::Split { down, ratio, a, b } => Split::Node {
            dir: if *down { Dir::Down } else { Dir::Right },
            ratio: ratio.clamp(0.1, 0.9),
            a: Box::new(build(app, a)),
            b: Box::new(build(app, b)),
        },
        leaf => Split::Leaf(open(app, leaf)),
    }
}

pub fn restore(app: &mut App) {
    let Some(saved) = app.s.svc.store.setting(KEY).and_then(|j| serde_json::from_str::<Saved>(&j).ok()) else { return };
    let mut ids = vec![];
    for t in &saved.tabs {
        let (id, split) = match &t.layout {
            Layout::Split { .. } => {
                let sp = build(app, &t.layout);
                let Some(first) = sp.sessions().first().cloned() else { continue };
                (first, Some(sp))
            }
            leaf => match open(app, leaf) {
                Some(id) => (id, None),
                None => continue,
            },
        };
        if !t.name.is_empty() {
            let _ = app.s.svc.sessions.rename(&id, &t.name);
            if let Some(p) = app.s.pane_mut(&id) {
                p.info.tab_name = t.name.clone();
            }
        }
        if t.pinned {
            let _ = app.s.svc.sessions.pin(&id, true);
            if let Some(p) = app.s.pane_mut(&id) {
                p.info.pinned = true;
            }
        }
        app.s.tabs.push(Tab { id: id.clone(), activity: false, split });
        ids.push(id);
    }
    if let Some(id) = ids.get(saved.active).or(ids.first()) {
        app.s.active = Some(id.clone());
        app.s.focused = Some(id.clone());
    }
    if !ids.is_empty() {
        app.s.info(format!("Restored {} tab(s) from last time", ids.len()));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn layouts_round_trip() {
        let saved = Saved {
            active: 1,
            tabs: vec![
                SavedTab { name: "web".into(), pinned: true, layout: Layout::Profile { id: "p1".into() } },
                SavedTab {
                    name: String::new(),
                    pinned: false,
                    layout: Layout::Split {
                        down: true,
                        ratio: 0.3,
                        a: Box::new(Layout::Local { shell: "/bin/zsh".into(), cwd: Some("/tmp".into()) }),
                        b: Box::new(Layout::Empty),
                    },
                },
            ],
        };
        let json = serde_json::to_string(&saved).unwrap();
        assert_eq!(serde_json::from_str::<Saved>(&json).unwrap(), saved);
    }
}
