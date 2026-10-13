// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Telnet client (port of Go `internal/telnetclient`): RFC 854/855 option negotiation (ECHO, SGA,
//! TTYPE, NAWS; everything else refused), IAC escaping, line-ending conversion, ISO 8859-1 output
//! encoding and BRK. Go read but never acted on `lineMode`, and neither do we.

use parking_lot::Mutex;
use std::sync::Arc;

use super::{Callbacks, DataFn, DebugFn, TcpConn};
use crate::models::{LineEnding, Profile, TelnetConfig};

pub const IAC: u8 = 255;
pub const DONT: u8 = 254;
pub const DO: u8 = 253;
pub const WONT: u8 = 252;
pub const WILL: u8 = 251;
pub const SB: u8 = 250;
pub const SE: u8 = 240;
pub const BRK: u8 = 243;

pub const OPT_ECHO: u8 = 1;
pub const OPT_SGA: u8 = 3;
pub const OPT_TERM_TYPE: u8 = 24;
pub const OPT_NAWS: u8 = 31;

const SUB_IS: u8 = 0;
const SUB_SEND: u8 = 1;

/// An unterminated IAC sequence longer than this is dropped rather than buffered forever.
const MAX_PENDING: usize = 8192;

pub struct TelnetClient {
    inner: Arc<Inner>,
}

struct Inner {
    conn: Arc<TcpConn>,
    host: String,
    port: i64,
    connect_timeout: i64,
    cfg: TelnetConfig,
    encoding: String,
    on_data: DataFn,
    on_debug: Option<DebugFn>,
    state: Mutex<State>,
}

struct State {
    local_echo: bool,
    cols: u16,
    rows: u16,
    /// Options we have agreed to perform (WILL) and the peer has agreed to (DO), so a request
    /// for a state we're already in isn't answered (RFC 854 forbids it; answering unconditionally
    /// lets a hostile or naive peer drive an endless DONT/WONT ping-pong at line rate).
    us: std::collections::HashSet<u8>,
    him: std::collections::HashSet<u8>,
}

impl TelnetClient {
    /// `on_debug` receives negotiation messages ("received DO 31", "sending WILL 31", …), always,
    /// as in Go; the profile's `negotiationDebug` is for the caller to honour.
    pub fn new(profile: &Profile, cb: Callbacks, on_debug: Option<DebugFn>) -> TelnetClient {
        let cfg = profile.telnet.clone().unwrap_or_default();
        let encoding = if cfg.encoding.is_empty() { "utf-8".to_string() } else { cfg.encoding.clone() };
        let state = State { local_echo: cfg.local_echo, cols: 80, rows: 24, us: Default::default(), him: Default::default() };
        TelnetClient {
            inner: Arc::new(Inner {
                conn: Arc::new(TcpConn::new(cb.on_error, cb.on_close)),
                host: profile.host.clone(),
                port: if profile.port == 0 { 23 } else { profile.port },
                connect_timeout: profile.connect_timeout,
                cfg,
                encoding,
                on_data: cb.on_data,
                on_debug,
                state: Mutex::new(state),
            }),
        }
    }

    pub fn connect(&self) -> crate::Result<()> {
        let inner = &self.inner;
        if inner.conn.is_connected() {
            return Err("already connected".into());
        }
        let (addr, res) = super::dial(&inner.host, inner.port, inner.connect_timeout);
        let stream = res.map_err(|e| format!("Telnet connection to {addr} failed: {e}"))?;
        let me = inner.clone();
        let mut pending = Vec::new();
        inner.conn.connect(stream, move |chunk| me.process(&mut pending, chunk))
    }

    /// Sends user input: line endings converted, encoded, literal 0xFF doubled.
    pub fn write(&self, data: &[u8]) -> crate::Result<()> {
        let converted = convert_line_ending(data, self.inner.cfg.line_ending);
        let encoded = encode_outgoing(&converted, &self.inner.encoding);
        self.inner.conn.write(&escape_iac(&encoded))
    }

    /// Records the size and sends a NAWS update (sent even if the server never asked for NAWS, as in Go).
    pub fn resize(&self, cols: u16, rows: u16) -> crate::Result<()> {
        if !self.inner.conn.is_connected() {
            return Err("not connected".into());
        }
        {
            let mut st = self.inner.state.lock();
            st.cols = cols;
            st.rows = rows;
        }
        self.inner.send_naws(cols, rows)
    }

    /// Sends IAC BRK.
    pub fn send_break(&self) -> crate::Result<()> {
        self.inner.conn.write(&[IAC, BRK])
    }

    pub fn close(&self) {
        self.inner.conn.close();
    }

    pub fn is_connected(&self) -> bool {
        self.inner.conn.is_connected()
    }

    /// The negotiated local-echo state: starts at the profile's `localEcho`, cleared by the
    /// server's WILL ECHO and set by WONT ECHO. Like Go, the client itself never echoes.
    pub fn local_echo(&self) -> bool {
        self.inner.state.lock().local_echo
    }
}

impl Inner {
    /// Splits a received chunk into data and IAC sequences. Unlike Go, a sequence cut off at the
    /// end of a read is kept for the next read instead of dropped.
    fn process(&self, pending: &mut Vec<u8>, chunk: &[u8]) {
        pending.extend_from_slice(chunk);
        let mut data = Vec::new();
        let mut i = 0;
        while i < pending.len() {
            if pending[i] != IAC {
                data.push(pending[i]);
                i += 1;
                continue;
            }
            if !data.is_empty() {
                (self.on_data)(&data);
                data.clear();
            }
            let consumed = self.handle_iac(&pending[i..]);
            if consumed == 0 {
                break;
            }
            i += consumed;
        }
        if !data.is_empty() {
            (self.on_data)(&data);
        }
        pending.drain(..i);
        if pending.len() > MAX_PENDING {
            pending.clear();
        }
    }

    /// Handles the IAC sequence at `data[0]`; returns bytes consumed, 0 if incomplete.
    fn handle_iac(&self, data: &[u8]) -> usize {
        if data.len() < 2 {
            return 0;
        }
        match data[1] {
            IAC => {
                (self.on_data)(&[IAC]);
                2
            }
            DO | DONT | WILL | WONT => {
                let Some(&opt) = data.get(2) else { return 0 };
                match data[1] {
                    DO => self.handle_do(opt),
                    DONT => self.handle_dont(opt),
                    WILL => self.handle_will(opt),
                    _ => self.handle_wont(opt),
                }
                3
            }
            SB => match (2..data.len().saturating_sub(1)).find(|&i| data[i] == IAC && data[i + 1] == SE) {
                Some(end) => {
                    self.process_subnegotiation(&data[2..end]);
                    end + 2
                }
                None => 0,
            },
            cmd => {
                self.debug(format!("received IAC command: {cmd}"));
                2
            }
        }
    }

    fn handle_do(&self, opt: u8) {
        self.debug(format!("received DO {opt}"));
        match opt {
            OPT_TERM_TYPE | OPT_SGA | OPT_NAWS => {
                if !self.state.lock().us.insert(opt) {
                    return;
                }
                self.negotiate(WILL, opt);
                if opt == OPT_NAWS {
                    let (cols, rows) = {
                        let st = self.state.lock();
                        (st.cols, st.rows)
                    };
                    let _ = self.send_naws(cols, rows);
                }
            }
            _ => self.negotiate(WONT, opt),
        }
    }

    fn handle_dont(&self, opt: u8) {
        self.debug(format!("received DONT {opt}"));
        if self.state.lock().us.remove(&opt) {
            self.negotiate(WONT, opt);
        }
    }

    fn handle_will(&self, opt: u8) {
        self.debug(format!("received WILL {opt}"));
        match opt {
            OPT_ECHO | OPT_SGA => {
                if !self.state.lock().him.insert(opt) {
                    return;
                }
                self.negotiate(DO, opt);
                if opt == OPT_ECHO {
                    self.state.lock().local_echo = false;
                }
            }
            _ => self.negotiate(DONT, opt),
        }
    }

    fn handle_wont(&self, opt: u8) {
        self.debug(format!("received WONT {opt}"));
        if opt == OPT_ECHO {
            self.state.lock().local_echo = true;
        }
        if self.state.lock().him.remove(&opt) {
            self.negotiate(DONT, opt);
        }
    }

    fn process_subnegotiation(&self, payload: &[u8]) {
        let Some(&opt) = payload.first() else { return };
        self.debug(format!("sub-negotiation for option {opt}"));
        if opt == OPT_TERM_TYPE && payload.get(1) == Some(&SUB_SEND) {
            let tt = if self.cfg.terminal_type.is_empty() { "xterm-256color" } else { &self.cfg.terminal_type };
            self.debug(format!("sending terminal type: {tt}"));
            let mut resp = vec![IAC, SB, OPT_TERM_TYPE, SUB_IS];
            resp.extend_from_slice(tt.to_uppercase().as_bytes());
            resp.extend_from_slice(&[IAC, SE]);
            let _ = self.conn.write(&resp);
        }
    }

    fn negotiate(&self, cmd: u8, opt: u8) {
        self.debug(format!("sending {} {opt}", command_name(cmd)));
        let _ = self.conn.write(&[IAC, cmd, opt]);
    }

    fn send_naws(&self, cols: u16, rows: u16) -> crate::Result<()> {
        self.debug(format!("sending NAWS: {cols}x{rows}"));
        // 0xFF inside the subnegotiation must be doubled (RFC 1073), or a 255-column window
        // sends a bare IAC the server parses as a command.
        let mut msg = vec![IAC, SB, OPT_NAWS];
        for b in cols.to_be_bytes().into_iter().chain(rows.to_be_bytes()) {
            msg.push(b);
            if b == IAC {
                msg.push(IAC);
            }
        }
        msg.extend_from_slice(&[IAC, SE]);
        self.conn.write(&msg)
    }

    fn debug(&self, msg: String) {
        if let Some(f) = &self.on_debug {
            f(msg);
        }
    }
}

fn command_name(cmd: u8) -> String {
    match cmd {
        WILL => "WILL".into(),
        WONT => "WONT".into(),
        DO => "DO".into(),
        DONT => "DONT".into(),
        _ => format!("CMD({cmd})"),
    }
}

/// Normalises CRLF/CR/LF to the configured ending (`None` leaves data untouched).
fn convert_line_ending(data: &[u8], le: LineEnding) -> Vec<u8> {
    if le == LineEnding::None {
        return data.to_vec();
    }
    let mut out = Vec::with_capacity(data.len() + 8);
    let mut i = 0;
    while i < data.len() {
        match data[i] {
            b'\r' => {
                if data.get(i + 1) == Some(&b'\n') {
                    i += 1;
                }
                out.extend_from_slice(le.bytes());
            }
            b'\n' => out.extend_from_slice(le.bytes()),
            b => out.push(b),
        }
        i += 1;
    }
    out
}

/// Doubles literal 0xFF bytes.
fn escape_iac(data: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(data.len());
    for &b in data {
        out.push(b);
        if b == IAC {
            out.push(IAC);
        }
    }
    out
}

/// UTF-8 → ISO 8859-1 for the Latin-1 encodings; everything else is sent as-is. Unlike Go this
/// runs before IAC escaping, so a Latin-1 'ÿ' (0xFF) is escaped correctly.
fn encode_outgoing(data: &[u8], encoding: &str) -> Vec<u8> {
    if super::is_latin1(encoding) { super::utf8_to_latin1(data) } else { data.to_vec() }
}

/// Fuzzing entry point: `data` through the IAC negotiation parser, in uneven chunks.
#[cfg(feature = "fuzzing")]
#[doc(hidden)]
pub fn fuzz_negotiation(data: &[u8]) {
    let c = TelnetClient::new(&crate::models::Profile::new(crate::models::Protocol::Telnet), Callbacks::noop(), None);
    let mut pending = Vec::new();
    for chunk in data.chunks(37) {
        c.inner.process(&mut pending, chunk);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;
    use std::time::Duration;

    #[test]
    fn hostile_negotiation_never_panics() {
        let c = TelnetClient::new(&Profile::new(crate::models::Protocol::Telnet), Callbacks::noop(), None);
        let mut n = crate::Noise::new(0x7e1);
        let mut pending = Vec::new();
        for _ in 0..5000 {
            // IAC, SE, SB, WILL, WONT, DO, DONT and the options the client handles.
            let chunk = n.bytes(64, &[255, 240, 250, 251, 252, 253, 254, 1, 3, 24, 31, 32, 0, b'a']);
            c.inner.process(&mut pending, &chunk);
            assert!(pending.len() <= 16 << 10, "pending must stay bounded");
        }
    }

    #[test]
    fn negotiation_replies_only_on_state_change() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let profile = Profile { host: "127.0.0.1".into(), port: listener.local_addr().unwrap().port() as i64, ..Profile::new(crate::models::Protocol::Telnet) };
        let client = TelnetClient::new(&profile, Callbacks::noop(), None);
        client.connect().unwrap();
        let (mut srv, _) = listener.accept().unwrap();
        srv.set_read_timeout(Some(Duration::from_millis(500))).unwrap();
        // DONT/WONT for options that are already off, and repeated WILL/DO, get no reply;
        // only the first WILL SGA and the first DO SGA are answered.
        let mut flood = vec![];
        for _ in 0..100 {
            flood.extend_from_slice(&[IAC, DONT, 1, IAC, WONT, 1, IAC, WILL, OPT_SGA, IAC, DO, OPT_SGA]);
        }
        srv.write_all(&flood).unwrap();
        let mut got = vec![];
        let mut buf = [0u8; 4096];
        while let Ok(n) = srv.read(&mut buf) {
            if n == 0 {
                break;
            }
            got.extend_from_slice(&buf[..n]);
        }
        assert_eq!(got, [IAC, DO, OPT_SGA, IAC, WILL, OPT_SGA]);
        // A 255-column window doubles the 0xFF inside the NAWS subnegotiation.
        srv.write_all(&[IAC, DO, OPT_NAWS]).unwrap();
        let _ = read_n(&mut srv, 3 + 9);
        client.resize(255, 24).unwrap();
        assert_eq!(read_n(&mut srv, 10), [IAC, SB, OPT_NAWS, 0, 255, 255, 0, 24, IAC, SE]);
        client.close();
    }

    #[test]
    fn line_endings() {
        let cases = [
            (LineEnding::Crlf, "a\nb\r\nc\rd", "a\r\nb\r\nc\r\nd"),
            (LineEnding::Lf, "a\r\nb\rc\n", "a\nb\nc\n"),
            (LineEnding::Cr, "a\r\nb\n", "a\rb\r"),
            (LineEnding::None, "a\r\nb\n", "a\r\nb\n"),
        ];
        for (le, input, want) in cases {
            assert_eq!(convert_line_ending(input.as_bytes(), le), want.as_bytes(), "{le:?}");
        }
    }

    #[test]
    fn iac_escaping_and_latin1() {
        assert_eq!(escape_iac(&[1, 0xff, 2]), vec![1, 0xff, 0xff, 2]);
        assert_eq!(encode_outgoing("é".as_bytes(), "Latin-1"), vec![0xe9]);
        assert_eq!(encode_outgoing("€".as_bytes(), "latin1"), "€".as_bytes()); // unencodable: unchanged
        assert_eq!(encode_outgoing("é".as_bytes(), "utf-8"), "é".as_bytes());
    }

    fn read_n(s: &mut std::net::TcpStream, n: usize) -> Vec<u8> {
        let mut buf = vec![0; n];
        s.read_exact(&mut buf).unwrap();
        buf
    }

    #[test]
    fn negotiates_and_frames_data() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let profile = Profile { host: "127.0.0.1".into(), port: listener.local_addr().unwrap().port() as i64, ..Profile::new(crate::models::Protocol::Telnet) };
        let (dtx, drx) = mpsc::channel::<Vec<u8>>();
        let (etx, erx) = mpsc::channel::<String>();
        let (ctx, crx) = mpsc::channel::<()>();
        let debug = Arc::new(Mutex::new(Vec::<String>::new()));
        let dbg = debug.clone();
        let cb = Callbacks {
            on_data: Arc::new(move |d| dtx.send(d.to_vec()).unwrap()),
            on_error: Arc::new(move |e| etx.send(e).unwrap()),
            on_close: Arc::new(move || ctx.send(()).unwrap()),
        };
        let client = TelnetClient::new(&profile, cb, Some(Arc::new(move |m| dbg.lock().push(m))));
        client.connect().unwrap();
        let (mut srv, _) = listener.accept().unwrap();
        srv.set_read_timeout(Some(Duration::from_secs(5))).unwrap();

        srv.write_all(&[IAC, DO, OPT_TERM_TYPE, IAC, DO, OPT_NAWS, IAC, WILL, OPT_ECHO, IAC, WILL, OPT_SGA, IAC, DO, 99]).unwrap();
        let mut want = vec![IAC, WILL, OPT_TERM_TYPE, IAC, WILL, OPT_NAWS, IAC, SB, OPT_NAWS, 0, 80, 0, 24, IAC, SE];
        want.extend_from_slice(&[IAC, DO, OPT_ECHO, IAC, DO, OPT_SGA, IAC, WONT, 99]);
        assert_eq!(read_n(&mut srv, want.len()), want);
        assert!(!client.local_echo());

        // Data with an escaped 0xFF, then a TTYPE SEND split across two writes.
        srv.write_all(b"hi").unwrap();
        srv.write_all(&[IAC, IAC, IAC, SB, OPT_TERM_TYPE]).unwrap();
        std::thread::sleep(Duration::from_millis(50));
        srv.write_all(&[SUB_SEND, IAC, SE]).unwrap();
        let mut want = vec![IAC, SB, OPT_TERM_TYPE, SUB_IS];
        want.extend_from_slice(b"XTERM-256COLOR");
        want.extend_from_slice(&[IAC, SE]);
        assert_eq!(read_n(&mut srv, want.len()), want);
        let mut got = Vec::new();
        while got.len() < 3 {
            got.extend(drx.recv_timeout(Duration::from_secs(5)).unwrap());
        }
        assert_eq!(got, b"hi\xff");

        srv.write_all(&[IAC, WONT, OPT_ECHO]).unwrap();
        assert_eq!(read_n(&mut srv, 3), [IAC, DONT, OPT_ECHO]);
        assert!(client.local_echo());

        client.resize(132, 50).unwrap();
        assert_eq!(read_n(&mut srv, 9), [IAC, SB, OPT_NAWS, 0, 132, 0, 50, IAC, SE]);
        client.send_break().unwrap();
        assert_eq!(read_n(&mut srv, 2), [IAC, BRK]);
        client.write(b"ls\n\xff").unwrap();
        assert_eq!(read_n(&mut srv, 6), b"ls\r\n\xff\xff");

        let log = debug.lock().clone();
        for m in ["received DO 24", "sending WILL 31", "sending NAWS: 80x24", "sending WONT 99", "sending terminal type: xterm-256color"] {
            assert!(log.iter().any(|l| l == m), "missing debug {m:?} in {log:?}");
        }

        // Remote close: Go reports "read error: EOF" then closes, exactly once.
        drop(srv);
        assert_eq!(erx.recv_timeout(Duration::from_secs(5)).unwrap(), "read error: EOF");
        crx.recv_timeout(Duration::from_secs(5)).unwrap();
        client.close();
        assert!(crx.recv_timeout(Duration::from_millis(100)).is_err());
        assert!(client.write(b"x").is_err());
    }

    #[test]
    fn local_close_fires_once_without_error() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let profile = Profile { host: "127.0.0.1".into(), port: listener.local_addr().unwrap().port() as i64, ..Profile::new(crate::models::Protocol::Telnet) };
        let (etx, erx) = mpsc::channel::<String>();
        let (ctx, crx) = mpsc::channel::<()>();
        let cb = Callbacks { on_error: Arc::new(move |e| etx.send(e).unwrap()), on_close: Arc::new(move || ctx.send(()).unwrap()), ..Callbacks::noop() };
        let client = TelnetClient::new(&profile, cb, None);
        client.connect().unwrap();
        assert!(client.connect().is_err());
        let _srv = listener.accept().unwrap();
        client.close();
        client.close();
        crx.recv_timeout(Duration::from_secs(1)).unwrap();
        assert!(crx.recv_timeout(Duration::from_millis(100)).is_err());
        assert!(erx.recv_timeout(Duration::from_millis(100)).is_err());
    }
}
