//! GitHub repository browser: owner groups of repo cards with ZIP download and Visit. Port of
//! `github.js`.

use crate::state::Shared;
use crate::theme::{self, Tokens};
use crate::widgets;
use chrono::{DateTime, Utc};
use egui::{Color32, RichText, Sense, Ui, vec2};
use egui_phosphor::regular as icon;
use nexus_core::models::GitHubRepo;
use std::sync::{Arc, Mutex};

enum Msg {
    Repos(Vec<GitHubRepo>),
    RefreshFailed(String),
    Downloaded(String, String),
    DownloadFailed(String),
}

pub struct GithubView {
    pub session: String,
    title: String,
    login: String,
    repos: Vec<GitHubRepo>,
    filter: String,
    status: String,
    status_error: bool,
    inbox: Arc<Mutex<Vec<Msg>>>,
}

fn parse_time(s: &str) -> Option<DateTime<Utc>> {
    DateTime::parse_from_rfc3339(s).ok().map(|d| d.with_timezone(&Utc))
}

/// Latest commit, else push, else update time, in unix ms (0 when unknown).
fn commit_ts(r: &GitHubRepo) -> i64 {
    [&r.latest_commit_at, &r.pushed_at, &r.updated_at].into_iter().find(|s| !s.is_empty()).and_then(|s| parse_time(s)).map_or(0, |d| d.timestamp_millis())
}

/// Filters, groups by owner (repos newest first), and orders groups by their newest repo with
/// the user's own login first on ties, then by name.
fn grouped<'a>(repos: &'a [GitHubRepo], login: &str, filter: &str) -> Vec<(String, Vec<&'a GitHubRepo>)> {
    let f = filter.to_lowercase();
    let mut groups: Vec<(String, Vec<&GitHubRepo>)> = Vec::new();
    for r in repos.iter().filter(|r| {
        f.is_empty() || r.full_name.to_lowercase().contains(&f) || r.description.to_lowercase().contains(&f) || r.language.to_lowercase().contains(&f)
    }) {
        let owner = if r.owner.is_empty() { "unknown" } else { &r.owner };
        match groups.iter_mut().find(|(o, _)| o == owner) {
            Some((_, v)) => v.push(r),
            None => groups.push((owner.to_string(), vec![r])),
        }
    }
    for (_, v) in &mut groups {
        v.sort_by_key(|r| std::cmp::Reverse(commit_ts(r)));
    }
    let newest = |v: &[&GitHubRepo]| v.first().map_or(0, |r| commit_ts(r));
    groups.sort_by(|(a, av), (b, bv)| newest(bv).cmp(&newest(av)).then_with(|| (b == login).cmp(&(a == login))).then_with(|| a.cmp(b)));
    groups
}

/// "just now", "3 minutes ago", "1 day ago"… (the web build's relativeTime).
fn relative_time(iso: &str, now: DateTime<Utc>) -> String {
    let Some(d) = parse_time(iso) else { return String::new() };
    let secs = (now - d).num_seconds();
    if secs < 60 {
        return "just now".into();
    }
    let plural = |n: i64, unit: &str| if n == 1 { format!("1 {unit} ago") } else { format!("{n} {unit}s ago") };
    let mins = secs / 60;
    let hrs = mins / 60;
    let days = hrs / 24;
    let months = days / 30;
    if mins < 60 {
        plural(mins, "minute")
    } else if hrs < 24 {
        plural(hrs, "hour")
    } else if days < 30 {
        plural(days, "day")
    } else if months < 12 {
        plural(months, "month")
    } else {
        plural(months / 12, "year")
    }
}

fn local_time(iso: &str) -> String {
    parse_time(iso).map(|d| d.with_timezone(&chrono::Local).format("%Y-%m-%d %H:%M").to_string()).unwrap_or_else(|| iso.to_string())
}

/// A subset of GitHub's linguist colours.
fn lang_colour(lang: &str) -> Color32 {
    let hex = match lang {
        "JavaScript" => 0xf1e05a,
        "TypeScript" => 0x3178c6,
        "Python" => 0x3572a5,
        "Go" => 0x00add8,
        "Rust" => 0xdea584,
        "Java" => 0xb07219,
        "C" => 0x555555,
        "C++" => 0xf34b7d,
        "C#" => 0x178600,
        "Ruby" => 0x701516,
        "PHP" => 0x4f5d95,
        "Swift" => 0xf05138,
        "Kotlin" => 0xa97bff,
        "Shell" => 0x89e051,
        "HTML" => 0xe34c26,
        "CSS" => 0x563d7c,
        "Dart" => 0x00b4ab,
        "Lua" => 0x000080,
        "Scala" => 0xc22d40,
        "Elixir" => 0x6e4a7e,
        "Haskell" => 0x5e5086,
        "Zig" => 0xec915c,
        "Vue" => 0x41b883,
        "SCSS" => 0xc6538c,
        _ => 0x8b949e,
    };
    Color32::from_rgb((hex >> 16) as u8, (hex >> 8) as u8, hex as u8)
}

enum Action {
    Zip(GitHubRepo),
    Visit(String),
    Copy(String),
}

impl GithubView {
    pub fn new(session: &str, title: &str) -> GithubView {
        GithubView {
            session: session.into(),
            title: if title.is_empty() { "GitHub".into() } else { title.into() },
            login: String::new(),
            repos: vec![],
            filter: String::new(),
            status: "Connecting…".into(),
            status_error: false,
            inbox: Default::default(),
        }
    }

    pub fn on_status(&mut self, status: String) {
        self.status = status;
        self.status_error = false;
    }

    pub fn on_repos(&mut self, login: String, repos: Vec<GitHubRepo>) {
        self.login = login;
        self.repos = repos;
        self.update_status();
    }

    pub fn on_error(&mut self, error: String) {
        self.status = format!("{}  {error}", icon::WARNING);
        self.status_error = true;
    }

    fn update_status(&mut self) {
        let n = self.repos.len();
        let now = chrono::Local::now().format("%H:%M:%S");
        self.on_status(format!("{} · {n} repositor{} · updated {now}", self.login, if n == 1 { "y" } else { "ies" }));
    }

    fn refresh(&mut self, s: &Shared) {
        self.on_status("Refreshing…".into());
        let (svc, sid, inbox, ctx) = (s.svc.clone(), self.session.clone(), self.inbox.clone(), s.ctx.clone());
        std::thread::spawn(move || {
            let msg = match svc.sessions.github(&sid).and_then(|c| c.fetch_repos()) {
                Ok(r) => Msg::Repos(r),
                Err(e) => Msg::RefreshFailed(e.to_string()),
            };
            inbox.lock().unwrap().push(msg);
            ctx.request_repaint();
        });
    }

    fn download(&mut self, s: &Shared, repo: &GitHubRepo) {
        let reference = repo.default_branch.clone();
        let name = if reference.is_empty() { format!("{}.zip", repo.name) } else { format!("{}-{reference}.zip", repo.name) };
        let Some(path) = rfd::FileDialog::new().set_file_name(name).add_filter("ZIP archive", &["zip"]).save_file() else { return };
        self.on_status(format!("Downloading {}...", repo.full_name));
        let (svc, sid, inbox, ctx, full) = (s.svc.clone(), self.session.clone(), self.inbox.clone(), s.ctx.clone(), repo.full_name.clone());
        std::thread::spawn(move || {
            let msg = match svc.sessions.github(&sid).and_then(|c| c.download_repo_archive(&full, &reference, &path)) {
                Ok(()) => Msg::Downloaded(full, path.display().to_string()),
                Err(e) => Msg::DownloadFailed(e.to_string()),
            };
            inbox.lock().unwrap().push(msg);
            ctx.request_repaint();
        });
    }

    pub fn ui(&mut self, ui: &mut Ui, s: &mut Shared) {
        let msgs = std::mem::take(&mut *self.inbox.lock().unwrap());
        for m in msgs {
            match m {
                Msg::Repos(r) => {
                    let login = self.login.clone();
                    self.on_repos(login, r);
                }
                Msg::RefreshFailed(e) => self.on_error(e),
                Msg::Downloaded(name, path) => {
                    self.update_status();
                    s.success(format!("Downloaded {name} to {path}"));
                }
                Msg::DownloadFailed(e) => {
                    self.on_error(format!("Download failed: {e}"));
                    s.error(format!("Download failed: {e}"));
                }
            }
        }

        let t = Tokens::get(ui.ctx());
        egui::Frame::new().fill(t.chrome).inner_margin(egui::Margin::symmetric(12, 8)).show(ui, |ui| {
            ui.set_width(ui.available_width());
            ui.horizontal(|ui| {
                ui.label(RichText::new(icon::GITHUB_LOGO).size(18.0));
                ui.label(RichText::new(&self.title).font(theme::semibold(14.0)));
                ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                    if widgets::icon_button(ui, icon::ARROW_CLOCKWISE, "Refresh now").clicked() {
                        self.refresh(s);
                    }
                    ui.add(egui::TextEdit::singleline(&mut self.filter).hint_text(format!("{}  Filter repositories…", icon::MAGNIFYING_GLASS)).desired_width(240.0));
                });
            });
            let c = if self.status_error { t.danger } else { t.text_dim };
            ui.label(RichText::new(&self.status).color(c).size(11.5));
        });

        let mut action = None;
        egui::ScrollArea::vertical().id_salt(("gh", &self.session)).auto_shrink([false, false]).show(ui, |ui| {
            ui.add_space(8.0);
            let groups = grouped(&self.repos, &self.login, &self.filter);
            if groups.is_empty() {
                let msg = if self.filter.is_empty() { "No repositories found." } else { "No matching repositories." };
                widgets::empty(ui, icon::GITHUB_LOGO, msg, "");
                return;
            }
            let now = Utc::now();
            egui::Frame::new().inner_margin(egui::Margin::symmetric(14, 0)).show(ui, |ui| {
                for (owner, repos) in groups {
                    ui.horizontal(|ui| {
                        ui.label(RichText::new(&owner).font(theme::semibold(13.0)));
                        widgets::chip(ui, &repos.len().to_string(), t.text_dim);
                    });
                    ui.add_space(2.0);
                    for r in repos {
                        if let Some(a) = card(ui, r, now) {
                            action = Some(a);
                        }
                        ui.add_space(6.0);
                    }
                    ui.add_space(8.0);
                }
            });
        });
        match action {
            Some(Action::Zip(r)) => self.download(s, &r),
            Some(Action::Visit(url)) if !url.is_empty() => ui.ctx().open_url(egui::OpenUrl::new_tab(url)),
            Some(Action::Copy(url)) => {
                ui.ctx().copy_text(url);
                s.info("URL copied");
            }
            _ => {}
        }
    }
}

fn card(ui: &mut Ui, r: &GitHubRepo, now: DateTime<Utc>) -> Option<Action> {
    let t = Tokens::get(ui.ctx());
    let mut action = None;
    let resp = egui::Frame::new().fill(t.card).stroke(egui::Stroke::new(1.0, t.card_border)).corner_radius(6).inner_margin(egui::Margin::symmetric(12, 9)).show(ui, |ui| {
        ui.set_width(ui.available_width());
        ui.horizontal(|ui| {
            ui.label(RichText::new(&r.name).font(theme::semibold(13.5)).color(t.accent));
            if r.private {
                widgets::chip(ui, "Private", t.warning);
            }
            if r.fork {
                widgets::chip(ui, "Fork", t.text_dim);
            }
            if r.archived {
                widgets::chip(ui, "Archived", t.danger);
            }
            ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                if ui.button(format!("{} Visit", icon::ARROW_SQUARE_OUT)).on_hover_text("Open repository in browser").clicked() {
                    action = Some(Action::Visit(r.html_url.clone()));
                }
                if ui.button(format!("{} ZIP", icon::FILE_ZIP)).on_hover_text("Download repository as zip").clicked() {
                    action = Some(Action::Zip(r.clone()));
                }
            });
        });
        if !r.latest_committer.is_empty() && !r.latest_commit_at.is_empty() {
            ui.label(RichText::new(format!("Last commit: {} by {}", local_time(&r.latest_commit_at), r.latest_committer)).color(t.text_faint).size(11.5));
        }
        if !r.description.is_empty() {
            ui.label(RichText::new(&r.description).color(t.text_dim));
        }
        ui.horizontal(|ui| {
            if !r.language.is_empty() {
                let (dot, _) = ui.allocate_exact_size(vec2(10.0, 10.0), Sense::hover());
                ui.painter().circle_filled(dot.center(), 5.0, lang_colour(&r.language));
                ui.label(RichText::new(&r.language).color(t.text_dim).size(11.5));
                ui.add_space(8.0);
            }
            if r.stars > 0 {
                ui.label(RichText::new(format!("{} {}", icon::STAR, widgets::group(r.stars as u64))).color(t.text_dim).size(11.5));
                ui.add_space(8.0);
            }
            if !r.pushed_at.is_empty() {
                ui.label(RichText::new(relative_time(&r.pushed_at, now)).color(t.text_faint).size(11.5)).on_hover_text(local_time(&r.pushed_at));
            }
        });
    });
    let resp = resp.response.interact(Sense::click());
    if resp.double_clicked() {
        action = Some(Action::Visit(r.html_url.clone()));
    }
    resp.context_menu(|ui| {
        if ui.button(format!("{}  Visit", icon::ARROW_SQUARE_OUT)).clicked() {
            action = Some(Action::Visit(r.html_url.clone()));
        }
        if ui.button(format!("{}  Download ZIP…", icon::FILE_ZIP)).clicked() {
            action = Some(Action::Zip(r.clone()));
        }
        if ui.button(format!("{}  Copy URL", icon::COPY)).clicked() {
            action = Some(Action::Copy(r.html_url.clone()));
        }
    });
    action
}

#[cfg(test)]
mod tests {
    use super::*;

    fn repo(owner: &str, name: &str, at: &str) -> GitHubRepo {
        GitHubRepo { owner: owner.into(), name: name.into(), full_name: format!("{owner}/{name}"), pushed_at: at.into(), ..Default::default() }
    }

    #[test]
    fn groups_by_newest_with_login_first_on_ties() {
        let repos = vec![
            repo("org", "a", "2024-01-01T00:00:00Z"),
            repo("me", "old", "2023-01-01T00:00:00Z"),
            repo("me", "new", "2024-01-01T00:00:00Z"),
            repo("zed", "z", "2025-01-01T00:00:00Z"),
        ];
        let g = grouped(&repos, "me", "");
        let names: Vec<(&str, Vec<&str>)> = g.iter().map(|(o, v)| (o.as_str(), v.iter().map(|r| r.name.as_str()).collect())).collect();
        assert_eq!(names, [("zed", vec!["z"]), ("me", vec!["new", "old"]), ("org", vec!["a"])]);
        assert_eq!(grouped(&repos, "me", "OLD").len(), 1);
    }

    #[test]
    fn relative_times() {
        let now = parse_time("2025-06-10T12:00:00Z").unwrap();
        assert_eq!(relative_time("2025-06-10T11:59:30Z", now), "just now");
        assert_eq!(relative_time("2025-06-10T11:59:00Z", now), "1 minute ago");
        assert_eq!(relative_time("2025-06-10T09:00:00Z", now), "3 hours ago");
        assert_eq!(relative_time("2025-06-01T12:00:00Z", now), "9 days ago");
        assert_eq!(relative_time("2023-06-01T12:00:00Z", now), "2 years ago");
        assert_eq!(relative_time("bad", now), "");
    }
}
