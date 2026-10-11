//! Profile export/import: encrypted `.nexus` (also reads `.link`) and plain JSON.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;

#[derive(Default)]
pub struct Transfer {}

impl Transfer {
    /// Encrypted export of these profiles (all when empty): pick profiles, password, file.
    pub fn export_encrypted(&mut self, _ids: Vec<String>) {}
    pub fn import_encrypted(&mut self) {}
    pub fn export_json(&mut self) {}
    pub fn import_json(&mut self) {}
    pub fn ui(&mut self, _ctx: &egui::Context, _s: &mut Shared) {}
}
