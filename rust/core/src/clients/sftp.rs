// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! SFTP operations over an SSH session's subsystem (internal/sftpclient). Every call blocks the
//! caller, so the UI runs them on worker threads.

use super::rt;
use crate::Result;
use russh_sftp::client::SftpSession;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

#[derive(Clone, Debug, PartialEq)]
pub struct FileEntry {
    pub name: String,
    /// Full remote path.
    pub path: String,
    pub size: u64,
    pub is_dir: bool,
    /// Unix permission string, e.g. "-rwxr-xr-x".
    pub mode: String,
    /// Unix epoch milliseconds.
    pub modified_at: i64,
}

#[derive(Clone, Debug, Default)]
pub struct Progress {
    pub transferred: u64,
    pub total: u64,
    pub path: String,
    pub done: bool,
    pub error: String,
}

fn mode_string(m: &russh_sftp::protocol::FileAttributes) -> String {
    let kind = if m.is_dir() {
        'd'
    } else if m.is_symlink() {
        'L'
    } else {
        '-'
    };
    format!("{kind}{}", m.permissions())
}

fn entry(name: String, path: String, m: &russh_sftp::protocol::FileAttributes) -> FileEntry {
    FileEntry { name, path, size: m.size.unwrap_or(0), is_dir: m.is_dir(), mode: mode_string(m), modified_at: m.mtime.map_or(0, |t| t as i64 * 1000) }
}

/// `path.Join` for remote (always '/') paths.
pub fn join(dir: &str, name: &str) -> String {
    if dir.is_empty() {
        name.to_string()
    } else if dir.ends_with('/') {
        format!("{dir}{name}")
    } else {
        format!("{dir}/{name}")
    }
}

/// Directories first, then case-insensitive name order.
pub fn list(s: &Arc<SftpSession>, path: &str) -> Result<Vec<FileEntry>> {
    let rd = rt().block_on(s.read_dir(path)).map_err(|e| format!("sftpclient: failed to list {path}: {e}"))?;
    let mut out: Vec<FileEntry> =
        rd.filter(|e| e.file_name() != "." && e.file_name() != "..").map(|e| entry(e.file_name(), join(path, &e.file_name()), &e.metadata())).collect();
    out.sort_by(|a, b| b.is_dir.cmp(&a.is_dir).then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase())));
    Ok(out)
}

pub fn stat(s: &Arc<SftpSession>, path: &str) -> Result<FileEntry> {
    let m = rt().block_on(s.metadata(path)).map_err(|e| format!("sftpclient: stat failed for {path}: {e}"))?;
    let name = path.rsplit('/').next().unwrap_or(path).to_string();
    Ok(entry(name, path.to_string(), &m))
}

pub fn mkdir(s: &Arc<SftpSession>, path: &str) -> Result<()> {
    Ok(rt().block_on(s.create_dir(path))?)
}

/// Removes a file, or an empty directory.
pub fn remove(s: &Arc<SftpSession>, path: &str) -> Result<()> {
    rt().block_on(async {
        match s.remove_file(path).await {
            Ok(()) => Ok(()),
            Err(e) => s.remove_dir(path).await.map_err(|_| e),
        }
    })?;
    Ok(())
}

pub fn rename(s: &Arc<SftpSession>, from: &str, to: &str) -> Result<()> {
    Ok(rt().block_on(s.rename(from, to))?)
}

pub fn pwd(s: &Arc<SftpSession>) -> Result<String> {
    Ok(rt().block_on(s.canonicalize("."))?)
}

async fn pump(
    mut src: impl tokio::io::AsyncRead + Unpin,
    mut dst: impl tokio::io::AsyncWrite + Unpin,
    total: u64,
    path: &str,
    on: &dyn Fn(Progress),
) -> Result<()> {
    let mut buf = vec![0u8; 32 * 1024];
    let (mut done, mut last) = (0u64, Instant::now());
    loop {
        let n = src.read(&mut buf).await.map_err(|e| format!("sftpclient: read failed: {e}"))?;
        if n == 0 {
            break;
        }
        dst.write_all(&buf[..n]).await.map_err(|e| format!("sftpclient: write failed: {e}"))?;
        done += n as u64;
        if last.elapsed() > Duration::from_millis(100) {
            on(Progress { transferred: done, total, path: path.into(), ..Default::default() });
            last = Instant::now();
        }
    }
    dst.flush().await?;
    dst.shutdown().await?;
    on(Progress { transferred: done, total, path: path.into(), done: true, ..Default::default() });
    Ok(())
}

pub fn download(s: &Arc<SftpSession>, remote: &str, local: &std::path::Path, on: &dyn Fn(Progress)) -> Result<()> {
    rt().block_on(async {
        let total = s.metadata(remote).await.map_err(|e| format!("sftpclient: stat failed: {e}"))?.size.unwrap_or(0);
        let src = s.open(remote).await.map_err(|e| format!("sftpclient: failed to open remote file: {e}"))?;
        let dst = tokio::fs::File::create(local).await.map_err(|e| format!("sftpclient: failed to create local file: {e}"))?;
        pump(src, dst, total, remote, on).await
    })
}

pub fn upload(s: &Arc<SftpSession>, local: &std::path::Path, remote: &str, on: &dyn Fn(Progress)) -> Result<()> {
    rt().block_on(async {
        let src = tokio::fs::File::open(local).await.map_err(|e| format!("sftpclient: failed to open local file: {e}"))?;
        let total = src.metadata().await?.len();
        let dst = s.create(remote).await.map_err(|e| format!("sftpclient: failed to create remote file: {e}"))?;
        pump(src, dst, total, remote, on).await
    })
}

#[cfg(test)]
mod tests {
    #[test]
    fn join_paths() {
        assert_eq!(super::join("/", "a"), "/a");
        assert_eq!(super::join("/home/x", "a"), "/home/x/a");
    }
}
