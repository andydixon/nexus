//! Local shell in a PTY (forkpty on Unix, ConPTY on Windows), a port of internal/localshell.

use super::Callbacks;
use crate::Result;
use parking_lot::Mutex;
use portable_pty::{CommandBuilder, MasterPty, PtySize, native_pty_system};
use std::io::{Read, Write};
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

pub fn default_shell() -> String {
    if cfg!(windows) {
        let ps = std::env::var_os("SystemRoot").map(|r| PathBuf::from(r).join(r"System32\WindowsPowerShell\v1.0\powershell.exe")).filter(|p| p.exists());
        return ps.map_or_else(|| "cmd.exe".into(), |p| p.to_string_lossy().into());
    }
    std::env::var("SHELL").ok().filter(|s| !s.is_empty()).unwrap_or_else(|| "/bin/sh".into())
}

struct Running {
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    killer: Box<dyn portable_pty::ChildKiller + Send + Sync>,
    pid: Option<u32>,
}

pub struct LocalShell {
    shell: String,
    cb: Callbacks,
    state: Mutex<(Option<Running>, PathBuf, u16, u16, bool)>, // running, cwd, cols, rows, closed
    /// A line was entered since the last cwd lookup. On macOS the lookup shells out to lsof, too
    /// slow for the write path, so it runs off-thread once the shell next prints something.
    cwd_stale: AtomicBool,
    capturing: AtomicBool,
}

impl LocalShell {
    pub fn new(shell: &str, start_dir: Option<PathBuf>, cols: u16, rows: u16, cb: Callbacks) -> Arc<LocalShell> {
        let shell = if shell.is_empty() { default_shell() } else { shell.to_string() };
        let dir = start_dir.filter(|d| d.is_dir()).or_else(home_dir).unwrap_or_else(|| PathBuf::from("/"));
        Arc::new(LocalShell {
            shell,
            cb,
            state: Mutex::new((None, dir, cols.max(1), rows.max(1), false)),
            cwd_stale: Default::default(),
            capturing: Default::default(),
        })
    }

    pub fn start(self: &Arc<Self>) -> Result<()> {
        let mut st = self.state.lock();
        if st.4 {
            return Err("shell already closed".into());
        }
        if st.0.is_some() {
            return Err("shell already running".into());
        }
        let pair = native_pty_system()
            .openpty(PtySize { rows: st.3, cols: st.2, pixel_width: 0, pixel_height: 0 })
            .map_err(|e| format!("failed to start shell {:?}: {e}", self.shell))?;
        let mut cmd = CommandBuilder::new(&self.shell);
        cmd.env_clear();
        // `vars()` panics on a non-UTF-8 entry (a launcher can plant one); skip those instead.
        let env = crate::terminalenv::local_shell_env(std::env::vars_os().filter_map(|(k, v)| Some(format!("{}={}", k.to_str()?, v.to_str()?))).collect());
        for kv in env {
            if let Some((k, v)) = kv.split_once('=') {
                cmd.env(k, v);
            }
        }
        cmd.cwd(&st.1);
        let mut child = pair.slave.spawn_command(cmd).map_err(|e| format!("failed to start shell {:?}: {e}", self.shell))?;
        drop(pair.slave);
        let mut reader = pair.master.try_clone_reader()?;
        let writer = pair.master.take_writer()?;
        let pid = child.process_id();
        st.0 = Some(Running { master: pair.master, writer, killer: child.clone_killer(), pid });
        drop(st);

        let me = self.clone();
        let (on_error, on_close) = (self.cb.on_error.clone(), self.cb.on_close.clone());
        std::thread::Builder::new().name("nexus-local-read".into()).spawn(super::guarded(on_error, on_close, move || {
            let mut buf = vec![0u8; 32 * 1024];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) | Err(_) => break,
                    Ok(n) => {
                        (me.cb.on_data)(&buf[..n]);
                        if me.cwd_stale.swap(false, Ordering::SeqCst) {
                            me.capture_cwd_in_background();
                        }
                    }
                }
            }
        }))?;
        let me = self.clone();
        std::thread::Builder::new().name("nexus-local-wait".into()).spawn(move || {
            let _ = child.wait();
            let was_closed = {
                let mut st = me.state.lock();
                let was = st.4;
                st.4 = true;
                st.0 = None;
                was
            };
            if !was_closed {
                (me.cb.on_close)();
            }
        })?;
        Ok(())
    }

    pub fn write(&self, data: &[u8]) -> Result<()> {
        if data.iter().any(|b| matches!(b, b'\r' | b'\n' | 4)) {
            self.capture_cwd();
        }
        let mut st = self.state.lock();
        let r = st.0.as_mut().ok_or("shell not running")?;
        r.writer.write_all(data)?;
        Ok(())
    }

    pub fn resize(&self, cols: u16, rows: u16) -> Result<()> {
        let mut st = self.state.lock();
        if cols > 0 {
            st.2 = cols;
        }
        if rows > 0 {
            st.3 = rows;
        }
        let size = PtySize { rows: st.3, cols: st.2, pixel_width: 0, pixel_height: 0 };
        if let Some(r) = &st.0 {
            r.master.resize(size)?;
        }
        Ok(())
    }

    /// Idempotent; does not fire on_close (matches Go: an explicit close is not an exit).
    pub fn close(&self) {
        let mut st = self.state.lock();
        if st.4 {
            return;
        }
        st.4 = true;
        if let Some(mut r) = st.0.take() {
            let _ = r.killer.kill();
        }
    }

    /// The last directory the shell was seen in (used to restart in the same place).
    pub fn working_directory(&self) -> PathBuf {
        // On Linux the live directory is a cheap readlink; elsewhere the last capture.
        if cfg!(target_os = "linux")
            && let Some(dir) = self.state.lock().0.as_ref().and_then(|r| r.pid).and_then(process_cwd).filter(|d| d.is_dir())
        {
            return dir;
        }
        self.state.lock().1.clone()
    }

    fn capture_cwd(&self) {
        if cfg!(target_os = "macos") {
            self.cwd_stale.store(true, Ordering::SeqCst);
            return;
        }
        let pid = self.state.lock().0.as_ref().and_then(|r| r.pid);
        if let Some(dir) = pid.and_then(process_cwd).filter(|d| d.is_dir()) {
            self.state.lock().1 = dir;
        }
    }

    fn capture_cwd_in_background(self: &Arc<Self>) {
        let Some(pid) = self.state.lock().0.as_ref().and_then(|r| r.pid) else { return };
        if self.capturing.swap(true, Ordering::SeqCst) {
            return;
        }
        let me = self.clone();
        std::thread::spawn(move || {
            // A failed lookup (the shell already exited) keeps the last known directory.
            if let Some(dir) = process_cwd(pid).filter(|d| d.is_dir()) {
                me.state.lock().1 = dir;
            }
            me.capturing.store(false, Ordering::SeqCst);
        });
    }
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" }).map(PathBuf::from)
}

fn process_cwd(pid: u32) -> Option<PathBuf> {
    if cfg!(target_os = "linux") {
        return std::fs::read_link(format!("/proc/{pid}/cwd")).ok();
    }
    if cfg!(target_os = "macos") {
        let out = std::process::Command::new("/usr/sbin/lsof").args(["-a", "-p", &pid.to_string(), "-d", "cwd", "-Fn"]).output().ok()?;
        return String::from_utf8_lossy(&out.stdout).lines().find_map(|l| l.strip_prefix('n').map(PathBuf::from));
    }
    None
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::time::{Duration, Instant};

    #[test]
    fn runs_echoes_and_reports_exit() {
        let seen: Arc<Mutex<Vec<u8>>> = Arc::default();
        let closed = Arc::new(Mutex::new(0));
        let (s2, c2) = (seen.clone(), closed.clone());
        let cb =
            Callbacks { on_data: Arc::new(move |d| s2.lock().extend_from_slice(d)), on_error: Arc::new(|_| {}), on_close: Arc::new(move || *c2.lock() += 1) };
        let sh = LocalShell::new("/bin/sh", Some("/tmp".into()), 80, 24, cb);
        sh.start().unwrap();
        sh.write(b"cd / && echo local-$((2*21))\n").unwrap();
        let t = Instant::now();
        while !String::from_utf8_lossy(&seen.lock()).contains("local-42") {
            assert!(t.elapsed() < Duration::from_secs(5));
            std::thread::sleep(Duration::from_millis(20));
        }
        sh.resize(100, 30).unwrap();
        sh.write(b"exit\n").unwrap();
        while *closed.lock() == 0 {
            assert!(t.elapsed() < Duration::from_secs(5));
            std::thread::sleep(Duration::from_millis(20));
        }
        assert_eq!(sh.working_directory(), PathBuf::from("/"));
    }
}
