//! Port forwards dialog for a live SSH session: list, stop, add.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;

#[derive(Default)]
pub struct Forwards {
    session: Option<String>,
}

impl Forwards {
    pub fn open(&mut self, session: &str) {
        self.session = Some(session.into());
    }

    pub fn ui(&mut self, _ctx: &egui::Context, _s: &mut Shared) {}
}
