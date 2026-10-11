//! Protocol clients. Conventions, shared by every client so the session manager treats them alike:
//! - `connect()` blocks the calling thread (the manager calls it from a worker thread, as Go
//!   called it from a goroutine) and returns once the connection is usable.
//! - After connecting, the client owns its own reader thread(s) and reports through callbacks:
//!   data, a fatal error, and close (exactly once, whether remote or local).
//! - `write`/`resize`/`close` are callable from any thread; `close` is idempotent and must not
//!   fire `on_close` re-entrantly while holding locks.

pub mod local;
pub mod sftp;
pub mod ssh;

use std::sync::{Arc, OnceLock};

/// The shared tokio runtime for async protocol stacks (russh). Blocking clients use threads.
pub fn rt() -> &'static tokio::runtime::Runtime {
    static RT: OnceLock<tokio::runtime::Runtime> = OnceLock::new();
    RT.get_or_init(|| {
        tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all().thread_name("nexus-net").build().expect("tokio runtime")
    })
}

pub type DataFn = Arc<dyn Fn(&[u8]) + Send + Sync>;
pub type ErrorFn = Arc<dyn Fn(String) + Send + Sync>;
pub type CloseFn = Arc<dyn Fn() + Send + Sync>;

#[derive(Clone)]
pub struct Callbacks {
    pub on_data: DataFn,
    pub on_error: ErrorFn,
    pub on_close: CloseFn,
}

impl Callbacks {
    pub fn noop() -> Callbacks {
        Callbacks { on_data: Arc::new(|_| {}), on_error: Arc::new(|_| {}), on_close: Arc::new(|| {}) }
    }
}

pub mod raw;
pub mod serial;
pub mod telnet;

/// Called with protocol negotiation debug messages (Go's `onDebug`).
pub type DebugFn = Arc<dyn Fn(String) + Send + Sync>;

/// TCP dial shared by the Telnet and RAW clients: Go's `net.DialTimeout` with a 30 s default.
/// Returns the stream and the `host:port` string used in error messages.
pub(crate) fn dial(host: &str, port: i64, timeout_secs: i64) -> (String, std::io::Result<std::net::TcpStream>) {
    use std::net::{TcpStream, ToSocketAddrs};
    let timeout = std::time::Duration::from_secs(if timeout_secs > 0 { timeout_secs as u64 } else { 30 });
    // Go's net.JoinHostPort brackets IPv6 literals.
    let addr = if host.contains(':') { format!("[{host}]:{port}") } else { format!("{host}:{port}") };
    let result = (|| {
        let mut last = std::io::Error::new(std::io::ErrorKind::NotFound, "no addresses found");
        for sa in addr.to_socket_addrs()? {
            match TcpStream::connect_timeout(&sa, timeout) {
                Ok(s) => return Ok(s),
                Err(e) => last = e,
            }
        }
        Err(last)
    })();
    (addr, result)
}

/// The TCP connection lifecycle shared by the Telnet and RAW clients: one writer-side stream behind
/// a lock, a reader thread that feeds `on_read`, and Go's error/close semantics (a read error or EOF
/// reports `read error: …` then closes; `close` is idempotent and fires `on_close` once).
pub(crate) struct TcpConn {
    stream: std::sync::Mutex<Option<std::net::TcpStream>>,
    on_error: ErrorFn,
    on_close: CloseFn,
}

impl TcpConn {
    pub(crate) fn new(on_error: ErrorFn, on_close: CloseFn) -> TcpConn {
        TcpConn { stream: std::sync::Mutex::new(None), on_error, on_close }
    }

    pub(crate) fn connect(
        self: &Arc<Self>,
        stream: std::net::TcpStream,
        mut on_read: impl FnMut(&[u8]) + Send + 'static,
    ) -> crate::Result<()> {
        let mut reader = stream.try_clone()?;
        {
            let mut guard = self.stream.lock().unwrap();
            if guard.is_some() {
                return Err("already connected".into());
            }
            *guard = Some(stream);
        }
        let me = self.clone();
        std::thread::spawn(move || {
            use std::io::Read;
            let mut buf = [0u8; 4096];
            loop {
                let err = match reader.read(&mut buf) {
                    Ok(0) => "EOF".to_string(),
                    Ok(n) => {
                        on_read(&buf[..n]);
                        continue;
                    }
                    Err(e) if e.kind() == std::io::ErrorKind::Interrupted => continue,
                    Err(e) => e.to_string(),
                };
                // A local close also ends the read; Go reported that as an error too, we don't.
                if me.is_connected() {
                    (me.on_error)(format!("read error: {err}"));
                    me.close();
                }
                return;
            }
        });
        Ok(())
    }

    pub(crate) fn write(&self, data: &[u8]) -> crate::Result<()> {
        use std::io::Write;
        let guard = self.stream.lock().unwrap();
        let mut s = guard.as_ref().ok_or("not connected")?;
        s.write_all(data)?;
        Ok(())
    }

    pub(crate) fn is_connected(&self) -> bool {
        self.stream.lock().unwrap().is_some()
    }

    pub(crate) fn close(&self) {
        let taken = self.stream.lock().unwrap().take();
        if let Some(s) = taken {
            let _ = s.shutdown(std::net::Shutdown::Both);
            (self.on_close)();
        }
    }
}

/// True for the encoding names Go mapped to ISO 8859-1.
pub(crate) fn is_latin1(encoding: &str) -> bool {
    matches!(encoding.trim().to_ascii_lowercase().as_str(), "iso-8859-1" | "latin1" | "latin-1")
}

/// UTF-8 → ISO 8859-1. Like Go's charmap encoder, input that isn't valid UTF-8 or holds a
/// character above U+00FF is returned unchanged.
pub(crate) fn utf8_to_latin1(data: &[u8]) -> Vec<u8> {
    std::str::from_utf8(data)
        .ok()
        .and_then(|s| s.chars().map(|c| u8::try_from(c).ok()).collect())
        .unwrap_or_else(|| data.to_vec())
}

/// ISO 8859-1 → UTF-8 (every byte maps to the code point of the same value).
pub(crate) fn latin1_to_utf8(data: &[u8]) -> String {
    data.iter().map(|&b| b as char).collect()
}
