//! OpenSSH `known_hosts` interchange and SSH certificate authorities.
//!
//! Imported host keys become ordinary known-host rows. `@cert-authority` lines become rows with
//! `key_type` [`CA_KEY_TYPE`], the host *pattern* as `host`, port 0, and the CA's public key in
//! OpenSSH form; a host certificate signed by a CA whose pattern matches the host is trusted
//! without a first-use prompt. Hashed entries (`|1|…`) can't be mapped back to a host name and
//! are skipped, as are `@revoked` lines.

use crate::models::KnownHost;
use russh::keys::{HashAlg, PublicKey};

/// `key_type` of a certificate-authority row.
pub const CA_KEY_TYPE: &str = "@cert-authority";

#[derive(Debug, Default, PartialEq)]
pub struct Parsed {
    pub hosts: Vec<(String, u16, PublicKey)>,
    /// (host pattern, CA key)
    pub authorities: Vec<(String, PublicKey)>,
    /// Lines skipped: hashed host names, revocations, unparseable keys, wildcard host keys.
    pub skipped: usize,
}

pub fn parse(text: &str) -> Parsed {
    let mut out = Parsed::default();
    for line in text.lines().map(str::trim).filter(|l| !l.is_empty() && !l.starts_with('#')) {
        let mut fields = line.split_whitespace().peekable();
        let marker = fields.next_if(|f| f.starts_with('@'));
        let (Some(hosts), Some(kind), Some(b64)) = (fields.next(), fields.next(), fields.next()) else {
            out.skipped += 1;
            continue;
        };
        let Ok(key) = PublicKey::from_openssh(&format!("{kind} {b64}")) else {
            out.skipped += 1;
            continue;
        };
        match marker {
            Some("@cert-authority") => out.authorities.push((hosts.to_string(), key)),
            Some(_) => out.skipped += 1,
            None if hosts.starts_with('|') => out.skipped += 1,
            None => {
                for h in hosts.split(',') {
                    let (host, port) = split_host(h);
                    if host.contains(['*', '?', '!']) {
                        out.skipped += 1;
                    } else {
                        out.hosts.push((host.to_string(), port, key.clone()));
                    }
                }
            }
        }
    }
    out
}

/// `[host]:port` → (host, port); a bare host is port 22.
fn split_host(h: &str) -> (&str, u16) {
    if let Some(rest) = h.strip_prefix('[')
        && let Some((host, port)) = rest.split_once("]:")
        && let Ok(p) = port.parse()
    {
        return (host, p);
    }
    (h, 22)
}

/// OpenSSH form of a host: `host` on port 22, `[host]:port` otherwise.
fn host_field(host: &str, port: i64) -> String {
    if port == 22 { host.to_string() } else { format!("[{host}]:{port}") }
}

/// Known-hosts lines for `rows` (SSH host keys and CAs; other protocols' certificates are left out).
pub fn export(rows: &[KnownHost]) -> String {
    let mut out = String::new();
    for kh in rows {
        if kh.key_type == CA_KEY_TYPE {
            out += &format!("@cert-authority {} {}\n", kh.host, kh.public_key);
        } else if !kh.public_key.is_empty() && ["ssh-", "ecdsa-", "sk-"].iter().any(|p| kh.key_type.starts_with(p)) {
            out += &format!("{} {} {}\n", host_field(&kh.host, kh.port), kh.key_type, kh.public_key);
        }
    }
    out
}

/// A row for a CA under a host pattern.
pub fn authority_row(pattern: &str, key: &PublicKey) -> KnownHost {
    let now = crate::models::now();
    KnownHost {
        id: String::new(),
        host: pattern.into(),
        port: 0,
        key_type: CA_KEY_TYPE.into(),
        fingerprint: key.fingerprint(HashAlg::Sha256).to_string(),
        public_key: key.to_openssh().unwrap_or_default(),
        first_seen: now,
        last_seen: now,
        trusted: true,
    }
}

/// Whether `host:port` matches an OpenSSH host pattern list (`*`, `?`, `!negation`, commas). As in
/// OpenSSH, patterns match `host` for port 22 and `[host]:port` otherwise, so `[*.corp]:*`
/// covers every port.
pub fn pattern_matches(patterns: &str, host: &str, port: u16) -> bool {
    let target = host_field(host, port.into()).to_ascii_lowercase();
    let mut matched = false;
    for p in patterns.split(',') {
        let (neg, p) = p.strip_prefix('!').map_or((false, p), |r| (true, r));
        if glob(&p.to_ascii_lowercase(), &target) {
            if neg {
                return false;
            }
            matched = true;
        }
    }
    matched
}

fn glob(p: &str, s: &str) -> bool {
    match (p.chars().next(), s.chars().next()) {
        (None, None) => true,
        (Some('*'), _) => glob(&p[1..], s) || (!s.is_empty() && glob(p, &s[s.chars().next().unwrap().len_utf8()..])),
        (Some('?'), Some(c)) => glob(&p[1..], &s[c.len_utf8()..]),
        (Some(a), Some(b)) if a == b => glob(&p[a.len_utf8()..], &s[b.len_utf8()..]),
        _ => false,
    }
}

/// CA keys trusted for `host:port`.
pub fn authorities_for(rows: &[KnownHost], host: &str, port: u16) -> Vec<PublicKey> {
    rows.iter()
        .filter(|r| r.key_type == CA_KEY_TYPE && pattern_matches(&r.host, host, port))
        .filter_map(|r| PublicKey::from_openssh(&r.public_key).ok())
        .collect()
}

#[cfg(test)]
#[allow(non_snake_case)]
mod tests {
    use super::*;

    /// A real (deterministic) Ed25519 public key in OpenSSH form.
    fn ed() -> String {
        use russh::keys::ssh_key::private::Ed25519Keypair;
        let kp = Ed25519Keypair::from_seed(&[7; 32]);
        PublicKey::new(kp.public.into(), "").to_openssh().unwrap()
    }

    #[test]
    fn parses_openssh_known_hosts() {
        let ED = ed();
        let text = format!(
            "# comment\n\
             github.com,140.82.112.3 {ED}\n\
             [git.example.com]:2222 {ED} laptop\n\
             |1|abc=|def= {ED}\n\
             *.wild {ED}\n\
             @cert-authority *.corp.example,!bad.corp.example {ED}\n\
             @revoked old.example {ED}\n\
             broken ssh-ed25519 notbase64\n"
        );
        let p = parse(&text);
        let hosts: Vec<_> = p.hosts.iter().map(|(h, port, _)| (h.as_str(), *port)).collect();
        assert_eq!(hosts, [("github.com", 22), ("140.82.112.3", 22), ("git.example.com", 2222)]);
        assert_eq!(p.authorities.len(), 1);
        assert_eq!(p.skipped, 4, "hashed, wildcard, revoked, broken");
        assert_eq!(p.authorities[0].0, "*.corp.example,!bad.corp.example");
    }

    #[test]
    fn host_patterns() {
        let pats = "*.corp.example,!bad.corp.example,[jump.?.example]:2200,[*.any]:*";
        assert!(pattern_matches(pats, "x.any", 2201) && !pattern_matches(pats, "x.any", 22), "[..]:* is any port but 22");
        assert!(pattern_matches(pats, "web.corp.example", 22));
        assert!(pattern_matches(pats, "WEB.Corp.Example", 22), "case-insensitive");
        assert!(!pattern_matches(pats, "bad.corp.example", 22), "negated");
        assert!(!pattern_matches(pats, "web.corp.example", 2222), "bare patterns are port 22");
        assert!(pattern_matches(pats, "jump.a.example", 2200));
        assert!(!pattern_matches(pats, "jump.ab.example", 2200));
        assert!(!pattern_matches(pats, "corp.example", 22));
    }

    #[test]
    fn exports_what_it_imports() {
        let ED = ed();
        let p = parse(&format!("[h.example]:2022 {ED}\n@cert-authority *.corp {ED}\n"));
        let (h, port, key) = &p.hosts[0];
        let mut row = authority_row("x", key);
        row.host = h.clone();
        row.port = *port as i64;
        row.key_type = key.algorithm().as_str().into();
        row.public_key = ED.split_whitespace().nth(1).unwrap().into();
        let ca = authority_row(&p.authorities[0].0, &p.authorities[0].1);
        let out = export(&[row, ca.clone()]);
        assert_eq!(out, format!("[h.example]:2022 {ED}\n@cert-authority *.corp {ED}\n"));
        assert_eq!(authorities_for(&[ca], "db.corp", 22).len(), 1);
    }
}
