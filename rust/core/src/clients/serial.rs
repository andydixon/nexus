// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Serial client (new protocol, no Go original) on the `serialport` crate. The device path is
//! `Profile::host`, line settings come from `Profile::serial`. Same conventions as the other
//! clients: blocking `connect`, an own reader thread, `on_close` exactly once (unplug included).

use parking_lot::Mutex;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use serialport::{DataBits, FlowControl, Parity, SerialPort, StopBits};

use super::Callbacks;
use crate::models::{self, LineEnding, Profile, SerialConfig};

/// The reader polls this often, so `close` stops it within this long.
const READ_POLL: Duration = Duration::from_millis(100);
const WRITE_TIMEOUT: Duration = Duration::from_secs(5);
const BREAK_DURATION: Duration = Duration::from_millis(250);

type OpenPort = (Box<dyn SerialPort>, Arc<AtomicBool>);

pub struct SerialClient {
    inner: Arc<Inner>,
}

struct Inner {
    path: String,
    cfg: SerialConfig,
    cb: Callbacks,
    /// The writer handle plus this connection's stop flag (the reader owns a clone of the port).
    port: Mutex<Option<OpenPort>>,
}

/// Device paths of the serial ports on this machine (empty if enumeration fails).
pub fn list_ports() -> Vec<String> {
    serialport::available_ports().map(|v| v.into_iter().map(|p| p.port_name).collect()).unwrap_or_default()
}

impl SerialClient {
    pub fn new(profile: &Profile, cb: Callbacks) -> SerialClient {
        SerialClient {
            inner: Arc::new(Inner { path: profile.host.trim().to_string(), cfg: profile.serial.clone().unwrap_or_default(), cb, port: Mutex::new(None) }),
        }
    }

    /// Opens and configures the port, applies DTR/RTS, and starts the reader thread.
    pub fn connect(&self) -> crate::Result<()> {
        let inner = &self.inner;
        if inner.port.lock().is_some() {
            return Err("already connected".into());
        }
        if inner.path.is_empty() {
            return Err("no serial device specified".into());
        }
        let c = &inner.cfg;
        let mut port = serialport::new(&inner.path, c.baud_rate)
            .data_bits(match c.data_bits {
                5 => DataBits::Five,
                6 => DataBits::Six,
                7 => DataBits::Seven,
                _ => DataBits::Eight,
            })
            .parity(match c.parity {
                models::Parity::None => Parity::None,
                models::Parity::Odd => Parity::Odd,
                models::Parity::Even => Parity::Even,
            })
            .stop_bits(if c.stop_bits == 2 { StopBits::Two } else { StopBits::One })
            .flow_control(match c.flow_control {
                models::FlowControl::None => FlowControl::None,
                models::FlowControl::Xonxoff => FlowControl::Software,
                models::FlowControl::Rtscts => FlowControl::Hardware,
            })
            .timeout(WRITE_TIMEOUT)
            .open()
            .map_err(|e| open_error(&inner.path, &e))?;
        // Opening asserts both lines on most OSes, so set them either way. Ptys and some adapters
        // have no modem lines; that's not fatal.
        let _ = port.write_data_terminal_ready(c.dtr_on_connect);
        let _ = port.write_request_to_send(c.rts_on_connect);

        let mut reader = port.try_clone()?;
        reader.set_timeout(READ_POLL)?;
        let stop = Arc::new(AtomicBool::new(false));
        {
            let mut guard = inner.port.lock();
            if guard.is_some() {
                return Err("already connected".into());
            }
            *guard = Some((port, stop.clone()));
        }
        let me = inner.clone();
        std::thread::spawn(super::guarded(inner.cb.on_error.clone(), inner.cb.on_close.clone(), move || me.read_loop(reader, stop)));
        Ok(())
    }

    /// Sends user input: the Enter key's `\r` becomes the configured line ending, text is encoded,
    /// and with local echo on the input is fed back through `on_data` (Enter shown as CRLF).
    pub fn write(&self, data: &[u8]) -> crate::Result<()> {
        let out = translate_enter(data, self.inner.cfg.line_ending);
        let out = if super::is_latin1(&self.inner.cfg.encoding) { super::utf8_to_latin1(&out) } else { out };
        self.with_port(|p| Ok(p.write_all(&out)?))?;
        if self.inner.cfg.local_echo {
            (self.inner.cb.on_data)(&translate_enter(data, LineEnding::Crlf));
        }
        Ok(())
    }

    pub fn set_lines(&self, dtr: bool, rts: bool) -> crate::Result<()> {
        self.with_port(|p| {
            p.write_data_terminal_ready(dtr)?;
            p.write_request_to_send(rts)?;
            Ok(())
        })
    }

    /// Holds the line in break for ~250 ms (blocks the caller, and other writes, meanwhile).
    pub fn send_break(&self) -> crate::Result<()> {
        self.with_port(|p| {
            p.set_break()?;
            std::thread::sleep(BREAK_DURATION);
            p.clear_break()?;
            Ok(())
        })
    }

    pub fn close(&self) {
        self.inner.close();
    }

    pub fn is_connected(&self) -> bool {
        self.inner.port.lock().is_some()
    }

    fn with_port(&self, f: impl FnOnce(&mut Box<dyn SerialPort>) -> crate::Result<()>) -> crate::Result<()> {
        let mut guard = self.inner.port.lock();
        let (port, _) = guard.as_mut().ok_or("not connected")?;
        f(port)
    }
}

impl Inner {
    fn read_loop(&self, mut reader: Box<dyn SerialPort>, stop: Arc<AtomicBool>) {
        let mut buf = [0u8; 4096];
        let latin1 = super::is_latin1(&self.cfg.encoding);
        while !stop.load(Ordering::SeqCst) {
            let err = match reader.read(&mut buf) {
                Ok(0) => "device disconnected".to_string(),
                Ok(n) if latin1 => {
                    (self.cb.on_data)(super::latin1_to_utf8(&buf[..n]).as_bytes());
                    continue;
                }
                Ok(n) => {
                    (self.cb.on_data)(&buf[..n]);
                    continue;
                }
                Err(e) if matches!(e.kind(), std::io::ErrorKind::TimedOut | std::io::ErrorKind::Interrupted) => continue,
                Err(e) => format!("device disconnected: {e}"),
            };
            if !stop.load(Ordering::SeqCst) {
                (self.cb.on_error)(format!("read error: {err}"));
                self.close();
            }
            return;
        }
    }

    fn close(&self) {
        let taken = self.port.lock().take();
        if let Some((port, stop)) = taken {
            stop.store(true, Ordering::SeqCst);
            drop(port);
            (self.cb.on_close)();
        }
    }
}

/// Replaces each `\r` (the Enter key) with `le`; `None` leaves it alone.
fn translate_enter(data: &[u8], le: LineEnding) -> Vec<u8> {
    if le == LineEnding::None {
        return data.to_vec();
    }
    let mut out = Vec::with_capacity(data.len() + 4);
    for &b in data {
        if b == b'\r' { out.extend_from_slice(le.bytes()) } else { out.push(b) }
    }
    out
}

fn open_error(path: &str, e: &serialport::Error) -> crate::Error {
    let desc = e.description.to_lowercase();
    let busy = ["busy", "lock", "access is denied", "in use"].iter().any(|s| desc.contains(s));
    let hint = match e.kind {
        serialport::ErrorKind::NoDevice if busy => " (the port is in use by another program)",
        serialport::ErrorKind::Io(std::io::ErrorKind::PermissionDenied) => {
            " (permission denied: add your user to the dialout (Linux) / uucp group, then log in again)"
        }
        _ => "",
    };
    format!("cannot open serial port {path}: {}{hint}", e.description).into()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(unix)]
    use std::sync::mpsc;

    #[test]
    fn enter_translation() {
        assert_eq!(translate_enter(b"AT\r", LineEnding::Crlf), b"AT\r\n");
        assert_eq!(translate_enter(b"AT\r", LineEnding::Lf), b"AT\n");
        assert_eq!(translate_enter(b"AT\r", LineEnding::Cr), b"AT\r");
        assert_eq!(translate_enter(b"AT\r", LineEnding::None), b"AT\r");
    }

    #[test]
    fn error_hints() {
        let perm = serialport::Error::new(serialport::ErrorKind::Io(std::io::ErrorKind::PermissionDenied), "Permission denied");
        assert!(open_error("/dev/ttyUSB0", &perm).to_string().contains("dialout"));
        let busy = serialport::Error::new(serialport::ErrorKind::NoDevice, "Device or resource busy");
        assert!(open_error("/dev/ttyUSB0", &busy).to_string().contains("in use by another program"));
        let missing = serialport::Error::new(serialport::ErrorKind::Io(std::io::ErrorKind::NotFound), "No such file or directory");
        assert_eq!(open_error("/dev/x", &missing).to_string(), "cannot open serial port /dev/x: No such file or directory");
    }

    #[test]
    fn not_connected() {
        let c = SerialClient::new(&Profile::new(models::Protocol::Serial), Callbacks::noop());
        assert!(c.write(b"x").is_err());
        assert_eq!(c.connect().unwrap_err().to_string(), "no serial device specified");
        let _ = list_ports();
    }

    /// A pty pair: the client opens the slave as its "device", the test plays the far end on the master.
    #[cfg(target_os = "linux")]
    mod pty {
        use super::*;
        use std::fs::File;
        use std::io::Write;
        use std::os::fd::{AsRawFd, FromRawFd};

        struct Pty {
            master: File,
            slave: File,
            path: String,
        }

        fn open_pty() -> Pty {
            let (mut m, mut s) = (0, 0);
            let mut name = [0 as libc::c_char; 128];
            let rc = unsafe { libc::openpty(&mut m, &mut s, name.as_mut_ptr(), std::ptr::null(), std::ptr::null()) };
            assert_eq!(rc, 0);
            let path = unsafe { std::ffi::CStr::from_ptr(name.as_ptr()) }.to_string_lossy().into_owned();
            Pty { master: unsafe { File::from_raw_fd(m) }, slave: unsafe { File::from_raw_fd(s) }, path }
        }

        /// Reads from the master until something arrives or `timeout` passes.
        fn read_master(master: &File, timeout: Duration) -> Vec<u8> {
            let mut pfd = libc::pollfd { fd: master.as_raw_fd(), events: libc::POLLIN, revents: 0 };
            if unsafe { libc::poll(&mut pfd, 1, timeout.as_millis() as i32) } != 1 {
                return Vec::new();
            }
            let mut buf = [0u8; 256];
            let n = std::io::Read::read(&mut &*master, &mut buf).unwrap();
            buf[..n].to_vec()
        }

        fn master_n(master: &File, n: usize) -> Vec<u8> {
            let mut got = Vec::new();
            while got.len() < n {
                let chunk = read_master(master, Duration::from_secs(5));
                assert!(!chunk.is_empty(), "timed out");
                got.extend(chunk);
            }
            got
        }

        fn recv_n(rx: &mpsc::Receiver<Vec<u8>>, n: usize) -> Vec<u8> {
            let mut got = Vec::new();
            while got.len() < n {
                got.extend(rx.recv_timeout(Duration::from_secs(5)).expect("timed out"));
            }
            got
        }

        fn client(pty: &Pty, cfg: SerialConfig) -> (SerialClient, mpsc::Receiver<Vec<u8>>, mpsc::Receiver<String>, mpsc::Receiver<()>) {
            let (dtx, drx) = mpsc::channel();
            let (etx, erx) = mpsc::channel();
            let (ctx, crx) = mpsc::channel();
            let cb = Callbacks {
                on_data: Arc::new(move |d| {
                    let _ = dtx.send(d.to_vec());
                }),
                on_error: Arc::new(move |e| {
                    let _ = etx.send(e);
                }),
                on_close: Arc::new(move || {
                    let _ = ctx.send(());
                }),
            };
            let profile = Profile { host: pty.path.clone(), serial: Some(cfg), ..Profile::new(models::Protocol::Serial) };
            (SerialClient::new(&profile, cb), drx, erx, crx)
        }

        #[test]
        fn data_echo_line_endings_and_close() {
            let mut pty = open_pty();
            let cfg = SerialConfig { local_echo: true, line_ending: LineEnding::Crlf, ..Default::default() };
            let (c, data, errors, closed) = client(&pty, cfg);
            c.connect().unwrap();
            assert!(c.connect().is_err());

            c.write(b"AT\r").unwrap();
            assert_eq!(master_n(&pty.master, 4), b"AT\r\n");
            assert_eq!(recv_n(&data, 4), b"AT\r\n"); // local echo

            pty.master.write_all(b"OK\r\n").unwrap();
            assert_eq!(recv_n(&data, 4), b"OK\r\n");

            c.close();
            c.close();
            closed.recv_timeout(Duration::from_secs(1)).unwrap();
            assert!(closed.recv_timeout(Duration::from_millis(300)).is_err());
            assert!(errors.try_recv().is_err());
            assert!(c.write(b"x").is_err());
        }

        #[test]
        fn latin1_encoding() {
            let mut pty = open_pty();
            let cfg = SerialConfig { encoding: "latin1".into(), line_ending: LineEnding::Lf, ..Default::default() };
            let (c, data, _e, _c) = client(&pty, cfg);
            c.connect().unwrap();
            c.write("é\r".as_bytes()).unwrap();
            assert_eq!(master_n(&pty.master, 2), [0xe9, b'\n']);
            pty.master.write_all(&[0xe9]).unwrap();
            assert_eq!(recv_n(&data, 2), "é".as_bytes());
        }

        #[test]
        fn xon_xoff_flow_control() {
            let mut pty = open_pty();
            let cfg = SerialConfig { flow_control: models::FlowControl::Xonxoff, line_ending: LineEnding::None, ..Default::default() };
            let (c, _d, _e, _c) = client(&pty, cfg);
            c.connect().unwrap();
            pty.master.write_all(&[0x13]).unwrap(); // XOFF
            std::thread::sleep(Duration::from_millis(100));
            let c = Arc::new(c);
            let w = c.clone();
            let writer = std::thread::spawn(move || w.write(b"x"));
            assert!(read_master(&pty.master, Duration::from_millis(300)).is_empty(), "output not paused by XOFF");
            pty.master.write_all(&[0x11]).unwrap(); // XON
            assert_eq!(master_n(&pty.master, 1), b"x");
            writer.join().unwrap().unwrap();
        }

        #[test]
        fn unplug_reports_error_and_closes() {
            let pty = open_pty();
            let (c, _d, errors, closed) = client(&pty, SerialConfig::default());
            c.connect().unwrap();
            drop(pty.master);
            drop(pty.slave);
            let err = errors.recv_timeout(Duration::from_secs(5)).unwrap();
            assert!(err.starts_with("read error: device disconnected"), "{err}");
            closed.recv_timeout(Duration::from_secs(1)).unwrap();
            assert!(!c.is_connected());
        }
    }
}
