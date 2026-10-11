//! End-to-end SSH tests against a throwaway OpenSSH server run as the current user. Skipped
//! (passing) when `sshd`/`ssh-keygen` aren't installed.

use nexus_core::clients::Callbacks;
use nexus_core::clients::sftp;
use nexus_core::clients::ssh::{HostKeyCheck, SshClient};
use nexus_core::models::{ForwardDirection, JumpHost, PortForward, Profile, Protocol};
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

struct Server {
    dir: PathBuf,
    port: u16,
    child: Child,
    agent: Option<Child>,
}

impl Drop for Server {
    fn drop(&mut self) {
        let _ = self.child.kill();
        if let Some(a) = &mut self.agent {
            let _ = a.kill();
        }
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}

fn which(p: &str) -> Option<PathBuf> {
    ["/usr/sbin", "/usr/bin", "/usr/local/sbin", "/usr/local/bin"].iter().map(|d| Path::new(d).join(p)).find(|p| p.exists())
}

fn free_port() -> u16 {
    TcpListener::bind("127.0.0.1:0").unwrap().local_addr().unwrap().port()
}

fn start(name: &str) -> Option<Server> {
    let sshd = which("sshd")?;
    let keygen = which("ssh-keygen")?;
    let sftp_server =
        ["/usr/lib/openssh/sftp-server", "/usr/libexec/openssh/sftp-server", "/usr/libexec/sftp-server"].iter().find(|p| Path::new(p).exists())?;
    let dir = std::env::temp_dir().join(format!("nexus-sshd-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    for k in ["host", "client"] {
        Command::new(&keygen).args(["-q", "-t", "ed25519", "-N", "", "-f"]).arg(dir.join(k)).status().unwrap();
    }
    std::fs::copy(dir.join("client.pub"), dir.join("authorized_keys")).unwrap();
    let port = free_port();
    let cfg = format!(
        "Port {port}\nListenAddress 127.0.0.1\nHostKey {d}/host\nAuthorizedKeysFile {d}/authorized_keys\nPidFile {d}/pid\n\
         StrictModes no\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\n\
         AllowTcpForwarding yes\nAllowAgentForwarding yes\nSubsystem sftp {sftp_server}\n",
        d = dir.display()
    );
    std::fs::write(dir.join("sshd_config"), cfg).unwrap();
    let child = Command::new(sshd).args(["-D", "-e", "-f"]).arg(dir.join("sshd_config")).spawn().ok()?;
    let start = Instant::now();
    while TcpStream::connect(("127.0.0.1", port)).is_err() {
        if start.elapsed() > Duration::from_secs(5) {
            return None;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    Some(Server { dir, port, child, agent: None })
}

fn profile(s: &Server) -> Profile {
    let mut p = Profile::new(Protocol::Ssh);
    p.host = "127.0.0.1".into();
    p.port = s.port as i64;
    p.username = std::env::var("USER").unwrap_or_else(|_| "root".into());
    let c = p.ssh.as_mut().unwrap();
    c.auth_methods = vec!["publickey".into()];
    c.private_key_path = s.dir.join("client").to_string_lossy().into();
    // Never start the user's login shell: profiles can auto-attach tmux/byobu to their real session.
    c.startup_command = "/bin/sh -i".into();
    p
}

type Seen = Arc<Mutex<Vec<u8>>>;

fn callbacks() -> (Callbacks, Seen) {
    let seen: Seen = Arc::default();
    let s2 = seen.clone();
    let mut cb = Callbacks::noop();
    cb.on_data = Arc::new(move |d| s2.lock().unwrap().extend_from_slice(d));
    (cb, seen)
}

fn accept_all() -> (HostKeyCheck, Arc<Mutex<Vec<String>>>) {
    let fps: Arc<Mutex<Vec<String>>> = Arc::default();
    let f2 = fps.clone();
    (
        Arc::new(move |i| {
            f2.lock().unwrap().push(format!("{} {}", i.key_type, i.fingerprint));
            Ok(())
        }),
        fps,
    )
}

fn wait_for(seen: &Seen, needle: &str) -> bool {
    let start = Instant::now();
    while start.elapsed() < Duration::from_secs(10) {
        if String::from_utf8_lossy(&seen.lock().unwrap()).contains(needle) {
            return true;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    false
}

fn echo_server() -> u16 {
    let l = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = l.local_addr().unwrap().port();
    std::thread::spawn(move || {
        for s in l.incoming().flatten() {
            std::thread::spawn(move || {
                let mut s = s;
                let mut buf = [0u8; 1024];
                while let Ok(n) = s.read(&mut buf) {
                    if n == 0 || s.write_all(&buf[..n]).is_err() {
                        break;
                    }
                }
            });
        }
    });
    port
}

fn echo_through(port: u16) -> String {
    let start = Instant::now();
    loop {
        if let Ok(mut s) = TcpStream::connect(("127.0.0.1", port)) {
            s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
            s.write_all(b"ping").unwrap();
            let mut buf = [0u8; 4];
            if s.read_exact(&mut buf).is_ok() {
                return String::from_utf8_lossy(&buf).into();
            }
        }
        if start.elapsed() > Duration::from_secs(5) {
            return String::new();
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

#[test]
fn shell_exec_forwards_sftp() {
    let Some(s) = start("main") else {
        return eprintln!("sshd not available; skipping");
    };
    let (cb, seen) = callbacks();
    let (check, fps) = accept_all();
    let c = SshClient::new(&profile(&s), cb);
    c.connect(check, None, None).unwrap();
    assert!(fps.lock().unwrap()[0].starts_with("ssh-ed25519 SHA256:"));

    c.resize(120, 40).unwrap();
    c.write(b"echo nexus-$((40+2)) $COLUMNS\n").unwrap();
    assert!(wait_for(&seen, "nexus-42"), "shell output: {}", String::from_utf8_lossy(&seen.lock().unwrap()));
    assert_eq!(c.run_command("echo hi").unwrap(), "hi");
    assert!(c.ping().unwrap() >= 0);

    let echo = echo_server();
    let lport = free_port();
    let rule = PortForward {
        id: "l".into(),
        direction: ForwardDirection::Local,
        bind_port: lport as i64,
        dest_host: "127.0.0.1".into(),
        dest_port: echo as i64,
        enabled: true,
        ..Default::default()
    };
    c.start_forward(rule).unwrap();
    assert_eq!(echo_through(lport), "ping");
    let rport = free_port();
    let rule = PortForward {
        id: "r".into(),
        direction: ForwardDirection::Remote,
        bind_host: "127.0.0.1".into(),
        bind_port: rport as i64,
        dest_host: "127.0.0.1".into(),
        dest_port: echo as i64,
        enabled: true,
        ..Default::default()
    };
    c.start_forward(rule).unwrap();
    assert_eq!(echo_through(rport), "ping");
    assert_eq!(c.active_forwards().len(), 2);
    c.stop_forward("l").unwrap();
    let start = Instant::now();
    while TcpStream::connect(("127.0.0.1", lport)).is_ok() {
        assert!(start.elapsed() < Duration::from_secs(2), "listener still open after stop");
        std::thread::sleep(Duration::from_millis(20));
    }

    let sf = c.sftp().unwrap();
    let local = s.dir.join("up.txt");
    std::fs::write(&local, b"sftp payload").unwrap();
    let remote = s.dir.join("remote.txt").to_string_lossy().to_string();
    sftp::upload(&sf, &local, &remote, &|_| {}).unwrap();
    let names: Vec<String> = sftp::list(&sf, &s.dir.to_string_lossy()).unwrap().into_iter().map(|e| e.name).collect();
    assert!(names.contains(&"remote.txt".to_string()));
    let back = s.dir.join("back.txt");
    sftp::download(&sf, &remote, &back, &|_| {}).unwrap();
    assert_eq!(std::fs::read(back).unwrap(), b"sftp payload");
    assert_eq!(sftp::stat(&sf, &remote).unwrap().size, 12);
    c.close();
}

#[test]
fn close_fires_once_and_remote_exit_closes() {
    let Some(s) = start("close") else { return };
    let (mut cb, _) = callbacks();
    let n = Arc::new(Mutex::new(0));
    let n2 = n.clone();
    cb.on_close = Arc::new(move || *n2.lock().unwrap() += 1);
    let c = SshClient::new(&profile(&s), cb);
    c.connect(accept_all().0, None, None).unwrap();
    c.write(b"exit\n").unwrap();
    let start = Instant::now();
    while *n.lock().unwrap() == 0 && start.elapsed() < Duration::from_secs(10) {
        std::thread::sleep(Duration::from_millis(50));
    }
    c.close();
    assert_eq!(*n.lock().unwrap(), 1);
}

#[test]
fn rejected_host_key_reports_reason() {
    let Some(s) = start("reject") else { return };
    let c = SshClient::new(&profile(&s), Callbacks::noop());
    let err = c.connect(Arc::new(|_| Err("host key rejected by user".into())), None, None).unwrap_err();
    assert!(err.to_string().contains("rejected by user"), "{err}");
}

#[test]
fn jump_host_chain() {
    let Some(s) = start("jump") else { return };
    let mut p = profile(&s);
    let key = p.ssh.as_ref().unwrap().private_key_path.clone();
    p.ssh.as_mut().unwrap().jump_hosts =
        vec![JumpHost { host: "127.0.0.1".into(), port: s.port as i64, username: p.username.clone(), private_key_path: key, ..Default::default() }];
    let (check, fps) = accept_all();
    let c = SshClient::new(&p, Callbacks::noop());
    c.connect(check, None, None).unwrap();
    assert_eq!(fps.lock().unwrap().len(), 2, "jump and target both verified");
    assert_eq!(c.run_command("echo via-jump").unwrap(), "via-jump");
    c.close();
}

#[test]
fn agent_auth_and_forwarding() {
    let Some(mut s) = start("agent") else { return };
    let (Some(agent), Some(add)) = (which("ssh-agent"), which("ssh-add")) else {
        return;
    };
    let sock = s.dir.join("agent.sock");
    s.agent = Some(Command::new(agent).arg("-D").arg("-a").arg(&sock).spawn().unwrap());
    let start = Instant::now();
    while !sock.exists() && start.elapsed() < Duration::from_secs(5) {
        std::thread::sleep(Duration::from_millis(50));
    }
    assert!(Command::new(add).arg(s.dir.join("client")).env("SSH_AUTH_SOCK", &sock).status().unwrap().success());
    // SAFETY: tests in this file don't read SSH_AUTH_SOCK concurrently except through this client.
    unsafe { std::env::set_var("SSH_AUTH_SOCK", &sock) };
    let mut p = profile(&s);
    let c = p.ssh.as_mut().unwrap();
    c.private_key_path.clear();
    c.agent_auth = true;
    c.agent_forwarding = true;
    let (cb, seen) = callbacks();
    let cl = SshClient::new(&p, cb);
    cl.connect(accept_all().0, None, None).unwrap();
    cl.write(b"ssh-add -l >/dev/null && echo agent-$((1+1))-ok\n").unwrap();
    assert!(wait_for(&seen, "agent-2-ok"), "forwarded agent not usable: {}", String::from_utf8_lossy(&seen.lock().unwrap()));
    cl.close();
}
