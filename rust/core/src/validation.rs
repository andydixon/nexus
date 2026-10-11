//! Field-level validation of profiles and their protocol configs. Enum-typed fields (secret modes,
//! host-key policy, line endings, forward direction, protocol) can't hold invalid values in Rust,
//! so the Go checks for those are enforced by the types instead.

use crate::models::{GitHubConfig, IrcConfig, Profile, Protocol, RawConfig, SerialConfig, SshConfig, TelnetConfig, VncConfig};
use serde::Serialize;
use std::fmt;

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ValidationError {
    pub field: String,
    pub message: String,
}

impl fmt::Display for ValidationError {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        write!(f, "{}: {}", self.field, self.message)
    }
}
impl std::error::Error for ValidationError {}

#[derive(Clone, Debug, PartialEq, Default, Serialize)]
pub struct ValidationErrors {
    pub errors: Vec<ValidationError>,
}

impl fmt::Display for ValidationErrors {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let msgs: Vec<String> = self.errors.iter().map(|e| e.to_string()).collect();
        write!(f, "validation failed: {}", msgs.join("; "))
    }
}
impl std::error::Error for ValidationErrors {}

impl ValidationErrors {
    pub fn add(&mut self, field: impl Into<String>, message: impl Into<String>) {
        self.errors.push(ValidationError { field: field.into(), message: message.into() });
    }
    fn into_result(self) -> Result<(), ValidationErrors> {
        if self.errors.is_empty() { Ok(()) } else { Err(self) }
    }
    fn merge(&mut self, r: Result<(), ValidationErrors>) {
        if let Err(e) = r {
            self.errors.extend(e.errors);
        }
    }
    /// Adds `r`'s message (if any) under `field`.
    fn check(&mut self, field: &str, r: Result<(), ValidationError>) {
        if let Err(e) = r {
            self.add(field, e.message);
        }
    }
}

fn err(field: &str, message: &str) -> Result<(), ValidationError> {
    Err(ValidationError { field: field.into(), message: message.into() })
}

pub fn validate_profile(p: &Profile) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    if p.id.trim().is_empty() {
        errs.add("id", "profile ID must not be empty");
    }
    if p.display_name.trim().is_empty() {
        errs.add("displayName", "display name must not be empty");
    }

    match p.protocol {
        Protocol::Ssh => {
            host_port(&mut errs, &p.host, p.port);
            if p.username.trim().is_empty() {
                errs.add("username", "username is required for SSH connections");
            } else {
                errs.check("username", validate_username(&p.username));
            }
            match &p.ssh {
                None => errs.add("ssh", "SSH configuration is required for SSH protocol"),
                Some(c) => errs.merge(validate_ssh_config(c)),
            }
        }
        Protocol::Telnet => {
            host_port(&mut errs, &p.host, p.port);
            match &p.telnet {
                None => errs.add("telnet", "Telnet configuration is required for Telnet protocol"),
                Some(c) => errs.merge(validate_telnet_config(c)),
            }
        }
        Protocol::Raw => {
            host_port(&mut errs, &p.host, p.port);
            match &p.raw {
                None => errs.add("raw", "RAW configuration is required for RAW protocol"),
                Some(c) => errs.merge(validate_raw_config(c)),
            }
        }
        Protocol::Github => match &p.github {
            None => errs.add("github", "GitHub configuration is required for GitHub protocol"),
            Some(c) => errs.merge(validate_github_config(c)),
        },
        Protocol::Vnc => match &p.vnc {
            None => errs.add("vnc", "VNC configuration is required for VNC protocol"),
            Some(c) => errs.merge(validate_vnc_config(&p.host, c)),
        },
        Protocol::Irc => {
            host_port(&mut errs, &p.host, p.port);
            match &p.irc {
                None => errs.add("irc", "IRC configuration is required for IRC protocol"),
                Some(c) => errs.merge(validate_irc_config(c)),
            }
        }
        Protocol::Serial => {
            if p.host.trim().is_empty() {
                errs.add("host", "serial device path must not be empty");
            }
            match &p.serial {
                None => errs.add("serial", "Serial configuration is required for Serial protocol"),
                Some(c) => errs.merge(validate_serial_config(c)),
            }
        }
        Protocol::Local => errs.add("protocol", format!("unsupported protocol {:?}", p.protocol.id())),
    }

    if p.connect_timeout < 0 {
        errs.add("connectTimeout", "connect timeout must not be negative");
    }
    if p.read_timeout < 0 {
        errs.add("readTimeout", "read timeout must not be negative");
    }
    if !(1..=72).contains(&p.terminal_font_size) {
        errs.add("terminalFontSize", "font size must be between 1 and 72");
    }
    if p.terminal_scrollback < 0 {
        errs.add("terminalScrollback", "scrollback must not be negative");
    }
    errs.into_result()
}

fn host_port(errs: &mut ValidationErrors, host: &str, port: i64) {
    errs.check("host", validate_host(host));
    errs.check("port", validate_port(port));
}

/// Non-empty, no whitespace, no URI scheme, no leading hyphen.
pub fn validate_host(host: &str) -> Result<(), ValidationError> {
    let h = host.trim();
    if h.is_empty() {
        return err("host", "host must not be empty");
    }
    if h.contains([' ', '\t', '\n', '\r']) {
        return err("host", "host must not contain whitespace");
    }
    if h.contains("://") {
        return err("host", "host must not include a URI scheme");
    }
    // An IP address never starts with '-', so this only rejects hostnames (as in Go).
    if h.starts_with('-') {
        return err("host", "host must not begin with a hyphen");
    }
    Ok(())
}

pub fn validate_port(port: i64) -> Result<(), ValidationError> {
    if !(1..=65535).contains(&port) {
        return err("port", "port must be between 1 and 65535");
    }
    Ok(())
}

/// Non-empty and free of control characters.
pub fn validate_username(username: &str) -> Result<(), ValidationError> {
    let u = username.trim();
    if u.is_empty() {
        return err("username", "username must not be empty");
    }
    if u.chars().any(is_control) {
        return err("username", "username must not contain control characters");
    }
    Ok(())
}

fn is_control(c: char) -> bool {
    c < '\x20' || c == '\x7f'
}

pub fn validate_ssh_config(cfg: &SshConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    if cfg.auth_methods.is_empty() {
        errs.add("ssh.authMethods", "at least one authentication method is required");
    }
    for m in &cfg.auth_methods {
        if !matches!(m.as_str(), "password" | "publickey" | "keyboard-interactive") {
            errs.add("ssh.authMethods", format!("unsupported authentication method {m:?}"));
        }
    }
    if cfg.keepalive_interval < 0 {
        errs.add("ssh.keepaliveInterval", "keepalive interval must not be negative");
    }
    if cfg.keepalive_count_max < 0 {
        errs.add("ssh.keepaliveCountMax", "keepalive count max must not be negative");
    }
    for (i, jh) in cfg.jump_hosts.iter().enumerate() {
        let prefix = format!("ssh.jumpHosts[{i}]");
        if jh.host.trim().is_empty() {
            errs.add(format!("{prefix}.host"), "jump host address must not be empty");
        }
        if !(1..=65535).contains(&jh.port) {
            errs.add(format!("{prefix}.port"), "jump host port must be between 1 and 65535");
        }
        if jh.username.trim().is_empty() {
            errs.add(format!("{prefix}.username"), "jump host username must not be empty");
        }
    }
    for (i, pf) in cfg.port_forwards.iter().enumerate() {
        let prefix = format!("ssh.portForwards[{i}]");
        if !(1..=65535).contains(&pf.bind_port) {
            errs.add(format!("{prefix}.bindPort"), "bind port must be between 1 and 65535");
        }
        if !(1..=65535).contains(&pf.dest_port) {
            errs.add(format!("{prefix}.destPort"), "destination port must be between 1 and 65535");
        }
        if pf.dest_host.trim().is_empty() {
            errs.add(format!("{prefix}.destHost"), "destination host must not be empty");
        }
    }
    errs.into_result()
}

pub fn validate_telnet_config(cfg: &TelnetConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    if cfg.encoding.trim().is_empty() {
        errs.add("telnet.encoding", "encoding must not be empty");
    }
    errs.into_result()
}

pub fn validate_raw_config(cfg: &RawConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    if cfg.encoding.trim().is_empty() {
        errs.add("raw.encoding", "encoding must not be empty");
    }
    for (i, preset) in cfg.payload_presets.iter().enumerate() {
        let prefix = format!("raw.payloadPresets[{i}]");
        if preset.name.trim().is_empty() {
            errs.add(format!("{prefix}.name"), "payload preset name must not be empty");
        }
        if !matches!(preset.format.as_str(), "text" | "hex" | "escaped") {
            errs.add(format!("{prefix}.format"), format!("unsupported payload format {:?}", preset.format));
        }
    }
    errs.into_result()
}

/// The API base URL must be absolute HTTPS (HTTP only to loopback) without credentials.
pub fn validate_github_config(cfg: &GitHubConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    match split_url(cfg.base_url.trim()) {
        None => errs.add("github.baseURL", "API base URL must be an absolute HTTPS URL"),
        Some((scheme, has_user, hostname)) => {
            if has_user {
                errs.add("github.baseURL", "API base URL must not include credentials");
            }
            if scheme != "https" && !(scheme == "http" && is_loopback_host(hostname)) {
                errs.add("github.baseURL", "API base URL must use HTTPS unless it points to loopback");
            }
        }
    }
    if cfg.refresh_interval_secs != 0 && cfg.refresh_interval_secs < 60 {
        errs.add("github.refreshIntervalSecs", "refresh interval must be at least 60 seconds");
    }
    errs.into_result()
}

/// (lowercased scheme, has userinfo, hostname without port/brackets) of an absolute URL.
fn split_url(s: &str) -> Option<(String, bool, &str)> {
    let (scheme, rest) = s.split_once("://")?;
    if scheme.is_empty() {
        return None;
    }
    let authority = &rest[..rest.find(['/', '?', '#']).unwrap_or(rest.len())];
    let (has_user, host) = match authority.rsplit_once('@') {
        Some((_, h)) => (true, h),
        None => (false, authority),
    };
    if host.is_empty() {
        return None;
    }
    let hostname = match host.strip_prefix('[') {
        Some(h) => h.split(']').next().unwrap_or(h),
        None => host.rsplit_once(':').map_or(host, |(h, _)| h),
    };
    Some((scheme.to_ascii_lowercase(), has_user, hostname))
}

fn is_loopback_host(host: &str) -> bool {
    let h = host.trim().to_lowercase();
    h == "localhost" || h.parse::<std::net::IpAddr>().is_ok_and(|ip| ip.to_canonical().is_loopback())
}

/// The VNC host falls back to the profile host.
pub fn validate_vnc_config(profile_host: &str, cfg: &VncConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    let host = if cfg.vnc_host.trim().is_empty() { profile_host } else { &cfg.vnc_host };
    errs.check("vnc.vncHost", validate_host(host));
    errs.check("vnc.vncPort", validate_port(cfg.vnc_port));
    if !(0..=9).contains(&cfg.quality) {
        errs.add("vnc.quality", "quality must be between 0 and 9");
    }
    if cfg.ssh_tunnel {
        ssh_tunnel(&mut errs, "vnc", &cfg.ssh_host, cfg.ssh_port, &cfg.ssh_username, &cfg.ssh_auth_method);
    }
    errs.into_result()
}

fn ssh_tunnel(errs: &mut ValidationErrors, p: &str, host: &str, port: i64, user: &str, auth: &str) {
    errs.check(&format!("{p}.sshHost"), validate_host(host));
    errs.check(&format!("{p}.sshPort"), validate_port(port));
    if user.trim().is_empty() {
        errs.add(format!("{p}.sshUsername"), "SSH username is required when tunnelling");
    } else {
        errs.check(&format!("{p}.sshUsername"), validate_username(user));
    }
    if auth != "password" && auth != "publickey" {
        errs.add(format!("{p}.sshAuthMethod"), format!("unsupported SSH auth method {auth:?}"));
    }
}

pub fn validate_irc_config(cfg: &IrcConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    let nick = cfg.nick.trim();
    if nick.is_empty() {
        errs.add("irc.nick", "nickname is required");
    } else if !is_valid_nick(nick) {
        errs.add("irc.nick", "nickname is not valid for IRC");
    }
    if cfg.real_name.contains(['\r', '\n', '\0']) {
        errs.add("irc.realName", "real name must not contain control characters");
    }
    if cfg.nick_serv_identify && cfg.nick_serv_account.chars().any(is_control) {
        errs.add("irc.nickServAccount", "NickServ account must not contain control characters");
    }
    let mut seen = std::collections::HashSet::new();
    for (i, room) in cfg.auto_join_rooms.iter().enumerate() {
        let field = format!("irc.autoJoinRooms[{i}]");
        let name = room.trim();
        if name.is_empty() {
            errs.add(field, "room name must not be empty");
            continue;
        }
        if !is_valid_channel(name) {
            errs.add(field.clone(), "room name must start with a valid IRC channel prefix");
        }
        if !seen.insert(name.to_lowercase()) {
            errs.add(field, "room is listed more than once");
        }
    }
    if cfg.ssh_tunnel {
        ssh_tunnel(&mut errs, "irc", &cfg.ssh_host, cfg.ssh_port, &cfg.ssh_username, &cfg.ssh_auth_method);
    }
    if cfg.max_reconnect_tries < 0 {
        errs.add("irc.maxReconnectTries", "max reconnect attempts must not be negative");
    }
    errs.into_result()
}

/// Device path lives in `Profile::host` and is checked by `validate_profile`.
pub fn validate_serial_config(cfg: &SerialConfig) -> Result<(), ValidationErrors> {
    let mut errs = ValidationErrors::default();
    if cfg.baud_rate == 0 {
        errs.add("serial.baudRate", "baud rate must be greater than 0");
    }
    if !(5..=8).contains(&cfg.data_bits) {
        errs.add("serial.dataBits", "data bits must be between 5 and 8");
    }
    if cfg.stop_bits != 1 && cfg.stop_bits != 2 {
        errs.add("serial.stopBits", "stop bits must be 1 or 2");
    }
    errs.into_result()
}

/// girc's IsValidNick: first byte in 'A'..='}' or '?', the rest also digits or '-'.
fn is_valid_nick(nick: &str) -> bool {
    let b = nick.as_bytes();
    let Some((&first, rest)) = b.split_first() else { return false };
    ((b'A'..=b'}').contains(&first) || first == b'?') && rest.iter().all(|c| (b'A'..=b'}').contains(c) || c.is_ascii_digit() || *c == b'-')
}

/// girc's IsValidChannel.
fn is_valid_channel(ch: &str) -> bool {
    let b = ch.as_bytes();
    if b.len() <= 1 || b.len() > 50 || !b"!#&*~+".contains(&b[0]) {
        return false;
    }
    // !<5-char ID><name>
    if b[0] == b'!' && (b.len() < 7 || !b[1..6].iter().all(|c| c.is_ascii_digit() || c.is_ascii_uppercase())) {
        return false;
    }
    !b[1..].iter().any(|c| [0x00, 0x07, 0x0d, 0x0a, 0x20, 0x2c, 0x3a].contains(c))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::{JumpHost, PayloadPreset};

    #[test]
    fn hosts() {
        for (h, ok) in [
            ("example.com", true),
            ("192.168.1.1", true),
            ("::1", true),
            ("", false),
            ("   ", false),
            ("host name", false),
            ("host\tname", false),
            ("ssh://example.com", false),
            ("-bad-host", false),
            ("server.internal.corp", true),
        ] {
            assert_eq!(validate_host(h).is_ok(), ok, "{h:?}");
        }
    }

    #[test]
    fn ports() {
        for (p, ok) in [(1, true), (22, true), (443, true), (65535, true), (0, false), (-1, false), (65536, false), (99999, false)] {
            assert_eq!(validate_port(p).is_ok(), ok, "{p}");
        }
    }

    #[test]
    fn usernames() {
        for (u, ok) in [
            ("admin", true),
            ("", false),
            ("   ", false),
            ("my user", true),
            ("abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789", true),
            ("bad\x01user", false),
            ("bad\x7fuser", false),
        ] {
            assert_eq!(validate_username(u).is_ok(), ok, "{u:?}");
        }
    }

    fn ssh() -> Profile {
        let mut p = Profile::new(Protocol::Ssh);
        p.display_name = "Test SSH".into();
        p.host = "example.com".into();
        p.username = "root".into();
        p
    }

    #[test]
    fn profiles() {
        let mut telnet = Profile::new(Protocol::Telnet);
        telnet.display_name = "Test Telnet".into();
        telnet.host = "switch.local".into();
        let mut raw = Profile::new(Protocol::Raw);
        raw.display_name = "Test RAW".into();
        raw.host = "device.local".into();
        let mut vnc = Profile::new(Protocol::Vnc);
        vnc.display_name = "Test VNC".into();
        vnc.vnc.as_mut().unwrap().vnc_host = "desktop.local".into();
        for p in [ssh(), telnet, raw, vnc] {
            assert!(validate_profile(&p).is_ok(), "{:?}", validate_profile(&p));
        }

        let bad: [fn(&mut Profile); 7] = [
            |p| p.host.clear(),
            |p| p.port = 0,
            |p| p.protocol = Protocol::Local,
            |p| p.username.clear(),
            |p| p.ssh = None,
            |p| p.connect_timeout = -1,
            |p| p.terminal_font_size = 0,
        ];
        for (i, f) in bad.iter().enumerate() {
            let mut p = ssh();
            f(&mut p);
            assert!(validate_profile(&p).is_err(), "case {i}");
        }
    }

    #[test]
    fn serial_profiles() {
        let mut p = Profile::new(Protocol::Serial);
        p.display_name = "Console".into();
        p.host = "/dev/ttyUSB0".into();
        assert!(validate_profile(&p).is_ok());
        let bad: [fn(&mut Profile); 6] = [
            |p| p.host = "  ".into(),
            |p| p.serial = None,
            |p| p.serial.as_mut().unwrap().baud_rate = 0,
            |p| p.serial.as_mut().unwrap().data_bits = 4,
            |p| p.serial.as_mut().unwrap().data_bits = 9,
            |p| p.serial.as_mut().unwrap().stop_bits = 3,
        ];
        for (i, f) in bad.iter().enumerate() {
            let mut q = p.clone();
            f(&mut q);
            assert!(validate_profile(&q).is_err(), "case {i}");
        }
    }

    #[test]
    fn github() {
        let cfg = |url: &str, secs| GitHubConfig { base_url: url.into(), refresh_interval_secs: secs, ..Default::default() };
        for (c, ok) in [
            (cfg("https://api.github.com", 300), true),
            (cfg("http://localhost:3000/api/v3", 60), true),
            (cfg("http://[::1]:3000/api/v3", 60), true),
            (cfg("http://127.0.0.1/api/v3", 0), true),
            (cfg("http://evil.example/api/v3", 300), false),
            (cfg("https://token@example.com/api/v3", 300), false),
            (cfg("https://api.github.com", 1), false),
            (cfg("api.github.com", 300), false),
        ] {
            assert_eq!(validate_github_config(&c).is_ok(), ok, "{c:?}");
        }
    }

    #[test]
    fn vnc() {
        let base = VncConfig { vnc_port: 5900, quality: 6, ..Default::default() };
        let named = VncConfig { vnc_host: "desktop.local".into(), ..base.clone() };
        assert!(validate_vnc_config("", &named).is_ok());
        assert!(validate_vnc_config("desktop.local", &base).is_ok());
        assert!(validate_vnc_config("", &base).is_err());
        let tunnel =
            VncConfig { ssh_tunnel: true, ssh_host: "jump.local".into(), ssh_port: 22, ssh_username: "admin".into(), ssh_auth_method: "token".into(), ..base };
        assert!(validate_vnc_config("desktop.local", &tunnel).is_err());
    }

    #[test]
    fn ssh_configs() {
        let valid = || SshConfig { auth_methods: vec!["password".into()], keepalive_interval: 30, keepalive_count_max: 3, ..Default::default() };
        assert!(validate_ssh_config(&valid()).is_ok());
        assert!(validate_ssh_config(&SshConfig { auth_methods: vec![], ..valid() }).is_err());
        assert!(validate_ssh_config(&SshConfig { auth_methods: vec!["oauth".into()], ..valid() }).is_err());
        assert!(validate_ssh_config(&SshConfig { jump_hosts: vec![JumpHost::default()], ..valid() }).is_err());
    }

    #[test]
    fn telnet_and_raw() {
        assert!(validate_telnet_config(&TelnetConfig { encoding: "utf-8".into(), ..Default::default() }).is_ok());
        assert!(validate_telnet_config(&TelnetConfig::default()).is_err());
        let raw = |presets| RawConfig { encoding: "utf-8".into(), payload_presets: presets, ..Default::default() };
        let preset = |n: &str, f: &str| PayloadPreset { name: n.into(), format: f.into(), ..Default::default() };
        assert!(validate_raw_config(&raw(vec![])).is_ok());
        assert!(validate_raw_config(&RawConfig::default()).is_err());
        assert!(validate_raw_config(&raw(vec![preset("ping", "binary")])).is_err());
        assert!(validate_raw_config(&raw(vec![preset("", "text")])).is_err());
    }

    #[test]
    fn irc() {
        let ok = IrcConfig { nick: "nexus".into(), auto_join_rooms: vec!["#rust".into(), "&local".into()], ..Default::default() };
        assert!(validate_irc_config(&ok).is_ok());
        for nick in ["", "1abc", "bad nick"] {
            assert!(validate_irc_config(&IrcConfig { nick: nick.into(), ..ok.clone() }).is_err(), "{nick:?}");
        }
        for rooms in [vec!["rust"], vec!["#"], vec!["#a,b"], vec!["#rust", "#RUST"], vec![" "]] {
            let c = IrcConfig { auto_join_rooms: rooms.iter().map(|r| r.to_string()).collect(), ..ok.clone() };
            assert!(validate_irc_config(&c).is_err(), "{rooms:?}");
        }
    }
}
