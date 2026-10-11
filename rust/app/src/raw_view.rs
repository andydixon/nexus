//! RAW TCP viewer: text / hex / split views of the capture and a send bar.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::models::Profile;

pub struct RawView {
    pub session: String,
}

impl RawView {
    pub fn new(session: &str, _profile: &Profile) -> RawView {
        RawView { session: session.into() }
    }

    /// Data from `Event::RawData` (direction "send"/"recv", unix ms timestamp).
    pub fn on_data(&mut self, _data: &[u8], _direction: &str, _timestamp: i64) {}

    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        ui.label("RAW viewer");
    }
}
