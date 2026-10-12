//! Parses OpenSSH `~/.ssh/config` Host blocks into SSH profiles. Supports HostName, Port, User,
//! IdentityFile, ProxyJump (first hop), ForwardAgent and Compression. Match blocks, wildcard-only
//! hosts, Include and `%h`-style tokens are ignored, as in the Go app.

use crate::Result;
use crate::models::{HostKeyPolicy, JumpHost, Profile, Protocol, SecretMode, SshConfig};
use std::path::{Path, PathBuf};

const MAX_CONFIG_FILE_BYTES: u64 = 5 * 1024 * 1024;
const MAX_CONFIG_LINE_BYTES: usize = 256 * 1024;
const MAX_HOST_BLOCKS: usize = 5000;

#[derive(Clone, Debug, PartialEq, Default)]
pub struct HostBlock {
    pub aliases: Vec<String>,
    pub host_name: String,
    pub port: i64,
    pub user: String,
    pub identity_file: String,
    pub proxy_jump: String,
    pub forward_agent: bool,
    pub compression: bool,
}

pub fn parse_file(path: &Path) -> Result<Vec<HostBlock>> {
    let p = path.display();
    let meta = std::fs::metadata(path).map_err(|e| format!("sshconfig: failed to open {p}: {e}"))?;
    if meta.is_dir() {
        return Err(format!("sshconfig: {p} is a directory").into());
    }
    if meta.len() > MAX_CONFIG_FILE_BYTES {
        return Err(format!("sshconfig: config file is too large: {} bytes exceeds {MAX_CONFIG_FILE_BYTES} bytes", meta.len()).into());
    }
    let raw = std::fs::read(path).map_err(|e| format!("sshconfig: failed to open {p}: {e}"))?;
    parse(&String::from_utf8_lossy(&raw))
}

fn parse(text: &str) -> Result<Vec<HostBlock>> {
    let mut blocks = Vec::new();
    let mut current: Option<HostBlock> = None;
    let mut commit = |cur: &mut Option<HostBlock>| -> Result<()> {
        if let Some(b) = cur.take() {
            if blocks.len() >= MAX_HOST_BLOCKS {
                return Err(format!("sshconfig: too many Host blocks (max {MAX_HOST_BLOCKS})").into());
            }
            blocks.push(b);
        }
        Ok(())
    };
    for line in text.lines() {
        if line.len() > MAX_CONFIG_LINE_BYTES {
            return Err("sshconfig: read error: token too long".into());
        }
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let (key, value) = split_directive(line);
        match key.to_lowercase().as_str() {
            "host" => {
                commit(&mut current)?;
                let aliases: Vec<String> = value.split_whitespace().map(String::from).collect();
                // Skip wildcard-only hosts (e.g. "Host *").
                let wildcard = aliases.iter().any(|a| a.contains(['*', '?']));
                if !(wildcard && aliases.len() == 1) {
                    current = Some(HostBlock { aliases, port: 22, ..Default::default() });
                }
            }
            "match" => commit(&mut current)?,
            key => {
                let Some(b) = current.as_mut() else { continue };
                match key {
                    "hostname" => b.host_name = value.into(),
                    "port" => {
                        if let Ok(p) = value.parse() {
                            b.port = p
                        }
                    }
                    "user" => b.user = value.into(),
                    "identityfile" => b.identity_file = expand_tilde(value),
                    "proxyjump" => b.proxy_jump = value.into(),
                    "forwardagent" => b.forward_agent = is_trueish(value),
                    "compression" => b.compression = is_trueish(value),
                    _ => {}
                }
            }
        }
    }
    commit(&mut current)?;
    Ok(blocks)
}

/// `~/.ssh/config`, if the home directory is known.
pub fn default_config_path() -> Option<PathBuf> {
    std::env::home_dir().map(|h| h.join(".ssh").join("config"))
}

/// One SSH profile per block; the first alias becomes the display name.
pub fn to_profiles(blocks: &[HostBlock], folder: &str) -> Vec<Profile> {
    blocks
        .iter()
        .filter(|b| !b.aliases.is_empty())
        .map(|b| {
            let mut ssh = SshConfig {
                auth_methods: vec!["publickey".into(), "password".into()],
                secret_mode: SecretMode::AskEveryTime,
                host_key_policy: HostKeyPolicy::Ask,
                terminal_type: "xterm-256color".into(),
                agent_auth: b.forward_agent,
                agent_forwarding: b.forward_agent,
                compression: b.compression,
                private_key_path: b.identity_file.clone(),
                ..Default::default()
            };
            if !b.proxy_jump.is_empty() && b.proxy_jump != "none" {
                ssh.jump_hosts = parse_proxy_jump(&b.proxy_jump).into_iter().collect();
            }
            let mut p = Profile::new(Protocol::Ssh);
            p.display_name = b.aliases[0].clone();
            p.host = if b.host_name.is_empty() { b.aliases[0].clone() } else { b.host_name.clone() };
            p.port = b.port;
            p.username = b.user.clone();
            p.folder = folder.into();
            p.ssh = Some(ssh);
            p.tags = vec!["ssh-config-import".into()];
            p
        })
        .collect()
}

/// `[user@]host[:port]`; only the first hop of a comma-separated chain.
fn parse_proxy_jump(value: &str) -> Option<JumpHost> {
    let mut value = value.split(',').next().unwrap_or("").trim();
    if value.is_empty() || value == "none" {
        return None;
    }
    let mut jh = JumpHost { port: 22, auth_method: "publickey".into(), secret_mode: SecretMode::AskEveryTime, ..Default::default() };
    if let Some((user, rest)) = value.split_once('@') {
        jh.username = user.into();
        value = rest;
    }
    if let Some((host, port)) = value.rsplit_once(':')
        && let Ok(p) = port.parse()
    {
        jh.port = p;
        value = host;
    }
    jh.host = value.into();
    Some(jh)
}

/// `Key Value` or `Key=Value`.
fn split_directive(line: &str) -> (&str, &str) {
    if let Some((k, v)) = line.split_once('=') {
        return (k.trim(), v.trim());
    }
    match line.split_once(' ').or_else(|| line.split_once('\t')) {
        Some((k, v)) => (k.trim(), v.trim()),
        None => (line, ""),
    }
}

fn expand_tilde(path: &str) -> String {
    match (path.strip_prefix("~/"), std::env::home_dir()) {
        (Some(rest), Some(home)) => home.join(rest).to_string_lossy().into_owned(),
        _ => path.into(),
    }
}

fn is_trueish(v: &str) -> bool {
    matches!(v.to_lowercase().as_str(), "yes" | "true" | "on" | "1")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hostile_configs_never_panic() {
        let mut n = crate::Noise::new(0x55c);
        let words: &[&[u8]] =
            &[b"Host ", b"HostName ", b"Port ", b"User ", b"ProxyJump ", b"IdentityFile ", b"Match ", b"*", b"=", b"\n", b"\"", b" ", b"a@b:22,", b"99999"];
        for _ in 0..3000 {
            let mut text = Vec::new();
            for _ in 0..n.below(40) {
                text.extend_from_slice(words[n.below(words.len())]);
            }
            text.extend(n.bytes(20, b" \t\n="));
            if let Ok(blocks) = parse(&String::from_utf8_lossy(&text)) {
                let _ = to_profiles(&blocks, "f");
            }
        }
    }

    const TEST_CONFIG: &str = "# Test SSH config

Host *
    ServerAliveInterval 60

Host webserver
    HostName web.example.com
    Port 2222
    User deploy
    IdentityFile ~/.ssh/deploy_key
    ForwardAgent yes
    Compression yes

Host dbserver
    HostName db.internal.example.com
    User postgres
    ProxyJump bastion@jump.example.com:2200

Host simple
    HostName 10.0.0.5

Host wildcard-*
    User admin
";

    #[test]
    fn parses_file() {
        let path = std::env::temp_dir().join(format!("nexus-sshconfig-{}", std::process::id()));
        std::fs::write(&path, TEST_CONFIG).unwrap();
        let blocks = parse_file(&path).unwrap();
        std::fs::remove_file(&path).unwrap();
        assert_eq!(blocks.len(), 3);
        let wb = &blocks[0];
        assert_eq!(wb.aliases[0], "webserver");
        assert_eq!((wb.host_name.as_str(), wb.port, wb.user.as_str()), ("web.example.com", 2222, "deploy"));
        assert!(wb.forward_agent && wb.compression);
        assert!(wb.identity_file.ends_with(".ssh/deploy_key") && !wb.identity_file.starts_with('~'));
        assert_eq!(blocks[1].aliases[0], "dbserver");
        assert_eq!(blocks[1].proxy_jump, "bastion@jump.example.com:2200");
        assert_eq!((blocks[2].host_name.as_str(), blocks[2].port), ("10.0.0.5", 22));
    }

    #[test]
    fn rejects_oversized_file() {
        let path = std::env::temp_dir().join(format!("nexus-sshconfig-big-{}", std::process::id()));
        std::fs::File::create(&path).unwrap().set_len(MAX_CONFIG_FILE_BYTES + 1).unwrap();
        let err = parse_file(&path).unwrap_err();
        std::fs::remove_file(&path).unwrap();
        assert!(err.to_string().contains("too large"));
    }

    #[test]
    fn rejects_too_many_host_blocks() {
        let text: String = (0..=MAX_HOST_BLOCKS).map(|i| format!("Host host{i}\n    HostName example.com\n")).collect();
        assert!(parse(&text).unwrap_err().to_string().contains("too many Host blocks"));
    }

    #[test]
    fn converts_to_profiles() {
        let blocks = vec![
            HostBlock {
                aliases: vec!["myserver".into()],
                host_name: "srv.example.com".into(),
                port: 22,
                user: "admin".into(),
                forward_agent: true,
                ..Default::default()
            },
            HostBlock {
                aliases: vec!["jumpbox".into()],
                host_name: "jump.example.com".into(),
                port: 2200,
                user: "jumper".into(),
                proxy_jump: "gateway@gw.example.com".into(),
                ..Default::default()
            },
        ];
        let ps = to_profiles(&blocks, "Imported");
        assert_eq!(ps.len(), 2);
        assert_eq!((ps[0].display_name.as_str(), ps[0].host.as_str()), ("myserver", "srv.example.com"));
        assert_eq!(ps[0].folder, "Imported");
        assert!(ps[0].ssh.as_ref().unwrap().agent_auth);
        let jh = &ps[1].ssh.as_ref().unwrap().jump_hosts[0];
        assert_eq!((jh.host.as_str(), jh.username.as_str()), ("gw.example.com", "gateway"));
    }

    #[test]
    fn parses_proxy_jump() {
        for (input, host, user, port) in [
            ("bastion.example.com", "bastion.example.com", "", 22),
            ("user@bastion.example.com", "bastion.example.com", "user", 22),
            ("user@bastion.example.com:2200", "bastion.example.com", "user", 2200),
            ("host:443", "host", "", 443),
        ] {
            let jh = parse_proxy_jump(input).unwrap();
            assert_eq!((jh.host.as_str(), jh.username.as_str(), jh.port), (host, user, port), "{input}");
        }
    }
}
