//! Lightweight GitHub REST client for repository metadata and archive downloads. Works with
//! github.com and GitHub Enterprise via a configurable API base URL.

use std::io::{Read, Write};
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use chrono::{DateTime, FixedOffset, SecondsFormat};
use serde::Deserialize;

use crate::models::GitHubRepo;

const MAX_JSON_BODY_BYTES: u64 = 4 << 20;
const MAX_ERROR_BODY_BYTES: u64 = 64 << 10;
const MAX_ARCHIVE_BYTES: u64 = 2 << 30;
const USER_AGENT: &str = "NexusTerminal/1.0";
const COMMIT_LOOKUP_WORKERS: usize = 5;
/// Commit lookups per refresh, most recently pushed first; the rest keep cached (or no) commit
/// info until they're pushed to. One lookup per repo per refresh would exhaust the API's hourly
/// rate limit for accounts with thousands of repos.
const MAX_COMMIT_LOOKUPS: usize = 100;

pub struct Client {
    base_url: String,
    token: String,
    agent: ureq::Agent,
    /// full_name → (pushed_at it was looked up for, committer, commit time).
    commits: parking_lot::Mutex<std::collections::HashMap<String, (String, String, String)>>,
}

#[derive(Deserialize)]
struct ApiRepo {
    #[serde(default)]
    full_name: Option<String>,
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    description: Option<String>,
    #[serde(default)]
    html_url: Option<String>,
    #[serde(default)]
    default_branch: Option<String>,
    #[serde(default)]
    private: Option<bool>,
    #[serde(default)]
    fork: Option<bool>,
    #[serde(default)]
    archived: Option<bool>,
    #[serde(default)]
    language: Option<String>,
    #[serde(default)]
    stargazers_count: Option<i64>,
    #[serde(default)]
    pushed_at: Option<String>,
    #[serde(default)]
    updated_at: Option<String>,
    #[serde(default)]
    owner: Option<ApiOwner>,
}

#[derive(Deserialize)]
struct ApiOwner {
    #[serde(default)]
    login: Option<String>,
}

#[derive(Deserialize, Default)]
struct ApiPerson {
    #[serde(default)]
    name: Option<String>,
    #[serde(default)]
    date: Option<String>,
}

impl Client {
    /// `base_url` is the API root, e.g. `https://api.github.com` or `https://ghe.example.com/api/v3`.
    pub fn new(base_url: &str, token: &str) -> Client {
        let agent = ureq::Agent::config_builder().timeout_global(Some(Duration::from_secs(30))).http_status_as_error(false).build().into();
        Client { base_url: base_url.trim_end_matches('/').to_string(), token: token.to_string(), agent, commits: Default::default() }
    }

    fn get(&self, url: &str) -> Result<ureq::http::Response<ureq::Body>, ureq::Error> {
        self.agent
            .get(url)
            .header("Authorization", &format!("Bearer {}", self.token))
            .header("Accept", "application/vnd.github+json")
            .header("User-Agent", USER_AGENT)
            .call()
    }

    /// Confirms the token works and returns the authenticated login.
    pub fn validate_token(&self) -> crate::Result<String> {
        let mut resp = self.get(&format!("{}/user", self.base_url)).map_err(|e| format!("contacting GitHub API: {e}"))?;
        let status = resp.status().as_u16();
        if status != 200 {
            return Err(format!("token validation failed (HTTP {status})").into());
        }
        let body = read_limited(resp.body_mut().as_reader(), MAX_JSON_BODY_BYTES).map_err(|e| format!("reading user response: {e}"))?;
        #[derive(Deserialize)]
        struct User {
            #[serde(default)]
            login: Option<String>,
        }
        let user: User = serde_json::from_slice(&body).map_err(|e| format!("decoding user: {e}"))?;
        Ok(user.login.unwrap_or_default())
    }

    /// All repositories visible to the token, newest push first, enriched with latest-commit info.
    pub fn fetch_repos(&self) -> crate::Result<Vec<GitHubRepo>> {
        self.fetch_repos_with_progress(|_, _| {})
    }

    /// Like `fetch_repos`, reporting commit-enrichment progress as `(done, total)`; it is called
    /// from worker threads.
    pub fn fetch_repos_with_progress(&self, progress: impl Fn(usize, usize) + Sync) -> crate::Result<Vec<GitHubRepo>> {
        let mut all: Vec<ApiRepo> = Vec::new();
        let mut page = 1;
        loop {
            let url = format!("{}/user/repos?per_page=100&page={page}&sort=pushed&direction=desc", self.base_url);
            let mut resp = self.get(&url).map_err(|e| format!("fetching repos page {page}: {e}"))?;
            let body = read_limited(resp.body_mut().as_reader(), MAX_JSON_BODY_BYTES).map_err(|e| format!("reading response body: {e}"))?;
            let status = resp.status().as_u16();
            if status == 401 || status == 403 {
                return Err(format!("authentication failed (HTTP {status}) — check your access token").into());
            }
            if status != 200 {
                return Err(format!("GitHub API returned HTTP {status}: {}", String::from_utf8_lossy(&body)).into());
            }
            let batch: Vec<ApiRepo> = serde_json::from_slice(&body).map_err(|e| format!("decoding repos: {e}"))?;
            if batch.is_empty() {
                break;
            }
            let short = batch.len() < 100;
            all.extend(batch);
            // Safety cap — 5000 repos should be more than enough.
            if all.len() >= 5000 || short {
                break;
            }
            page += 1;
        }

        let mut parsed = Vec::with_capacity(all.len());
        for r in all {
            let pushed = parse_time(r.pushed_at.as_deref()).map_err(|e| format!("decoding repos: {e}"))?;
            let updated = parse_time(r.updated_at.as_deref()).map_err(|e| format!("decoding repos: {e}"))?;
            parsed.push((r, pushed, updated));
        }
        parsed.sort_by_key(|p| std::cmp::Reverse(p.1));

        let mut result: Vec<GitHubRepo> = parsed
            .into_iter()
            .map(|(r, pushed, updated)| GitHubRepo {
                full_name: r.full_name.unwrap_or_default(),
                name: r.name.unwrap_or_default(),
                owner: r.owner.and_then(|o| o.login).unwrap_or_default(),
                description: r.description.unwrap_or_default(),
                html_url: r.html_url.unwrap_or_default(),
                default_branch: r.default_branch.unwrap_or_default(),
                private: r.private.unwrap_or_default(),
                fork: r.fork.unwrap_or_default(),
                archived: r.archived.unwrap_or_default(),
                language: r.language.unwrap_or_default(),
                stars: r.stargazers_count.unwrap_or_default(),
                pushed_at: format_time(pushed),
                updated_at: format_time(updated),
                ..Default::default()
            })
            .collect();

        // Enrich with latest commit metadata (best-effort), five lookups at a time. A repo not
        // pushed to since its last lookup reuses the cached answer.
        let mut lookups: Vec<(usize, String, String)> = Vec::new();
        {
            let cache = self.commits.lock();
            for (i, r) in result.iter_mut().enumerate() {
                match cache.get(&r.full_name) {
                    Some((pushed, who, when)) if *pushed == r.pushed_at => (r.latest_committer, r.latest_commit_at) = (who.clone(), when.clone()),
                    _ if lookups.len() < MAX_COMMIT_LOOKUPS => {
                        let reference = if r.default_branch.is_empty() { "HEAD" } else { &r.default_branch };
                        lookups.push((i, r.full_name.clone(), reference.to_string()));
                    }
                    _ => {}
                }
            }
        }
        let total = lookups.len();
        progress(0, total);
        let next = AtomicUsize::new(0);
        let done = AtomicUsize::new(0);
        let found: Vec<(usize, String, String)> = std::thread::scope(|s| {
            let workers: Vec<_> = (0..COMMIT_LOOKUP_WORKERS.min(total))
                .map(|_| {
                    s.spawn(|| {
                        let mut out = Vec::new();
                        loop {
                            let i = next.fetch_add(1, Ordering::SeqCst);
                            let Some((idx, name, reference)) = lookups.get(i) else { break };
                            match self.fetch_latest_commit(name, reference) {
                                Ok((who, when)) => out.push((*idx, who, when)),
                                // No such ref / empty repository: a definite answer, cache it. Auth
                                // and rate-limit failures are retried next refresh.
                                Err(e) if ["HTTP 404", "HTTP 409"].iter().any(|c| e.to_string().contains(c)) => out.push((*idx, String::new(), String::new())),
                                Err(_) => {}
                            }
                            progress(done.fetch_add(1, Ordering::SeqCst) + 1, total);
                        }
                        out
                    })
                })
                .collect();
            workers.into_iter().flat_map(|w| w.join().unwrap_or_default()).collect()
        });
        let mut cache = self.commits.lock();
        for (idx, who, when) in found {
            let r = &mut result[idx];
            cache.insert(r.full_name.clone(), (r.pushed_at.clone(), who.clone(), when.clone()));
            r.latest_committer = who;
            r.latest_commit_at = when;
        }
        Ok(result)
    }

    /// Committer name and RFC 3339 commit time for a repo and ref (`HEAD` when empty).
    pub fn fetch_latest_commit(&self, full_name: &str, reference: &str) -> crate::Result<(String, String)> {
        let reference = if reference.is_empty() { "HEAD" } else { reference };
        let (owner, repo) = split_repo_full_name(full_name)?;
        let url = self.endpoint_url(&["repos", &owner, &repo, "commits", reference])?;
        let mut resp = self.get(&url).map_err(|e| format!("fetching commit: {e}"))?;
        let status = resp.status().as_u16();
        if status == 401 || status == 403 {
            return Err(format!("authentication failed (HTTP {status})").into());
        }
        if status != 200 {
            return Err(format!("GitHub API returned HTTP {status}: {}", read_error_body(resp.body_mut().as_reader())).into());
        }
        let body = read_limited(resp.body_mut().as_reader(), MAX_JSON_BODY_BYTES).map_err(|e| format!("reading commit response: {e}"))?;

        #[derive(Deserialize)]
        struct Payload {
            #[serde(default)]
            commit: Commit,
        }
        #[derive(Deserialize, Default)]
        struct Commit {
            #[serde(default)]
            committer: Option<ApiPerson>,
            #[serde(default)]
            author: Option<ApiPerson>,
        }
        let payload: Payload = serde_json::from_slice(&body).map_err(|e| format!("decoding commit: {e}"))?;
        let committer = payload.commit.committer.unwrap_or_default();
        let author = payload.commit.author.unwrap_or_default();
        let (mut name, mut date) = (committer.name.unwrap_or_default(), committer.date);
        if name.is_empty() {
            name = author.name.unwrap_or_default();
            date = author.date;
        }
        let date = parse_time(date.as_deref()).map_err(|e| format!("decoding commit: {e}"))?;
        match date {
            Some(d) if !name.is_empty() => Ok((name, format_time(Some(d)))),
            _ => Err("commit metadata missing".into()),
        }
    }

    /// Downloads the repository zipball (default branch when `reference` is empty) to `dest_path`.
    pub fn download_repo_archive(&self, full_name: &str, reference: &str, dest_path: &Path) -> crate::Result<()> {
        let (owner, repo) = split_repo_full_name(full_name)?;
        let mut segments = vec!["repos", &owner, &repo, "zipball"];
        if !reference.is_empty() {
            segments.push(reference);
        }
        let url = self.endpoint_url(&segments)?;
        let mut resp = self.get(&url).map_err(|e| format!("downloading archive: {e}"))?;
        let status = resp.status().as_u16();
        if status == 401 || status == 403 {
            return Err(format!("authentication failed (HTTP {status}) — check your access token").into());
        }
        if status != 200 {
            return Err(format!("GitHub API returned HTTP {status}: {}", read_error_body(resp.body_mut().as_reader())).into());
        }
        let length = resp.headers().get("content-length").and_then(|v| v.to_str().ok()).and_then(|v| v.trim().parse::<u64>().ok());
        if let Some(length) = length.filter(|&l| l > MAX_ARCHIVE_BYTES) {
            return Err(format!("archive is too large: {length} bytes exceeds {MAX_ARCHIVE_BYTES} bytes").into());
        }
        let mut file = std::fs::File::create(dest_path).map_err(|e| format!("creating file: {e}"))?;
        copy_limited_archive(&mut file, resp.body_mut().as_reader(), MAX_ARCHIVE_BYTES).map_err(|e| format!("writing archive: {e}"))?;
        Ok(())
    }

    /// Base URL plus path-escaped segments.
    fn endpoint_url(&self, segments: &[&str]) -> crate::Result<String> {
        let mut url = self.base_url.clone();
        for segment in segments {
            if segment.is_empty() {
                return Err("empty GitHub API path segment".into());
            }
            url.push('/');
            url.push_str(&path_escape(segment));
        }
        Ok(url)
    }
}

/// Go's `url.PathEscape`.
fn path_escape(segment: &str) -> String {
    let mut out = String::new();
    for b in segment.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~$&+:=@".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

fn parse_time(value: Option<&str>) -> Result<Option<DateTime<FixedOffset>>, String> {
    match value {
        None => Ok(None),
        Some(v) => {
            let t = DateTime::parse_from_rfc3339(v).map_err(|e| format!("parsing time {v:?}: {e}"))?;
            // Go's zero time means "unset".
            Ok(Some(t).filter(|t| t.timestamp() != -62135596800))
        }
    }
}

/// Go's `time.Time.Format(time.RFC3339)`, with the zero time for a missing value.
fn format_time(value: Option<DateTime<FixedOffset>>) -> String {
    match value {
        Some(t) => t.to_rfc3339_opts(SecondsFormat::Secs, true),
        None => "0001-01-01T00:00:00Z".to_string(),
    }
}

fn read_limited(r: impl Read, max_bytes: u64) -> crate::Result<Vec<u8>> {
    let mut body = Vec::new();
    r.take(max_bytes + 1).read_to_end(&mut body)?;
    if body.len() as u64 > max_bytes {
        return Err(format!("response body exceeds {max_bytes} bytes").into());
    }
    Ok(body)
}

fn read_error_body(r: impl Read) -> String {
    match read_limited(r, MAX_ERROR_BODY_BYTES) {
        Ok(body) => String::from_utf8_lossy(&body).trim().to_string(),
        Err(e) => format!("<response body unavailable: {e}>"),
    }
}

fn split_repo_full_name(full_name: &str) -> crate::Result<(String, String)> {
    let parts: Vec<&str> = full_name.trim().split('/').collect();
    let [owner, repo] = parts.as_slice() else {
        return Err("repository name must be in owner/repo format".into());
    };
    let (owner, repo) = (owner.trim(), repo.trim());
    if owner.is_empty() || repo.is_empty() {
        return Err("repository name must be in owner/repo format".into());
    }
    Ok((owner.to_string(), repo.to_string()))
}

/// Copies at most `max_bytes`; returns the number copied, or an error once the source exceeds it.
fn copy_limited_archive(dst: &mut impl Write, src: impl Read, max_bytes: u64) -> crate::Result<u64> {
    let n = std::io::copy(&mut src.take(max_bytes + 1), dst)?;
    if n > max_bytes {
        return Err(format!("archive exceeds {max_bytes} bytes").into());
    }
    Ok(n)
}

#[cfg(test)]
mod tests {
    use super::*;
    use parking_lot::Mutex;
    use std::sync::Arc;

    #[test]
    fn read_limited_rejects_oversized_response() {
        let err = read_limited(&b"abcd"[..], 3).unwrap_err();
        assert!(err.to_string().contains("response body exceeds"), "{err}");
    }

    #[test]
    fn read_limited_allows_response_at_limit() {
        assert_eq!(read_limited(&b"abc"[..], 3).unwrap(), b"abc");
    }

    #[test]
    fn endpoint_url_escapes_repo_path_segments() {
        let client = Client::new("https://ghe.example.com/api/v3", "");
        let got = client.endpoint_url(&["repos", "owner", "repo", "commits", "feature/a b"]).unwrap();
        assert_eq!(got, "https://ghe.example.com/api/v3/repos/owner/repo/commits/feature%2Fa%20b");
        assert!(client.endpoint_url(&["repos", ""]).is_err());
    }

    #[test]
    fn split_repo_full_name_rejects_extra_segments() {
        assert!(split_repo_full_name("owner/repo/../../user").is_err());
        assert!(split_repo_full_name("owner/").is_err());
        assert_eq!(split_repo_full_name(" a / b ").unwrap(), ("a".into(), "b".into()));
    }

    #[test]
    fn copy_limited_archive_rejects_oversized_stream() {
        let mut dst = Vec::new();
        let err = copy_limited_archive(&mut dst, &b"abcd"[..], 3).unwrap_err();
        assert!(err.to_string().contains("archive exceeds"), "{err}");
        assert_eq!(dst.len(), 4);
    }

    #[test]
    fn copy_limited_archive_allows_stream_at_limit() {
        let mut dst = Vec::new();
        assert_eq!(copy_limited_archive(&mut dst, &b"abc"[..], 3).unwrap(), 3);
        assert_eq!(dst, b"abc");
    }

    type Seen = Arc<Mutex<Vec<(String, Option<String>, Option<String>)>>>;

    /// A local HTTP server answering with `handler(url) -> (status, body)`; records
    /// (url, authorization, user-agent) for every request.
    fn serve(handler: impl Fn(&str) -> (u16, String) + Send + 'static) -> (String, Seen) {
        let server = tiny_http::Server::http("127.0.0.1:0").unwrap();
        let base = format!("http://{}", server.server_addr().to_ip().unwrap());
        let seen: Seen = Arc::default();
        let log = seen.clone();
        std::thread::spawn(move || {
            for req in server.incoming_requests() {
                let header = |name: &'static str| req.headers().iter().find(|h| h.field.equiv(name)).map(|h| h.value.to_string());
                log.lock().push((req.url().to_string(), header("Authorization"), header("User-Agent")));
                let (status, body) = handler(req.url());
                let _ = req.respond(tiny_http::Response::from_string(body).with_status_code(status));
            }
        });
        (base, seen)
    }

    #[test]
    fn validate_token_returns_login_and_reports_status() {
        let (base, seen) = serve(|_| (200, r#"{"login":"octocat"}"#.into()));
        assert_eq!(Client::new(&base, "tok").validate_token().unwrap(), "octocat");
        let (url, auth, ua) = seen.lock()[0].clone();
        assert_eq!(url, "/user");
        assert_eq!(auth.as_deref(), Some("Bearer tok"));
        assert_eq!(ua.as_deref(), Some(USER_AGENT));

        let (base, _) = serve(|_| (401, "{}".into()));
        let err = Client::new(&base, "bad").validate_token().unwrap_err();
        assert_eq!(err.to_string(), "token validation failed (HTTP 401)");
    }

    fn repo_json(n: usize, pushed: &str) -> serde_json::Value {
        serde_json::json!({
            "full_name": format!("me/r{n}"), "name": format!("r{n}"), "description": null,
            "html_url": format!("https://example/me/r{n}"), "default_branch": if n == 0 { "" } else { "main" },
            "private": n.is_multiple_of(2), "fork": false, "archived": false, "language": null,
            "stargazers_count": n, "pushed_at": pushed, "updated_at": "2024-01-01T00:00:00Z",
            "owner": {"login": "me"}
        })
    }

    #[test]
    fn fetch_repos_paginates_sorts_and_enriches() {
        let (base, seen) = serve(|url| {
            if url.starts_with("/user/repos") && url.contains("page=1&") {
                let repos: Vec<_> = (0..100).map(|n| repo_json(n, &format!("2024-02-{:02}T00:00:00Z", 1 + n % 28))).collect();
                (200, serde_json::to_string(&repos).unwrap())
            } else if url.starts_with("/user/repos") {
                (200, serde_json::to_string(&[repo_json(100, "2025-06-01T12:00:00Z")]).unwrap())
            } else if url == "/repos/me/r100/commits/main" {
                (200, r#"{"commit":{"committer":{"name":"","date":"0001-01-01T00:00:00Z"},"author":{"name":"Ann","date":"2025-06-01T11:00:00Z"}}}"#.into())
            } else if url.starts_with("/repos/me/r0/commits/HEAD") {
                (200, r#"{"commit":{"committer":{"name":"Bob","date":"2024-01-05T10:00:00+00:00"}}}"#.into())
            } else {
                (404, "nope".into())
            }
        });
        let calls = Mutex::new(Vec::new());
        let client = Client::new(&base, "t");
        let repos = client.fetch_repos_with_progress(|done, total| calls.lock().push((done, total))).unwrap();
        assert_eq!(repos.len(), 101);
        assert_eq!(repos[0].full_name, "me/r100");
        assert_eq!(repos[0].pushed_at, "2025-06-01T12:00:00Z");
        assert_eq!(repos[0].latest_committer, "Ann");
        assert_eq!(repos[0].latest_commit_at, "2025-06-01T11:00:00Z");
        assert!(repos.windows(2).all(|w| w[0].pushed_at >= w[1].pushed_at));
        let r0 = repos.iter().find(|r| r.name == "r0").unwrap();
        assert_eq!((r0.latest_committer.as_str(), r0.latest_commit_at.as_str()), ("Bob", "2024-01-05T10:00:00Z"));
        assert_eq!(r0.owner, "me");
        assert!(r0.private && r0.description.is_empty() && r0.language.is_empty());
        assert!(repos.iter().filter(|r| r.latest_committer.is_empty()).count() == 99);

        let mut calls = calls.into_inner();
        // At most MAX_COMMIT_LOOKUPS lookups per refresh, most recently pushed first.
        assert_eq!(calls[0], (0, MAX_COMMIT_LOOKUPS));
        calls.sort();
        assert_eq!(calls.last(), Some(&(MAX_COMMIT_LOOKUPS, MAX_COMMIT_LOOKUPS)));
        assert_eq!(calls.len(), MAX_COMMIT_LOOKUPS + 1);
        assert_eq!(seen.lock().iter().filter(|s| s.0.starts_with("/user/repos")).count(), 2);

        // A refresh with nothing newly pushed reuses the answers it got (found or not).
        let commit_calls = || seen.lock().iter().filter(|s| s.0.contains("/commits/")).count();
        let before = commit_calls();
        let again = client.fetch_repos_with_progress(|_, _| {}).unwrap();
        assert_eq!(again[0].latest_committer, "Ann");
        assert_eq!(commit_calls() - before, 101 - MAX_COMMIT_LOOKUPS, "only repos not looked up yet are fetched");
        let third = commit_calls();
        client.fetch_repos_with_progress(|_, _| {}).unwrap();
        assert_eq!(commit_calls(), third, "everything is cached now");
    }

    #[test]
    fn fetch_repos_reports_auth_and_api_errors() {
        let (base, _) = serve(|_| (403, "{}".into()));
        let err = Client::new(&base, "t").fetch_repos().unwrap_err().to_string();
        assert_eq!(err, "authentication failed (HTTP 403) — check your access token");

        let (base, _) = serve(|_| (500, "boom".into()));
        let err = Client::new(&base, "t").fetch_repos().unwrap_err().to_string();
        assert_eq!(err, "GitHub API returned HTTP 500: boom");
    }

    #[test]
    fn download_repo_archive_writes_file() {
        let (base, seen) = serve(|url| match url {
            "/repos/me/r/zipball/v1%2F2" => (200, "ZIPDATA".into()),
            _ => (404, " missing \n".into()),
        });
        let dir = std::env::temp_dir().join(format!("nexus-gh-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&dir).unwrap();
        let dest = dir.join("a.zip");
        let client = Client::new(&base, "t");
        client.download_repo_archive("me/r", "v1/2", &dest).unwrap();
        assert_eq!(std::fs::read(&dest).unwrap(), b"ZIPDATA");
        let err = client.download_repo_archive("me/r", "", &dest).unwrap_err().to_string();
        assert_eq!(err, "GitHub API returned HTTP 404: missing");
        assert_eq!(seen.lock()[1].0, "/repos/me/r/zipball");
        assert!(client.download_repo_archive("bad", "", &dest).is_err());
        std::fs::remove_dir_all(dir).unwrap();
    }
}
