//! Trigger rules: match terminal output lines and notify / highlight / beep.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;

#[derive(Default)]
pub struct Triggers {
    open: bool,
}

impl Triggers {
    pub fn open(&mut self) {
        self.open = true;
    }

    /// Feeds session output; fires matching rules (toasts, highlight request, bell).
    /// Returns true if the tab should be highlighted.
    pub fn check(&mut self, _s: &mut Shared, _session: &str, _data: &[u8]) -> bool {
        false
    }

    pub fn cleanup(&mut self, _session: &str) {}

    pub fn ui(&mut self, _ctx: &egui::Context, _s: &mut Shared) {}
}
