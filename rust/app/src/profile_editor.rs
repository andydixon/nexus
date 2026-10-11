//! Profile editor (right dock): create and edit every protocol's settings.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::models::Protocol;

#[derive(Default)]
pub struct ProfileEditor {
    open: bool,
}

impl ProfileEditor {
    pub fn open_new(&mut self, _s: &Shared, _protocol: Option<Protocol>, _folder: &str) {
        self.open = true;
    }

    pub fn open_edit(&mut self, _s: &Shared, _id: &str) {
        self.open = true;
    }

    pub fn is_open(&self) -> bool {
        self.open
    }

    /// Drawn inside the right dock while open.
    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        if ui.button("Close").clicked() {
            self.open = false;
        }
    }
}
