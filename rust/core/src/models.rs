//! Data model shared by storage, the session manager and the UI. Field names serialise exactly as
//! the Go structs did, so database JSON columns and `.link`/`.nexus` exports round-trip unchanged.

use chrono::{DateTime, FixedOffset, Local};
use serde::{Deserialize, Deserializer, Serialize};
use std::collections::BTreeMap;

pub const DEFAULT_SSH_PORT: u16 = 22;
pub const DEFAULT_TELNET_PORT: u16 = 23;
pub const DEFAULT_RAW_PORT: u16 = 23;
pub const DEFAULT_GITHUB_PORT: u16 = 443;
pub const DEFAULT_VNC_PORT: u16 = 5900;
pub const DEFAULT_IRC_PORT: u16 = 6667;
pub const DEFAULT_IRC_TLS_PORT: u16 = 6697;
pub const DEFAULT_CONNECT_TIMEOUT: i64 = 30;
pub const DEFAULT_SCROLLBACK: i64 = 10000;
pub const DEFAULT_FONT_SIZE: i64 = 14;
pub const DEFAULT_FONT_FAMILY: &str = "JetBrains Mono, Menlo, Monaco, Consolas, monospace";
pub const DEFAULT_CURSOR_STYLE: &str = "block";
pub const DEFAULT_TERMINAL_TYPE: &str = "xterm-256color";
pub const DEFAULT_KEEPALIVE_INTERVAL: i64 = 30;
pub const DEFAULT_KEEPALIVE_COUNT_MAX: i64 = 3;

pub type Time = DateTime<FixedOffset>;

pub fn now() -> Time {
    Local::now().fixed_offset()
}

/// Go's zero `time.Time`, which is what a missing timestamp decodes to.
pub fn zero_time() -> Time {
    DateTime::parse_from_rfc3339("0001-01-01T00:00:00Z").unwrap()
}

/// Go writes nil slices and maps as `null`; treat that as empty.
fn nd<'de, D: Deserializer<'de>, T: Default + Deserialize<'de>>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// For enum fields: an unknown value (e.g. written by a newer build) falls back to the default
/// for that field instead of failing the whole config, which would reset every other setting.
fn lenient<'de, D: Deserializer<'de>, T: Default + serde::de::DeserializeOwned>(d: D) -> Result<T, D::Error> {
    Ok(Option::<serde_json::Value>::deserialize(d)?.and_then(|v| T::deserialize(v).ok()).unwrap_or_default())
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Protocol {
    #[default]
    Ssh,
    Telnet,
    Raw,
    Github,
    Vnc,
    Irc,
    Serial,
    Local,
}

impl Protocol {
    pub const SAVED: [Protocol; 7] = [Protocol::Ssh, Protocol::Telnet, Protocol::Raw, Protocol::Github, Protocol::Vnc, Protocol::Irc, Protocol::Serial];
    pub fn id(self) -> &'static str {
        match self {
            Protocol::Ssh => "ssh",
            Protocol::Telnet => "telnet",
            Protocol::Raw => "raw",
            Protocol::Github => "github",
            Protocol::Vnc => "vnc",
            Protocol::Irc => "irc",
            Protocol::Serial => "serial",
            Protocol::Local => "local",
        }
    }
    pub fn from_id(s: &str) -> Option<Self> {
        [Protocol::Local].into_iter().chain(Self::SAVED).find(|p| p.id() == s)
    }
    pub fn label(self) -> &'static str {
        match self {
            Protocol::Ssh => "SSH",
            Protocol::Telnet => "Telnet",
            Protocol::Raw => "RAW TCP",
            Protocol::Github => "GitHub",
            Protocol::Vnc => "VNC",
            Protocol::Irc => "IRC",
            Protocol::Serial => "Serial",
            Protocol::Local => "Local",
        }
    }
    /// Protocols whose sessions render in a terminal view.
    pub fn is_terminal(self) -> bool {
        matches!(self, Protocol::Ssh | Protocol::Telnet | Protocol::Serial | Protocol::Local)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
pub enum HostKeyPolicy {
    #[serde(rename = "strict")]
    Strict,
    #[default]
    #[serde(rename = "ask")]
    Ask,
    #[serde(rename = "accept-new")]
    AcceptNew,
    #[serde(rename = "insecure")]
    Insecure,
}

impl HostKeyPolicy {
    pub const ALL: [HostKeyPolicy; 4] = [Self::Strict, Self::Ask, Self::AcceptNew, Self::Insecure];
    pub fn label(self) -> &'static str {
        match self {
            Self::Strict => "Strict (known hosts only)",
            Self::Ask => "Ask on new or changed keys",
            Self::AcceptNew => "Accept new, reject changed",
            Self::Insecure => "Insecure (accept anything)",
        }
    }
}

/// How a credential is obtained. Unknown strings (and the legacy "prompt") are kept as
/// ask-every-time, matching Go's normaliseSecretMode.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Default)]
pub enum SecretMode {
    #[default]
    #[serde(rename = "saved")]
    Saved,
    #[serde(rename = "session-only")]
    SessionOnly,
    #[serde(rename = "ask-every-time")]
    AskEveryTime,
}

impl<'de> Deserialize<'de> for SecretMode {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        Ok(match Option::<String>::deserialize(d)?.as_deref().unwrap_or("") {
            "saved" | "" => SecretMode::Saved,
            "session-only" => SecretMode::SessionOnly,
            _ => SecretMode::AskEveryTime,
        })
    }
}

impl SecretMode {
    pub const ALL: [SecretMode; 3] = [Self::Saved, Self::SessionOnly, Self::AskEveryTime];
    pub fn label(self) -> &'static str {
        match self {
            Self::Saved => "Save securely",
            Self::SessionOnly => "Remember for this session",
            Self::AskEveryTime => "Ask every time",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum LineEnding {
    Lf,
    Cr,
    #[default]
    Crlf,
    None,
}

impl LineEnding {
    pub const ALL: [LineEnding; 4] = [Self::Crlf, Self::Lf, Self::Cr, Self::None];
    pub fn bytes(self) -> &'static [u8] {
        match self {
            Self::Lf => b"\n",
            Self::Cr => b"\r",
            Self::Crlf => b"\r\n",
            Self::None => b"",
        }
    }
    pub fn label(self) -> &'static str {
        match self {
            Self::Lf => "LF (\\n)",
            Self::Cr => "CR (\\r)",
            Self::Crlf => "CRLF (\\r\\n)",
            Self::None => "None",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Profile {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub protocol: Protocol,
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub port: i64,
    #[serde(default)]
    pub username: String,
    #[serde(default, deserialize_with = "nd")]
    pub tags: Vec<String>,
    #[serde(default)]
    pub folder: String,
    #[serde(default)]
    pub environment: String,
    #[serde(default)]
    pub notes: String,
    #[serde(default)]
    pub favourite: bool,
    #[serde(default)]
    pub last_connected: Option<Time>,
    #[serde(default)]
    pub connect_timeout: i64,
    #[serde(default)]
    pub read_timeout: i64,
    #[serde(default = "zero_time")]
    pub created_at: Time,
    #[serde(default = "zero_time")]
    pub updated_at: Time,
    #[serde(default)]
    pub terminal_font_family: String,
    #[serde(default)]
    pub terminal_font_size: i64,
    #[serde(default)]
    pub terminal_scrollback: i64,
    #[serde(default)]
    pub terminal_cursor_style: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ssh: Option<SshConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub telnet: Option<TelnetConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub raw: Option<RawConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub github: Option<GitHubConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub vnc: Option<VncConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub irc: Option<IrcConfig>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub serial: Option<SerialConfig>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct SshConfig {
    #[serde(deserialize_with = "nd")]
    pub auth_methods: Vec<String>,
    pub secret_mode: SecretMode,
    pub private_key_path: String,
    pub key_secret_mode: SecretMode,
    #[serde(deserialize_with = "lenient")]
    pub host_key_policy: HostKeyPolicy,
    pub terminal_type: String,
    pub keepalive_interval: i64,
    pub keepalive_count_max: i64,
    pub compression: bool,
    pub agent_auth: bool,
    pub agent_forwarding: bool,
    pub auto_reconnect: bool,
    pub max_reconnect_tries: i64,
    #[serde(deserialize_with = "nd")]
    pub login_scripts: Vec<LoginScriptStep>,
    pub proxy_type: String,
    pub proxy_host: String,
    pub proxy_port: i64,
    pub proxy_username: String,
    #[serde(deserialize_with = "nd")]
    pub jump_hosts: Vec<JumpHost>,
    #[serde(deserialize_with = "nd")]
    pub port_forwards: Vec<PortForward>,
    pub startup_command: String,
    #[serde(deserialize_with = "nd")]
    pub env_vars: BTreeMap<String, String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct LoginScriptStep {
    #[serde(rename = "type")]
    pub kind: String,
    pub value: String,
    pub timeout: i64,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct JumpHost {
    pub host: String,
    pub port: i64,
    pub username: String,
    pub auth_method: String,
    pub secret_mode: SecretMode,
    pub private_key_path: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum ForwardDirection {
    #[default]
    Local,
    Remote,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct PortForward {
    pub id: String,
    #[serde(deserialize_with = "lenient")]
    pub direction: ForwardDirection,
    pub bind_host: String,
    pub bind_port: i64,
    pub dest_host: String,
    pub dest_port: i64,
    pub enabled: bool,
    pub label: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct TelnetConfig {
    pub local_echo: bool,
    #[serde(deserialize_with = "lenient")]
    pub line_ending: LineEnding,
    pub terminal_type: String,
    pub encoding: String,
    pub negotiation_debug: bool,
    pub line_mode: bool,
    pub auto_reconnect: bool,
    pub max_reconnect_tries: i64,
    #[serde(deserialize_with = "nd")]
    pub login_scripts: Vec<LoginScriptStep>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct RawConfig {
    pub auto_reconnect: bool,
    #[serde(deserialize_with = "lenient")]
    pub line_ending: LineEnding,
    pub encoding: String,
    pub show_timestamps: bool,
    pub show_direction: bool,
    #[serde(deserialize_with = "nd")]
    pub payload_presets: Vec<PayloadPreset>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct PayloadPreset {
    pub id: String,
    pub name: String,
    /// base64-encoded for binary safety
    pub payload: String,
    /// text, hex, escaped
    pub format: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct GitHubConfig {
    #[serde(rename = "baseURL")]
    pub base_url: String,
    pub secret_mode: SecretMode,
    pub refresh_interval_secs: i64,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct VncConfig {
    pub vnc_host: String,
    pub vnc_port: i64,
    pub secret_mode: SecretMode,
    pub view_only: bool,
    pub scale_to_fit: bool,
    pub quality: i64,
    pub shared_session: bool,
    pub ssh_tunnel: bool,
    pub ssh_host: String,
    pub ssh_port: i64,
    pub ssh_username: String,
    pub ssh_auth_method: String,
    pub ssh_secret_mode: SecretMode,
    #[serde(rename = "sshPrivateKeyPath")]
    pub ssh_private_key: String,
    pub ssh_agent_auth: bool,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct IrcConfig {
    pub nick: String,
    pub real_name: String,
    #[serde(rename = "useTLS")]
    pub use_tls: bool,
    pub server_password_required: bool,
    pub server_password_mode: SecretMode,
    pub nick_serv_identify: bool,
    pub nick_serv_account: String,
    pub nick_serv_mode: SecretMode,
    #[serde(deserialize_with = "nd")]
    pub auto_join_rooms: Vec<String>,
    pub ssh_tunnel: bool,
    pub ssh_host: String,
    pub ssh_port: i64,
    pub ssh_username: String,
    pub ssh_auth_method: String,
    pub ssh_secret_mode: SecretMode,
    #[serde(rename = "sshPrivateKeyPath")]
    pub ssh_private_key: String,
    pub ssh_agent_auth: bool,
    pub auto_reconnect: bool,
    pub max_reconnect_tries: i64,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Parity {
    #[default]
    None,
    Odd,
    Even,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum FlowControl {
    #[default]
    None,
    Xonxoff,
    Rtscts,
}

/// Serial line settings. The device path lives in `Profile::host`.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SerialConfig {
    pub baud_rate: u32,
    pub data_bits: u8,
    #[serde(deserialize_with = "lenient")]
    pub parity: Parity,
    pub stop_bits: u8,
    #[serde(deserialize_with = "lenient")]
    pub flow_control: FlowControl,
    #[serde(deserialize_with = "lenient")]
    pub line_ending: LineEnding,
    pub local_echo: bool,
    pub dtr_on_connect: bool,
    pub rts_on_connect: bool,
    pub encoding: String,
    pub auto_reconnect: bool,
    pub max_reconnect_tries: i64,
}

impl Default for SerialConfig {
    fn default() -> Self {
        Self {
            baud_rate: 115200,
            data_bits: 8,
            parity: Parity::None,
            stop_bits: 1,
            flow_control: FlowControl::None,
            line_ending: LineEnding::Cr,
            local_echo: false,
            dtr_on_connect: true,
            rts_on_connect: true,
            encoding: "utf-8".into(),
            auto_reconnect: true,
            max_reconnect_tries: 5,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase", default)]
pub struct GitHubRepo {
    pub full_name: String,
    pub name: String,
    pub owner: String,
    pub description: String,
    #[serde(rename = "htmlURL")]
    pub html_url: String,
    pub default_branch: String,
    pub latest_committer: String,
    pub latest_commit_at: String,
    pub private: bool,
    pub fork: bool,
    pub archived: bool,
    pub language: String,
    pub stars: i64,
    pub pushed_at: String,
    pub updated_at: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum SessionState {
    #[default]
    Connecting,
    Connected,
    Disconnected,
    Reconnecting,
    Error,
}

#[derive(Clone, Debug)]
pub struct SessionInfo {
    pub id: String,
    pub profile_id: String,
    pub profile_name: String,
    pub protocol: Protocol,
    pub host: String,
    pub port: i64,
    pub state: SessionState,
    pub tab_name: String,
    pub pinned: bool,
    pub connected_at: Time,
    pub error: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct KnownHost {
    pub id: String,
    pub host: String,
    pub port: i64,
    pub key_type: String,
    pub fingerprint: String,
    pub public_key: String,
    pub first_seen: Time,
    pub last_seen: Time,
    pub trusted: bool,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct AppPreferences {
    pub theme: String,
    pub terminal_theme: String,
    pub default_protocol: String,
    pub confirm_on_close: bool,
    pub confirm_on_disconnect: bool,
    pub show_status_bar: bool,
    pub sidebar_width: i64,
    pub default_font_family: String,
    pub default_font_size: i64,
    pub default_scrollback: i64,
    pub default_cursor_style: String,
    pub use_native_paste: bool,
    pub local_shell: String,
    /// Check the update manifest once at launch.
    pub check_for_updates: bool,
    /// Let remote hosts set the local clipboard (terminal OSC 52, VNC ServerCutText).
    pub allow_remote_clipboard: bool,
    /// Ask before pasting multi-line text into a terminal that isn't in bracketed-paste mode.
    pub confirm_multiline_paste: bool,
}

impl Default for AppPreferences {
    fn default() -> Self {
        Self {
            theme: "dark".into(),
            terminal_theme: "charcoal".into(),
            default_protocol: "ssh".into(),
            confirm_on_close: true,
            confirm_on_disconnect: true,
            show_status_bar: true,
            sidebar_width: 280,
            default_font_family: DEFAULT_FONT_FAMILY.into(),
            default_font_size: DEFAULT_FONT_SIZE,
            default_scrollback: DEFAULT_SCROLLBACK,
            default_cursor_style: DEFAULT_CURSOR_STYLE.into(),
            use_native_paste: false,
            local_shell: String::new(),
            check_for_updates: true,
            allow_remote_clipboard: true,
            confirm_multiline_paste: true,
        }
    }
}

impl AppPreferences {
    /// Fills zero values a stored preference blob may carry (Go's applyDefaults).
    pub fn apply_defaults(&mut self) {
        let d = Self::default();
        if self.theme.is_empty() {
            self.theme = d.theme;
        }
        if self.terminal_theme.is_empty() {
            self.terminal_theme = d.terminal_theme;
        }
        if self.default_protocol.is_empty() {
            self.default_protocol = d.default_protocol;
        }
        if self.sidebar_width == 0 {
            self.sidebar_width = d.sidebar_width;
        }
        if self.default_font_family.is_empty() {
            self.default_font_family = d.default_font_family;
        }
        if self.default_font_size == 0 {
            self.default_font_size = d.default_font_size;
        }
        if self.default_scrollback == 0 {
            self.default_scrollback = d.default_scrollback;
        }
        if self.default_cursor_style.is_empty() {
            self.default_cursor_style = d.default_cursor_style;
        }
    }
}

#[derive(Clone, Debug)]
pub struct AuditEntry {
    pub id: String,
    pub timestamp: Time,
    pub action: String,
    pub profile_id: String,
    pub session_id: String,
    pub details: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Snippet {
    pub id: String,
    pub name: String,
    pub command: String,
    pub description: String,
    pub folder: String,
    pub tags: Vec<String>,
    pub created_at: Time,
    pub updated_at: Time,
}

pub fn new_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

impl Profile {
    /// A profile with protocol defaults filled in (Go's NewProfile).
    pub fn new(protocol: Protocol) -> Profile {
        let now = now();
        let mut p = Profile {
            id: new_id(),
            display_name: String::new(),
            description: String::new(),
            protocol,
            host: String::new(),
            port: 0,
            username: String::new(),
            tags: vec![],
            folder: String::new(),
            environment: String::new(),
            notes: String::new(),
            favourite: false,
            last_connected: None,
            connect_timeout: DEFAULT_CONNECT_TIMEOUT,
            read_timeout: 0,
            created_at: now,
            updated_at: now,
            terminal_font_family: DEFAULT_FONT_FAMILY.into(),
            terminal_font_size: DEFAULT_FONT_SIZE,
            terminal_scrollback: DEFAULT_SCROLLBACK,
            terminal_cursor_style: DEFAULT_CURSOR_STYLE.into(),
            ssh: None,
            telnet: None,
            raw: None,
            github: None,
            vnc: None,
            irc: None,
            serial: None,
        };
        match protocol {
            Protocol::Ssh => {
                p.port = DEFAULT_SSH_PORT.into();
                p.ssh = Some(SshConfig::defaults());
            }
            Protocol::Telnet => {
                p.port = DEFAULT_TELNET_PORT.into();
                p.telnet = Some(TelnetConfig::defaults());
            }
            Protocol::Raw => {
                p.port = DEFAULT_RAW_PORT.into();
                p.raw = Some(RawConfig::defaults());
            }
            Protocol::Github => {
                p.port = DEFAULT_GITHUB_PORT.into();
                p.host = "github.com".into();
                p.github = Some(GitHubConfig::defaults());
            }
            Protocol::Vnc => {
                p.port = DEFAULT_VNC_PORT.into();
                p.vnc = Some(VncConfig::defaults());
            }
            Protocol::Irc => {
                p.port = DEFAULT_IRC_PORT.into();
                p.irc = Some(IrcConfig::defaults());
            }
            Protocol::Serial => p.serial = Some(SerialConfig::default()),
            Protocol::Local => {}
        }
        p
    }

    /// Fills missing fields the way Go's NormalizeProfileDefaults did.
    pub fn normalize(&mut self) {
        let d = Profile::new(self.protocol);
        if self.id.is_empty() {
            self.id = d.id.clone();
        }
        if self.connect_timeout == 0 {
            self.connect_timeout = DEFAULT_CONNECT_TIMEOUT;
        }
        if self.terminal_font_family.is_empty() {
            self.terminal_font_family = DEFAULT_FONT_FAMILY.into();
        }
        if self.terminal_font_size == 0 {
            self.terminal_font_size = DEFAULT_FONT_SIZE;
        }
        if self.terminal_scrollback == 0 {
            self.terminal_scrollback = DEFAULT_SCROLLBACK;
        }
        if self.terminal_cursor_style.is_empty() {
            self.terminal_cursor_style = DEFAULT_CURSOR_STYLE.into();
        }
        if self.created_at == zero_time() {
            self.created_at = d.created_at;
        }
        if self.updated_at == zero_time() {
            self.updated_at = d.updated_at;
        }
        if self.port == 0 && self.protocol != Protocol::Serial {
            self.port = d.port;
        }
        if self.protocol == Protocol::Github && self.host.is_empty() {
            self.host = d.host.clone();
        }
        if self.display_name.is_empty() {
            self.display_name = self.default_name();
        }
        match self.protocol {
            Protocol::Ssh => {
                let c = self.ssh.get_or_insert_with(SshConfig::defaults);
                if c.auth_methods.is_empty() {
                    c.auth_methods = SshConfig::defaults().auth_methods;
                }
                if c.terminal_type.is_empty() {
                    c.terminal_type = DEFAULT_TERMINAL_TYPE.into();
                }
                if c.keepalive_interval == 0 {
                    c.keepalive_interval = DEFAULT_KEEPALIVE_INTERVAL;
                }
                if c.keepalive_count_max == 0 {
                    c.keepalive_count_max = DEFAULT_KEEPALIVE_COUNT_MAX;
                }
            }
            Protocol::Telnet => {
                let c = self.telnet.get_or_insert_with(TelnetConfig::defaults);
                if c.terminal_type.is_empty() {
                    c.terminal_type = DEFAULT_TERMINAL_TYPE.into();
                }
                if c.encoding.is_empty() {
                    c.encoding = "utf-8".into();
                }
            }
            Protocol::Raw => {
                let c = self.raw.get_or_insert_with(RawConfig::defaults);
                if c.encoding.is_empty() {
                    c.encoding = "utf-8".into();
                }
                for p in &mut c.payload_presets {
                    if p.id.is_empty() {
                        p.id = new_id();
                    }
                    if p.format.is_empty() {
                        p.format = "text".into();
                    }
                }
            }
            Protocol::Github => {
                let c = self.github.get_or_insert_with(GitHubConfig::defaults);
                if c.base_url.is_empty() {
                    c.base_url = "https://api.github.com".into();
                }
                if c.refresh_interval_secs == 0 {
                    c.refresh_interval_secs = 300;
                }
            }
            Protocol::Vnc => {
                let c = self.vnc.get_or_insert_with(VncConfig::defaults);
                if c.vnc_port == 0 {
                    c.vnc_port = DEFAULT_VNC_PORT.into();
                }
                if c.quality < 0 {
                    c.quality = 6;
                }
                if c.ssh_port == 0 {
                    c.ssh_port = DEFAULT_SSH_PORT.into();
                }
                if c.ssh_auth_method.is_empty() {
                    c.ssh_auth_method = "publickey".into();
                }
            }
            Protocol::Irc => {
                let c = self.irc.get_or_insert_with(IrcConfig::defaults);
                if c.nick.is_empty() {
                    c.nick = "nexus".into();
                }
                if c.real_name.is_empty() {
                    c.real_name = "Nexus Terminal".into();
                }
                if c.ssh_port == 0 {
                    c.ssh_port = DEFAULT_SSH_PORT.into();
                }
                if c.ssh_auth_method.is_empty() {
                    c.ssh_auth_method = "publickey".into();
                }
            }
            Protocol::Serial => {
                self.serial.get_or_insert_with(SerialConfig::default);
            }
            Protocol::Local => {}
        }
    }

    fn default_name(&self) -> String {
        if !self.host.is_empty() {
            return self.host.clone();
        }
        if let Some(v) = &self.vnc
            && !v.vnc_host.is_empty()
        {
            return v.vnc_host.clone();
        }
        match self.protocol {
            Protocol::Github => "GitHub".into(),
            p => p.label().into(),
        }
    }

    /// The default tab name (Go's createTabName).
    pub fn tab_name(&self) -> String {
        if !self.display_name.is_empty() {
            self.display_name.clone()
        } else if !self.username.is_empty() {
            format!("{}@{}", self.username, self.host)
        } else {
            self.host.clone()
        }
    }

    pub fn auto_reconnect(&self) -> bool {
        match self.protocol {
            Protocol::Ssh => self.ssh.as_ref().is_some_and(|c| c.auto_reconnect),
            Protocol::Telnet => self.telnet.as_ref().is_some_and(|c| c.auto_reconnect),
            Protocol::Raw => self.raw.as_ref().is_some_and(|c| c.auto_reconnect),
            Protocol::Irc => self.irc.as_ref().is_some_and(|c| c.auto_reconnect),
            Protocol::Serial => self.serial.as_ref().is_some_and(|c| c.auto_reconnect),
            _ => false,
        }
    }

    pub fn max_reconnect_tries(&self) -> i64 {
        let n = match self.protocol {
            Protocol::Ssh => self.ssh.as_ref().map_or(0, |c| c.max_reconnect_tries),
            Protocol::Telnet => self.telnet.as_ref().map_or(0, |c| c.max_reconnect_tries),
            Protocol::Irc => self.irc.as_ref().map_or(0, |c| c.max_reconnect_tries),
            Protocol::Serial => self.serial.as_ref().map_or(0, |c| c.max_reconnect_tries),
            _ => 0,
        };
        if n > 0 { n } else { 5 }
    }
}

impl SshConfig {
    pub fn defaults() -> Self {
        SshConfig {
            auth_methods: vec!["publickey".into(), "password".into(), "keyboard-interactive".into()],
            terminal_type: DEFAULT_TERMINAL_TYPE.into(),
            keepalive_interval: DEFAULT_KEEPALIVE_INTERVAL,
            keepalive_count_max: DEFAULT_KEEPALIVE_COUNT_MAX,
            ..Default::default()
        }
    }
}

impl TelnetConfig {
    pub fn defaults() -> Self {
        TelnetConfig { terminal_type: DEFAULT_TERMINAL_TYPE.into(), encoding: "utf-8".into(), ..Default::default() }
    }
}

impl RawConfig {
    pub fn defaults() -> Self {
        RawConfig { encoding: "utf-8".into(), ..Default::default() }
    }
}

impl GitHubConfig {
    pub fn defaults() -> Self {
        GitHubConfig { base_url: "https://api.github.com".into(), refresh_interval_secs: 300, ..Default::default() }
    }
}

impl VncConfig {
    pub fn defaults() -> Self {
        VncConfig {
            vnc_port: DEFAULT_VNC_PORT.into(),
            scale_to_fit: true,
            shared_session: true,
            quality: 6,
            ssh_port: DEFAULT_SSH_PORT.into(),
            ssh_auth_method: "publickey".into(),
            ..Default::default()
        }
    }
}

impl IrcConfig {
    pub fn defaults() -> Self {
        IrcConfig {
            nick: "nexus".into(),
            real_name: "Nexus Terminal".into(),
            server_password_mode: SecretMode::AskEveryTime,
            ssh_port: DEFAULT_SSH_PORT.into(),
            ssh_auth_method: "publickey".into(),
            auto_reconnect: true,
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn profiles_never_carry_proxy_passwords_or_totp_seeds() {
        let c: SshConfig = serde_json::from_str(r#"{"proxyPassword":"p","totpSecret":"JBSWY3DPEHPK3PXP","proxyUsername":"u"}"#).unwrap();
        let out = serde_json::to_string(&c).unwrap();
        assert!(!out.contains("proxyPassword") && !out.contains("totpSecret") && out.contains("\"proxyUsername\":\"u\""), "{out}");
    }

    #[test]
    fn unknown_enum_values_fall_back_per_field() {
        let c: SshConfig = serde_json::from_str(r#"{"hostKeyPolicy":"from-the-future","keepaliveInterval":42,"agentForwarding":true}"#).unwrap();
        assert_eq!((c.host_key_policy, c.keepalive_interval, c.agent_forwarding), (HostKeyPolicy::Ask, 42, true));
        let c: SshConfig = serde_json::from_str(r#"{"hostKeyPolicy":"strict"}"#).unwrap();
        assert_eq!(c.host_key_policy, HostKeyPolicy::Strict);
        let t: TelnetConfig = serde_json::from_str(r#"{"lineEnding":"weird","localEcho":true}"#).unwrap();
        assert_eq!((t.line_ending, t.local_echo), (LineEnding::default(), true));
    }
}
