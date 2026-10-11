//! Quick Connect dialog: an ad-hoc session without saving a profile.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;

#[derive(Default)]
pub struct QuickConnect {
    open: bool,
}

impl QuickConnect {
    pub fn open(&mut self) {
        self.open = true;
    }

    pub fn is_open(&self) -> bool {
        self.open
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, _s: &mut Shared) {
        let mut open = self.open;
        egui::Window::new("QuickConnect").open(&mut open).show(ctx, |ui| ui.label("todo"));
        self.open = open;
    }
}
