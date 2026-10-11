//! SSH client on russh: interactive PTY shell, the profile's auth chain (agent, public key,
//! password, keyboard-interactive with TOTP), jump hosts, SOCKS5/HTTP proxies, local and remote
//! port forwards, agent forwarding and SFTP. A port of internal/sshclient + sftpclient.

use super::{Callbacks, rt};
use crate::Result;
use crate::models::{ForwardDirection, JumpHost, PortForward, Profile, SshConfig};
use base64::Engine;
use russh::client::{self, Handle, Msg};
use russh::keys::{HashAlg, PrivateKeyWithHashAlg, PublicKey};
use russh::{Channel, ChannelMsg, Pty};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};
use tokio::sync::mpsc;

const MAX_PRIVATE_KEY_BYTES: u64 = 1 << 20;

/// What a host-key decision needs. The manager's checker consults the known-hosts table and,
/// per policy, prompts the user; it returns Err with the reason to reject.
#[derive(Clone, Debug)]
pub struct HostKeyInfo {
    pub host: String,
    pub port: u16,
    pub key_type: String,
    pub fingerprint: String,
    pub public_key_b64: String,
}

pub type HostKeyCheck = Arc<dyn Fn(&HostKeyInfo) -> std::result::Result<(), String> + Send + Sync>;
/// Blocking credential source (may prompt the user).
pub type SecretFn = Arc<dyn Fn() -> Result<String> + Send + Sync>;

pub fn host_key_info(host: &str, port: u16, key: &PublicKey) -> HostKeyInfo {
    HostKeyInfo {
        host: host.into(),
        port,
        key_type: key.algorithm().as_str().into(),
        fingerprint: key.fingerprint(HashAlg::Sha256).to_string(),
        public_key_b64: base64::engine::general_purpose::STANDARD.encode(key.to_bytes().unwrap_or_default()),
    }
}

/// State the russh handler shares with the client: remote-forward targets and the last
/// host-key rejection reason (russh only reports "unknown key").
#[derive(Default)]
struct Shared {
    remote_targets: Mutex<HashMap<(String, u32), (String, u16)>>,
    rejection: Mutex<Option<String>>,
    agent_forwarding: std::sync::atomic::AtomicBool,
}

struct Handler {
    host: String,
    port: u16,
    check: HostKeyCheck,
    shared: Arc<Shared>,
}

impl client::Handler for Handler {
    type Error = russh::Error;

    async fn check_server_key(&mut self, key: &russh::keys::PublicKeyOrCertificate) -> std::result::Result<bool, Self::Error> {
        let key = match key {
            russh::keys::PublicKeyOrCertificate::PublicKey { key, .. } => key.clone(),
            russh::keys::PublicKeyOrCertificate::Certificate(c) => c.public_key().clone().into(),
        };
        let info = host_key_info(&self.host, self.port, &key);
        let check = self.check.clone();
        // The check may block on a UI prompt.
        let res = tokio::task::spawn_blocking(move || check(&info)).await.unwrap_or(Err("host key check failed".into()));
        match res {
            Ok(()) => Ok(true),
            Err(why) => {
                *self.shared.rejection.lock().unwrap() = Some(why);
                Ok(false)
            }
        }
    }

    async fn server_channel_open_forwarded_tcpip(
        &mut self,
        channel: Channel<Msg>,
        connected_address: &str,
        connected_port: u32,
        _originator_address: &str,
        _originator_port: u32,
        reply: client::ChannelOpenHandle,
        _session: &mut client::Session,
    ) -> std::result::Result<(), Self::Error> {
        let dest = self.shared.remote_targets.lock().unwrap().get(&(connected_address.to_string(), connected_port)).cloned();
        let Some((host, port)) = dest else {
            reply.reject(russh::ChannelOpenFailure::AdministrativelyProhibited).await;
            return Ok(());
        };
        reply.accept().await;
        tokio::spawn(async move {
            if let Ok(mut local) = tokio::net::TcpStream::connect((host.as_str(), port)).await {
                let mut remote = channel.into_stream();
                let _ = tokio::io::copy_bidirectional(&mut remote, &mut local).await;
            }
        });
        Ok(())
    }

    async fn server_channel_open_agent_forward(
        &mut self,
        channel: Channel<Msg>,
        reply: client::ChannelOpenHandle,
        _session: &mut client::Session,
    ) -> std::result::Result<(), Self::Error> {
        if !self.shared.agent_forwarding.load(std::sync::atomic::Ordering::Relaxed) {
            reply.reject(russh::ChannelOpenFailure::AdministrativelyProhibited).await;
            return Ok(());
        }
        reply.accept().await;
        tokio::spawn(async move {
            if let Ok(mut agent) = connect_agent_socket().await {
                let mut remote = channel.into_stream();
                let _ = tokio::io::copy_bidirectional(&mut remote, &mut agent).await;
            }
        });
        Ok(())
    }
}

trait Stream: AsyncRead + AsyncWrite + Unpin + Send {}
impl<T: AsyncRead + AsyncWrite + Unpin + Send> Stream for T {}

/// The local agent's socket: `SSH_AUTH_SOCK` on Unix, the OpenSSH named pipe on Windows.
async fn connect_agent_socket() -> std::io::Result<Box<dyn Stream>> {
    #[cfg(unix)]
    {
        let p = std::env::var("SSH_AUTH_SOCK").map_err(|_| std::io::Error::other("SSH_AUTH_SOCK not set — SSH agent unavailable"))?;
        Ok(Box::new(tokio::net::UnixStream::connect(p).await?))
    }
    #[cfg(windows)]
    {
        let p = std::env::var("SSH_AUTH_SOCK").unwrap_or_else(|_| r"\\.\pipe\openssh-ssh-agent".into());
        Ok(Box::new(tokio::net::windows::named_pipe::ClientOptions::new().open(p)?))
    }
}

type Agent = russh::keys::agent::client::AgentClient<Box<dyn russh::keys::agent::client::AgentStream + Send + Unpin>>;

async fn connect_agent() -> Result<Agent> {
    #[cfg(unix)]
    {
        Ok(russh::keys::agent::client::AgentClient::connect_env().await.map_err(|e| format!("SSH agent unavailable: {e}"))?.dynamic())
    }
    #[cfg(windows)]
    {
        if let Ok(a) = russh::keys::agent::client::AgentClient::connect_named_pipe(r"\\.\pipe\openssh-ssh-agent").await {
            return Ok(a.dynamic());
        }
        Ok(russh::keys::agent::client::AgentClient::connect_pageant().await.map_err(|e| format!("SSH agent unavailable: {e}"))?.dynamic())
    }
}

fn is_totp_prompt(q: &str) -> bool {
    let q = q.to_lowercase();
    ["verification code", "otp", "one-time", "one time", "two-factor", "two factor", "2fa", "totp", "authenticator", "security code", "6-digit", "6 digit"]
        .iter()
        .any(|k| q.contains(k))
}

/// RFC 6238 code (SHA-1, 6 digits, 30 s) for a base32 secret, normalised as pquerna/otp does.
pub fn totp_code(secret: &str) -> Result<String> {
    totp_at(secret, std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap_or_default().as_secs())
}

fn totp_at(secret: &str, unix: u64) -> Result<String> {
    use hmac::{Hmac, KeyInit, Mac};
    let key = base32_decode(secret).ok_or("invalid TOTP secret (not base32)")?;
    let mut mac = Hmac::<sha1::Sha1>::new_from_slice(&key).map_err(|e| e.to_string())?;
    mac.update(&(unix / 30).to_be_bytes());
    let h = mac.finalize().into_bytes();
    let o = (h[19] & 0xf) as usize;
    let bin = u32::from_be_bytes([h[o] & 0x7f, h[o + 1], h[o + 2], h[o + 3]]);
    Ok(format!("{:06}", bin % 1_000_000))
}

fn base32_decode(s: &str) -> Option<Vec<u8>> {
    let (mut out, mut buf, mut bits) = (vec![], 0u64, 0u32);
    for c in s.chars().filter(|c| !c.is_whitespace() && *c != '=') {
        let v = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567".find(c.to_ascii_uppercase())? as u64;
        buf = (buf << 5) | v;
        bits += 5;
        if bits >= 8 {
            bits -= 8;
            out.push((buf >> bits) as u8);
        }
    }
    (!out.is_empty()).then_some(out)
}

/// Reads an OpenSSH/PEM private key, asking for a passphrase only if it's encrypted.
pub fn load_private_key(path: &str, passphrase: Option<&SecretFn>) -> Result<russh::keys::PrivateKey> {
    let meta = std::fs::metadata(path).map_err(|e| format!("unable to stat key file: {e}"))?;
    if meta.is_dir() {
        return Err("key path is a directory".into());
    }
    if meta.len() > MAX_PRIVATE_KEY_BYTES {
        return Err(format!("key file is too large: {} bytes exceeds {MAX_PRIVATE_KEY_BYTES} bytes", meta.len()).into());
    }
    let data = std::fs::read_to_string(path).map_err(|e| format!("unable to read key file: {e}"))?;
    match russh::keys::decode_secret_key(&data, None) {
        Ok(k) => Ok(k),
        Err(first) => {
            let Some(get) = passphrase else {
                return Err(format!("key is encrypted and no passphrase callback provided: {first}").into());
            };
            let pass = get().map_err(|e| format!("failed to get passphrase: {e}"))?;
            russh::keys::decode_secret_key(&data, Some(&pass)).map_err(|e| format!("failed to parse encrypted key: {e}").into())
        }
    }
}

async fn blocking<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    tokio::task::spawn_blocking(f).await.expect("blocking task panicked")
}

async fn dial(sc: &SshConfig, host: &str, port: u16, timeout: Duration) -> Result<tokio::net::TcpStream> {
    let fut = async {
        match sc.proxy_type.to_lowercase().as_str() {
            "socks5" => socks5(sc, host, port).await,
            "http" => http_connect(sc, host, port).await,
            _ => Ok(tokio::net::TcpStream::connect((host, port)).await?),
        }
    };
    tokio::time::timeout(timeout, fut).await.map_err(|_| format!("dial tcp {host}:{port}: i/o timeout"))?
}

async fn socks5(sc: &SshConfig, host: &str, port: u16) -> Result<tokio::net::TcpStream> {
    let pport = if sc.proxy_port == 0 { 1080 } else { sc.proxy_port as u16 };
    let mut s = tokio::net::TcpStream::connect((sc.proxy_host.as_str(), pport)).await.map_err(|e| format!("SOCKS5 proxy setup failed: {e}"))?;
    let auth = !sc.proxy_username.is_empty();
    s.write_all(if auth { &[5, 2, 0, 2] } else { &[5, 1, 0] }).await?;
    let mut r = [0u8; 2];
    s.read_exact(&mut r).await?;
    match r[1] {
        0 => {}
        2 if auth => {
            let mut m = vec![1, sc.proxy_username.len() as u8];
            m.extend(sc.proxy_username.as_bytes());
            m.push(sc.proxy_password.len() as u8);
            m.extend(sc.proxy_password.as_bytes());
            s.write_all(&m).await?;
            s.read_exact(&mut r).await?;
            if r[1] != 0 {
                return Err("SOCKS5 proxy: authentication failed".into());
            }
        }
        _ => return Err("SOCKS5 proxy: no acceptable authentication method".into()),
    }
    let mut req = vec![5, 1, 0, 3, host.len() as u8];
    req.extend(host.as_bytes());
    req.extend(port.to_be_bytes());
    s.write_all(&req).await?;
    let mut head = [0u8; 4];
    s.read_exact(&mut head).await?;
    if head[1] != 0 {
        return Err(format!("SOCKS5 proxy connection to {host}:{port} failed: reply code {}", head[1]).into());
    }
    let skip = match head[3] {
        1 => 4,
        4 => 16,
        3 => s.read_u8().await? as usize,
        _ => 0,
    } + 2;
    let mut rest = vec![0u8; skip];
    s.read_exact(&mut rest).await?;
    Ok(s)
}

async fn http_connect(sc: &SshConfig, host: &str, port: u16) -> Result<tokio::net::TcpStream> {
    let pport = if sc.proxy_port == 0 { 8080 } else { sc.proxy_port as u16 };
    let mut s = tokio::net::TcpStream::connect((sc.proxy_host.as_str(), pport)).await.map_err(|e| format!("HTTP proxy connection failed: {e}"))?;
    let mut req = format!("CONNECT {host}:{port} HTTP/1.1\r\nHost: {host}:{port}\r\n");
    if !sc.proxy_username.is_empty() {
        let cred = base64::engine::general_purpose::STANDARD.encode(format!("{}:{}", sc.proxy_username, sc.proxy_password));
        req += &format!("Proxy-Authorization: Basic {cred}\r\n");
    }
    req += "\r\n";
    s.write_all(req.as_bytes()).await?;
    // Read the response head byte by byte so no tunnelled bytes are consumed.
    let mut head = Vec::new();
    while !head.ends_with(b"\r\n\r\n") {
        head.push(s.read_u8().await.map_err(|e| format!("HTTP CONNECT response read failed: {e}"))?);
        if head.len() > 16 * 1024 {
            return Err("HTTP CONNECT response too large".into());
        }
    }
    let status = String::from_utf8_lossy(&head).lines().next().unwrap_or("").to_string();
    if status.split_whitespace().nth(1) != Some("200") {
        return Err(format!("HTTP CONNECT proxy returned {status}").into());
    }
    Ok(s)
}

fn config(sc: &SshConfig) -> Arc<client::Config> {
    let mut c = client::Config::default();
    if sc.keepalive_interval > 0 {
        c.keepalive_interval = Some(Duration::from_secs(sc.keepalive_interval as u64));
        c.keepalive_max = sc.keepalive_count_max.max(1) as usize;
    }
    c.preferred.compression = if sc.compression {
        vec![russh::compression::ZLIB_LEGACY, russh::compression::ZLIB, russh::compression::NONE].into()
    } else {
        vec![russh::compression::NONE].into()
    };
    c.nodelay = true;
    Arc::new(c)
}

struct Auth<'a> {
    user: &'a str,
    methods: Vec<String>,
    agent: bool,
    key: Option<russh::keys::PrivateKey>,
    password: Option<SecretFn>,
    totp_secret: String,
}

async fn authenticate(h: &mut Handle<Handler>, a: Auth<'_>) -> Result<()> {
    if a.agent
        && let Ok(mut agent) = connect_agent().await
        && let Ok(ids) = agent.request_identities().await
    {
        for id in ids {
            let key = id.public_key().into_owned();
            let hash = if key.algorithm().is_rsa() { h.best_supported_rsa_hash().await.ok().flatten().flatten() } else { None };
            if let Ok(r) = h.authenticate_publickey_with(a.user, key, hash, &mut agent).await
                && r.success()
            {
                return Ok(());
            }
        }
    }
    for m in &a.methods {
        match m.as_str() {
            "publickey" => {
                if let Some(k) = &a.key {
                    let hash = if k.algorithm().is_rsa() { h.best_supported_rsa_hash().await?.flatten() } else { None };
                    if h.authenticate_publickey(a.user, PrivateKeyWithHashAlg::new(Arc::new(k.clone()), hash)).await?.success() {
                        return Ok(());
                    }
                }
            }
            "password" => {
                if let Some(get) = a.password.clone() {
                    let pw = blocking(move || get()).await?;
                    if h.authenticate_password(a.user, pw).await?.success() {
                        return Ok(());
                    }
                }
            }
            "keyboard-interactive" => {
                let Some(get) = a.password.clone() else { continue };
                let mut resp = h.authenticate_keyboard_interactive_start(a.user, None).await?;
                loop {
                    match resp {
                        client::KeyboardInteractiveAuthResponse::Success => return Ok(()),
                        client::KeyboardInteractiveAuthResponse::Failure { .. } => break,
                        client::KeyboardInteractiveAuthResponse::InfoRequest { prompts, .. } => {
                            let mut answers = vec![];
                            for p in prompts {
                                if !a.totp_secret.is_empty() && is_totp_prompt(&p.prompt) {
                                    answers.push(totp_code(&a.totp_secret).map_err(|e| format!("failed to generate TOTP code: {e}"))?);
                                } else {
                                    let get = get.clone();
                                    answers.push(blocking(move || get()).await?);
                                }
                            }
                            resp = h.authenticate_keyboard_interactive_respond(answers).await?;
                        }
                    }
                }
            }
            _ => {}
        }
    }
    Err("ssh: handshake failed: ssh: unable to authenticate, no supported methods remain".into())
}

enum Cmd {
    Data(Vec<u8>),
    Resize(u32, u32),
}

struct Forward {
    rule: PortForward,
    tasks: Arc<Mutex<Vec<tokio::task::AbortHandle>>>,
}

pub struct SshClient {
    profile: Profile,
    cb: Callbacks,
    shared: Arc<Shared>,
    handle: Mutex<Option<Arc<Handle<Handler>>>>,
    /// Earlier hops of a jump chain; kept alive for the session's lifetime.
    hops: Mutex<Vec<Arc<Handle<Handler>>>>,
    tx: Mutex<Option<mpsc::UnboundedSender<Cmd>>>,
    forwards: Mutex<HashMap<String, Forward>>,
    sftp: tokio::sync::Mutex<Option<Arc<russh_sftp::client::SftpSession>>>,
    closed: std::sync::atomic::AtomicBool,
}

impl SshClient {
    pub fn new(profile: &Profile, cb: Callbacks) -> Arc<SshClient> {
        Arc::new(SshClient {
            profile: profile.clone(),
            cb,
            shared: Arc::default(),
            handle: Mutex::new(None),
            hops: Mutex::new(vec![]),
            tx: Mutex::new(None),
            forwards: Mutex::new(HashMap::new()),
            sftp: tokio::sync::Mutex::new(None),
            closed: false.into(),
        })
    }

    fn sc(&self) -> SshConfig {
        self.profile.ssh.clone().unwrap_or_else(SshConfig::defaults)
    }

    /// Connects, authenticates and starts the shell. Blocks the calling thread.
    pub fn connect(self: &Arc<Self>, check: HostKeyCheck, password: Option<SecretFn>, passphrase: Option<SecretFn>) -> Result<()> {
        let me = self.clone();
        rt().block_on(async move { me.connect_async(check, password, passphrase, true).await })
    }

    /// Connects and authenticates without opening a shell, for tunnelling another protocol.
    pub fn connect_tunnel_only(self: &Arc<Self>, check: HostKeyCheck, password: Option<SecretFn>, passphrase: Option<SecretFn>) -> Result<()> {
        let me = self.clone();
        rt().block_on(async move { me.connect_async(check, password, passphrase, false).await })
    }

    /// A `direct-tcpip` channel to `host:port` presented as an ordinary loopback TCP stream,
    /// so blocking clients (IRC, VNC) can use it with read timeouts.
    pub fn open_tunnel(&self, host: &str, port: u16) -> Result<std::net::TcpStream> {
        let h = self.handle()?;
        let host = host.to_string();
        let listener = std::net::TcpListener::bind("127.0.0.1:0")?;
        let addr = listener.local_addr()?;
        let ch = rt()
            .block_on(h.channel_open_direct_tcpip(host.clone(), port as u32, "127.0.0.1", 0))
            .map_err(|e| format!("SSH tunnel to {host}:{port} failed: {e}"))?;
        let client = std::net::TcpStream::connect(addr)?;
        let (server, _) = listener.accept()?;
        server.set_nonblocking(true)?;
        rt().spawn(async move {
            if let Ok(mut local) = tokio::net::TcpStream::from_std(server) {
                let mut remote = ch.into_stream();
                let _ = tokio::io::copy_bidirectional(&mut local, &mut remote).await;
            }
        });
        Ok(client)
    }

    async fn handshake(
        &self,
        stream: impl AsyncRead + AsyncWrite + Unpin + Send + 'static,
        host: &str,
        port: u16,
        check: &HostKeyCheck,
    ) -> Result<Handle<Handler>> {
        let handler = Handler { host: host.into(), port, check: check.clone(), shared: self.shared.clone() };
        let timeout = Duration::from_secs(self.profile.connect_timeout.max(1) as u64);
        match tokio::time::timeout(timeout, client::connect_stream(config(&self.sc()), stream, handler)).await {
            Err(_) => Err(format!("SSH handshake with {host}:{port} timed out").into()),
            Ok(Ok(h)) => Ok(h),
            Ok(Err(e)) => match self.shared.rejection.lock().unwrap().take() {
                Some(why) => Err(why.into()),
                None => Err(format!("SSH handshake failed: {e}").into()),
            },
        }
    }

    async fn connect_async(self: &Arc<Self>, check: HostKeyCheck, password: Option<SecretFn>, passphrase: Option<SecretFn>, shell: bool) -> Result<()> {
        let sc = self.sc();
        let p = &self.profile;
        let port = if p.port == 0 { 22 } else { p.port as u16 };
        let timeout = Duration::from_secs(if p.connect_timeout == 0 { 30 } else { p.connect_timeout as u64 });
        let methods = if sc.auth_methods.is_empty() { SshConfig::defaults().auth_methods } else { sc.auth_methods.clone() };
        let key = if methods.iter().any(|m| m == "publickey") && !sc.private_key_path.is_empty() {
            let (path, pp) = (sc.private_key_path.clone(), passphrase.clone());
            Some(
                blocking(move || load_private_key(&path, pp.as_ref()))
                    .await
                    .map_err(|e| format!("failed to build auth methods: failed to parse private key {}: {e}", sc.private_key_path))?,
            )
        } else {
            None
        };

        let mut handle = if sc.jump_hosts.is_empty() {
            let tcp = dial(&sc, &p.host, port, timeout).await.map_err(|e| format!("SSH connection failed: {e}"))?;
            self.handshake(tcp, &p.host, port, &check).await?
        } else {
            let (prev, h) = self.via_jumps(&sc, &check, passphrase.clone(), timeout).await?;
            let ch = prev
                .channel_open_direct_tcpip(p.host.clone(), port as u32, "127.0.0.1", 0)
                .await
                .map_err(|e| format!("failed to dial target {}:{port} through jump hosts: {e}", p.host))?;
            self.hops.lock().unwrap().extend(h);
            self.hops.lock().unwrap().push(prev);
            self.handshake(ch.into_stream(), &p.host, port, &check).await?
        };

        authenticate(&mut handle, Auth { user: &p.username, methods, agent: sc.agent_auth, key, password, totp_secret: sc.totp_secret.clone() }).await?;
        let handle = Arc::new(handle);
        if !shell {
            *self.handle.lock().unwrap() = Some(handle);
            return Ok(());
        }

        let ch = handle.channel_open_session().await.map_err(|e| format!("failed to create SSH session: {e}"))?;
        for (k, v) in crate::terminalenv::ssh_env_vars(&sc.env_vars) {
            let _ = ch.set_env(false, k, v).await;
        }
        let term = if sc.terminal_type.is_empty() { "xterm-256color" } else { &sc.terminal_type };
        let modes = [(Pty::ECHO, 1), (Pty::TTY_OP_ISPEED, 115200), (Pty::TTY_OP_OSPEED, 115200), (Pty::IUTF8, 1)];
        ch.request_pty(true, term, 80, 24, 0, 0, &modes).await.map_err(|e| format!("PTY request failed: {e}"))?;
        if sc.agent_forwarding {
            self.shared.agent_forwarding.store(true, std::sync::atomic::Ordering::Relaxed);
            if let Err(e) = ch.agent_forward(false).await {
                (self.cb.on_error)(format!("agent forwarding: {e}"));
            }
        }
        if sc.startup_command.is_empty() {
            ch.request_shell(true).await.map_err(|e| format!("failed to start shell: {e}"))?;
        } else {
            ch.exec(true, sc.startup_command.as_bytes()).await.map_err(|e| format!("failed to start command: {e}"))?;
        }

        let (mut read, write) = ch.split();
        let (tx, mut rx) = mpsc::unbounded_channel::<Cmd>();
        *self.tx.lock().unwrap() = Some(tx);
        *self.handle.lock().unwrap() = Some(handle);
        tokio::spawn(async move {
            while let Some(cmd) = rx.recv().await {
                let r = match cmd {
                    Cmd::Data(d) => write.data_bytes(d).await,
                    Cmd::Resize(c, r) => write.window_change(c, r, 0, 0).await,
                };
                if r.is_err() {
                    break;
                }
            }
        });
        let me = self.clone();
        tokio::spawn(async move {
            while let Some(msg) = read.wait().await {
                match msg {
                    ChannelMsg::Data { data } | ChannelMsg::ExtendedData { data, .. } => (me.cb.on_data)(&data),
                    ChannelMsg::Eof | ChannelMsg::Close => break,
                    _ => {}
                }
            }
            let me2 = me.clone();
            let _ = tokio::task::spawn_blocking(move || me2.close()).await;
        });
        Ok(())
    }

    /// Authenticates through each jump host in turn; returns the last hop and the ones before it.
    async fn via_jumps(
        &self,
        sc: &SshConfig,
        check: &HostKeyCheck,
        passphrase: Option<SecretFn>,
        timeout: Duration,
    ) -> Result<(Arc<Handle<Handler>>, Vec<Arc<Handle<Handler>>>)> {
        let mut chain: Vec<Arc<Handle<Handler>>> = vec![];
        for (i, j) in sc.jump_hosts.iter().enumerate() {
            let JumpHost { host, port, username, private_key_path, .. } = j;
            let jport = if *port == 0 { 22 } else { *port as u16 };
            let key = if private_key_path.is_empty() {
                None
            } else {
                let (path, pp) = (private_key_path.clone(), passphrase.clone());
                Some(blocking(move || load_private_key(&path, pp.as_ref())).await.map_err(|e| format!("jump host {i} private key error: {e}"))?)
            };
            let mut h = match chain.last() {
                None => {
                    let tcp = tokio::time::timeout(timeout, tokio::net::TcpStream::connect((host.as_str(), jport)))
                        .await
                        .map_err(|_| format!("failed to connect to jump host {i} ({host}:{jport}): timeout"))?
                        .map_err(|e| format!("failed to connect to jump host {i} ({host}:{jport}): {e}"))?;
                    self.handshake(tcp, host, jport, check).await?
                }
                Some(prev) => {
                    let ch = prev
                        .channel_open_direct_tcpip(host.clone(), jport as u32, "127.0.0.1", 0)
                        .await
                        .map_err(|e| format!("failed to dial through jump host {i} ({host}:{jport}): {e}"))?;
                    self.handshake(ch.into_stream(), host, jport, check).await?
                }
            };
            authenticate(
                &mut h,
                Auth {
                    user: username,
                    methods: vec!["publickey".into()],
                    agent: key.is_none() || sc.agent_auth,
                    key,
                    password: None,
                    totp_secret: String::new(),
                },
            )
            .await
            .map_err(|e| format!("failed to establish SSH through jump host {i}: {e}"))?;
            chain.push(Arc::new(h));
        }
        let last = chain.pop().expect("at least one jump host");
        Ok((last, chain))
    }

    fn handle(&self) -> Result<Arc<Handle<Handler>>> {
        self.handle.lock().unwrap().clone().ok_or_else(|| "not connected".into())
    }

    pub fn write(&self, data: &[u8]) -> Result<()> {
        let tx = self.tx.lock().unwrap();
        tx.as_ref().ok_or("not connected")?.send(Cmd::Data(data.to_vec())).map_err(|_| "not connected".into())
    }

    pub fn resize(&self, cols: u32, rows: u32) -> Result<()> {
        let tx = self.tx.lock().unwrap();
        tx.as_ref().ok_or("not connected")?.send(Cmd::Resize(cols, rows)).map_err(|_| "not connected".into())
    }

    /// Round trip of a keepalive, in milliseconds.
    pub fn ping(&self) -> Result<i64> {
        let h = self.handle()?;
        let start = Instant::now();
        rt().block_on(h.send_ping())?;
        Ok(start.elapsed().as_millis() as i64)
    }

    /// Runs a command on a separate channel and returns trimmed stdout.
    pub fn run_command(&self, command: &str) -> Result<String> {
        let h = self.handle()?;
        let command = command.to_string();
        rt().block_on(async move {
            let mut ch = h.channel_open_session().await.map_err(|e| format!("failed to open SSH session: {e}"))?;
            ch.exec(true, command.as_bytes()).await?;
            let mut out = vec![];
            while let Some(m) = ch.wait().await {
                match m {
                    ChannelMsg::Data { data } => out.extend_from_slice(&data),
                    ChannelMsg::Eof | ChannelMsg::Close => break,
                    _ => {}
                }
            }
            Ok(String::from_utf8_lossy(&out).trim().to_string())
        })
    }

    pub fn is_connected(&self) -> bool {
        self.handle.lock().unwrap().as_ref().is_some_and(|h| !h.is_closed())
    }

    /// Idempotent; fires on_close once.
    pub fn close(&self) {
        if self.closed.swap(true, std::sync::atomic::Ordering::SeqCst) {
            return;
        }
        self.stop_all_forwards();
        *self.tx.lock().unwrap() = None;
        let h = self.handle.lock().unwrap().take();
        let hops = std::mem::take(&mut *self.hops.lock().unwrap());
        rt().spawn(async move {
            if let Some(h) = h {
                let _ = h.disconnect(russh::Disconnect::ByApplication, "", "en").await;
            }
            for h in hops.into_iter().rev() {
                let _ = h.disconnect(russh::Disconnect::ByApplication, "", "en").await;
            }
        });
        (self.cb.on_close)();
    }

    // --- port forwarding -------------------------------------------------------------------

    pub fn start_forward(&self, rule: PortForward) -> Result<()> {
        if self.forwards.lock().unwrap().contains_key(&rule.id) {
            return Err(format!("forward {:?} is already active", rule.id).into());
        }
        let h = self.handle()?;
        let tasks: Arc<Mutex<Vec<tokio::task::AbortHandle>>> = Arc::default();
        match rule.direction {
            ForwardDirection::Local => {
                let bind = if rule.bind_host.is_empty() { "127.0.0.1".to_string() } else { rule.bind_host.clone() };
                let addr = format!("{bind}:{}", rule.bind_port);
                let listener =
                    rt().block_on(tokio::net::TcpListener::bind(addr.clone())).map_err(|e| format!("local forward: listen on {addr} failed: {e}"))?;
                let (dhost, dport) = (rule.dest_host.clone(), rule.dest_port as u32);
                let t2 = tasks.clone();
                let task = rt().spawn(async move {
                    while let Ok((mut sock, peer)) = listener.accept().await {
                        let (h, dhost) = (h.clone(), dhost.clone());
                        let conn = tokio::spawn(async move {
                            if let Ok(ch) = h.channel_open_direct_tcpip(dhost, dport, peer.ip().to_string(), peer.port() as u32).await {
                                let mut remote = ch.into_stream();
                                let _ = tokio::io::copy_bidirectional(&mut sock, &mut remote).await;
                            }
                        });
                        t2.lock().unwrap().push(conn.abort_handle());
                    }
                });
                tasks.lock().unwrap().push(task.abort_handle());
            }
            ForwardDirection::Remote => {
                let bind = if rule.bind_host.is_empty() { "0.0.0.0".to_string() } else { rule.bind_host.clone() };
                let port = rt()
                    .block_on(h.tcpip_forward(bind.clone(), rule.bind_port as u32))
                    .map_err(|e| format!("remote forward: server listen on {bind}:{} failed: {e}", rule.bind_port))?;
                let port = if rule.bind_port == 0 { port } else { rule.bind_port as u32 };
                self.shared.remote_targets.lock().unwrap().insert((bind, port), (rule.dest_host.clone(), rule.dest_port as u16));
            }
        }
        self.forwards.lock().unwrap().insert(rule.id.clone(), Forward { rule, tasks });
        Ok(())
    }

    pub fn stop_forward(&self, id: &str) -> Result<()> {
        let f = self.forwards.lock().unwrap().remove(id).ok_or_else(|| format!("forward {id:?} not found"))?;
        self.shutdown_forward(f);
        Ok(())
    }

    fn shutdown_forward(&self, f: Forward) {
        for t in f.tasks.lock().unwrap().drain(..) {
            t.abort();
        }
        if f.rule.direction == ForwardDirection::Remote {
            let bind = if f.rule.bind_host.is_empty() { "0.0.0.0".to_string() } else { f.rule.bind_host.clone() };
            self.shared.remote_targets.lock().unwrap().retain(|(a, p), _| !(a == &bind && *p == f.rule.bind_port as u32));
            if let Ok(h) = self.handle() {
                let port = f.rule.bind_port as u32;
                rt().spawn(async move {
                    let _ = h.cancel_tcpip_forward(bind, port).await;
                });
            }
        }
    }

    fn stop_all_forwards(&self) {
        let all: Vec<Forward> = self.forwards.lock().unwrap().drain().map(|(_, f)| f).collect();
        for f in all {
            self.shutdown_forward(f);
        }
    }

    pub fn active_forwards(&self) -> Vec<PortForward> {
        self.forwards.lock().unwrap().values().map(|f| f.rule.clone()).collect()
    }

    // --- SFTP -------------------------------------------------------------------------------

    /// The SFTP subsystem, opened on first use.
    pub fn sftp(&self) -> Result<Arc<russh_sftp::client::SftpSession>> {
        rt().block_on(async {
            let mut slot = self.sftp.lock().await;
            if let Some(s) = slot.as_ref() {
                return Ok(s.clone());
            }
            let h = self.handle()?;
            let ch = h.channel_open_session().await?;
            ch.request_subsystem(true, "sftp").await?;
            let s =
                Arc::new(russh_sftp::client::SftpSession::new(ch.into_stream()).await.map_err(|e| format!("sftpclient: failed to start SFTP subsystem: {e}"))?);
            *slot = Some(s.clone());
            Ok(s)
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn totp_matches_rfc_vector() {
        // RFC 6238 SHA-1 secret "12345678901234567890" is GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ in base32.
        let secret = "gezd gnbv gy3t qojq gezd gnbv gy3t qojq";
        assert_eq!(totp_at(secret, 59).unwrap(), "287082");
        assert_eq!(totp_at(secret, 1111111109).unwrap(), "081804");
        assert!(totp_code("JBSWY3DPEHPK3PXP").is_ok());
    }

    #[test]
    fn totp_prompt_detection() {
        assert!(is_totp_prompt("Verification code: "));
        assert!(is_totp_prompt("Enter your 6-digit OTP"));
        assert!(!is_totp_prompt("Password: "));
    }
}
