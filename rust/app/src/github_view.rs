//! GitHub repository browser.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::models::GitHubRepo;

pub struct GithubView {
    pub session: String,
}

impl GithubView {
    pub fn new(session: &str, _title: &str) -> GithubView {
        GithubView { session: session.into() }
    }

    pub fn on_status(&mut self, _status: String) {}

    pub fn on_repos(&mut self, _login: String, _repos: Vec<GitHubRepo>) {}

    pub fn on_error(&mut self, _error: String) {}

    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        ui.label("GitHub");
    }
}
