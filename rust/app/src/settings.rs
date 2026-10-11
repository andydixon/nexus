//! Settings window: General, Terminal, Security, Known Hosts, Help, About.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;

#[derive(Default)]
pub struct Settings {
    open: bool,
}

impl Settings {
    pub fn open(&mut self, _tab: crate::state::SettingsTab) {
        self.open = true;
    }

    pub fn is_open(&self) -> bool {
        self.open
    }

    /// Called every frame; draws its window(s) while open.
    pub fn ui(&mut self, ctx: &egui::Context, _s: &mut Shared) {
        let mut open = self.open;
        egui::Window::new("Settings").open(&mut open).show(ctx, |ui| ui.label("todo"));
        self.open = open;
    }
}
