//! Profile sync through a folder the user picks (a cloud drive, a network share, a git checkout):
//! one encrypted file, `nexus-profiles.nexus`, in the export format (Argon2id + AES-256-GCM) under
//! a sync password. Each side merges profile by profile — the newer `updated_at` wins — and
//! deletions travel as dated tombstones, so a profile removed on one machine goes away on the
//! others. Saved credentials are never synced: they live in each machine's keychain or vault
//! (values a login script sends travel with their profile, as in an export).
//!
//! Timestamps in the shared file are clipped to the merging machine's clock: a record stamped in
//! the future would otherwise win over every later local edit (or keep deleting a profile) for
//! as long as the forged date, and the sync password is shared by every machine.

use crate::Result;
use crate::models::{Profile, Time};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashMap};
use std::path::Path;

pub const FILE_NAME: &str = "nexus-profiles.nexus";
/// Tombstones older than this are forgotten (every machine has had time to see them).
const TOMBSTONE_DAYS: i64 = 180;
const MAX_FILE: u64 = 20 << 20;

#[derive(Serialize, Deserialize, Default, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SyncFile {
    pub version: i64,
    pub profiles: Vec<Profile>,
    /// Deleted profile id → when.
    #[serde(default)]
    pub deleted: BTreeMap<String, Time>,
}

#[derive(Debug, Default, PartialEq)]
pub struct Merged {
    /// Profiles to create or update locally.
    pub upsert: Vec<Profile>,
    /// Local profiles to delete.
    pub delete: Vec<String>,
    /// What the shared file should now hold.
    pub file: SyncFile,
}

/// Merges local state with the shared file.
pub fn merge(local: &[Profile], local_deleted: &BTreeMap<String, Time>, remote: &SyncFile, now: Time) -> Merged {
    let mut deleted: BTreeMap<String, Time> = remote.deleted.iter().map(|(id, t)| (id.clone(), (*t).min(now))).collect();
    for (id, t) in local_deleted {
        let e = deleted.entry(id.clone()).or_insert(*t);
        *e = (*e).max(*t).min(now);
    }
    deleted.retain(|_, t| (now - *t).num_days() < TOMBSTONE_DAYS);

    let remote_profiles: Vec<Profile> = remote
        .profiles
        .iter()
        .map(|p| {
            let mut p = p.clone();
            p.updated_at = p.updated_at.min(now);
            p
        })
        .collect();
    let remote_by: HashMap<&str, &Profile> = remote_profiles.iter().map(|p| (p.id.as_str(), p)).collect();
    let local_by: HashMap<&str, &Profile> = local.iter().map(|p| (p.id.as_str(), p)).collect();
    let mut out = Merged::default();
    let mut ids: Vec<&str> = local_by.keys().chain(remote_by.keys()).copied().collect();
    ids.sort_unstable();
    ids.dedup();
    for id in ids {
        let winner = match (local_by.get(id), remote_by.get(id)) {
            (Some(l), Some(r)) => {
                if r.updated_at > l.updated_at {
                    out.upsert.push((*r).clone());
                    *r
                } else {
                    *l
                }
            }
            (Some(l), None) => *l,
            (None, Some(r)) => *r,
            (None, None) => continue,
        };
        // A deletion newer than the last edit wins; an edit at or after the deletion brings it back.
        if deleted.get(id).is_some_and(|t| *t > winner.updated_at) {
            if local_by.contains_key(id) {
                out.delete.push(id.to_string());
            }
            out.upsert.retain(|p| p.id != id);
            continue;
        }
        deleted.remove(id);
        if !local_by.contains_key(id) {
            out.upsert.push(winner.clone());
        }
        out.file.profiles.push(winner.clone());
    }
    out.file.version = 1;
    out.file.deleted = deleted;
    out
}

pub fn read(folder: &Path, password: &str) -> Result<Option<SyncFile>> {
    let path = folder.join(FILE_NAME);
    let Ok(meta) = std::fs::metadata(&path) else { return Ok(None) };
    if meta.len() > MAX_FILE {
        return Err("sync file is too large".into());
    }
    let plain = crate::exportimport::open(&std::fs::read(&path)?, password).map_err(|_| "the sync password doesn't open the sync file (or it is damaged)")?;
    Ok(Some(serde_json::from_slice(&plain).map_err(|e| format!("sync file isn't readable: {e}"))?))
}

pub fn write(folder: &Path, password: &str, file: &SyncFile) -> Result<()> {
    crate::secrets::write_private(&folder.join(FILE_NAME), &crate::exportimport::seal(&serde_json::to_vec(file)?, password)?)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::Protocol;

    fn p(id: &str, host: &str, t: i64) -> Profile {
        let mut p = Profile::new(Protocol::Ssh);
        p.id = id.into();
        p.host = host.into();
        p.updated_at = chrono::DateTime::from_timestamp(t, 0).unwrap().fixed_offset();
        p
    }
    fn at(t: i64) -> Time {
        chrono::DateTime::from_timestamp(t, 0).unwrap().fixed_offset()
    }

    #[test]
    fn newest_wins_and_deletions_travel() {
        let now = at(1_000_000);
        let local = vec![p("a", "a-local-new", 200), p("b", "b-local-old", 100), p("c", "only-local", 100), p("d", "deleted-remotely", 100)];
        let remote = SyncFile {
            version: 1,
            profiles: vec![p("a", "a-remote-old", 150), p("b", "b-remote-new", 300), p("e", "only-remote", 100), p("f", "deleted-locally", 100)],
            deleted: [("d".to_string(), at(150))].into(),
        };
        let local_deleted = [("f".to_string(), at(120))].into();
        let m = merge(&local, &local_deleted, &remote, now);
        let hosts = |v: &[Profile]| v.iter().map(|p| p.host.clone()).collect::<Vec<_>>();
        assert_eq!(hosts(&m.upsert), ["b-remote-new", "only-remote"]);
        assert_eq!(m.delete, ["d"]);
        assert_eq!(hosts(&m.file.profiles), ["a-local-new", "b-remote-new", "only-local", "only-remote"]);
        assert_eq!(m.file.deleted.keys().collect::<Vec<_>>(), ["d", "f"]);
        // An edit after a deletion brings the profile back (and clears the tombstone).
        let remote = SyncFile { version: 1, profiles: vec![], deleted: [("x".to_string(), at(100))].into() };
        let m = merge(&[p("x", "edited-later", 200)], &BTreeMap::new(), &remote, now);
        assert!(m.delete.is_empty() && m.file.deleted.is_empty() && m.file.profiles.len() == 1);
        // Old tombstones are forgotten.
        let remote = SyncFile { version: 1, profiles: vec![], deleted: [("old".to_string(), at(0))].into() };
        assert!(merge(&[], &BTreeMap::new(), &remote, at(TOMBSTONE_DAYS * 86_400 + 10)).file.deleted.is_empty());
    }

    #[test]
    fn future_dated_records_cannot_win_forever() {
        let now = at(1_000_000);
        // A forged remote edit "from the future" is clipped to now, so the user's later local edit
        // (also at now) keeps winning, and the rewritten file no longer carries the forged date.
        let remote = SyncFile { version: 1, profiles: vec![p("a", "forged", 9_000_000)], deleted: BTreeMap::new() };
        let m = merge(&[p("a", "mine", 1_000_000)], &BTreeMap::new(), &remote, now);
        assert!(m.upsert.is_empty());
        assert_eq!(m.file.profiles[0].host, "mine");
        // A brand-new remote profile stamped in the future is accepted, but with a sane time.
        let remote = SyncFile { version: 1, profiles: vec![p("b", "new", 9_000_000)], deleted: BTreeMap::new() };
        let m = merge(&[], &BTreeMap::new(), &remote, now);
        assert_eq!(m.upsert[0].updated_at, now);
        // A future-dated tombstone can't keep deleting a profile the user re-creates afterwards.
        let remote = SyncFile { version: 1, profiles: vec![], deleted: [("c".to_string(), at(9_000_000))].into() };
        let m = merge(&[p("c", "recreated", 1_000_001)], &BTreeMap::new(), &remote, at(1_000_001));
        assert!(m.delete.is_empty() && m.file.profiles.len() == 1 && m.file.deleted.is_empty());
    }

    #[test]
    fn file_round_trips_under_the_password() {
        let dir = std::env::temp_dir().join(format!("nexus-sync-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let f = SyncFile { version: 1, profiles: vec![p("a", "h", 1)], deleted: BTreeMap::new() };
        write(&dir, "pw", &f).unwrap();
        assert_eq!(read(&dir, "pw").unwrap().unwrap().profiles[0].host, "h");
        assert!(read(&dir, "wrong").is_err());
        assert!(read(&dir.join("missing"), "pw").unwrap().is_none());
        std::fs::remove_dir_all(dir).unwrap();
    }
}
