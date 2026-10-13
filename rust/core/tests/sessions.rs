// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Session manager end to end: events, writes, host-key prompts, auto-reconnect.

use nexus_core::models::{Profile, Protocol, SessionState};
use nexus_core::secrets::{Keychain, Secrets, Vault};
use nexus_core::sessions::{Event, HostKeyDecision, Manager};
use nexus_core::storage::Store;
use parking_lot::Mutex;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::sync::Arc;
use std::sync::mpsc;
use std::time::Duration;

fn manager(name: &str) -> (Arc<Manager>, mpsc::Receiver<Event>, std::path::PathBuf) {
    let dir = std::env::temp_dir().join(format!("nexus-mgr-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    let store = Arc::new(Store::open(&dir.join("nexus.db")).unwrap());
    let secrets = Arc::new(Secrets { vault: Vault::new(dir.join("vault")), keychain: Keychain::new("NexusTerminalTest"), keychain_ok: false });
    let m = Manager::new(store, secrets, dir.join("recordings"));
    let (tx, rx) = mpsc::channel();
    let tx = Mutex::new(tx);
    m.set_sink(Arc::new(move |e| {
        let _ = tx.lock().send(e);
    }));
    (m, rx, dir)
}

/// Waits for an event matching `f`, answering host-key prompts with `accept`.
fn wait<T>(rx: &mpsc::Receiver<Event>, mut f: impl FnMut(&Event) -> Option<T>) -> T {
    let deadline = std::time::Instant::now() + Duration::from_secs(15);
    loop {
        let e = rx.recv_timeout(deadline.saturating_duration_since(std::time::Instant::now())).expect("timed out waiting for event");
        if let Some(t) = f(&e) {
            return t;
        }
        if let Event::HostKeyVerify { reply, .. } = e {
            let _ = reply.send(HostKeyDecision::AcceptAndSave);
        }
    }
}

fn state(e: &Event, want: SessionState) -> Option<String> {
    match e {
        Event::State { state, error, session, .. } if *state == want => Some(format!("{session}:{error}")),
        _ => None,
    }
}

#[cfg(unix)]
#[test]
fn local_shell_round_trip_and_restart() {
    let (m, rx, dir) = manager("local");
    let id = m.connect_local("/bin/sh");
    m.resize(&id, 100, 30).unwrap();
    wait(&rx, |e| state(e, SessionState::Connected));
    m.start_recording(&id, 100, 30).unwrap();
    m.write(&id, b"echo mgr-$((6*7))\n").unwrap();
    let mut seen = Vec::new();
    wait(&rx, |e| match e {
        Event::Data { data, .. } => {
            seen.extend_from_slice(data);
            String::from_utf8_lossy(&seen).contains("mgr-42").then_some(())
        }
        _ => None,
    });
    m.stop_recording(&id).unwrap();
    assert_eq!(m.recordings().list().unwrap().len(), 1);
    m.write(&id, b"exit\n").unwrap();
    wait(&rx, |e| matches!(e, Event::Closed { .. }).then_some(()));
    assert_eq!(m.info(&id).unwrap().state, SessionState::Disconnected);
    m.restart_local(&id, 80, 24).unwrap();
    wait(&rx, |e| state(e, SessionState::Connected));
    m.disconnect(&id).unwrap();
    assert!(m.info(&id).is_none());
    let _ = std::fs::remove_dir_all(dir);
}

#[test]
fn raw_session_and_auto_reconnect() {
    let (m, rx, dir) = manager("raw");
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = l.local_addr().unwrap().port();
    std::thread::spawn(move || {
        // First connection: echo one message then hang up; the second stays open.
        for (n, s) in l.incoming().flatten().enumerate() {
            let mut s = s;
            let mut buf = [0u8; 64];
            if let Ok(k) = s.read(&mut buf) {
                let _ = s.write_all(&buf[..k]);
            }
            if n > 0 {
                std::thread::sleep(Duration::from_secs(30));
            }
        }
    });
    let mut p = Profile::new(Protocol::Raw);
    p.host = "127.0.0.1".into();
    p.port = port as i64;
    p.raw.as_mut().unwrap().auto_reconnect = true;
    let id = m.connect(p);
    wait(&rx, |e| state(e, SessionState::Connected));
    m.raw_write_text(&id, "ping").unwrap();
    wait(&rx, |e| matches!(e, Event::RawData { direction, data, .. } if direction == "recv" && data.starts_with(b"ping")).then_some(()));
    // Server hung up: reconnecting with a backoff, then connected again under the same id.
    let retry = wait(&rx, |e| match e {
        Event::State { state: SessionState::Reconnecting, retry, .. } => *retry,
        _ => None,
    });
    assert_eq!((retry.attempt, retry.delay_secs), (1, 1));
    let back = wait(&rx, |e| state(e, SessionState::Connected));
    assert!(back.starts_with(&id));
    m.disconnect(&id).unwrap();
    let _ = std::fs::remove_dir_all(dir);
}

#[test]
fn connect_failure_reports_error_state() {
    let (m, rx, dir) = manager("fail");
    let port = TcpListener::bind("127.0.0.1:0").unwrap().local_addr().unwrap().port();
    let mut p = Profile::new(Protocol::Telnet);
    p.host = "127.0.0.1".into();
    p.port = port as i64;
    let id = m.connect(p);
    let err = wait(&rx, |e| state(e, SessionState::Error));
    assert!(err.starts_with(&id) && err.len() > id.len() + 1, "{err}");
    let _ = std::fs::remove_dir_all(dir);
}

/// NickServ identification happens on every registration, including auto-reconnects, and a
/// session-only NickServ password is asked once, not again for the reconnect.
#[test]
fn irc_reconnect_identifies_with_nickserv_again() {
    use std::io::{BufRead, BufReader};
    use std::sync::atomic::{AtomicUsize, Ordering};
    let (m, rx, dir) = manager("irc-nickserv");
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = l.local_addr().unwrap().port();
    let identified = Arc::new(AtomicUsize::new(0));
    let seen = identified.clone();
    std::thread::spawn(move || {
        for (n, s) in l.incoming().flatten().enumerate() {
            let mut w = s.try_clone().unwrap();
            let mut r = BufReader::new(s);
            let mut line = String::new();
            // Registration, then welcome.
            while r.read_line(&mut line).unwrap_or(0) > 0 {
                if line.starts_with("USER ") {
                    break;
                }
                line.clear();
            }
            w.write_all(b":srv 001 nexus :Welcome\r\n").unwrap();
            line.clear();
            while r.read_line(&mut line).unwrap_or(0) > 0 {
                if line == "PRIVMSG NickServ :IDENTIFY acct pw-once\r\n" {
                    seen.fetch_add(1, Ordering::SeqCst);
                    break;
                }
                line.clear();
            }
            // First connection: hang up so the session auto-reconnects. Second: stay open.
            if n == 0 {
                drop(w);
            } else {
                std::thread::sleep(Duration::from_secs(30));
            }
        }
    });
    let mut p = Profile::new(Protocol::Irc);
    p.host = "127.0.0.1".into();
    p.port = port as i64;
    let c = p.irc.as_mut().unwrap();
    c.nick_serv_identify = true;
    c.nick_serv_account = "acct".into();
    c.nick_serv_mode = nexus_core::models::SecretMode::SessionOnly;
    c.auto_reconnect = true;
    let id = m.connect(p);
    let prompts = Arc::new(AtomicUsize::new(0));
    let answer = |e: &Event| {
        if let Event::SecretPrompt { reply, .. } = e {
            prompts.fetch_add(1, Ordering::SeqCst);
            let _ = reply.send("pw-once".into());
        }
    };
    wait(&rx, |e| {
        answer(e);
        state(e, SessionState::Connected)
    });
    wait(&rx, |e| {
        answer(e);
        matches!(e, Event::State { state: SessionState::Reconnecting, .. }).then_some(())
    });
    wait(&rx, |e| {
        answer(e);
        state(e, SessionState::Connected)
    });
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    while identified.load(Ordering::SeqCst) < 2 && std::time::Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(identified.load(Ordering::SeqCst), 2, "IDENTIFY sent on both registrations");
    assert_eq!(prompts.load(Ordering::SeqCst), 1, "session-only password asked once");
    m.disconnect(&id).unwrap();
    let _ = std::fs::remove_dir_all(dir);
}
