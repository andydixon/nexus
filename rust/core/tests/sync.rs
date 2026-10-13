// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Profile sync between two "machines" (data directories) sharing one folder.

use nexus_core::config::AppConfig;
use nexus_core::models::{Profile, Protocol};
use nexus_core::service::Service;

fn machine(root: &std::path::Path, name: &str, share: &std::path::Path) -> Service {
    let cfg = AppConfig::in_dir(root.join(name));
    std::fs::create_dir_all(&cfg.vault_dir).unwrap();
    let svc = Service::open(cfg).unwrap();
    svc.secrets.vault.initialise("master").unwrap();
    let mut prefs = svc.preferences();
    prefs.sync_folder = share.to_string_lossy().into_owned();
    svc.save_preferences(&prefs).unwrap();
    svc.set_sync_password("shared-secret").unwrap();
    svc
}

#[test]
fn profiles_follow_edits_and_deletes_across_machines() {
    let root = std::env::temp_dir().join(format!("nexus-sync-e2e-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&root);
    let share = root.join("share");
    std::fs::create_dir_all(&share).unwrap();
    let (a, b) = (machine(&root, "a", &share), machine(&root, "b", &share));

    let mut p = Profile::new(Protocol::Ssh);
    p.display_name = "web".into();
    p.host = "web.example".into();
    p.username = "me".into();
    let p = a.create_profile(p).unwrap();
    let pw_key = nexus_core::secrets::SecretKind::Password.key(&p.id);
    a.secrets.set_checked(&pw_key, "hunter2").unwrap();
    assert!(a.sync_profiles().unwrap().pushed);
    assert_eq!(b.sync_profiles().unwrap().pulled, 1);
    assert_eq!(b.profiles()[0].host, "web.example");

    std::thread::sleep(std::time::Duration::from_millis(1100)); // updated_at has 1 s resolution
    let mut edited = b.profiles()[0].clone();
    edited.host = "web2.example".into();
    b.update_profile(edited).unwrap();
    b.sync_profiles().unwrap();
    assert_eq!(a.sync_profiles().unwrap().pulled, 1);
    assert_eq!(a.profiles()[0].host, "web2.example");
    // A's saved password was for web.example; the synced copy points elsewhere, so it's forgotten
    // rather than sent to the new host without asking.
    assert!(a.secrets.get(&pw_key).is_none());

    a.delete_profile(&p.id).unwrap();
    a.sync_profiles().unwrap();
    assert_eq!(b.sync_profiles().unwrap().removed, 1);
    assert!(b.profiles().is_empty());
    // Nothing left to do on either side.
    assert!(!a.sync_profiles().unwrap().pushed && !b.sync_profiles().unwrap().pushed);
    let _ = std::fs::remove_dir_all(root);
}
