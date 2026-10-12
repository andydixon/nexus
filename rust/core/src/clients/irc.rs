//! IRC client: registration, PING/PONG, channel/user tracking, CTCP ACTION, NickServ identify,
//! auto-join and the composer's /commands, reporting through typed `Event`s.
//!
//! One I/O thread owns the transport (TCP, or a caller-supplied stream such as an SSH tunnel,
//! optionally wrapped in TLS). It alternates between short reads and draining the outgoing
//! queue, so the transport never has to be split.

use parking_lot::{Mutex, MutexGuard};
use std::collections::HashMap;
use std::io::{ErrorKind, Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::sync::Arc;
use std::sync::mpsc::{self, Receiver, Sender};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};

use super::{CloseFn, ErrorFn};

pub const SERVER_TARGET: &str = "server";
const MAX_ROOM_LIST_ENTRIES: usize = 2000;
const QUIT_MESSAGE: &str = "Nexus Terminal disconnected";
const READ_POLL: Duration = Duration::from_millis(50);
const PING_DELAY: Duration = Duration::from_secs(20);
const PING_TIMEOUT: Duration = Duration::from_secs(60);
const PING_GRACE: Duration = Duration::from_secs(30);
const MAX_LINE_BYTES: usize = 1 << 20;
/// Users tracked per channel; a hostile server can't grow the client past this.
const MAX_CHANNEL_USERS: usize = 10_000;

/// A bidirectional byte stream. Reads must not block indefinitely: they should return
/// `WouldBlock` or `TimedOut` after a short interval (tens of milliseconds) so the client's
/// I/O thread can interleave writes.
pub trait ReadWrite: Read + Write + Send {}
impl<T: Read + Write + Send> ReadWrite for T {}

pub type Connector = Arc<dyn Fn() -> crate::Result<Box<dyn ReadWrite>> + Send + Sync>;
pub type EventFn = Arc<dyn Fn(Event) + Send + Sync>;
pub type RoomsChangedFn = Arc<dyn Fn(Vec<String>) + Send + Sync>;

#[derive(Clone)]
pub struct Config {
    pub server: String,
    pub port: u16,
    pub nick: String,
    pub user: String,
    pub real_name: String,
    pub use_tls: bool,
    pub server_pass: String,
    pub auto_join_rooms: Vec<String>,

    pub nickserv_identify: bool,
    pub nickserv_account: String,
    pub nickserv_password: String,

    /// Provides the transport instead of a direct TCP connect (e.g. an SSH tunnel). TLS, when
    /// enabled, is layered on top of it.
    pub connector: Option<Connector>,

    pub connect_timeout: Duration,
    pub on_event: EventFn,
    pub on_error: ErrorFn,
    pub on_close: CloseFn,
    pub on_rooms_changed: RoomsChangedFn,
}

impl Default for Config {
    fn default() -> Config {
        Config {
            server: String::new(),
            port: 6667,
            nick: String::new(),
            user: String::new(),
            real_name: String::new(),
            use_tls: false,
            server_pass: String::new(),
            auto_join_rooms: Vec::new(),
            nickserv_identify: false,
            nickserv_account: String::new(),
            nickserv_password: String::new(),
            connector: None,
            connect_timeout: Duration::ZERO,
            on_event: Arc::new(|_| {}),
            on_error: Arc::new(|_| {}),
            on_close: Arc::new(|| {}),
            on_rooms_changed: Arc::new(|_| {}),
        }
    }
}

/// A typed IRC event, serialised for the UI exactly as Go's `ircclient.Event`.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Event {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub target: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub from: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub command: String,
    pub timestamp: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub rooms: Vec<RoomInfo>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub users: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub topic: String,
}

/// A joined or listed IRC room.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RoomInfo {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub topic: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub users: i64,
}

fn is_zero(n: &i64) -> bool {
    *n == 0
}

enum Out {
    Line(String),
    Quit(String),
}

struct Channel {
    name: String,
    topic: String,
    users: Vec<String>,
}

struct State {
    nick: String,
    connected: bool,
    closing: bool,
    list_rooms: Vec<RoomInfo>,
    room_names: Vec<String>,
    /// Keyed by RFC 1459 casefolded name.
    channels: HashMap<String, Channel>,
    ready: Option<Sender<Result<(), String>>>,
    last_pong: Instant,
}

pub struct Client {
    cfg: Config,
    state: Mutex<State>,
    out: Mutex<Option<Sender<Out>>>,
}

/// A parsed server line.
struct Message {
    source: String,
    command: String,
    params: Vec<String>,
}

impl Message {
    fn last(&self) -> &str {
        self.params.last().map(String::as_str).unwrap_or("")
    }
    fn param(&self, i: usize) -> &str {
        self.params.get(i).map(String::as_str).unwrap_or("")
    }
}

impl Client {
    pub fn new(mut cfg: Config) -> Arc<Client> {
        if cfg.connect_timeout.is_zero() {
            cfg.connect_timeout = Duration::from_secs(30);
        }
        if cfg.user.is_empty() {
            cfg.user = cfg.nick.clone();
        }
        if cfg.real_name.is_empty() {
            cfg.real_name = cfg.nick.clone();
        }
        let nick = cfg.nick.clone();
        Arc::new(Client {
            cfg,
            state: Mutex::new(State {
                nick,
                connected: false,
                closing: false,
                list_rooms: Vec::new(),
                room_names: Vec::new(),
                channels: HashMap::new(),
                ready: None,
                last_pong: Instant::now(),
            }),
            out: Mutex::new(None),
        })
    }

    fn st(&self) -> MutexGuard<'_, State> {
        self.state.lock()
    }

    /// Connects and blocks until registration succeeds (RPL_WELCOME), fails, or times out.
    pub fn connect(self: &Arc<Self>) -> crate::Result<()> {
        let (ready_tx, ready_rx) = mpsc::channel();
        let (out_tx, out_rx) = mpsc::channel();
        self.st().ready = Some(ready_tx);
        *self.out.lock() = Some(out_tx);

        let client = self.clone();
        std::thread::spawn(move || client.io_thread(out_rx));

        let timeout = self.cfg.connect_timeout;
        match ready_rx.recv_timeout(timeout) {
            Ok(Ok(())) => Ok(()),
            Ok(Err(e)) => Err(e.into()),
            Err(_) => {
                self.close();
                Err(format!("IRC connection timed out after {timeout:?}").into())
            }
        }
    }

    /// Sends QUIT and disconnects. Idempotent; `on_close` fires from the I/O thread.
    pub fn close(&self) {
        {
            let mut st = self.st();
            if st.closing {
                return;
            }
            st.closing = true;
        }
        self.queue(Out::Quit(QUIT_MESSAGE.to_string()));
    }

    fn queue(&self, out: Out) -> bool {
        let guard = self.out.lock();
        guard.as_ref().is_some_and(|tx| tx.send(out).is_ok())
    }

    /// Sends one IRC line (CR/LF are stripped).
    fn send_raw(&self, line: &str) -> crate::Result<()> {
        let line: String = line.chars().filter(|c| *c != '\r' && *c != '\n').collect();
        if self.queue(Out::Line(line)) { Ok(()) } else { Err("not connected".into()) }
    }

    fn send(&self, command: &str, params: &[&str]) -> crate::Result<()> {
        self.send_raw(&encode(command, params))
    }

    /// PRIVMSG/NOTICE, split so each line fits the 512-byte limit.
    fn send_text(&self, command: &str, target: &str, text: &str) -> crate::Result<()> {
        // ponytail: fixed prefix allowance (nick!user@host) like girc's default; no ISUPPORT tuning.
        let max = 510usize.saturating_sub(115 + command.len() + target.len() + 3).max(64);
        let mut rest = text;
        while !rest.is_empty() {
            let mut cut = rest.len().min(max);
            while !rest.is_char_boundary(cut) {
                cut -= 1;
            }
            self.send(command, &[target, &rest[..cut]])?;
            rest = &rest[cut..];
        }
        Ok(())
    }

    fn nick(&self) -> String {
        self.st().nick.clone()
    }

    /// Parses a composer line: plain text goes to `target`, `/commands` are interpreted.
    pub fn handle_input(&self, target: &str, input: &str) -> crate::Result<()> {
        let text = input.trim();
        if text.is_empty() {
            return Ok(());
        }
        let target = if target.is_empty() { SERVER_TARGET } else { target };

        if !text.starts_with('/') || text == "/" {
            if target == SERVER_TARGET {
                return Err("select a room or use /msg <nick> <message>".into());
            }
            self.send_text("PRIVMSG", target, text)?;
            self.emit_local_message("message", target, text);
            return Ok(());
        }

        let command_line = text[1..].trim();
        let Some(first) = command_line.split_whitespace().next() else { return Ok(()) };
        let cmd = first.to_lowercase();
        let args = command_line[first.len()..].trim();

        match cmd.as_str() {
            "me" | "action" => {
                if target == SERVER_TARGET {
                    return Err("/me requires an active room or private tab".into());
                }
                if args.is_empty() {
                    return Err("/me requires an action".into());
                }
                self.send("PRIVMSG", &[target, &format!("\x01ACTION {args}\x01")])?;
                self.emit_local_message("action", target, args);
            }
            "msg" | "query" => {
                let (recipient, body) = split_first_arg(args);
                if recipient.is_empty() || body.is_empty() {
                    return Err("/msg requires a target and message".into());
                }
                self.send_text("PRIVMSG", recipient, body)?;
                self.emit_local_message("message", recipient, body);
            }
            "notice" => {
                let (recipient, body) = split_first_arg(args);
                if recipient.is_empty() || body.is_empty() {
                    return Err("/notice requires a target and message".into());
                }
                self.send_text("NOTICE", recipient, body)?;
                self.emit_local_message("notice", recipient, body);
            }
            "join" | "j" => {
                let (room, key) = split_first_arg(args);
                if room.is_empty() {
                    return Err("/join requires a room".into());
                }
                if key.is_empty() {
                    self.send("JOIN", &[room])?;
                } else {
                    self.send("JOIN", &[room, key])?;
                }
            }
            "part" | "leave" => {
                let (room, msg) = split_first_arg(args);
                let room = if room.is_empty() { target } else { room };
                if room == SERVER_TARGET {
                    return Err("/part requires a room".into());
                }
                if msg.is_empty() {
                    self.send("PART", &[room])?;
                } else {
                    self.send("PART", &[room, msg])?;
                }
            }
            "quit" => {
                let msg = if args.is_empty() { QUIT_MESSAGE } else { args };
                self.queue(Out::Quit(msg.to_string()));
            }
            "nick" => {
                let Some(nick) = args.split_whitespace().next() else {
                    return Err("/nick requires a nickname".into());
                };
                self.send("NICK", &[nick])?;
            }
            "topic" => {
                let (room, topic) = split_first_arg(args);
                let room = if room.is_empty() { target } else { room };
                if room == SERVER_TARGET {
                    return Err("/topic requires a room".into());
                }
                if topic.is_empty() {
                    self.send("TOPIC", &[room])?;
                } else {
                    self.send("TOPIC", &[room, topic])?;
                }
            }
            "list" => {
                if args.is_empty() {
                    self.send("LIST", &[])?;
                } else {
                    self.send("LIST", &[&args.split_whitespace().collect::<Vec<_>>().join(",")])?;
                }
            }
            "names" => {
                let room = if args.is_empty() && target != SERVER_TARGET { target } else { args };
                if room.is_empty() {
                    return self.send_raw("NAMES");
                }
                return self.send_raw(&format!("NAMES {room}"));
            }
            "whois" => {
                if args.is_empty() {
                    return Err("/whois requires a nickname".into());
                }
                for nick in args.split_whitespace() {
                    self.send("WHOIS", &[nick])?;
                }
            }
            "who" => {
                let args = if args.is_empty() {
                    if target == SERVER_TARGET {
                        return Err("/who requires a target".into());
                    }
                    target
                } else {
                    args
                };
                for who in args.split_whitespace() {
                    self.send("WHO", &[who, "%tcuhnr,2"])?;
                }
            }
            "raw" | "quote" => {
                if args.is_empty() {
                    return Err("/raw requires an IRC command".into());
                }
                return self.send_raw(args);
            }
            "help" => self.emit(Event {
                kind: "system".into(),
                target: target.into(),
                text: "Commands: /join, /part, /msg, /notice, /me, /nick, /topic, /list, /names, /whois, /who, /raw, /quit".into(),
                ..Default::default()
            }),
            _ => {
                let mut raw = first.to_uppercase();
                if !args.is_empty() {
                    raw.push(' ');
                    raw.push_str(args);
                }
                return self.send_raw(&raw);
            }
        }
        Ok(())
    }

    pub fn join(&self, room: &str) -> crate::Result<()> {
        let room = room.trim();
        if room.is_empty() {
            return Err("room is required".into());
        }
        self.send("JOIN", &[room])
    }

    pub fn part(&self, room: &str) -> crate::Result<()> {
        let room = room.trim();
        if room.is_empty() {
            return Err("room is required".into());
        }
        self.send("PART", &[room])
    }

    /// Requests the server's room list; results arrive as a `room-list` event.
    pub fn list_rooms(&self) -> crate::Result<()> {
        self.st().list_rooms.clear();
        self.send("LIST", &[])
    }

    /// Currently joined rooms, sorted case-insensitively.
    pub fn rooms(&self) -> Vec<RoomInfo> {
        let st = self.st();
        let mut rooms: Vec<RoomInfo> = st
            .room_names
            .iter()
            .map(|name| match st.channels.get(&fold(name)) {
                Some(ch) => RoomInfo { name: ch.name.clone(), topic: ch.topic.clone(), users: ch.users.len() as i64 },
                None => RoomInfo { name: name.clone(), ..Default::default() },
            })
            .collect();
        rooms.sort_by_key(|r| r.name.to_lowercase());
        rooms
    }

    /// Known users in a room, sorted ignoring mode prefixes.
    pub fn users(&self, room: &str) -> Vec<String> {
        let st = self.st();
        let Some(ch) = st.channels.get(&fold(room)) else { return Vec::new() };
        let mut users = ch.users.clone();
        users.sort_by_key(|u| u.trim_start_matches(['~', '&', '@', '%', '+']).to_lowercase());
        users
    }

    // ---- I/O thread ----

    fn io_thread(self: Arc<Self>, out_rx: Receiver<Out>) {
        let result = self.open_transport().and_then(|stream| self.run(stream, &out_rx));
        *self.out.lock() = None;
        let (was_closing, was_connected, ready) = {
            let mut st = self.st();
            let r = (st.closing, st.connected, st.ready.take());
            st.connected = false;
            r
        };
        if let Err(e) = result
            && !was_closing
        {
            let msg = e.to_string();
            self.emit(Event { kind: "error".into(), text: msg.clone(), ..Default::default() });
            (self.cfg.on_error)(msg.clone());
            if let Some(ready) = ready {
                let _ = ready.send(Err(msg));
            }
        }
        if was_connected {
            (self.cfg.on_close)();
        }
    }

    fn open_transport(&self) -> crate::Result<Box<dyn ReadWrite>> {
        let stream: Box<dyn ReadWrite> = match &self.cfg.connector {
            Some(connector) => connector()?,
            None => {
                let addr = format!("{}:{}", self.cfg.server, self.cfg.port);
                let tcp = dial(&self.cfg.server, self.cfg.port, self.cfg.connect_timeout).map_err(|e| format!("dial tcp {addr}: {e}"))?;
                tcp.set_read_timeout(Some(READ_POLL))?;
                tcp.set_write_timeout(Some(self.cfg.connect_timeout))?;
                let _ = tcp.set_nodelay(true);
                Box::new(tcp)
            }
        };
        if !self.cfg.use_tls {
            return Ok(stream);
        }
        self.tls_wrap(stream).map_err(|e| format!("IRC TLS handshake failed: {e}").into())
    }

    fn tls_wrap(&self, sock: Box<dyn ReadWrite>) -> crate::Result<Box<dyn ReadWrite>> {
        let mut roots = rustls::RootCertStore::empty();
        roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
        let config = rustls::ClientConfig::builder_with_provider(Arc::new(rustls::crypto::ring::default_provider()))
            .with_safe_default_protocol_versions()?
            .with_root_certificates(roots)
            .with_no_client_auth();
        let name = rustls::pki_types::ServerName::try_from(self.cfg.server.clone())?;
        let conn = rustls::ClientConnection::new(Arc::new(config), name)?;
        let mut tls = rustls::StreamOwned::new(conn, sock);
        let deadline = Instant::now() + self.cfg.connect_timeout;
        while tls.conn.is_handshaking() {
            match tls.conn.complete_io(&mut tls.sock) {
                Ok(_) => {}
                Err(e) if is_poll_timeout(&e) && Instant::now() < deadline => {}
                Err(e) => return Err(e.into()),
            }
        }
        Ok(Box::new(tls))
    }

    /// Registers, then pumps reads, the outgoing queue and keepalive pings until the connection
    /// ends. `Ok` means a deliberate QUIT.
    fn run(&self, mut stream: Box<dyn ReadWrite>, out_rx: &Receiver<Out>) -> crate::Result<()> {
        if self.st().closing {
            return Ok(());
        }
        let write = |stream: &mut Box<dyn ReadWrite>, line: &str| -> crate::Result<()> {
            stream.write_all(line.as_bytes())?;
            stream.write_all(b"\r\n")?;
            Ok(stream.flush()?)
        };
        if !self.cfg.server_pass.is_empty() {
            write(&mut stream, &encode("PASS", &[&self.cfg.server_pass]))?;
        }
        write(&mut stream, &encode("NICK", &[&self.cfg.nick]))?;
        write(&mut stream, &encode("USER", &[&self.cfg.user, "*", "*", &self.cfg.real_name]))?;

        let started = Instant::now();
        let mut last_ping = Instant::now();
        let mut ping_sent = false;
        self.st().last_pong = Instant::now();
        let mut buf = Vec::new();
        let mut chunk = [0u8; 8192];
        loop {
            while let Ok(out) = out_rx.try_recv() {
                match out {
                    Out::Line(line) => write(&mut stream, &line)?,
                    Out::Quit(msg) => {
                        let _ = write(&mut stream, &encode("QUIT", &[&msg]));
                        return Ok(());
                    }
                }
            }

            if started.elapsed() >= PING_GRACE && last_ping.elapsed() >= PING_DELAY {
                if ping_sent && self.st().last_pong.elapsed() > PING_DELAY + PING_TIMEOUT {
                    return Err("timed out waiting for a requested PING response".into());
                }
                let id = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_nanos();
                write(&mut stream, &encode("PING", &[&id.to_string()]))?;
                last_ping = Instant::now();
                ping_sent = true;
            }

            match stream.read(&mut chunk) {
                Ok(0) => return Err("connection closed by server".into()),
                Ok(n) => buf.extend_from_slice(&chunk[..n]),
                Err(e) if is_poll_timeout(&e) || e.kind() == ErrorKind::Interrupted => continue,
                Err(e) => return Err(e.into()),
            }
            while let Some(pos) = buf.iter().position(|&b| b == b'\n') {
                let raw: Vec<u8> = buf.drain(..=pos).collect();
                let line = String::from_utf8_lossy(&raw);
                if let Some(msg) = parse_line(line.trim_end_matches(['\r', '\n'])) {
                    self.handle_message(&msg);
                }
            }
            if buf.len() > MAX_LINE_BYTES {
                return Err("server line too long".into());
            }
        }
    }

    // ---- server message handling (girc's state tracking, then Link's handlers) ----

    fn is_me(&self, nick: &str) -> bool {
        fold(nick) == fold(&self.st().nick)
    }

    /// Whether we're in `room`: state is only kept for channels the client itself joined, so a
    /// server can't grow it with JOINs for rooms we never asked for.
    fn tracks(&self, room: &str) -> bool {
        self.st().channels.contains_key(&fold(room))
    }

    fn handle_message(&self, m: &Message) {
        match m.command.as_str() {
            "001" => self.handle_welcome(m),
            "433" | "436" | "437" => {
                let mut st = self.st();
                if !st.connected {
                    st.nick.push('_');
                    let nick = st.nick.clone();
                    drop(st);
                    let _ = self.send("NICK", &[&nick]);
                }
            }
            "PING" => {
                let _ = self.send("PONG", &[m.last()]);
            }
            "PONG" => self.st().last_pong = Instant::now(),
            "PRIVMSG" => self.handle_privmsg(m),
            "NOTICE" => self.handle_notice(m),
            "JOIN" => self.handle_join(m),
            "PART" => self.handle_part(m),
            "KICK" => self.handle_kick(m),
            "QUIT" => self.handle_quit(m),
            "NICK" => self.handle_nick(m),
            "TOPIC" | "332" => self.handle_topic(m),
            "353" => self.handle_names(m),
            "366" => {
                if m.params.len() >= 2 {
                    self.emit_users(m.param(1));
                }
            }
            "322" => {
                if m.params.len() >= 4 {
                    let room = RoomInfo { name: m.param(1).into(), users: m.param(2).parse().unwrap_or(0), topic: m.param(3).into() };
                    let mut st = self.st();
                    if st.list_rooms.len() < MAX_ROOM_LIST_ENTRIES {
                        st.list_rooms.push(room);
                    }
                }
            }
            "323" => {
                let mut rooms = std::mem::take(&mut self.st().list_rooms);
                rooms.sort_by(|a, b| b.users.cmp(&a.users).then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase())));
                self.emit(Event { kind: "room-list".into(), rooms, ..Default::default() });
            }
            "ERROR" => self.emit(Event { kind: "error".into(), text: m.last().into(), ..Default::default() }),
            _ => {}
        }
    }

    fn handle_welcome(&self, m: &Message) {
        let ready = {
            let mut st = self.st();
            if !m.params.is_empty() {
                st.nick = m.param(0).to_string();
            }
            st.connected = true;
            st.ready.take()
        };
        let host = if self.cfg.server.contains(':') { format!("[{}]", self.cfg.server) } else { self.cfg.server.clone() };
        self.emit(Event { kind: "system".into(), text: format!("Connected to {host}:{}", self.cfg.port), ..Default::default() });
        self.identify_with_nickserv();
        if !self.cfg.auto_join_rooms.is_empty() {
            let _ = self.send("JOIN", &[&self.cfg.auto_join_rooms.join(",")]);
        }
        self.emit_rooms();
        if let Some(ready) = ready {
            let _ = ready.send(Ok(()));
        }
    }

    fn identify_with_nickserv(&self) {
        if !self.cfg.nickserv_identify || self.cfg.nickserv_password.is_empty() {
            return;
        }
        let body = if self.cfg.nickserv_account.is_empty() {
            format!("IDENTIFY {}", self.cfg.nickserv_password)
        } else {
            format!("IDENTIFY {} {}", self.cfg.nickserv_account, self.cfg.nickserv_password)
        };
        let _ = self.send("PRIVMSG", &["NickServ", &body]);
        self.emit(Event { kind: "system".into(), text: "Sent NickServ identify request".into(), ..Default::default() });
    }

    fn handle_privmsg(&self, m: &Message) {
        if m.params.len() < 2 || m.source.is_empty() {
            return;
        }
        let mut target = m.param(0).to_string();
        if self.is_me(&target) {
            target = m.source.clone();
        }
        let mut kind = "message";
        let mut text = m.last().to_string();
        if let Some((ctcp, args)) = decode_ctcp(m) {
            match ctcp.as_str() {
                "ACTION" => {
                    kind = "action";
                    text = args;
                }
                "VERSION" => {
                    let _ = self.send("NOTICE", &[&m.source, "\x01VERSION Nexus Terminal\x01"]);
                }
                "PING" => {
                    let body = if args.is_empty() { "\x01PING\x01".to_string() } else { format!("\x01PING {args}\x01") };
                    let _ = self.send("NOTICE", &[&m.source, &body]);
                }
                _ => {}
            }
        }
        self.emit(Event { kind: kind.into(), target, from: m.source.clone(), text, ..Default::default() });
    }

    fn handle_notice(&self, m: &Message) {
        let mut target = SERVER_TARGET.to_string();
        if !m.params.is_empty() {
            target = m.param(0).to_string();
            if target == "*" {
                target = SERVER_TARGET.into();
            }
            if self.is_me(&target) && !m.source.is_empty() {
                target = m.source.clone();
            }
        }
        self.emit(Event { kind: "notice".into(), target, from: m.source.clone(), text: m.last().into(), ..Default::default() });
    }

    fn handle_join(&self, m: &Message) {
        let room = m.param(0).to_string();
        if room.is_empty() {
            return;
        }
        let from = m.source.clone();
        let me = self.is_me(&from);
        if !me && !self.tracks(&room) {
            return;
        }
        if !from.is_empty() {
            let mut st = self.st();
            let ch = st.channels.entry(fold(&room)).or_insert_with(|| Channel { name: room.clone(), topic: String::new(), users: Vec::new() });
            add_user(ch, &from);
        }
        if me {
            self.remember_room_membership(&room, true);
        }
        self.emit(Event { kind: "join".into(), target: room.clone(), from: from.clone(), text: format!("{from} joined {room}"), ..Default::default() });
        self.emit_rooms();
        self.emit_users(&room);
    }

    fn handle_part(&self, m: &Message) {
        let room = m.param(0).to_string();
        if room.is_empty() {
            return;
        }
        let from = m.source.clone();
        let me = self.is_me(&from);
        if !self.tracks(&room) {
            return;
        }
        if !from.is_empty() {
            let mut st = self.st();
            if me {
                st.channels.remove(&fold(&room));
            } else if let Some(ch) = st.channels.get_mut(&fold(&room)) {
                remove_user(ch, &from);
            }
        }
        let message = if m.params.len() > 1 { m.last().trim().to_string() } else { String::new() };
        if me {
            self.remember_room_membership(&room, false);
        }
        self.emit(Event { kind: "part".into(), target: room.clone(), from, text: message, ..Default::default() });
        self.emit_rooms();
        self.emit_users(&room);
    }

    fn handle_kick(&self, m: &Message) {
        if m.params.len() < 2 {
            return;
        }
        let (room, kicked) = (m.param(0).to_string(), m.param(1).to_string());
        let me = self.is_me(&kicked);
        if !self.tracks(&room) {
            return;
        }
        {
            let mut st = self.st();
            if me {
                st.channels.remove(&fold(&room));
            } else if let Some(ch) = st.channels.get_mut(&fold(&room)) {
                remove_user(ch, &kicked);
            }
        }
        if me {
            self.remember_room_membership(&room, false);
        }
        self.emit(Event {
            kind: "kick".into(),
            target: room.clone(),
            from: m.source.clone(),
            command: kicked,
            text: m.last().trim().into(),
            ..Default::default()
        });
        self.emit_rooms();
        self.emit_users(&room);
    }

    fn handle_quit(&self, m: &Message) {
        if !m.source.is_empty() && !self.is_me(&m.source) {
            for ch in self.st().channels.values_mut() {
                remove_user(ch, &m.source);
            }
        }
        self.emit(Event { kind: "quit".into(), from: m.source.clone(), text: m.last().trim().into(), ..Default::default() });
        self.emit_users_for_all();
    }

    fn handle_nick(&self, m: &Message) {
        let new_nick = m.last().to_string();
        if !m.source.is_empty() && !m.params.is_empty() {
            let mut st = self.st();
            if fold(&m.source) == fold(&st.nick) {
                st.nick = new_nick.clone();
            }
            for ch in st.channels.values_mut() {
                if remove_user(ch, &m.source) {
                    add_user(ch, &new_nick);
                }
            }
        }
        self.emit(Event { kind: "nick".into(), from: m.source.clone(), text: new_nick, ..Default::default() });
        self.emit_users_for_all();
    }

    fn handle_topic(&self, m: &Message) {
        let (room, topic) = if m.command == "332" && m.params.len() >= 3 {
            (m.param(1), m.param(2))
        } else if m.params.len() >= 2 {
            (m.param(0), m.param(1))
        } else {
            return;
        };
        if room.is_empty() {
            return;
        }
        if let Some(ch) = self.st().channels.get_mut(&fold(room)) {
            ch.topic = topic.to_string();
        }
        self.emit(Event { kind: "topic".into(), target: room.into(), from: m.source.clone(), text: topic.into(), topic: topic.into(), ..Default::default() });
        self.emit_rooms();
    }

    fn handle_names(&self, m: &Message) {
        if m.params.len() < 4 || !self.tracks(m.param(2)) {
            return;
        }
        {
            let mut st = self.st();
            if let Some(ch) = st.channels.get_mut(&fold(m.param(2))) {
                for part in m.last().split(' ') {
                    let nick = part.trim_start_matches(['~', '&', '@', '%', '+']);
                    let nick = if nick.contains('@') { source_name(nick) } else { nick };
                    if is_valid_nick(nick) {
                        add_user(ch, nick);
                    }
                }
            }
        }
        self.emit(Event {
            kind: "users".into(),
            target: m.param(2).into(),
            users: m.param(3).split_whitespace().map(String::from).collect(),
            ..Default::default()
        });
    }

    // ---- emitting ----

    fn remember_room_membership(&self, room: &str, joined: bool) {
        let room = room.trim();
        if room.is_empty() {
            return;
        }
        let names = {
            let mut st = self.st();
            let position = st.room_names.iter().position(|r| r.eq_ignore_ascii_case(room));
            match (joined, position) {
                (true, None) => st.room_names.push(room.to_string()),
                (false, Some(i)) => {
                    st.room_names.remove(i);
                }
                _ => return,
            }
            st.room_names.clone()
        };
        (self.cfg.on_rooms_changed)(names);
    }

    fn emit_rooms(&self) {
        self.emit(Event { kind: "rooms".into(), rooms: self.rooms(), ..Default::default() });
    }

    fn emit_users(&self, room: &str) {
        if !room.is_empty() {
            self.emit(Event { kind: "users".into(), target: room.into(), users: self.users(room), ..Default::default() });
        }
    }

    fn emit_users_for_all(&self) {
        for room in self.rooms() {
            self.emit_users(&room.name);
        }
    }

    fn emit_local_message(&self, kind: &str, target: &str, text: &str) {
        self.emit(Event { kind: kind.into(), target: target.into(), from: self.nick(), text: text.into(), ..Default::default() });
    }

    fn emit(&self, mut event: Event) {
        if event.timestamp == 0 {
            event.timestamp = chrono::Utc::now().timestamp_millis();
        }
        if event.target.is_empty() {
            event.target = SERVER_TARGET.into();
        }
        (self.cfg.on_event)(event);
    }
}

fn dial(host: &str, port: u16, timeout: Duration) -> std::io::Result<TcpStream> {
    let mut last = std::io::Error::new(ErrorKind::NotFound, "no addresses found");
    for addr in (host, port).to_socket_addrs()? {
        match TcpStream::connect_timeout(&addr, timeout) {
            Ok(s) => return Ok(s),
            Err(e) => last = e,
        }
    }
    Err(last)
}

fn is_poll_timeout(e: &std::io::Error) -> bool {
    matches!(e.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut)
}

/// girc's `ToRFC1459`: ASCII A-Z and `[\]^` fold to a-z and `{|}~`.
fn fold(s: &str) -> String {
    s.chars().map(|c| if ('A'..='^').contains(&c) { (c as u8 + 32) as char } else { c }).collect()
}

fn add_user(ch: &mut Channel, nick: &str) {
    if ch.users.len() < MAX_CHANNEL_USERS && !ch.users.iter().any(|u| fold(u) == fold(nick)) {
        ch.users.push(nick.to_string());
    }
}

fn remove_user(ch: &mut Channel, nick: &str) -> bool {
    let before = ch.users.len();
    ch.users.retain(|u| fold(u) != fold(nick));
    ch.users.len() != before
}

/// girc's `IsValidNick`.
fn is_valid_nick(nick: &str) -> bool {
    let b = nick.as_bytes();
    let Some(&first) = b.first() else { return false };
    if !(b'A'..=b'}').contains(&first) && first != b'?' {
        return false;
    }
    b[1..].iter().all(|&c| (b'A'..=b'}').contains(&c) || c.is_ascii_digit() || c == b'-')
}

/// The nick part of `nick!user@host`.
fn source_name(prefix: &str) -> &str {
    prefix.split(['!', '@']).next().unwrap_or("")
}

/// Encodes a command line the way girc does: the last parameter gets a `:` when it is empty,
/// contains a space or starts with `:`; CR/LF are stripped.
fn encode(command: &str, params: &[&str]) -> String {
    let mut line = command.to_string();
    for (i, p) in params.iter().enumerate() {
        line.push(' ');
        if i == params.len() - 1 && (p.is_empty() || p.contains(' ') || p.starts_with(':')) {
            line.push(':');
        }
        line.push_str(p);
    }
    line.retain(|c| c != '\r' && c != '\n');
    line
}

fn parse_line(line: &str) -> Option<Message> {
    let mut rest = line.trim_start();
    if rest.starts_with('@') {
        rest = rest.split_once(' ')?.1.trim_start();
    }
    let mut source = String::new();
    if let Some(prefixed) = rest.strip_prefix(':') {
        let (prefix, tail) = prefixed.split_once(' ')?;
        source = source_name(prefix).to_string();
        rest = tail.trim_start();
    }
    let (head, trailing) = match rest.split_once(" :") {
        Some((h, t)) => (h, Some(t)),
        None => (rest, None),
    };
    let mut words = head.split(' ').filter(|w| !w.is_empty());
    let command = words.next()?.to_uppercase();
    let mut params: Vec<String> = words.map(String::from).collect();
    params.extend(trailing.map(String::from));
    Some(Message { source, command, params })
}

/// A CTCP request in a PRIVMSG: `(TAG, args)`, as girc's `DecodeCTCP`.
fn decode_ctcp(m: &Message) -> Option<(String, String)> {
    if m.params.len() != 2 {
        return None;
    }
    let text = m.param(1).strip_prefix('\x01')?.strip_suffix('\x01')?;
    let (tag, args) = text.split_once(' ').unwrap_or((text, ""));
    if tag.is_empty() || !tag.bytes().all(|b| b.is_ascii_uppercase() || b.is_ascii_digit()) {
        return None;
    }
    Some((tag.to_string(), args.to_string()))
}

/// The first whitespace-separated word, and the trimmed remainder.
fn split_first_arg(input: &str) -> (&str, &str) {
    let input = input.trim_start();
    match input.split_whitespace().next() {
        Some(first) => (first, input[first.len()..].trim()),
        None => ("", ""),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{BufRead, BufReader};
    use std::net::TcpListener;

    #[test]
    fn hostile_lines_never_panic() {
        let client = Client::new(Config::default());
        client.st().nick = "me".into();
        client.handle_message(&parse_line(":me!u@h JOIN #a").unwrap());
        let mut n = crate::Noise::new(0x1bc);
        for _ in 0..5000 {
            let raw = n.bytes(120, b":!@# ,;=\x01\x02\x03\x0f\x16\x1d\x1fme#aJOINPARTKICKPRIVMSGNOTICE3534330011234567890");
            let line = String::from_utf8_lossy(&raw);
            if let Some(m) = parse_line(&line) {
                client.handle_message(&m);
                let e = Event { kind: "message".into(), target: m.param(0).into(), from: m.source.clone(), text: m.last().into(), ..Default::default() };
                let _ = crate::ircformat::format_event(&e, "12:00");
            }
        }
    }

    #[test]
    fn remember_room_membership_tracks_confirmed_channels() {
        let updates = Arc::new(Mutex::new(Vec::new()));
        let log = updates.clone();
        let client = Client::new(Config { on_rooms_changed: Arc::new(move |rooms| log.lock().push(rooms.join(","))), ..Default::default() });
        client.remember_room_membership("#One", true);
        client.remember_room_membership("#two", true);
        client.remember_room_membership("#one", true);
        client.remember_room_membership("#missing", false);
        client.remember_room_membership("#ONE", false);
        assert_eq!(updates.lock().join("|"), "#One|#One,#two|#two");
        assert_eq!(client.st().room_names, vec!["#two"]);
    }

    #[test]
    fn only_joined_channels_are_tracked_and_users_are_capped() {
        let client = Client::new(Config::default());
        client.st().nick = "me".into();
        let feed = |l: &str| client.handle_message(&parse_line(l).unwrap());
        for n in 0..20_000 {
            feed(&format!(":x{n}!u@h JOIN #room-{n}"));
        }
        assert!(client.st().channels.is_empty(), "JOINs by others for rooms we're not in are ignored");
        feed(":me!u@h JOIN #ours");
        feed(":other!u@h JOIN #ours");
        assert_eq!(client.st().channels[&fold("#ours")].users, vec!["me", "other"]);
        let mut ch = Channel { name: "#big".into(), topic: String::new(), users: (0..MAX_CHANNEL_USERS).map(|n| format!("u{n}")).collect() };
        add_user(&mut ch, "one-too-many");
        assert_eq!(ch.users.len(), MAX_CHANNEL_USERS);
    }

    #[test]
    fn parses_and_encodes_lines() {
        let m = parse_line("@time=x :nick!u@h PRIVMSG #chan :hello there").unwrap();
        assert_eq!((m.source.as_str(), m.command.as_str()), ("nick", "PRIVMSG"));
        assert_eq!(m.params, vec!["#chan", "hello there"]);
        let m = parse_line("PING :abc").unwrap();
        assert_eq!((m.source.as_str(), m.last()), ("", "abc"));
        let m = parse_line(":srv 353 me = #c :@op +v plain").unwrap();
        assert_eq!(m.params.len(), 4);
        assert_eq!(encode("PRIVMSG", &["#c", "hi"]), "PRIVMSG #c hi");
        assert_eq!(encode("PRIVMSG", &["#c", "hi there"]), "PRIVMSG #c :hi there");
        assert_eq!(encode("TOPIC", &["#c", ""]), "TOPIC #c :");
        assert_eq!(encode("QUIT", &[":x\r\nJOIN #evil"]), "QUIT ::xJOIN #evil");
        assert_eq!(split_first_arg("  #room  key here "), ("#room", "key here"));
        assert_eq!(split_first_arg("  "), ("", ""));
        assert_eq!(fold("Nick[A]^"), "nick{a}~");
    }

    #[test]
    fn event_serialises_like_go() {
        let e = Event { kind: "users".into(), target: "#a".into(), timestamp: 5, ..Default::default() };
        assert_eq!(serde_json::to_string(&e).unwrap(), r##"{"type":"users","target":"#a","timestamp":5}"##);
        let e = Event { kind: "rooms".into(), rooms: vec![RoomInfo { name: "#a".into(), topic: String::new(), users: 0 }], ..Default::default() };
        assert_eq!(serde_json::to_string(&e).unwrap(), r##"{"type":"rooms","timestamp":0,"rooms":[{"name":"#a"}]}"##);
    }

    struct Fake {
        reader: BufReader<TcpStream>,
        writer: TcpStream,
    }

    impl Fake {
        fn line(&mut self) -> String {
            let mut s = String::new();
            self.reader.read_line(&mut s).unwrap();
            s.trim_end().to_string()
        }
        fn expect(&mut self, want: &str) {
            for _ in 0..20 {
                if self.line() == want {
                    return;
                }
            }
            panic!("never received {want:?}");
        }
        fn send(&mut self, line: &str) {
            self.writer.write_all(format!("{line}\r\n").as_bytes()).unwrap();
        }
    }

    fn harness(cfg: Config) -> (Arc<Client>, TcpListener, Receiver<Event>, Arc<Mutex<u32>>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let (tx, rx) = mpsc::channel();
        let tx = Mutex::new(tx);
        let closes = Arc::new(Mutex::new(0));
        let c = closes.clone();
        let client = Client::new(Config {
            server: "127.0.0.1".into(),
            port: listener.local_addr().unwrap().port(),
            nick: "me".into(),
            connect_timeout: Duration::from_secs(5),
            on_event: Arc::new(move |e| {
                let _ = tx.lock().send(e);
            }),
            on_close: Arc::new(move || *c.lock() += 1),
            ..cfg
        });
        (client, listener, rx, closes)
    }

    fn accept(listener: &TcpListener) -> Fake {
        let (s, _) = listener.accept().unwrap();
        s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        Fake { reader: BufReader::new(s.try_clone().unwrap()), writer: s }
    }

    fn wait_for(rx: &Receiver<Event>, pred: impl Fn(&Event) -> bool) -> Event {
        loop {
            let e = rx.recv_timeout(Duration::from_secs(5)).expect("event");
            if pred(&e) {
                return e;
            }
        }
    }

    #[test]
    fn registers_tracks_rooms_and_handles_input() {
        let (client, listener, rx, closes) = harness(Config {
            server_pass: "pw".into(),
            auto_join_rooms: vec!["#a".into(), "#b".into()],
            nickserv_identify: true,
            nickserv_account: "acct".into(),
            nickserv_password: "secret".into(),
            ..Default::default()
        });
        let c = client.clone();
        let connecting = std::thread::spawn(move || c.connect());
        let mut srv = accept(&listener);
        assert_eq!(srv.line(), "PASS pw");
        assert_eq!(srv.line(), "NICK me");
        assert_eq!(srv.line(), "USER me * * me");
        srv.send(":srv 433 * me :Nickname is already in use");
        assert_eq!(srv.line(), "NICK me_");
        srv.send(":srv 001 me_ :Welcome");
        connecting.join().unwrap().unwrap();
        let e = wait_for(&rx, |e| e.kind == "system");
        assert_eq!(e.text, format!("Connected to 127.0.0.1:{}", client.cfg.port));
        assert_eq!(e.target, SERVER_TARGET);
        assert_eq!(srv.line(), "PRIVMSG NickServ :IDENTIFY acct secret");
        assert_eq!(srv.line(), "JOIN #a,#b");
        assert_eq!(wait_for(&rx, |e| e.kind == "system").text, "Sent NickServ identify request");

        srv.send("PING :xyz");
        srv.expect("PONG xyz");

        srv.send(":me_!u@h JOIN #a");
        let e = wait_for(&rx, |e| e.kind == "join");
        assert_eq!((e.target.as_str(), e.from.as_str(), e.text.as_str()), ("#a", "me_", "me_ joined #a"));
        srv.send(":srv 332 me_ #a :the topic");
        assert_eq!(wait_for(&rx, |e| e.kind == "topic").topic, "the topic");
        srv.send(":srv 353 me_ = #a :@me_ +Bob carol");
        assert_eq!(wait_for(&rx, |e| e.kind == "users").users, vec!["@me_", "+Bob", "carol"]);
        srv.send(":srv 366 me_ #a :End of NAMES");
        assert_eq!(wait_for(&rx, |e| e.kind == "users").users, vec!["Bob", "carol", "me_"]);
        assert_eq!(client.rooms(), vec![RoomInfo { name: "#a".into(), topic: "the topic".into(), users: 3 }]);

        srv.send(":Bob!u@h PRIVMSG #a :hi all");
        let e = wait_for(&rx, |e| e.kind == "message");
        assert_eq!((e.target.as_str(), e.from.as_str(), e.text.as_str()), ("#a", "Bob", "hi all"));
        srv.send(":Bob!u@h PRIVMSG ME_ :\x01ACTION waves\x01");
        let e = wait_for(&rx, |e| e.kind == "action");
        assert_eq!((e.target.as_str(), e.text.as_str()), ("Bob", "waves"));
        srv.send(":Bob!u@h NICK :Robert");
        assert_eq!(wait_for(&rx, |e| e.kind == "nick").text, "Robert");
        assert_eq!(client.users("#A"), vec!["carol", "me_", "Robert"]);
        srv.send(":carol!u@h QUIT :bye");
        assert_eq!(wait_for(&rx, |e| e.kind == "quit").text, "bye");
        assert_eq!(client.users("#a"), vec!["me_", "Robert"]);
        srv.send(":Robert!u@h KICK #a me_ :out");
        let e = wait_for(&rx, |e| e.kind == "kick");
        assert_eq!((e.from.as_str(), e.command.as_str(), e.text.as_str()), ("Robert", "me_", "out"));
        assert!(client.rooms().is_empty());

        client.handle_input("#a", "hello there").unwrap();
        srv.expect("PRIVMSG #a :hello there");
        let e = wait_for(&rx, |e| e.kind == "message");
        assert_eq!((e.from.as_str(), e.text.as_str()), ("me_", "hello there"));
        client.handle_input("#a", "/me dances").unwrap();
        srv.expect("PRIVMSG #a :\x01ACTION dances\x01");
        client.handle_input("#a", "/join #c key").unwrap();
        srv.expect("JOIN #c key");
        client.handle_input("#a", "/part").unwrap();
        srv.expect("PART #a");
        client.handle_input("#a", "/topic").unwrap();
        srv.expect("TOPIC #a");
        client.handle_input("#a", "/names").unwrap();
        srv.expect("NAMES #a");
        client.handle_input("#a", "/mode #a +i").unwrap();
        srv.expect("MODE #a +i");
        assert_eq!(client.handle_input(SERVER_TARGET, "hi").unwrap_err().to_string(), "select a room or use /msg <nick> <message>");
        assert_eq!(client.handle_input("#a", "/msg bob").unwrap_err().to_string(), "/msg requires a target and message");
        client.handle_input("", "/help").unwrap();
        assert!(wait_for(&rx, |e| e.kind == "system").text.starts_with("Commands: /join"));

        client.list_rooms().unwrap();
        srv.expect("LIST");
        srv.send(":srv 322 me_ #small 2 :s");
        srv.send(":srv 322 me_ #Big 9 :b");
        srv.send(":srv 322 me_ #also 2 :a");
        srv.send(":srv 323 me_ :End");
        let names: Vec<_> = wait_for(&rx, |e| e.kind == "room-list").rooms.into_iter().map(|r| r.name).collect();
        assert_eq!(names, vec!["#Big", "#also", "#small"]);

        client.close();
        client.close();
        srv.expect("QUIT :Nexus Terminal disconnected");
        let deadline = Instant::now() + Duration::from_secs(5);
        while *closes.lock() == 0 && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(10));
        }
        std::thread::sleep(Duration::from_millis(100));
        assert_eq!(*closes.lock(), 1);
        assert!(client.handle_input("#a", "late").is_err());
    }

    #[test]
    fn remote_close_reports_error_and_close() {
        let errors = Arc::new(Mutex::new(Vec::new()));
        let log = errors.clone();
        let (client, listener, rx, closes) = harness(Config { on_error: Arc::new(move |e| log.lock().push(e)), ..Default::default() });
        let c = client.clone();
        let connecting = std::thread::spawn(move || c.connect());
        let mut srv = accept(&listener);
        srv.expect("USER me * * me");
        srv.send(":srv 001 me :Welcome");
        connecting.join().unwrap().unwrap();
        srv.send("ERROR :Closing link");
        assert_eq!(wait_for(&rx, |e| e.kind == "error").text, "Closing link");
        drop(srv);
        assert_eq!(wait_for(&rx, |e| e.kind == "error").text, "connection closed by server");
        std::thread::sleep(Duration::from_millis(100));
        assert_eq!(*errors.lock(), vec!["connection closed by server"]);
        assert_eq!(*closes.lock(), 1);
    }

    #[test]
    fn connect_times_out_without_welcome() {
        let (client, listener, _rx, closes) = harness(Config::default());
        let client = Client::new(Config { connect_timeout: Duration::from_millis(300), ..client.cfg.clone() });
        let err = client.connect().unwrap_err().to_string();
        assert_eq!(err, "IRC connection timed out after 300ms");
        drop(listener);
        assert_eq!(*closes.lock(), 0);
    }

    #[test]
    fn connector_supplies_the_transport() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let used = Arc::new(Mutex::new(false));
        let flag = used.clone();
        let client = Client::new(Config {
            server: "unresolvable.invalid".into(),
            nick: "me".into(),
            connect_timeout: Duration::from_secs(5),
            connector: Some(Arc::new(move || {
                *flag.lock() = true;
                let s = TcpStream::connect(addr)?;
                s.set_read_timeout(Some(Duration::from_millis(20)))?;
                Ok(Box::new(s) as Box<dyn ReadWrite>)
            })),
            ..Default::default()
        });
        let c = client.clone();
        let connecting = std::thread::spawn(move || c.connect());
        let mut srv = accept(&listener);
        srv.expect("USER me * * me");
        srv.send(":srv 001 me :Welcome");
        connecting.join().unwrap().unwrap();
        assert!(*used.lock());
        client.handle_input("", "/quit gone").unwrap();
        srv.expect("QUIT gone");
    }

    #[test]
    fn dial_failure_is_reported() {
        let port = TcpListener::bind("127.0.0.1:0").unwrap().local_addr().unwrap().port();
        let client = Client::new(Config { server: "127.0.0.1".into(), port, nick: "me".into(), ..Default::default() });
        let err = client.connect().unwrap_err().to_string();
        assert!(err.starts_with(&format!("dial tcp 127.0.0.1:{port}:")), "{err}");
    }
}
