//! Session recording in asciicast v2 (https://docs.asciinema.org/manual/asciicast/v2/): a JSON
//! header line, then one `[elapsed_secs, type, data]` array per line. Same files as the Go app.

use crate::Result;
use crate::models::Time;
use parking_lot::Mutex;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs::File;
use std::io::{BufRead, BufReader, Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::time::Instant;

pub const EVENT_OUTPUT: &str = "o";
pub const EVENT_INPUT: &str = "i";
/// Bounds a single active recording file.
pub const MAX_RECORDING_BYTES: u64 = 250 * 1024 * 1024;
/// Bounds the file size loaded for playback.
pub const MAX_PLAYBACK_BYTES: u64 = 100 * 1024 * 1024;
/// Bounds one asciicast line during playback parsing.
pub const MAX_PLAYBACK_LINE_BYTES: usize = 8 * 1024 * 1024;

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize, Default)]
#[serde(default)]
pub struct Header {
    pub version: i64,
    pub width: i64,
    pub height: i64,
    pub timestamp: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub env: BTreeMap<String, String>,
}

/// One recorded I/O event; `kind` is `"o"` or `"i"` (other types are kept as read).
#[derive(Clone, Debug, PartialEq)]
pub struct Event {
    pub elapsed: f64,
    pub kind: String,
    pub data: String,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RecordingMeta {
    pub id: String,
    pub session_id: String,
    pub title: String,
    pub width: i64,
    pub height: i64,
    /// seconds
    pub duration: f64,
    /// file size in bytes
    pub size: u64,
    pub created_at: Time,
    pub file_path: PathBuf,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StorageInfo {
    pub count: usize,
    pub total_bytes: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oldest_created_at: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub newest_created_at: Option<Time>,
    pub max_recording_bytes: u64,
    pub max_playback_bytes: u64,
}

#[derive(Clone, Debug, PartialEq, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CleanupResult {
    pub deleted: usize,
    pub freed_bytes: u64,
}

struct Inner {
    file: Option<File>,
    closed: bool,
    event_count: usize,
    bytes_written: u64,
}

/// Captures terminal I/O for one session. Writes are best-effort: errors are dropped and the
/// recording stops (file closed) once it would exceed its size limit.
pub struct Recorder {
    inner: Mutex<Inner>,
    start: Instant,
    file_path: PathBuf,
    max_bytes: u64,
}

fn mkdir_private(dir: &Path) -> std::io::Result<()> {
    let mut b = std::fs::DirBuilder::new();
    b.recursive(true);
    #[cfg(unix)]
    std::os::unix::fs::DirBuilderExt::mode(&mut b, 0o700);
    b.create(dir)
}

fn json_line<T: Serialize>(v: &T) -> Result<Vec<u8>> {
    let mut line = serde_json::to_vec(v)?;
    line.push(b'\n');
    Ok(line)
}

impl Recorder {
    /// Creates `<dir>/<id>.cast` and writes the header.
    pub fn new(dir: &Path, id: &str, title: &str, cols: i64, rows: i64) -> Result<Recorder> {
        Self::with_limit(dir, id, title, cols, rows, MAX_RECORDING_BYTES)
    }

    fn with_limit(dir: &Path, id: &str, title: &str, cols: i64, rows: i64, max_bytes: u64) -> Result<Recorder> {
        mkdir_private(dir).map_err(|e| format!("recording: failed to create directory: {e}"))?;
        let path = dir.join(format!("{id}.cast"));
        // Recordings capture typed input (passwords included): owner-only, like the vault.
        let mut opts = std::fs::OpenOptions::new();
        opts.write(true).create(true).truncate(true);
        #[cfg(unix)]
        std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
        let mut f = opts.open(&path).map_err(|e| format!("recording: failed to create file: {e}"))?;
        let start = Instant::now();
        let hdr = Header {
            version: 2,
            width: cols,
            height: rows,
            timestamp: chrono::Utc::now().timestamp(),
            title: title.into(),
            env: BTreeMap::from([("TERM".into(), "xterm-256color".into())]),
        };
        let line = json_line(&hdr)?;
        if let Err(e) = f.write_all(&line) {
            drop(f);
            let _ = std::fs::remove_file(&path);
            return Err(format!("recording: failed to write header: {e}").into());
        }
        Ok(Recorder {
            inner: Mutex::new(Inner { file: Some(f), closed: false, event_count: 0, bytes_written: line.len() as u64 }),
            start,
            file_path: path,
            max_bytes,
        })
    }

    /// Records data from the remote host.
    pub fn write_output(&self, data: &[u8]) {
        self.write_event(EVENT_OUTPUT, data)
    }

    /// Records data from the user.
    pub fn write_input(&self, data: &[u8]) {
        self.write_event(EVENT_INPUT, data)
    }

    fn write_event(&self, kind: &str, data: &[u8]) {
        let mut g = self.inner.lock();
        if g.closed || g.file.is_none() {
            return;
        }
        let elapsed = self.start.elapsed().as_secs_f64();
        let Ok(line) = json_line(&(elapsed, kind, String::from_utf8_lossy(data))) else { return };
        if self.max_bytes > 0 && g.bytes_written + line.len() as u64 > self.max_bytes {
            g.closed = true;
            if let Some(f) = g.file.take() {
                let _ = f.sync_all();
            }
            return;
        }
        if g.file.as_mut().unwrap().write_all(&line).is_ok() {
            g.bytes_written += line.len() as u64;
            g.event_count += 1;
        }
    }

    /// Flushes and closes the file. Idempotent.
    pub fn close(&self) -> Result<()> {
        let mut g = self.inner.lock();
        if g.closed {
            return Ok(());
        }
        g.closed = true;
        if let Some(f) = g.file.take() {
            f.sync_all().map_err(|e| format!("recording: sync failed: {e}"))?;
        }
        Ok(())
    }

    /// Seconds since recording started.
    pub fn duration(&self) -> f64 {
        self.start.elapsed().as_secs_f64()
    }

    pub fn file_path(&self) -> &Path {
        &self.file_path
    }

    pub fn event_count(&self) -> usize {
        self.inner.lock().event_count
    }
}

/// The recordings directory: listing, playback, deletion and retention.
#[derive(Clone, Debug)]
pub struct Store {
    dir: PathBuf,
}

impl Store {
    pub fn new(dir: impl Into<PathBuf>) -> Store {
        Store { dir: dir.into() }
    }

    pub fn dir(&self) -> &Path {
        &self.dir
    }

    /// Metadata for every readable `.cast` file, newest first.
    pub fn list(&self) -> Result<Vec<RecordingMeta>> {
        mkdir_private(&self.dir)?;
        let entries = std::fs::read_dir(&self.dir).map_err(|e| format!("recording: failed to read directory: {e}"))?;
        let mut out: Vec<RecordingMeta> = entries
            .flatten()
            .filter(|e| !e.file_type().is_ok_and(|t| t.is_dir()))
            .filter_map(|e| e.file_name().into_string().ok())
            .filter(|n| n.ends_with(".cast"))
            .filter_map(|n| self.read_meta(&n).ok()) // skip unreadable files
            .collect();
        out.sort_by_key(|m| std::cmp::Reverse(m.created_at));
        Ok(out)
    }

    /// Reads the full recording (header + events).
    pub fn get(&self, id: &str) -> Result<(Header, Vec<Event>)> {
        self.get_with_limit(id, MAX_PLAYBACK_BYTES)
    }

    fn get_with_limit(&self, id: &str, max_bytes: u64) -> Result<(Header, Vec<Event>)> {
        let path = self.recording_path(id)?;
        let size = std::fs::metadata(&path).map_err(|e| format!("recording: failed to stat file: {e}"))?.len();
        if max_bytes > 0 && size > max_bytes {
            return Err(format!("recording: file is too large for playback (max {max_bytes} bytes)").into());
        }
        let raw = std::fs::read(&path).map_err(|e| format!("recording: failed to open file: {e}"))?;
        let text = String::from_utf8_lossy(&raw);
        let mut lines = text.lines();
        let Some(first) = lines.next() else { return Err("recording: empty file".into()) };
        if first.len() > MAX_PLAYBACK_LINE_BYTES {
            return Err("recording: failed to scan header: token too long".into());
        }
        let hdr: Header = serde_json::from_str(first.trim()).map_err(|e| format!("recording: invalid header: {e}"))?;
        let mut events = Vec::new();
        for line in lines {
            if line.len() > MAX_PLAYBACK_LINE_BYTES {
                return Err("recording: failed to scan events: token too long".into());
            }
            let line = line.trim();
            if line.is_empty() {
                continue;
            }
            if let Ok((elapsed, kind, data)) = serde_json::from_str::<(f64, String, String)>(line) {
                events.push(Event { elapsed, kind, data });
            }
        }
        Ok((hdr, events))
    }

    pub fn delete(&self, id: &str) -> Result<()> {
        let path = self.recording_path(id)?;
        std::fs::remove_file(path).map_err(|e| format!("recording: failed to delete: {e}"))?;
        Ok(())
    }

    pub fn storage_info(&self) -> Result<StorageInfo> {
        let metas = self.list()?;
        Ok(StorageInfo {
            count: metas.len(),
            total_bytes: metas.iter().map(|m| m.size).sum(),
            oldest_created_at: metas.iter().map(|m| m.created_at).min(),
            newest_created_at: metas.iter().map(|m| m.created_at).max(),
            max_recording_bytes: MAX_RECORDING_BYTES,
            max_playback_bytes: MAX_PLAYBACK_BYTES,
        })
    }

    /// Removes recordings older than `max_age_days`, then the oldest until the total is at most
    /// `max_total_bytes`. Zero disables either limit.
    pub fn cleanup(&self, max_age_days: u32, max_total_bytes: u64) -> Result<CleanupResult> {
        let mut metas = self.list()?;
        let mut result = CleanupResult::default();
        let mut deleted = std::collections::HashSet::new();
        let mut total: u64 = metas.iter().map(|m| m.size).sum();
        let mut delete = |m: &RecordingMeta, total: &mut u64| {
            if !deleted.contains(&m.id) && self.delete(&m.id).is_ok() {
                deleted.insert(m.id.clone());
                result.deleted += 1;
                result.freed_bytes += m.size;
                *total -= m.size;
            }
        };
        if max_age_days > 0 {
            let cutoff = chrono::Utc::now() - chrono::Duration::days(max_age_days.into());
            for m in &metas {
                if m.created_at < cutoff {
                    delete(m, &mut total);
                }
            }
        }
        if max_total_bytes > 0 && total > max_total_bytes {
            metas.sort_by_key(|m| m.created_at);
            for m in &metas {
                if total <= max_total_bytes {
                    break;
                }
                delete(m, &mut total);
            }
        }
        Ok(result)
    }

    fn recording_path(&self, id: &str) -> Result<PathBuf> {
        let id = id.trim();
        if id.is_empty() || id == "." || id == ".." || id.contains(['/', '\\']) {
            return Err("recording: invalid recording ID".into());
        }
        Ok(self.dir.join(format!("{id}.cast")))
    }

    /// Header fields plus the last event's timestamp as the duration. Reads the header line and
    /// the file's tail rather than the whole recording (they can be hundreds of MiB).
    fn read_meta(&self, filename: &str) -> Result<RecordingMeta> {
        const TAIL: u64 = 64 << 10;
        let path = self.dir.join(filename);
        let mut f = BufReader::new(File::open(&path)?);
        let size = f.get_ref().metadata()?.len();
        let mut head = Vec::new();
        f.by_ref().take(1 << 20).read_until(b'\n', &mut head)?;
        if head.iter().all(u8::is_ascii_whitespace) {
            return Err("recording: empty file".into());
        }
        let hdr: Header = serde_json::from_slice(&head)?;
        let body = head.len() as u64;
        let mut duration = None;
        // The tail first; if one event is longer than the window, fall back to the whole body.
        for start in [size.saturating_sub(TAIL).max(body), body] {
            let mut tail = Vec::new();
            f.seek(SeekFrom::Start(start))?;
            f.read_to_end(&mut tail)?;
            duration = tail.split(|b| *b == b'\n').rev().find_map(|l| serde_json::from_slice::<serde_json::Value>(l).ok()?.get(0)?.as_f64());
            if duration.is_some() || start == body {
                break;
            }
        }
        let duration = duration.unwrap_or(0.0);
        let id = filename.trim_end_matches(".cast").to_string();
        let created_at = chrono::DateTime::from_timestamp(hdr.timestamp, 0).ok_or("recording: invalid timestamp")?.with_timezone(&chrono::Local).fixed_offset();
        Ok(RecordingMeta {
            session_id: id.clone(), // recording ID matches session ID by default
            id,
            title: hdr.title,
            width: hdr.width,
            height: hdr.height,
            duration,
            size,
            created_at,
            file_path: path,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("nexus-rec-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    #[test]
    fn write_and_close() {
        let dir = tmp("write");
        let rec = Recorder::new(&dir, "test-session-001", "Test Recording", 80, 24).unwrap();
        rec.write_output(b"Welcome to the server\r\n");
        rec.write_output(b"$ ");
        rec.write_input(b"ls -la\r\n");
        rec.write_output(b"total 42\r\n");
        assert_eq!(rec.event_count(), 4);
        rec.close().unwrap();
        rec.close().unwrap(); // double close is a no-op
        #[cfg(unix)]
        {
            let mode = std::os::unix::fs::PermissionsExt::mode(&std::fs::metadata(dir.join("test-session-001.cast")).unwrap().permissions());
            assert_eq!(mode & 0o777, 0o600);
        }
        let text = std::fs::read_to_string(dir.join("test-session-001.cast")).unwrap();
        let lines: Vec<&str> = text.trim().lines().collect();
        assert_eq!(lines.len(), 5);
        let hdr: Header = serde_json::from_str(lines[0]).unwrap();
        assert_eq!((hdr.version, hdr.width, hdr.height), (2, 80, 24));
        let (_, kind, _): (f64, String, String) = serde_json::from_str(lines[1]).unwrap();
        assert_eq!(kind, "o");
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn list_get_delete() {
        let dir = tmp("list");
        let rec = Recorder::new(&dir, "session-abc", "My Session", 120, 40).unwrap();
        rec.write_output(b"hello world\r\n");
        rec.write_output(b"done\r\n");
        rec.close().unwrap();
        let store = Store::new(&dir);
        let metas = store.list().unwrap();
        assert_eq!(metas.len(), 1);
        assert_eq!((metas[0].id.as_str(), metas[0].width, metas[0].height), ("session-abc", 120, 40));
        let (hdr, events) = store.get("session-abc").unwrap();
        assert_eq!(hdr.title, "My Session");
        assert_eq!(events.len(), 2);
        store.delete("session-abc").unwrap();
        assert!(store.list().unwrap().is_empty());
        assert!(store.delete("../outside").is_err());
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn duration_comes_from_the_tail_even_with_long_events() {
        let dir = tmp("tail");
        std::fs::create_dir_all(&dir).unwrap();
        let hdr = r#"{"version":2,"width":80,"height":24,"timestamp":1791633486}"#;
        let mut many = format!("{hdr}\n");
        for i in 0..20_000 {
            many.push_str(&format!("[{}.5, \"o\", \"line {i}\\r\\n\"]\n", i));
        }
        std::fs::write(dir.join("many.cast"), many).unwrap();
        // The last event alone is bigger than the tail window.
        std::fs::write(dir.join("long.cast"), format!("{hdr}\n[1.0, \"o\", \"a\"]\n[7.25, \"o\", \"{}\"]\n", "x".repeat(200 << 10))).unwrap();
        std::fs::write(dir.join("bare.cast"), format!("{hdr}\n")).unwrap();
        let metas = Store::new(&dir).list().unwrap();
        let d = |id: &str| metas.iter().find(|m| m.id == id).unwrap().duration;
        assert_eq!((d("many"), d("long"), d("bare")), (19_999.5, 7.25, 0.0));
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn stops_at_size_limit() {
        let dir = tmp("limit");
        let rec = Recorder::with_limit(&dir, "limited", "Limited", 80, 24, 512).unwrap();
        for _ in 0..50 {
            rec.write_output("x".repeat(80).as_bytes());
        }
        assert!(rec.event_count() < 50);
        rec.close().unwrap();
        assert!(std::fs::metadata(dir.join("limited.cast")).unwrap().len() <= 512);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_oversized_playback() {
        let dir = tmp("huge");
        std::fs::create_dir_all(&dir).unwrap();
        let body = format!("{{\"version\":2,\"width\":80,\"height\":24,\"timestamp\":1}}\n{}", "x".repeat(256));
        std::fs::write(dir.join("huge.cast"), body).unwrap();
        let err = Store::new(&dir).get_with_limit("huge", 128).unwrap_err();
        assert!(err.to_string().contains("too large"));
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn cleanup_by_age_and_size() {
        let dir = tmp("cleanup");
        let make = |id: &str, age_secs: i64, payload: &str| {
            let rec = Recorder::new(&dir, id, id, 80, 24).unwrap();
            rec.write_output(payload.as_bytes());
            rec.close().unwrap();
            let path = dir.join(format!("{id}.cast"));
            let text = std::fs::read_to_string(&path).unwrap();
            let (first, rest) = text.split_once('\n').unwrap();
            let mut hdr: Header = serde_json::from_str(first).unwrap();
            hdr.timestamp = chrono::Utc::now().timestamp() - age_secs;
            let mut out = json_line(&hdr).unwrap();
            out.extend(rest.as_bytes());
            std::fs::write(&path, out).unwrap();
        };
        make("old", 45 * 86400, "old");
        make("recent", 2 * 3600, &"recent".repeat(50));
        let store = Store::new(&dir);
        assert_eq!(store.cleanup(30, 0).unwrap().deleted, 1);
        assert!(!dir.join("old.cast").exists());
        let info = store.storage_info().unwrap();
        assert_eq!(info.count, 1);
        assert_eq!(store.cleanup(0, info.total_bytes - 1).unwrap().deleted, 1);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
