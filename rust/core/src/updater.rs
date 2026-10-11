//! Update checks against a self-hosted manifest (or, as a fallback, GitHub's latest release).
//! Opt-in and check-only: nothing is downloaded or installed. Calls block; run them off the UI
//! thread.

use crate::Result;
use serde::{Deserialize, Serialize};
use std::io::Read;
use std::time::Duration;

/// Manifest URL template; expand with [`expand_update_url`].
pub const DEFAULT_UPDATE_URL: &str = "https://dixon.cx/downloads/nexus/manifest-{platform}-{arch}.json";
const GITHUB_OWNER: &str = "nexus-terminal";
const GITHUB_REPO: &str = "nexus-terminal";
const USER_AGENT: &str = "NexusTerminal-UpdateChecker";
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);
const MAX_UPDATE_RESPONSE_BYTES: usize = 1 << 20;

#[derive(Clone, Debug, PartialEq, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateInfo {
    pub available: bool,
    pub current_version: String,
    pub latest_version: String,
    #[serde(rename = "releaseURL")]
    pub release_url: String,
    pub release_name: String,
    pub release_notes: String,
    pub published_at: String,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct Release {
    tag_name: String,
    name: String,
    html_url: String,
    published_at: String,
    body: String,
    prerelease: bool,
    draft: bool,
}

#[derive(Deserialize, Default)]
#[serde(default, rename_all = "camelCase")]
struct Manifest {
    latest_version: String,
    version: String,
    #[serde(rename = "releaseURL")]
    release_url: String,
    release_name: String,
    release_notes: String,
    published_at: String,
}

/// Fills `{platform}`/`{os}` and `{arch}` with Go's GOOS/GOARCH names, which the published
/// manifest file names use.
pub fn expand_update_url(template: &str) -> String {
    let os = match std::env::consts::OS {
        "macos" => "darwin",
        o => o,
    };
    let arch = match std::env::consts::ARCH {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        "x86" => "386",
        a => a,
    };
    template.replace("{platform}", os).replace("{os}", os).replace("{arch}", arch)
}

/// GET with the updater headers; the body is read (bounded) only on 200.
fn fetch(url: &str, accept: &str) -> Result<(u16, Vec<u8>)> {
    let agent: ureq::Agent = ureq::Agent::config_builder().timeout_global(Some(REQUEST_TIMEOUT)).http_status_as_error(false).build().into();
    let resp = agent.get(url).header("Accept", accept).header("User-Agent", USER_AGENT).call().map_err(|e| format!("updater: request failed: {e}"))?;
    let status = resp.status().as_u16();
    if status != 200 {
        return Ok((status, vec![]));
    }
    let mut body = Vec::new();
    resp.into_body()
        .into_reader()
        .take(MAX_UPDATE_RESPONSE_BYTES as u64 + 1)
        .read_to_end(&mut body)
        .map_err(|e| format!("updater: failed to read response: {e}"))?;
    if body.len() > MAX_UPDATE_RESPONSE_BYTES {
        return Err(format!("updater: failed to read response: response exceeds {MAX_UPDATE_RESPONSE_BYTES} bytes").into());
    }
    Ok((status, body))
}

/// Checks a manifest URL. The body is either a JSON manifest (`latestVersion` or `version`, plus
/// optional `releaseURL`, `releaseName`, `releaseNotes`, `publishedAt`) or a bare version string.
pub fn check_url(url: &str, current_version: &str) -> Result<UpdateInfo> {
    if url.is_empty() {
        return Err("updater: update manifest URL is empty".into());
    }
    let current = normalise_version(current_version);
    let (status, body) = fetch(url, "application/json, text/plain")?;
    if status != 200 {
        return Err(format!("updater: update server returned {status}").into());
    }
    if let Some(info) = parse_manifest(&body, &current) {
        return Ok(info);
    }
    let latest = normalise_version(&String::from_utf8_lossy(&body));
    if latest.is_empty() {
        return Err("updater: update manifest is empty".into());
    }
    Ok(UpdateInfo { available: is_newer(&latest, &current), current_version: current, latest_version: latest, ..Default::default() })
}

/// Checks the latest GitHub release of `nexus-terminal/nexus-terminal`.
pub fn check_github(current_version: &str) -> Result<UpdateInfo> {
    let current = normalise_version(current_version);
    let url = format!("https://api.github.com/repos/{GITHUB_OWNER}/{GITHUB_REPO}/releases/latest");
    let (status, body) = fetch(&url, "application/vnd.github+json")?;
    if status == 404 {
        // No releases yet.
        return Ok(UpdateInfo { latest_version: current.clone(), current_version: current, ..Default::default() });
    }
    if status != 200 {
        return Err(format!("updater: GitHub API returned {status}").into());
    }
    let release: Release = serde_json::from_slice(&body).map_err(|e| format!("updater: failed to decode response: {e}"))?;
    let latest = normalise_version(&release.tag_name);
    if release.draft || release.prerelease {
        return Ok(UpdateInfo { current_version: current, latest_version: latest, ..Default::default() });
    }
    Ok(UpdateInfo {
        available: is_newer(&latest, &current),
        current_version: current,
        latest_version: latest,
        release_url: release.html_url,
        release_name: release.name,
        release_notes: truncate_notes(&release.body, 500),
        published_at: release.published_at,
    })
}

fn normalise_version(v: &str) -> String {
    let v = v.trim();
    v.strip_prefix('v').unwrap_or(v).to_string()
}

fn parse_manifest(body: &[u8], current: &str) -> Option<UpdateInfo> {
    let m: Manifest = serde_json::from_slice(body).ok()?;
    let latest = normalise_version(if m.latest_version.is_empty() { &m.version } else { &m.latest_version });
    if latest.is_empty() {
        return None;
    }
    Some(UpdateInfo {
        available: is_newer(&latest, current),
        current_version: current.into(),
        latest_version: latest,
        release_url: m.release_url,
        release_name: m.release_name,
        release_notes: m.release_notes,
        published_at: m.published_at,
    })
}

/// Compares dot-separated numeric parts (leading integer of each part, else 0). Dev builds and an
/// empty current version never see updates.
fn is_newer(latest: &str, current: &str) -> bool {
    if current.is_empty() || current == "dev" {
        return false;
    }
    let l: Vec<&str> = latest.split('.').collect();
    let c: Vec<&str> = current.split('.').collect();
    for i in 0..l.len().max(c.len()) {
        let (lv, cv) = (leading_int(l.get(i)), leading_int(c.get(i)));
        if lv != cv {
            return lv > cv;
        }
    }
    false
}

/// Go's `fmt.Sscanf(s, "%d")`: optional sign then digits; anything else is 0.
fn leading_int(s: Option<&&str>) -> i64 {
    let s = s.map_or("", |s| s.trim_start());
    let end = s.char_indices().find(|&(i, c)| !(c.is_ascii_digit() || (i == 0 && (c == '+' || c == '-')))).map_or(s.len(), |(i, _)| i);
    s[..end].parse().unwrap_or(0)
}

/// Cuts at `max` bytes (backing off to a char boundary) and appends "…".
fn truncate_notes(body: &str, max: usize) -> String {
    if body.len() <= max {
        return body.into();
    }
    let mut i = max;
    while !body.is_char_boundary(i) {
        i -= 1;
    }
    format!("{}…", &body[..i])
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    /// Serves one HTTP response on a random local port; returns its URL.
    fn serve_once(content_type: &'static str, body: Vec<u8>) -> String {
        let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/", l.local_addr().unwrap());
        std::thread::spawn(move || {
            let (mut s, _) = l.accept().unwrap();
            let mut req: Vec<u8> = Vec::new();
            let mut buf = [0u8; 1024];
            while !req.windows(4).any(|w| w == b"\r\n\r\n") {
                let n = s.read(&mut buf).unwrap();
                if n == 0 {
                    break;
                }
                req.extend(&buf[..n]);
            }
            let head = format!("HTTP/1.1 200 OK\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", body.len());
            let _ = s.write_all(head.as_bytes());
            let _ = s.write_all(&body);
        });
        url
    }

    #[test]
    fn normalises_versions() {
        for (i, w) in [("v1.2.3", "1.2.3"), ("1.2.3", "1.2.3"), (" v0.5.0 ", "0.5.0"), ("dev", "dev")] {
            assert_eq!(normalise_version(i), w);
        }
    }

    #[test]
    fn compares_versions() {
        for (l, c, w) in [
            ("1.1.0", "1.0.0", true),
            ("2.0.0", "1.9.9", true),
            ("1.0.1", "1.0.0", true),
            ("1.0.0", "1.0.0", false),
            ("0.9.0", "1.0.0", false),
            ("1.0.0", "dev", false),
            ("1.0.0", "", false),
            ("1.2", "1.1.0", true),
            ("1.0.0", "1.0.0.1", false),
            ("1.3.0-beta", "1.2.9", true),
        ] {
            assert_eq!(is_newer(l, c), w, "{l} vs {c}");
        }
    }

    #[test]
    fn truncates_notes() {
        assert_eq!(truncate_notes("Short note", 100), "Short note");
        let got = truncate_notes(&"x".repeat(600), 500);
        assert!(got.ends_with('…'));
        assert_eq!(got.chars().count(), 501);
        assert_eq!(truncate_notes("aé", 2), "a…");
    }

    #[test]
    fn expands_url_template() {
        let u = expand_update_url(DEFAULT_UPDATE_URL);
        assert!(!u.contains('{') && !u.contains("macos") && !u.contains("x86_64"), "{u}");
    }

    #[test]
    fn url_manifest_json() {
        let url = serve_once("application/json", br#"{"latestVersion":"1.2.3","releaseURL":"https://example.com/release"}"#.to_vec());
        let info = check_url(&url, "1.2.0").unwrap();
        assert!(info.available);
        assert_eq!(info.latest_version, "1.2.3");
        assert_eq!(info.release_url, "https://example.com/release");
    }

    #[test]
    fn url_plain_text() {
        let url = serve_once("text/plain", b"2.0.0".to_vec());
        assert!(check_url(&url, "1.9.0").unwrap().available);
    }

    #[test]
    fn url_rejects_oversized_manifest() {
        let url = serve_once("text/plain", vec![b'x'; MAX_UPDATE_RESPONSE_BYTES + 1]);
        let err = check_url(&url, "1.9.0").unwrap_err();
        assert!(err.to_string().contains("response exceeds"), "{err}");
    }
}
