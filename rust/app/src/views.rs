//! How a session is drawn. Terminal-style protocols share `TermView`; the rest have their own.

use crate::github_view::GithubView;
use crate::irc_view::IrcView;
use crate::raw_view::RawView;
use crate::terminal::TermView;
use crate::vnc_view::VncView;

pub enum View {
    Terminal(Box<TermView>),
    Raw(Box<RawView>),
    Irc(Box<IrcView>),
    Github(Box<GithubView>),
    Vnc(Box<VncView>),
}

/// Protocol badge colour (the web build's --protocol-* tokens).
pub fn protocol_color(p: nexus_core::models::Protocol) -> egui::Color32 {
    use nexus_core::models::Protocol::*;
    let rgb = egui::Color32::from_rgb;
    match p {
        Ssh => rgb(0x4e, 0xcd, 0xc4),
        Telnet => rgb(0xff, 0xa7, 0x26),
        Raw => rgb(0xab, 0x47, 0xbc),
        Github => rgb(0x7c, 0x8a, 0xff),
        Vnc => rgb(0xe8, 0x5d, 0x4a),
        Irc => rgb(0xd8, 0xb2, 0x4c),
        Local => rgb(0x8b, 0xc3, 0x4a),
        Serial => rgb(0x4f, 0xa3, 0xe0),
    }
}

/// Short badge text ("SSH", "TELNET"…).
pub fn protocol_badge(p: nexus_core::models::Protocol) -> &'static str {
    use nexus_core::models::Protocol::*;
    match p {
        Ssh => "SSH",
        Telnet => "TELNET",
        Raw => "RAW",
        Github => "GitHub",
        Vnc => "VNC",
        Irc => "IRC",
        Local => "LOCAL",
        Serial => "SERIAL",
    }
}
