//! Per-protocol connect: SSH, GitHub, IRC, VNC and RDP, and the SSH tunnels they can use.

use super::*;

impl Manager {
    /// Known-hosts rows that are SSH certificate authorities.
    pub(super) fn cert_authorities(&self) -> Vec<KnownHost> {
        self.store.list_known_hosts().unwrap_or_default().into_iter().filter(|k| k.key_type == crate::known_hosts::CA_KEY_TYPE).collect()
    }

    // --- per-protocol connect -----------------------------------------------------------

    pub(super) fn connect_ssh(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let mut p = p.clone();
        let sc = p.ssh.get_or_insert_with(SshConfig::defaults).clone();
        let password = self.secret_fn(id, &p.id, SecretKind::Password, sc.secret_mode, prompt.clone());
        let passphrase = self.secret_fn(id, &p.id, SecretKind::Passphrase, sc.key_secret_mode, prompt);
        let c = SshClient::new(&p, self.terminal_callbacks(id, epoch));
        c.set_authorities(self.cert_authorities());
        let stored = |k: SecretKind| self.secrets.get(&k.key(&p.id)).unwrap_or_default();
        c.set_stored_secrets(stored(SecretKind::Totp), stored(SecretKind::ProxyPassword));
        c.connect(self.host_key_check(id, sc.host_key_policy), Some(password), Some(passphrase))?;
        for pf in sc.port_forwards.iter().filter(|f| f.enabled) {
            match c.start_forward(pf.clone()) {
                Ok(()) => self.emit(Event::ForwardStarted { session: id.into(), forward: pf.clone() }),
                Err(e) => self.emit(Event::ForwardError { session: id.into(), forward_id: pf.id.clone(), label: pf.label.clone(), error: e.to_string() }),
            }
        }
        self.start_login_scripts(id, epoch, sc.login_scripts.clone());
        Ok(Client::Ssh(c))
    }

    /// An SSH connection used only to tunnel another protocol (IRC, VNC).
    #[allow(clippy::too_many_arguments)]
    pub(super) fn tunnel(
        self: &Arc<Self>,
        id: &str,
        profile_id: &str,
        host: &str,
        port: i64,
        user: &str,
        method: &str,
        key: &str,
        agent: bool,
        mode: SecretMode,
        kind: &str,
        prompt: Prompt,
    ) -> Result<Arc<SshClient>> {
        let mut tp = Profile::new(Protocol::Ssh);
        tp.host = host.into();
        tp.port = if port == 0 { 22 } else { port };
        tp.username = user.into();
        let c = tp.ssh.as_mut().unwrap();
        c.auth_methods = vec![if method.is_empty() { "publickey".into() } else { method.into() }];
        c.private_key_path = key.into();
        c.agent_auth = agent;
        let (pw_kind, pp_kind) = match kind {
            "vnc" => (SecretKind::VncSshPassword, SecretKind::VncSshPassphrase),
            "rdp" => (SecretKind::RdpSshPassword, SecretKind::RdpSshPassphrase),
            _ => (SecretKind::IrcSshPassword, SecretKind::IrcSshPassphrase),
        };
        let password = (method == "password").then(|| self.secret_fn(id, profile_id, pw_kind, mode, prompt.clone()));
        let passphrase = self.secret_fn(id, profile_id, pp_kind, mode, prompt);
        let t = SshClient::new(&tp, Callbacks::noop());
        t.set_authorities(self.cert_authorities());
        t.connect_tunnel_only(self.host_key_check(id, HostKeyPolicy::Ask), password, Some(passphrase))?;
        Ok(t)
    }

    pub(super) fn connect_github(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let gh = p.github.clone().ok_or_else(|| format!("GitHub configuration missing on profile {:?}", p.id))?;
        let token = self.secret_fn(id, &p.id, SecretKind::GithubToken, gh.secret_mode, prompt.clone())().map_err(|e| format!("obtaining GitHub token: {e}"))?;
        if token.is_empty() {
            return err("GitHub token cannot be empty".into());
        }
        let mut client = github::Client::new(&gh.base_url, &token);
        let login = match client.validate_token() {
            Ok(l) => l,
            Err(e) => {
                let msg = e.to_string().to_lowercase();
                let auth = ["token validation failed", "authentication failed", "http 401", "http 403"].iter().any(|k| msg.contains(k));
                let retry = auth.then(|| prompt("GitHub token did not work. Please enter a valid token:").ok()).flatten().filter(|t| !t.is_empty());
                let Some(t) = retry else { return err(format!("GitHub authentication failed: {e}")) };
                if gh.secret_mode == SecretMode::Saved {
                    let _ = self.with(id, |s| s.session_secrets.lock().pending.insert(SecretKind::GithubToken.key(&p.id), Zeroizing::new(t.clone())));
                }
                client = github::Client::new(&gh.base_url, &t);
                client.validate_token().map_err(|e| format!("GitHub authentication failed: {e}"))?
            }
        };
        let client = Arc::new(client);
        if !self.is_current(id, epoch) {
            return err(format!("session {id:?} was closed while connecting"));
        }
        let fetch = {
            let (me, sid, client) = (self.clone(), id.to_string(), client.clone());
            move |status: &str| {
                me.emit(Event::GithubStatus { session: sid.clone(), status: status.into() });
                let (me2, sid2) = (me.clone(), sid.clone());
                client.fetch_repos_with_progress(move |done, total| {
                    if total > 0 {
                        me2.emit(Event::GithubStatus { session: sid2.clone(), status: format!("Loading commit info ({done}/{total})") });
                    }
                })
            }
        };
        let repos = fetch("Loading repositories...").map_err(|e| format!("fetching repositories: {e}"))?;
        self.emit(Event::GithubRepos { session: id.into(), login: login.clone(), repos });
        let interval = Duration::from_secs(if gh.refresh_interval_secs < 60 { 300 } else { gh.refresh_interval_secs as u64 });
        let stop = Arc::new(AtomicBool::new(false));
        let (me, sid, stop2) = (self.clone(), id.to_string(), stop.clone());
        std::thread::spawn(move || {
            loop {
                let deadline = std::time::Instant::now() + interval;
                while std::time::Instant::now() < deadline {
                    if stop2.load(Ordering::SeqCst) {
                        return;
                    }
                    std::thread::sleep(Duration::from_millis(500));
                }
                match fetch("Refreshing repositories...") {
                    Ok(repos) => me.emit(Event::GithubRepos { session: sid.clone(), login: login.clone(), repos }),
                    Err(e) => me.emit(Event::GithubError { session: sid.clone(), error: e.to_string() }),
                }
            }
        });
        Ok(Client::Github(client, stop))
    }

    pub(super) fn connect_irc(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let ic = p.irc.clone().ok_or("IRC configuration missing")?;
        let server_pass = if ic.server_password_required {
            self.secret_fn(id, &p.id, SecretKind::IrcServerPassword, ic.server_password_mode, prompt.clone())()
                .map_err(|e| format!("IRC server password: {e}"))?
                .to_string()
        } else {
            String::new()
        };
        let ns_pass = if ic.nick_serv_identify {
            self.secret_fn(id, &p.id, SecretKind::IrcNickServPassword, ic.nick_serv_mode, prompt.clone())()
                .map_err(|e| format!("NickServ password: {e}"))?
                .to_string()
        } else {
            String::new()
        };
        let (sasl_account, sasl_password) = if ic.sasl {
            let account = if ic.sasl_account.trim().is_empty() { ic.nick.clone() } else { ic.sasl_account.trim().to_string() };
            let pw = self.secret_fn(id, &p.id, SecretKind::IrcSaslPassword, ic.sasl_mode, prompt.clone())().map_err(|e| format!("SASL password: {e}"))?;
            (account, pw.to_string())
        } else {
            (String::new(), String::new())
        };
        let mut rooms = ic.auto_join_rooms.clone();
        if !p.id.is_empty() {
            let last = self.store.irc_last_rooms(&p.id).map_err(|e| format!("loading remembered IRC rooms: {e}"))?;
            rooms = crate::storage::dedup_rooms(&[&rooms, &last]);
        }
        let tunnel = if ic.ssh_tunnel {
            Some(self.tunnel(
                id,
                &p.id,
                &ic.ssh_host,
                ic.ssh_port,
                &ic.ssh_username,
                &ic.ssh_auth_method,
                &ic.ssh_private_key,
                ic.ssh_agent_auth,
                ic.ssh_secret_mode,
                "irc",
                prompt,
            )?)
        } else {
            None
        };
        let cb = self.terminal_callbacks(id, epoch);
        let (me, sid) = (self.clone(), id.to_string());
        let on_event: irc::EventFn = Arc::new(move |e| me.emit(Event::Irc { session: sid.clone(), event: e }));
        let (me, sid, pid) = (self.clone(), id.to_string(), p.id.clone());
        let on_rooms: irc::RoomsChangedFn = Arc::new(move |rooms: Vec<String>| {
            if pid.is_empty() {
                return;
            }
            if let Err(e) = me.store.save_irc_last_rooms(&pid, &rooms) {
                me.emit(Event::Irc {
                    session: sid.clone(),
                    event: irc::Event {
                        kind: "system".into(),
                        target: irc::SERVER_TARGET.into(),
                        text: format!("Could not remember open IRC rooms: {e}"),
                        timestamp: chrono::Utc::now().timestamp_millis(),
                        ..Default::default()
                    },
                });
            }
        });
        let connector: Option<irc::Connector> = tunnel.clone().map(|t| {
            let (host, port) = (p.host.clone(), p.port as u16);
            Arc::new(move || {
                let s = t.open_tunnel(&host, port)?;
                s.set_read_timeout(Some(Duration::from_millis(50)))?;
                Ok(Box::new(s) as Box<dyn irc::ReadWrite>)
            }) as irc::Connector
        });
        let cfg = irc::Config {
            server: p.host.clone(),
            port: p.port as u16,
            nick: ic.nick.clone(),
            user: if p.username.is_empty() { ic.nick.clone() } else { p.username.clone() },
            real_name: ic.real_name.clone(),
            use_tls: ic.use_tls,
            server_pass,
            auto_join_rooms: rooms,
            nickserv_identify: ic.nick_serv_identify,
            nickserv_account: ic.nick_serv_account.clone(),
            nickserv_password: ns_pass,
            sasl_account,
            sasl_password,
            connector,
            connect_timeout: Duration::from_secs(p.connect_timeout.max(1) as u64),
            on_event,
            on_error: cb.on_error,
            on_close: cb.on_close,
            on_rooms_changed: on_rooms,
        };
        let c = irc::Client::new(cfg);
        if let Err(e) = c.connect() {
            if let Some(t) = tunnel {
                t.close();
            }
            return Err(e);
        }
        Ok(Client::Irc(c, tunnel))
    }

    pub(super) fn connect_vnc(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let vc = p.vnc.clone().ok_or("VNC configuration missing")?;
        let host = if vc.vnc_host.is_empty() { p.host.clone() } else { vc.vnc_host.clone() };
        let port = if vc.vnc_port == 0 { 5900 } else { vc.vnc_port } as u16;
        let tunnel = if vc.ssh_tunnel {
            Some(self.tunnel(
                id,
                &p.id,
                &vc.ssh_host,
                vc.ssh_port,
                &vc.ssh_username,
                &vc.ssh_auth_method,
                &vc.ssh_private_key,
                vc.ssh_agent_auth,
                vc.ssh_secret_mode,
                "vnc",
                prompt.clone(),
            )?)
        } else {
            None
        };
        let password = self.secret_fn(id, &p.id, SecretKind::VncPassword, vc.secret_mode, prompt);
        let cb = self.terminal_callbacks(id, epoch);
        let (me, sid) = (self.clone(), id.to_string());
        let (me2, sid2) = (self.clone(), id.to_string());
        let clip: clients::vnc::ClipboardFn = Arc::new(move |text| me2.emit(Event::DesktopClipboard { session: sid2.clone(), text }));
        let c = connect_vnc_client(
            &host,
            port,
            &vc,
            p.connect_timeout,
            password,
            tunnel.clone(),
            cb,
            Arc::new(move || me.emit(Event::DesktopFrame { session: sid.clone() })),
            clip,
        );
        match c {
            Ok(c) => {
                self.emit(Event::DesktopReady { session: id.into() });
                Ok(Client::Vnc(c, tunnel))
            }
            Err(e) => {
                if let Some(t) = tunnel {
                    t.close();
                }
                Err(e)
            }
        }
    }

    pub(super) fn connect_rdp(self: &Arc<Self>, id: &str, epoch: u64, p: &Profile, prompt: Prompt) -> Result<Client> {
        let rc = p.rdp.clone().unwrap_or_else(RdpConfig::defaults);
        let port = if p.port == 0 { DEFAULT_RDP_PORT } else { p.port as u16 };
        let tunnel = if rc.ssh_tunnel {
            Some(self.tunnel(
                id,
                &p.id,
                &rc.ssh_host,
                rc.ssh_port,
                &rc.ssh_username,
                &rc.ssh_auth_method,
                &rc.ssh_private_key,
                rc.ssh_agent_auth,
                rc.ssh_secret_mode,
                "rdp",
                prompt.clone(),
            )?)
        } else {
            None
        };
        let r = self.rdp_client(id, epoch, p, &rc, port, tunnel.clone(), prompt);
        match r {
            Ok(c) => {
                self.emit(Event::DesktopReady { session: id.into() });
                Ok(Client::Rdp(c, tunnel))
            }
            Err(e) => {
                if let Some(t) = tunnel {
                    t.close();
                }
                Err(e)
            }
        }
    }

    #[allow(clippy::too_many_arguments)]
    pub(super) fn rdp_client(
        self: &Arc<Self>,
        id: &str,
        epoch: u64,
        p: &Profile,
        rc: &RdpConfig,
        port: u16,
        tunnel: Option<Arc<SshClient>>,
        prompt: Prompt,
    ) -> Result<Arc<clients::rdp::Client>> {
        let timeout = Duration::from_secs(p.connect_timeout.max(1) as u64);
        let host = p.host.clone();
        let gateway = (!rc.gateway_host.trim().is_empty()).then(|| rc.gateway_host.trim().to_string());
        let dial: clients::rdp::Dial = match (tunnel, gateway) {
            (_, Some(gw)) => {
                let pw = self.secret_fn(id, &p.id, SecretKind::RdpGatewayPassword, rc.gateway_secret_mode, prompt.clone())()?;
                clients::rdp::gateway_dial(gw, rc.gateway_username.clone(), pw, host.clone(), port)
            }
            (Some(t), None) => {
                let h = host.clone();
                Arc::new(move || t.open_tunnel(&h, port))
            }
            (None, None) => {
                let proxy = clients::proxy::Proxy {
                    kind: rc.proxy_type.clone(),
                    host: rc.proxy_host.clone(),
                    port: rc.proxy_port as u16,
                    username: rc.proxy_username.clone(),
                    password: self.secrets.get(&SecretKind::ProxyPassword.key(&p.id)).unwrap_or_default(),
                };
                let h = host.clone();
                Arc::new(move || proxy.connect(&h, port, timeout))
            }
        };
        let check = self.host_key_check(id, rc.host_key_policy);
        let h = host.clone();
        let check_cert: clients::rdp::CertCheck = Arc::new(move |fp: &str| {
            check(&HostKeyInfo { host: h.clone(), port, key_type: "rdp-tls".into(), fingerprint: fp.into(), public_key_b64: String::new() })
        });
        // A 0×0 configured size follows the tab, which reports its pixel size like a terminal.
        let (width, height) = match (rc.width, rc.height) {
            (w, h) if w > 0 && h > 0 => (w as u16, h as u16),
            _ => self.with(id, |s| s.pending_resize).ok().flatten().unwrap_or((1280, 800)),
        };
        let password = self.secret_fn(id, &p.id, SecretKind::RdpPassword, rc.secret_mode, prompt)()?;
        let cb = self.terminal_callbacks(id, epoch);
        let (me, sid) = (self.clone(), id.to_string());
        let (me2, sid2) = (self.clone(), id.to_string());
        let c = clients::rdp::Client::new(clients::rdp::Config {
            host,
            username: p.username.clone(),
            domain: rc.domain.clone(),
            password,
            width,
            height,
            allow_tls_only: rc.allow_tls_only,
            audio: rc.audio,
            kerberos_kdc: (!rc.kerberos_kdc.trim().is_empty()).then(|| rc.kerberos_kdc.trim().to_string()),
            shared_folder: (!rc.shared_folder.trim().is_empty()).then(|| (rc.shared_folder.trim().into(), "Nexus".into())),
            printer: rc.redirect_printer.then(|| {
                let (me, sid) = (self.clone(), id.to_string());
                let on_printed: clients::rdp_devices::PrintedFn =
                    Arc::new(move |f: PathBuf| me.emit(Event::Info { session: sid.clone(), message: format!("Print job saved to {}", f.display()) }));
                (downloads_dir(), on_printed)
            }),
            dial,
            check_cert,
            connect_timeout: timeout,
            on_frame: Arc::new(move || me.emit(Event::DesktopFrame { session: sid.clone() })),
            on_clipboard: Arc::new(move |text| me2.emit(Event::DesktopClipboard { session: sid2.clone(), text })),
            on_error: cb.on_error,
            on_close: cb.on_close,
        });
        c.connect()?;
        Ok(c)
    }
}

/// Builds and connects a VNC client from a profile's settings, optionally through an SSH tunnel.
#[allow(clippy::too_many_arguments)]
pub(super) fn connect_vnc_client(
    host: &str,
    port: u16,
    vc: &VncConfig,
    timeout: i64,
    password: SecretFn,
    tunnel: Option<Arc<SshClient>>,
    cb: Callbacks,
    on_frame: Arc<dyn Fn() + Send + Sync>,
    on_clipboard: clients::vnc::ClipboardFn,
) -> Result<Arc<clients::vnc::Client>> {
    let connector: Option<irc::Connector> = tunnel.map(|t| {
        let host = host.to_string();
        Arc::new(move || {
            let s = t.open_tunnel(&host, port)?;
            s.set_read_timeout(Some(Duration::from_millis(50)))?;
            Ok(Box::new(s) as Box<dyn irc::ReadWrite>)
        }) as irc::Connector
    });
    let mut cfg = clients::vnc::Config {
        host: host.into(),
        port,
        password: None,
        shared: vc.shared_session,
        view_only: vc.view_only,
        quality: vc.quality.clamp(0, 9) as u8,
        connector,
        connect_timeout: Duration::from_secs(timeout.max(1) as u64),
        on_frame,
        on_clipboard,
        on_error: cb.on_error,
        on_close: cb.on_close,
        ..Default::default()
    };
    let c = clients::vnc::Client::new(cfg.clone());
    match c.connect() {
        Ok(()) => Ok(c),
        // Ask for the password only when the server wants one.
        Err(e) if e.to_string().to_lowercase().contains("password") => {
            cfg.password = Some(password()?.to_string());
            let c = clients::vnc::Client::new(cfg);
            c.connect()?;
            Ok(c)
        }
        Err(e) => Err(e),
    }
}

/// Where print jobs and similar go: ~/Downloads when it exists, else the home folder.
fn downloads_dir() -> PathBuf {
    let home = std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" }).map(PathBuf::from).unwrap_or_else(std::env::temp_dir);
    let d = home.join("Downloads");
    if d.is_dir() { d } else { home }
}
