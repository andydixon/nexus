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
