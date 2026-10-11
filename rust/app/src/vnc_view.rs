//! VNC viewer: the remote framebuffer as a texture, with mouse and keyboard input.
//! STUB — interface fixed; implementation pending.

use crate::state::Shared;
use nexus_core::models::Profile;

pub struct VncView {
    pub session: String,
}

impl VncView {
    pub fn new(session: &str, _profile: &Profile) -> VncView {
        VncView { session: session.into() }
    }

    /// Called on `Event::VncReady` (the client is connected; fetch it via `s.svc.sessions.vnc(id)`).
    pub fn on_ready(&mut self) {}

    pub fn ui(&mut self, ui: &mut egui::Ui, _s: &mut Shared) {
        ui.label("VNC");
    }
}
