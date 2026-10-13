// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! SSH client on russh: interactive PTY shell, the profile's auth chain (agent, public key,
//! password, keyboard-interactive with TOTP), jump hosts, SOCKS5/HTTP proxies, local and remote
//! port forwards, agent forwarding and SFTP. A port of internal/sshclient + sftpclient.

use super::{Callbacks, rt};
use crate::Result;
use crate::models::{ForwardDirection, JumpHost, PortForward, Profile, SshConfig};
use base64::Engine;
use parking_lot::Mutex;
use russh::client::{self, Handle, Msg};
use russh::keys::{HashAlg, PrivateKeyWithHashAlg, PublicKey};
use russh::{Channel, ChannelMsg, Pty};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::sync::mpsc;
use zeroize::Zeroizing;

const MAX_PRIVATE_KEY_BYTES: u64 = 1 << 20;
/// Bounds what `run_command` buffers, so a hostile server can't stream unlimited output into memory.
const MAX_COMMAND_OUTPUT_BYTES: usize = 1 << 20;
/// How long `run_command` waits for a command to finish.
const COMMAND_TIMEOUT: Duration = Duration::from_secs(30);
/// Keyboard-interactive rounds answered before giving up on the method.
const MAX_KBD_INTERACTIVE_ROUNDS: usize = 10;
/// Extra handshake time allowed for the host-key prompt (see `credentials::host_key_check`).
const HOST_KEY_PROMPT_SECS: u64 = 60;
/// Remote forwards listen on the server's loopback unless told otherwise, as `ssh -R` does; an
/// empty bind host used to mean every interface, exposing the forwarded service to the server's
/// whole network wherever `GatewayPorts` is on.
const REMOTE_BIND_DEFAULT: &str = "localhost";

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
/// The value is wiped from memory when dropped; libraries that need a plain `String` get a copy.
pub type SecretFn = Arc<dyn Fn() -> Result<zeroize::Zeroizing<String>> + Send + Sync>;

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
    /// Whether this is the final hop. Only the target may open agent-forward and remote-forward
    /// channels; a jump host never asked for either and must not get them (as `ssh -J` behaves).
    target: bool,
    /// Certificate authorities trusted for this host (from `@cert-authority` known-hosts rows).
    cas: Vec<PublicKey>,
}

/// A host certificate is trusted when a CA for this host signed it, it is a host certificate,
/// it names the host as a principal and it is inside its validity window.
fn check_host_certificate(cert: &russh::keys::Certificate, cas: &[PublicKey], host: &str) -> std::result::Result<(), String> {
    use russh::keys::ssh_key::certificate::CertType;
    if cert.cert_type() != CertType::Host {
        return Err(format!("{host} presented a user certificate as its host key"));
    }
    if !cert.valid_principals().iter().any(|p| p.eq_ignore_ascii_case(host)) {
        return Err(format!("host certificate for {host} doesn't list it (principals: {})", cert.valid_principals().join(", ")));
    }
    let fps: Vec<_> = cas.iter().map(|k| k.fingerprint(HashAlg::Sha256)).collect();
    cert.validate(&fps).map_err(|_| format!("host certificate for {host} isn't signed by a trusted CA, or is expired"))
}

impl client::Handler for Handler {
    type Error = russh::Error;

    async fn check_server_key(&mut self, key: &russh::keys::PublicKeyOrCertificate) -> std::result::Result<bool, Self::Error> {
        let key = match key {
            russh::keys::PublicKeyOrCertificate::PublicKey { key, .. } => key.clone(),
            // Only asked for when a CA covers this host, so it must check out against one.
            russh::keys::PublicKeyOrCertificate::Certificate(c) => {
                return match check_host_certificate(c, &self.cas, &self.host) {
                    Ok(()) => {
                        tracing::info!(host = %self.host, ca = %c.signature_key().fingerprint(HashAlg::Sha256), "host certificate accepted");
                        Ok(true)
                    }
                    Err(why) => {
                        *self.shared.rejection.lock() = Some(why);
                        Ok(false)
                    }
                };
            }
        };
        let info = host_key_info(&self.host, self.port, &key);
        let check = self.check.clone();
        // The check may block on a UI prompt.
        let res = tokio::task::spawn_blocking(move || check(&info)).await.unwrap_or(Err("host key check failed".into()));
        match res {
            Ok(()) => Ok(true),
            Err(why) => {
                *self.shared.rejection.lock() = Some(why);
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
        let dest = self.shared.remote_targets.lock().get(&(connected_address.to_string(), connected_port)).cloned();
        let Some((host, port)) = dest.filter(|_| self.target) else {
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
        if !self.target || !self.shared.agent_forwarding.load(std::sync::atomic::Ordering::Relaxed) {
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
/// The OpenSSH user certificate next to a private key (`id_ed25519` → `id_ed25519-cert.pub`).
fn load_user_certificate(key_path: &str) -> Option<russh::keys::Certificate> {
    if key_path.is_empty() {
        return None;
    }
    let text = std::fs::read_to_string(format!("{key_path}-cert.pub")).ok().filter(|t| t.len() < 64 << 10)?;
    russh::keys::Certificate::from_openssh(text.trim()).ok()
}

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

async fn dial(sc: &SshConfig, proxy_password: &str, host: &str, port: u16, timeout: Duration) -> Result<tokio::net::TcpStream> {
    let proxy = super::proxy::Proxy {
        kind: sc.proxy_type.clone(),
        host: sc.proxy_host.clone(),
        port: sc.proxy_port as u16,
        username: sc.proxy_username.clone(),
        password: proxy_password.into(),
    };
    let host = host.to_string();
    let s = blocking(move || proxy.connect(&host, port, timeout)).await?;
    s.set_nonblocking(true)?;
    Ok(tokio::net::TcpStream::from_std(s)?)
}

/// `host_certificates`: ask for a host certificate first (when a CA applies), as OpenSSH does.
fn config(sc: &SshConfig, host_certificates: bool) -> Arc<client::Config> {
    let mut c = client::Config::default();
    if host_certificates {
        c.preferred.host_key_certificates = c.preferred.key.clone();
    }
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
    /// `<key>-cert.pub` next to the private key: log in with the certificate instead.
    cert: Option<russh::keys::Certificate>,
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
                if let (Some(k), Some(cert)) = (&a.key, &a.cert)
                    && h.authenticate_openssh_cert(a.user, Arc::new(k.clone()), cert.clone()).await?.success()
                {
                    return Ok(());
                }
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
                    if h.authenticate_password(a.user, pw.as_str()).await?.success() {
                        return Ok(());
                    }
                }
            }
            "keyboard-interactive" => {
                let Some(get) = a.password.clone() else { continue };
                let mut resp = h.authenticate_keyboard_interactive_start(a.user, None).await?;
                // A server can keep asking forever (harvesting TOTP codes, parking the connect
                // thread on prompts); OpenSSH gives up after a handful of rounds too.
                for _ in 0..MAX_KBD_INTERACTIVE_ROUNDS {
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
                                    answers.push(blocking(move || get()).await?.to_string());
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
    /// Credentials that live in the secret store rather than the profile: (TOTP seed, proxy
    /// password). Set by the session manager before connecting.
    stored: Mutex<(Zeroizing<String>, Zeroizing<String>)>,
    /// `@cert-authority` known-hosts rows (CA keys by host pattern), for host certificates.
    authorities: Mutex<Vec<crate::models::KnownHost>>,
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
            stored: Mutex::default(),
            authorities: Mutex::default(),
        })
    }

    /// Certificate authorities (known-hosts CA rows) to trust for host certificates.
    pub fn set_authorities(&self, rows: Vec<crate::models::KnownHost>) {
        *self.authorities.lock() = rows;
    }

    /// The TOTP seed (for keyboard-interactive 2FA prompts) and proxy password to use.
    pub fn set_stored_secrets(&self, totp_secret: String, proxy_password: String) {
        *self.stored.lock() = (Zeroizing::new(totp_secret), Zeroizing::new(proxy_password));
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
        let ch = rt()
            .block_on(h.channel_open_direct_tcpip(host.clone(), port as u32, "127.0.0.1", 0))
            .map_err(|e| format!("SSH tunnel to {host}:{port} failed: {e}"))?;
        let (client, server) = loopback_pair()?;
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
        target: bool,
    ) -> Result<Handle<Handler>> {
        let cas = crate::known_hosts::authorities_for(&self.authorities.lock(), host, port);
        let cfg = config(&self.sc(), !cas.is_empty());
        let handler = Handler { host: host.into(), port, check: check.clone(), shared: self.shared.clone(), cas, target };
        // The handshake may stop at the host-key prompt (up to 60 s): don't let the connect
        // timeout expire underneath the user while they read a fingerprint.
        let timeout = Duration::from_secs(self.profile.connect_timeout.max(1) as u64 + HOST_KEY_PROMPT_SECS);
        match tokio::time::timeout(timeout, client::connect_stream(cfg, stream, handler)).await {
            Err(_) => Err(format!("SSH handshake with {host}:{port} timed out").into()),
            Ok(Ok(h)) => Ok(h),
            Ok(Err(e)) => match self.shared.rejection.lock().take() {
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

        let (totp_secret, proxy_password) = {
            let s = self.stored.lock();
            (s.0.to_string(), s.1.clone())
        };
        let mut handle = if sc.jump_hosts.is_empty() {
            let tcp = dial(&sc, &proxy_password, &p.host, port, timeout).await.map_err(|e| format!("SSH connection failed: {e}"))?;
            self.handshake(tcp, &p.host, port, &check, true).await?
        } else {
            let (prev, h) = self.via_jumps(&sc, &check, passphrase.clone(), timeout).await?;
            let ch = tokio::time::timeout(timeout, prev.channel_open_direct_tcpip(p.host.clone(), port as u32, "127.0.0.1", 0))
                .await
                .map_err(|_| format!("failed to dial target {}:{port} through jump hosts: timeout", p.host))?
                .map_err(|e| format!("failed to dial target {}:{port} through jump hosts: {e}", p.host))?;
            self.hops.lock().extend(h);
            self.hops.lock().push(prev);
            self.handshake(ch.into_stream(), &p.host, port, &check, true).await?
        };

        let cert = load_user_certificate(&sc.private_key_path);
        authenticate(&mut handle, Auth { user: &p.username, methods, agent: sc.agent_auth, key, cert, password, totp_secret }).await?;
        let handle = Arc::new(handle);
        if !shell {
            *self.handle.lock() = Some(handle);
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
        *self.tx.lock() = Some(tx);
        *self.handle.lock() = Some(handle);
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
        super::guarded_task(self.cb.on_error.clone(), self.cb.on_close.clone(), async move {
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
                    self.handshake(tcp, host, jport, check, false).await?
                }
                Some(prev) => {
                    let ch = tokio::time::timeout(timeout, prev.channel_open_direct_tcpip(host.clone(), jport as u32, "127.0.0.1", 0))
                        .await
                        .map_err(|_| format!("failed to dial through jump host {i} ({host}:{jport}): timeout"))?
                        .map_err(|e| format!("failed to dial through jump host {i} ({host}:{jport}): {e}"))?;
                    self.handshake(ch.into_stream(), host, jport, check, false).await?
                }
            };
            authenticate(
                &mut h,
                Auth {
                    user: username,
                    methods: vec!["publickey".into()],
                    agent: key.is_none() || sc.agent_auth,
                    key,
                    cert: None,
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
        self.handle.lock().clone().ok_or_else(|| "not connected".into())
    }

    pub fn write(&self, data: &[u8]) -> Result<()> {
        let tx = self.tx.lock();
        tx.as_ref().ok_or("not connected")?.send(Cmd::Data(data.to_vec())).map_err(|_| "not connected".into())
    }

    pub fn resize(&self, cols: u32, rows: u32) -> Result<()> {
        let tx = self.tx.lock();
        tx.as_ref().ok_or("not connected")?.send(Cmd::Resize(cols, rows)).map_err(|_| "not connected".into())
    }

    /// Round trip of a keepalive, in milliseconds.
    pub fn ping(&self) -> Result<i64> {
        let h = self.handle()?;
        let start = Instant::now();
        rt().block_on(h.send_ping())?;
        Ok(start.elapsed().as_millis() as i64)
    }

    /// Runs a command on a separate channel and returns trimmed stdout, giving up after
    /// `COMMAND_TIMEOUT` so a server that never answers can't park the caller's thread.
    pub fn run_command(&self, command: &str) -> Result<String> {
        let h = self.handle()?;
        let command = command.to_string();
        rt().block_on(async move {
            match tokio::time::timeout(COMMAND_TIMEOUT, Self::run_command_inner(h, command)).await {
                Ok(r) => r,
                Err(_) => Err("command timed out".into()),
            }
        })
    }

    async fn run_command_inner(h: Arc<Handle<Handler>>, command: String) -> Result<String> {
        {
            let mut ch = h.channel_open_session().await.map_err(|e| format!("failed to open SSH session: {e}"))?;
            ch.exec(true, command.as_bytes()).await?;
            let mut out = vec![];
            while let Some(m) = ch.wait().await {
                match m {
                    ChannelMsg::Data { data } => {
                        if out.len() + data.len() > MAX_COMMAND_OUTPUT_BYTES {
                            return Err(format!("command output exceeds {MAX_COMMAND_OUTPUT_BYTES} bytes").into());
                        }
                        out.extend_from_slice(&data);
                    }
                    ChannelMsg::Eof | ChannelMsg::Close => break,
                    _ => {}
                }
            }
            Ok(String::from_utf8_lossy(&out).trim().to_string())
        }
    }

    pub fn is_connected(&self) -> bool {
        self.handle.lock().as_ref().is_some_and(|h| !h.is_closed())
    }

    /// Idempotent; fires on_close once.
    pub fn close(&self) {
        if self.closed.swap(true, std::sync::atomic::Ordering::SeqCst) {
            return;
        }
        self.stop_all_forwards();
        *self.tx.lock() = None;
        let h = self.handle.lock().take();
        let hops = std::mem::take(&mut *self.hops.lock());
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
        if self.forwards.lock().contains_key(&rule.id) {
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
                        let mut handles = t2.lock();
                        handles.retain(|h| !h.is_finished());
                        handles.push(conn.abort_handle());
                    }
                });
                tasks.lock().push(task.abort_handle());
            }
            ForwardDirection::Remote => {
                let bind = if rule.bind_host.is_empty() { REMOTE_BIND_DEFAULT.to_string() } else { rule.bind_host.clone() };
                let port = rt()
                    .block_on(h.tcpip_forward(bind.clone(), rule.bind_port as u32))
                    .map_err(|e| format!("remote forward: server listen on {bind}:{} failed: {e}", rule.bind_port))?;
                let port = if rule.bind_port == 0 { port } else { rule.bind_port as u32 };
                self.shared.remote_targets.lock().insert((bind, port), (rule.dest_host.clone(), rule.dest_port as u16));
            }
        }
        self.forwards.lock().insert(rule.id.clone(), Forward { rule, tasks });
        Ok(())
    }

    pub fn stop_forward(&self, id: &str) -> Result<()> {
        let f = self.forwards.lock().remove(id).ok_or_else(|| format!("forward {id:?} not found"))?;
        self.shutdown_forward(f);
        Ok(())
    }

    fn shutdown_forward(&self, f: Forward) {
        for t in f.tasks.lock().drain(..) {
            t.abort();
        }
        if f.rule.direction == ForwardDirection::Remote {
            let bind = if f.rule.bind_host.is_empty() { REMOTE_BIND_DEFAULT.to_string() } else { f.rule.bind_host.clone() };
            self.shared.remote_targets.lock().retain(|(a, p), _| !(a == &bind && *p == f.rule.bind_port as u32));
            if let Ok(h) = self.handle() {
                let port = f.rule.bind_port as u32;
                rt().spawn(async move {
                    let _ = h.cancel_tcpip_forward(bind, port).await;
                });
            }
        }
    }

    fn stop_all_forwards(&self) {
        let all: Vec<Forward> = self.forwards.lock().drain().map(|(_, f)| f).collect();
        for f in all {
            self.shutdown_forward(f);
        }
    }

    pub fn active_forwards(&self) -> Vec<PortForward> {
        self.forwards.lock().values().map(|f| f.rule.clone()).collect()
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

/// A connected pair of loopback sockets. The accepted end is checked against the connecting
/// end's address, so another local process racing for the ephemeral port can't be bridged
/// into the tunnel instead of our own client.
pub(crate) fn loopback_pair() -> std::io::Result<(std::net::TcpStream, std::net::TcpStream)> {
    let listener = std::net::TcpListener::bind("127.0.0.1:0")?;
    let client = std::net::TcpStream::connect(listener.local_addr()?)?;
    let ours = client.local_addr()?;
    loop {
        let (server, peer) = listener.accept()?;
        if peer == ours {
            return Ok((client, server));
        }
        // A stranger connected first: drop it and keep waiting for our own connection.
        drop(server);
    }
}

/// Generates `<dir>/<name>` and `<dir>/<name>.pub` in OpenSSH format, in-process, so a
/// passphrase never appears on a command line. `kind` is ed25519 (default), ecdsa (P-256) or rsa
/// (`bits` 2048/3072/4096, default 4096). Returns the private key path.
pub fn generate_key_pair(dir: &std::path::Path, name: &str, kind: &str, bits: u32, passphrase: &str) -> Result<std::path::PathBuf> {
    use russh::keys::ssh_key::private::{EcdsaKeypair, Ed25519Keypair, KeypairData, RsaKeypair};
    use russh::keys::ssh_key::{EcdsaCurve, LineEnding};
    let name = crate::keyfiles::validate_ssh_key_name(name)?;
    let kind = if kind.trim().is_empty() { "ed25519".to_string() } else { kind.trim().to_lowercase() };
    let mut rng = OsCryptoRng;
    let data = match kind.as_str() {
        "ed25519" => KeypairData::from(Ed25519Keypair::random(&mut rng)),
        "ecdsa" => KeypairData::from(EcdsaKeypair::random(&mut rng, EcdsaCurve::NistP256).map_err(|e| e.to_string())?),
        "rsa" => {
            let bits = match bits {
                0 => 4096,
                2048 | 3072 | 4096 => bits,
                b => return Err(format!("unsupported RSA key size {b} (use 2048, 3072, or 4096)").into()),
            };
            KeypairData::from(RsaKeypair::random(&mut rng, bits as usize).map_err(|e| e.to_string())?)
        }
        k => return Err(format!("unsupported SSH key type {k:?}").into()),
    };
    let comment = format!("{name}@nexus-terminal");
    let key = russh::keys::PrivateKey::new(data, comment.as_str()).map_err(|e| e.to_string())?;
    let key = if passphrase.is_empty() { key } else { key.encrypt(&mut rng, passphrase.as_bytes()).map_err(|e| e.to_string())? };
    crate::config::create_private_dir(dir)?;
    let path = dir.join(&name);
    if path.exists() || path.with_extension("pub").exists() {
        return Err(format!("key {name:?} already exists").into());
    }
    key.write_openssh_file(&path, LineEnding::LF).map_err(|e| format!("failed to write private key: {e}"))?;
    let mut public = key.public_key().clone();
    public.set_comment(comment.as_str());
    public.write_openssh_file(path.with_extension("pub")).map_err(|e| format!("failed to write public key: {e}"))?;
    Ok(path)
}

/// The OS CSPRNG as the `rand_core` 0.10 generator `ssh-key` expects (our `rand` is 0.8).
struct OsCryptoRng;

impl russh::keys::ssh_key::rand_core::TryRng for OsCryptoRng {
    type Error = std::convert::Infallible;
    fn try_next_u32(&mut self) -> std::result::Result<u32, Self::Error> {
        Ok(rand::RngCore::next_u32(&mut rand::rngs::OsRng))
    }
    fn try_next_u64(&mut self) -> std::result::Result<u64, Self::Error> {
        Ok(rand::RngCore::next_u64(&mut rand::rngs::OsRng))
    }
    fn try_fill_bytes(&mut self, dst: &mut [u8]) -> std::result::Result<(), Self::Error> {
        rand::RngCore::fill_bytes(&mut rand::rngs::OsRng, dst);
        Ok(())
    }
}

impl russh::keys::ssh_key::rand_core::TryCryptoRng for OsCryptoRng {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn loopback_pair_ignores_a_stranger_that_connects_first() {
        // Bind, then let a "stranger" race us for the port before our own connect: the pair must
        // still bridge our client to our server end.
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let _stranger = std::net::TcpStream::connect(addr).unwrap();
        let client = std::net::TcpStream::connect(addr).unwrap();
        let ours = client.local_addr().unwrap();
        let server = loop {
            let (s, peer) = listener.accept().unwrap();
            if peer == ours {
                break s;
            }
        };
        assert_eq!(server.peer_addr().unwrap(), ours);
        let (c, s) = loopback_pair().unwrap();
        assert_eq!(c.local_addr().unwrap(), s.peer_addr().unwrap());
    }

    #[test]
    fn generates_encrypted_key_pairs_in_process() {
        let dir = std::env::temp_dir().join(format!("nexus-keygen-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let path = generate_key_pair(&dir, "id_test", "ed25519", 0, "pass phrase").unwrap();
        assert_eq!(path, dir.join("id_test"));
        let public = std::fs::read_to_string(dir.join("id_test.pub")).unwrap();
        assert!(public.starts_with("ssh-ed25519 ") && public.trim_end().ends_with("id_test@nexus-terminal"), "{public}");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(std::fs::metadata(&path).unwrap().permissions().mode() & 0o777, 0o600);
            assert_eq!(std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777, 0o700, "a fresh ~/.ssh is private");
        }
        assert!(crate::keyfiles::is_likely_private_key(&path));
        // Encrypted: unreadable without the passphrase, readable with it.
        let p = path.to_string_lossy().into_owned();
        assert!(load_private_key(&p, None).is_err());
        let pp: SecretFn = Arc::new(|| Ok(zeroize::Zeroizing::new("pass phrase".into())));
        assert_eq!(load_private_key(&p, Some(&pp)).unwrap().algorithm().as_str(), "ssh-ed25519");
        assert!(generate_key_pair(&dir, "id_test", "ed25519", 0, "").is_err(), "must not overwrite");
        let plain = generate_key_pair(&dir, "id_plain", "ecdsa", 0, "").unwrap();
        assert!(load_private_key(&plain.to_string_lossy(), None).is_ok());
        assert!(generate_key_pair(&dir, "bad", "rsa", 1024, "").is_err());
        assert!(generate_key_pair(&dir, "../x", "ed25519", 0, "").is_err());
        std::fs::remove_dir_all(dir).unwrap();
    }

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
