// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Outbound proxies (SOCKS5, HTTP CONNECT) for the clients that dial TCP themselves: SSH and RDP.
//! Blocking; async callers run it on a blocking thread.

use crate::Result;
use base64::Engine;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::time::Duration;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Proxy {
    /// "socks5" or "http"; anything else (including "none" and "") dials directly.
    pub kind: String,
    pub host: String,
    pub port: u16,
    pub username: String,
    pub password: String,
}

impl Proxy {
    pub fn is_direct(&self) -> bool {
        !matches!(self.kind.to_lowercase().as_str(), "socks5" | "http")
    }

    /// A TCP stream to `host:port`, through the proxy when one is configured.
    pub fn connect(&self, host: &str, port: u16, timeout: Duration) -> Result<TcpStream> {
        match self.kind.to_lowercase().as_str() {
            "socks5" => self.socks5(host, port, timeout),
            "http" => self.http(host, port, timeout),
            _ => tcp(host, port, timeout).map_err(|e| format!("dial tcp {host}:{port}: {e}").into()),
        }
    }

    fn open(&self, default_port: u16, what: &str, timeout: Duration) -> Result<TcpStream> {
        let port = if self.port == 0 { default_port } else { self.port };
        let s = tcp(&self.host, port, timeout).map_err(|e| format!("{what} proxy connection failed: {e}"))?;
        // The handshake must not hang on a silent proxy either.
        s.set_read_timeout(Some(timeout))?;
        Ok(s)
    }

    fn socks5(&self, host: &str, port: u16, timeout: Duration) -> Result<TcpStream> {
        let mut s = self.open(1080, "SOCKS5", timeout)?;
        let auth = !self.username.is_empty();
        s.write_all(if auth { &[5, 2, 0, 2] } else { &[5, 1, 0] })?;
        let mut r = [0u8; 2];
        s.read_exact(&mut r)?;
        match r[1] {
            0 => {}
            2 if auth => {
                s.write_all(&socks5_auth_request(&self.username, &self.password)?)?;
                s.read_exact(&mut r)?;
                if r[1] != 0 {
                    return Err("SOCKS5 proxy: authentication failed".into());
                }
            }
            _ => return Err("SOCKS5 proxy: no acceptable authentication method".into()),
        }
        s.write_all(&socks5_connect_request(host, port)?)?;
        let mut head = [0u8; 4];
        s.read_exact(&mut head)?;
        if head[1] != 0 {
            return Err(format!("SOCKS5 proxy connection to {host}:{port} failed: reply code {}", head[1]).into());
        }
        let addr_len = match head[3] {
            1 => 4,
            4 => 16,
            3 => {
                let mut n = [0u8; 1];
                s.read_exact(&mut n)?;
                n[0] as usize
            }
            _ => 0,
        };
        let mut rest = vec![0u8; addr_len + 2];
        s.read_exact(&mut rest)?;
        s.set_read_timeout(None)?;
        Ok(s)
    }

    fn http(&self, host: &str, port: u16, timeout: Duration) -> Result<TcpStream> {
        let mut s = self.open(8080, "HTTP", timeout)?;
        let mut req = format!("CONNECT {host}:{port} HTTP/1.1\r\nHost: {host}:{port}\r\n");
        if !self.username.is_empty() {
            let cred = base64::engine::general_purpose::STANDARD.encode(format!("{}:{}", self.username, self.password));
            req += &format!("Proxy-Authorization: Basic {cred}\r\n");
        }
        req += "\r\n";
        s.write_all(req.as_bytes())?;
        // Read the response head byte by byte so no tunnelled bytes are consumed.
        let mut head = Vec::new();
        let mut b = [0u8; 1];
        while !head.ends_with(b"\r\n\r\n") {
            s.read_exact(&mut b).map_err(|e| format!("HTTP CONNECT response read failed: {e}"))?;
            head.push(b[0]);
            if head.len() > 16 * 1024 {
                return Err("HTTP CONNECT response too large".into());
            }
        }
        let status = String::from_utf8_lossy(&head).lines().next().unwrap_or("").to_string();
        if status.split_whitespace().nth(1) != Some("200") {
            return Err(format!("HTTP CONNECT proxy returned {status}").into());
        }
        s.set_read_timeout(None)?;
        Ok(s)
    }
}

fn tcp(host: &str, port: u16, timeout: Duration) -> std::io::Result<TcpStream> {
    use std::net::ToSocketAddrs;
    let mut last = std::io::Error::new(std::io::ErrorKind::NotFound, "no addresses found");
    for sa in (host, port).to_socket_addrs()? {
        match TcpStream::connect_timeout(&sa, timeout) {
            Ok(s) => return Ok(s),
            Err(e) => last = e,
        }
    }
    Err(last)
}

/// RFC 1929 username/password sub-negotiation. Both fields carry a one-byte length, so longer
/// values are rejected rather than silently truncated into a malformed request.
fn socks5_auth_request(user: &str, pass: &str) -> Result<Vec<u8>> {
    let ulen = u8::try_from(user.len()).map_err(|_| "SOCKS5 proxy: username longer than 255 bytes")?;
    let plen = u8::try_from(pass.len()).map_err(|_| "SOCKS5 proxy: password longer than 255 bytes")?;
    let mut m = vec![1, ulen];
    m.extend(user.as_bytes());
    m.push(plen);
    m.extend(pass.as_bytes());
    Ok(m)
}

/// RFC 1928 CONNECT with a domain-name address (one-byte length).
fn socks5_connect_request(host: &str, port: u16) -> Result<Vec<u8>> {
    let hlen = u8::try_from(host.len()).map_err(|_| "SOCKS5 proxy: host name longer than 255 bytes")?;
    let mut req = vec![5, 1, 0, 3, hlen];
    req.extend(host.as_bytes());
    req.extend(port.to_be_bytes());
    Ok(req)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::TcpListener;

    #[test]
    fn socks5_encoding() {
        assert_eq!(socks5_connect_request("example.com", 22).unwrap(), [&[5u8, 1, 0, 3, 11][..], b"example.com", &[0, 22]].concat());
        assert!(socks5_connect_request(&"h".repeat(256), 22).is_err());
        assert_eq!(socks5_auth_request("u", "pw").unwrap(), [1, 1, b'u', 2, b'p', b'w']);
        assert!(socks5_auth_request(&"u".repeat(256), "").is_err());
    }

    /// A one-shot proxy that checks the handshake, then echoes.
    fn fake_proxy(handshake: impl FnOnce(&mut TcpStream) + Send + 'static) -> u16 {
        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = l.local_addr().unwrap().port();
        std::thread::spawn(move || {
            let (mut s, _) = l.accept().unwrap();
            handshake(&mut s);
            let mut buf = [0u8; 4];
            s.read_exact(&mut buf).unwrap();
            s.write_all(&buf).unwrap();
        });
        port
    }

    fn echo_through(p: &Proxy) {
        let mut s = p.connect("target.example", 3389, Duration::from_secs(5)).unwrap();
        s.write_all(b"ping").unwrap();
        let mut buf = [0u8; 4];
        s.read_exact(&mut buf).unwrap();
        assert_eq!(&buf, b"ping");
    }

    #[test]
    fn socks5_with_auth_reaches_the_target() {
        let port = fake_proxy(|s| {
            let mut b = [0u8; 4];
            s.read_exact(&mut b).unwrap();
            assert_eq!(b, [5, 2, 0, 2]);
            s.write_all(&[5, 2]).unwrap();
            let mut auth = [0u8; 7];
            s.read_exact(&mut auth).unwrap();
            assert_eq!(auth, [1, 1, b'u', 3, b'p', b'w', b'd']);
            s.write_all(&[1, 0]).unwrap();
            let mut req = vec![0u8; 5 + 14 + 2];
            s.read_exact(&mut req).unwrap();
            assert_eq!(&req[5..19], b"target.example");
            assert_eq!(&req[19..], &3389u16.to_be_bytes());
            s.write_all(&[5, 0, 0, 1, 127, 0, 0, 1, 0, 0]).unwrap();
        });
        echo_through(&Proxy { kind: "socks5".into(), host: "127.0.0.1".into(), port, username: "u".into(), password: "pwd".into() });
    }

    #[test]
    fn http_connect_sends_basic_auth_and_needs_200() {
        let port = fake_proxy(|s| {
            let mut head = Vec::new();
            let mut b = [0u8; 1];
            while !head.ends_with(b"\r\n\r\n") {
                s.read_exact(&mut b).unwrap();
                head.push(b[0]);
            }
            let head = String::from_utf8(head).unwrap();
            assert!(head.starts_with("CONNECT target.example:3389 HTTP/1.1\r\n"), "{head}");
            assert!(head.contains("Proxy-Authorization: Basic dTpwd2Q=\r\n"), "{head}");
            s.write_all(b"HTTP/1.1 200 Connection established\r\n\r\n").unwrap();
        });
        echo_through(&Proxy { kind: "http".into(), host: "127.0.0.1".into(), port, username: "u".into(), password: "pwd".into() });

        let l = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = l.local_addr().unwrap().port();
        std::thread::spawn(move || {
            let (mut s, _) = l.accept().unwrap();
            let mut buf = [0u8; 256];
            let _ = s.read(&mut buf);
            s.write_all(b"HTTP/1.1 407 Proxy Authentication Required\r\n\r\n").unwrap();
        });
        let err = Proxy { kind: "http".into(), host: "127.0.0.1".into(), port, ..Default::default() }.connect("t", 1, Duration::from_secs(5)).unwrap_err();
        assert!(err.to_string().contains("407"), "{err}");
    }
}
