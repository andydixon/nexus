//! SFTP browser (right dock), the file-drop dialog and the remote directory picker.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::clients::sftp::Progress;
use std::path::PathBuf;

#[derive(Default)]
pub struct Sftp {
    open_for: Option<String>,
}

impl Sftp {
    pub fn toggle(&mut self, session: &str) {
        self.open_for = if self.open_for.as_deref() == Some(session) { None } else { Some(session.into()) };
    }

    /// The session whose browser is showing in the dock, if any.
    pub fn open_for(&self) -> Option<&str> {
        self.open_for.as_deref()
    }

    pub fn close(&mut self, session: &str) {
        if self.open_for.as_deref() == Some(session) {
            self.open_for = None;
        }
    }

    pub fn on_progress(&mut self, _session: &str, _p: Progress) {}

    /// Files dropped on a terminal: offer "paste contents" or (SSH) "upload to a directory".
    pub fn drop_files(&mut self, _s: &mut Shared, _session: &str, _paths: Vec<PathBuf>) {}

    /// The dock panel.
    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        ui.label("SFTP");
    }

    /// Modal dialogs (file drop, directory picker), drawn every frame.
    pub fn dialogs(&mut self, _ctx: &egui::Context, _s: &mut Shared) {}
}
