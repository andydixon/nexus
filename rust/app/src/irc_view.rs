//! IRC client view: room tabs, message log, composer and user/room lists.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::clients::irc;
use nexus_core::models::Profile;

pub struct IrcView {
    pub session: String,
}

impl IrcView {
    pub fn new(session: &str, _profile: &Profile) -> IrcView {
        IrcView { session: session.into() }
    }

    pub fn on_event(&mut self, _event: irc::Event) {}

    /// True when an event for a room other than the visible one arrived (for the tab marker).
    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        ui.label("IRC");
    }
}
