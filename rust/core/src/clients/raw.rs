// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! RAW TCP client (port of Go `internal/rawclient`): binary-safe send/receive, a capped history
//! of sent ("send") and received ("recv") frames, JSON capture, and the hex/escape encoders the
//! RAW view reuses.

use parking_lot::Mutex;
use std::collections::VecDeque;
use std::sync::Arc;

use base64::Engine;
use serde::Serialize;

use super::{CloseFn, ErrorFn, TcpConn};
use crate::models::{LineEnding, Profile, Time};

pub const MAX_HISTORY_ENTRIES: usize = 10_000;
pub const MAX_HISTORY_BYTES: usize = 10 * 1024 * 1024;

pub const SEND: &str = "send";
pub const RECV: &str = "recv";

/// `(data, direction, timestamp in unix ms)`; direction is [`SEND`] or [`RECV`].
pub type RawDataFn = Arc<dyn Fn(&[u8], &str, i64) + Send + Sync>;

/// One sent or received frame. Serialises like Go's (RFC 3339 timestamp, base64 data).
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct HistoryEntry {
    pub timestamp: Time,
    pub direction: String,
    #[serde(serialize_with = "ser_b64")]
    pub data: Vec<u8>,
}

fn ser_b64<S: serde::Serializer>(d: &[u8], s: S) -> Result<S::Ok, S::Error> {
    s.serialize_str(&base64::engine::general_purpose::STANDARD.encode(d))
}

pub struct RawClient {
    inner: Arc<Inner>,
}

struct Inner {
    conn: Arc<TcpConn>,
    host: String,
    port: i64,
    connect_timeout: i64,
    line_ending: LineEnding,
    on_data: RawDataFn,
    history: Mutex<History>,
}

#[derive(Default)]
struct History {
    entries: VecDeque<HistoryEntry>,
    bytes: usize,
}

impl RawClient {
    pub fn new(profile: &Profile, on_data: RawDataFn, on_error: ErrorFn, on_close: CloseFn) -> RawClient {
        RawClient {
            inner: Arc::new(Inner {
                conn: Arc::new(TcpConn::new(on_error, on_close)),
                host: profile.host.clone(),
                port: profile.port,
                connect_timeout: profile.connect_timeout,
                line_ending: profile.raw.as_ref().map(|r| r.line_ending).unwrap_or_default(),
                on_data,
                history: Mutex::default(),
            }),
        }
    }

    pub fn connect(&self) -> crate::Result<()> {
        let inner = &self.inner;
        if inner.conn.is_connected() {
            return Err("already connected".into());
        }
        if inner.port == 0 {
            return Err("port must be specified for raw TCP connections".into());
        }
        let (addr, res) = super::dial(&inner.host, inner.port, inner.connect_timeout);
        let stream = res.map_err(|e| format!("raw TCP connection to {addr} failed: {e}"))?;
        let me = inner.clone();
        inner.conn.connect(stream, move |d| me.record(RECV, d))
    }

    /// Sends bytes as-is, then records them and reports them through `on_data` as "send".
    pub fn write(&self, data: &[u8]) -> crate::Result<()> {
        self.inner.conn.write(data)?;
        self.inner.record(SEND, data);
        Ok(())
    }

    /// Sends text with the configured line ending appended (unless it already ends with it).
    pub fn write_text(&self, text: &str) -> crate::Result<()> {
        self.write(&apply_line_ending(text, self.inner.line_ending))
    }

    /// Sends hex such as "48656c6c6f", "48 65 6c" or "0x48 0x65".
    pub fn write_hex(&self, hex: &str) -> crate::Result<()> {
        let data = parse_hex_string(hex).map_err(|e| format!("invalid hex input: {e}"))?;
        self.write(&data)
    }

    /// Sends text after processing C-style escapes (see [`parse_escaped`]).
    pub fn write_escaped(&self, text: &str) -> crate::Result<()> {
        let data = parse_escaped(text).map_err(|e| format!("escape processing failed: {e}"))?;
        self.write(&data)
    }

    pub fn close(&self) {
        self.inner.conn.close();
    }

    pub fn is_connected(&self) -> bool {
        self.inner.conn.is_connected()
    }

    pub fn history(&self) -> Vec<HistoryEntry> {
        self.inner.history.lock().entries.iter().cloned().collect()
    }

    pub fn clear_history(&self) {
        *self.inner.history.lock() = History::default();
    }

    /// Writes the history to `path` as indented JSON (mode 0600 on Unix), as Go did.
    pub fn save_capture(&self, path: &std::path::Path) -> crate::Result<()> {
        let json = serde_json::to_vec_pretty(&self.history()).map_err(|e| format!("failed to marshal capture data: {e}"))?;
        let mut opts = std::fs::OpenOptions::new();
        opts.write(true).create(true).truncate(true);
        #[cfg(unix)]
        std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
        std::io::Write::write_all(&mut opts.open(path)?, &json)?;
        Ok(())
    }
}

impl Inner {
    fn record(&self, direction: &str, data: &[u8]) {
        let timestamp = crate::models::now();
        let ms = timestamp.timestamp_millis();
        self.append_history(HistoryEntry { timestamp, direction: direction.into(), data: data.to_vec() });
        (self.on_data)(data, direction, ms);
    }

    fn append_history(&self, mut entry: HistoryEntry) {
        if entry.data.len() > MAX_HISTORY_BYTES {
            entry.data.drain(..entry.data.len() - MAX_HISTORY_BYTES);
        }
        let mut h = self.history.lock();
        h.bytes += entry.data.len();
        h.entries.push_back(entry);
        while h.entries.len() > MAX_HISTORY_ENTRIES || h.bytes > MAX_HISTORY_BYTES {
            let Some(old) = h.entries.pop_front() else { break };
            h.bytes -= old.data.len();
        }
    }
}

fn apply_line_ending(text: &str, le: LineEnding) -> Vec<u8> {
    let suffix = le.bytes();
    let mut out = text.as_bytes().to_vec();
    if !suffix.is_empty() && !out.ends_with(suffix) {
        out.extend_from_slice(suffix);
    }
    out
}

/// Parses hex with optional whitespace and 0x/0X prefixes into bytes.
pub fn parse_hex_string(s: &str) -> crate::Result<Vec<u8>> {
    let s = s.trim();
    if s.is_empty() {
        return Err("empty hex string".into());
    }
    let s = s.replace("0x", "").replace("0X", "");
    let mut digits = Vec::new();
    for c in s.chars().filter(|c| !c.is_whitespace()) {
        let d = c.to_digit(16).ok_or_else(|| format!("invalid character in hex string: {c:?}"))?;
        digits.push(d as u8);
    }
    if digits.len() % 2 != 0 {
        return Err("hex string has odd number of digits".into());
    }
    Ok(digits.chunks(2).map(|p| (p[0] << 4) | p[1]).collect())
}

/// Space-separated lowercase hex, e.g. "48 65 6c 6c 6f".
pub fn format_hex(data: &[u8]) -> String {
    data.iter().map(|b| format!("{b:02x}")).collect::<Vec<_>>().join(" ")
}

/// Classic 16-bytes-per-line hex dump with offset, hex and ASCII columns.
pub fn format_hex_dump(data: &[u8]) -> String {
    use std::fmt::Write;
    let mut sb = String::new();
    for (line, chunk) in data.chunks(16).enumerate() {
        let _ = write!(sb, "{:08x}  ", line * 16);
        for i in 0..16 {
            if i == 8 {
                sb.push(' ');
            }
            match chunk.get(i) {
                Some(b) => {
                    let _ = write!(sb, "{b:02x} ");
                }
                None => sb.push_str("   "),
            }
        }
        sb.push_str(" |");
        sb.extend(chunk.iter().map(|&b| if (0x20..=0x7e).contains(&b) { b as char } else { '.' }));
        sb.extend(std::iter::repeat_n(' ', 16 - chunk.len()));
        sb.push_str("|\n");
    }
    sb
}

/// Processes C-style escapes: `\r \n \t \\ \0 \xNN` (and `\XNN`); anything else is an error.
pub fn parse_escaped(s: &str) -> crate::Result<Vec<u8>> {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] != b'\\' {
            out.push(b[i]);
            i += 1;
            continue;
        }
        let Some(&next) = b.get(i + 1) else {
            return Err(format!("trailing backslash at position {i}").into());
        };
        let simple = match next {
            b'r' => Some(b'\r'),
            b'n' => Some(b'\n'),
            b't' => Some(b'\t'),
            b'\\' => Some(b'\\'),
            b'0' => Some(0),
            _ => None,
        };
        if let Some(v) = simple {
            out.push(v);
            i += 2;
        } else if next == b'x' || next == b'X' {
            if i + 3 >= b.len() {
                return Err(format!("incomplete \\x escape at position {i}").into());
            }
            let hex = &b[i + 2..i + 4];
            let v = std::str::from_utf8(hex)
                .ok()
                .filter(|h| h.bytes().all(|c| c.is_ascii_hexdigit()))
                .and_then(|h| u8::from_str_radix(h, 16).ok())
                .ok_or_else(|| format!("invalid \\x escape at position {i}: {}", String::from_utf8_lossy(hex)))?;
            out.push(v);
            i += 4;
        } else {
            let c = s[i + 1..].chars().next().unwrap_or('?');
            return Err(format!("unknown escape sequence \\{c} at position {i}").into());
        }
    }
    Ok(out)
}

/// Bytes → display text: "ascii" (non-printables except \n \r \t become '.'), "iso-8859-1" /
/// "latin1" / "latin-1", otherwise UTF-8 with invalid bytes replaced by U+FFFD.
pub fn to_display_encoding(data: &[u8], encoding: &str) -> String {
    match encoding.trim().to_ascii_lowercase().as_str() {
        "ascii" => data.iter().map(|&b| if (0x20..=0x7e).contains(&b) || matches!(b, b'\n' | b'\r' | b'\t') { b as char } else { '.' }).collect(),
        e if super::is_latin1(e) => super::latin1_to_utf8(data),
        _ => String::from_utf8_lossy(data).into_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::mpsc;
    use std::time::Duration;

    fn client() -> RawClient {
        RawClient::new(&Profile::new(crate::models::Protocol::Raw), Arc::new(|_, _, _| {}), Arc::new(|_| {}), Arc::new(|| {}))
    }

    #[test]
    fn parse_hex_string_cases() {
        let ok: [(&str, &[u8]); 5] =
            [("48656c6c6f", b"Hello"), ("48 65 6c 6c 6f", b"Hello"), ("0x48 0x65 0x6c 0x6c 0x6f", b"Hello"), ("0000", &[0, 0]), ("ff ff", &[0xff, 0xff])];
        for (input, want) in ok {
            assert_eq!(parse_hex_string(input).unwrap(), want, "{input:?}");
        }
        for input in ["", "ZZZZ", "abc"] {
            assert!(parse_hex_string(input).is_err(), "{input:?}");
        }
    }

    #[test]
    fn parse_escaped_cases() {
        let ok: [(&str, &[u8]); 9] = [
            ("hello", b"hello"),
            (r"\n", b"\n"),
            (r"\r\n", b"\r\n"),
            (r"\t", b"\t"),
            (r"\0", &[0]),
            (r"\xff", &[0xff]),
            (r"\\", b"\\"),
            (r"AT\r\n", b"AT\r\n"),
            (r"\x00", &[0]),
        ];
        for (input, want) in ok {
            assert_eq!(parse_escaped(input).unwrap(), want, "{input:?}");
        }
        for input in [r"hello\", r"\q", r"\x0", r"\x+f"] {
            assert!(parse_escaped(input).is_err(), "{input:?}");
        }
    }

    #[test]
    fn format_hex_dump_cases() {
        assert_eq!(format_hex_dump(&[]), "");
        let got = format_hex_dump(b"Hi");
        assert!(got.contains("48 69") && got.contains("|Hi"), "{got}");
        let got = format_hex_dump(b"ABCDEFGHIJKLMNOPQRST");
        let lines: Vec<_> = got.trim_end_matches('\n').split('\n').collect();
        assert_eq!(lines.len(), 2);
        assert!(lines[0].starts_with("00000000") && lines[1].starts_with("00000010"));
        assert_eq!(lines[0], "00000000  41 42 43 44 45 46 47 48  49 4a 4b 4c 4d 4e 4f 50  |ABCDEFGHIJKLMNOP|");
        assert_eq!(lines[1].len(), lines[0].len());
    }

    #[test]
    fn format_hex_cases() {
        assert_eq!(format_hex(&[]), "");
        assert_eq!(format_hex(&[0x42]), "42");
        assert_eq!(format_hex(b"Hello"), "48 65 6c 6c 6f");
    }

    #[test]
    fn to_display_encoding_cases() {
        assert_eq!(to_display_encoding(b"Hello, World!", "utf-8"), "Hello, World!");
        assert_eq!(to_display_encoding(&[0x80, 0x41], "ascii"), ".A");
        assert_eq!(to_display_encoding(&[0xe9], "latin-1"), "é");
        assert_eq!(to_display_encoding(&[0xe9], "iso-8859-1"), "é");
    }

    #[test]
    fn history_is_capped_by_entry_count() {
        let c = client();
        for i in 0..MAX_HISTORY_ENTRIES + 25 {
            c.inner.record(RECV, &[i as u8]);
        }
        assert_eq!(c.history().len(), MAX_HISTORY_ENTRIES);
    }

    #[test]
    fn history_is_capped_by_bytes() {
        let c = client();
        let chunk = vec![b'a'; 1024];
        for _ in 0..MAX_HISTORY_BYTES / chunk.len() + 10 {
            c.inner.record(RECV, &chunk);
        }
        let total: usize = c.history().iter().map(|e| e.data.len()).sum();
        assert!(total <= MAX_HISTORY_BYTES);
        c.clear_history();
        assert!(c.history().is_empty());
    }

    #[test]
    fn line_ending_is_appended_once() {
        assert_eq!(apply_line_ending("AT", LineEnding::Crlf), b"AT\r\n");
        assert_eq!(apply_line_ending("AT\r\n", LineEnding::Crlf), b"AT\r\n");
        assert_eq!(apply_line_ending("AT", LineEnding::Cr), b"AT\r");
        assert_eq!(apply_line_ending("AT", LineEnding::None), b"AT");
    }

    #[test]
    fn send_receive_history_and_capture() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let profile = Profile { host: "127.0.0.1".into(), port: listener.local_addr().unwrap().port() as i64, ..Profile::new(crate::models::Protocol::Raw) };
        let (tx, rx) = mpsc::channel::<(Vec<u8>, String, i64)>();
        let (ctx, crx) = mpsc::channel::<()>();
        let c = RawClient::new(
            &profile,
            Arc::new(move |d, dir, ts| tx.send((d.to_vec(), dir.to_string(), ts)).unwrap()),
            Arc::new(|_| {}),
            Arc::new(move || ctx.send(()).unwrap()),
        );
        assert!(c.write(b"x").is_err());
        c.connect().unwrap();
        let (mut srv, _) = listener.accept().unwrap();
        srv.set_read_timeout(Some(Duration::from_secs(5))).unwrap();

        c.write_text("AT").unwrap();
        c.write_hex("01 ff").unwrap();
        c.write_escaped(r"\x00z").unwrap();
        assert!(c.write_hex("zz").unwrap_err().to_string().starts_with("invalid hex input"));
        let mut buf = [0u8; 8];
        srv.read_exact(&mut buf).unwrap();
        assert_eq!(&buf, b"AT\r\n\x01\xff\x00z");

        srv.write_all(b"OK").unwrap();
        let mut events = Vec::new();
        while events.iter().map(|(d, dir, _): &(Vec<u8>, String, i64)| if dir == RECV { d.len() } else { 0 }).sum::<usize>() < 2 {
            events.push(rx.recv_timeout(Duration::from_secs(5)).unwrap());
        }
        assert_eq!(events[0].1, SEND);
        assert!(events[0].2 > 1_600_000_000_000);
        let hist = c.history();
        assert_eq!(hist.iter().filter(|e| e.direction == SEND).count(), 3);
        assert_eq!(hist.last().unwrap().direction, RECV);

        let path = std::env::temp_dir().join(format!("nexus-raw-capture-{}.json", std::process::id()));
        c.save_capture(&path).unwrap();
        let v: serde_json::Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        std::fs::remove_file(&path).unwrap();
        assert_eq!(v[0]["direction"], "send");
        assert_eq!(v[0]["data"], "QVQNCg=="); // "AT\r\n"
        assert!(v[0]["timestamp"].as_str().unwrap().contains('T'));

        c.close();
        c.close();
        crx.recv_timeout(Duration::from_secs(1)).unwrap();
        assert!(crx.recv_timeout(Duration::from_millis(100)).is_err());
    }
}
