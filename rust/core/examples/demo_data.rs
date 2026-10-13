// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Fills a data directory with demo profiles, snippets, known hosts, trigger rules and recordings
//! for the user-guide screenshots (docs/guide/screenshots.sh).
//!
//! cargo run -p nexus-core --example demo_data -- DATA_DIR SSH_KEY

use nexus_core::config::AppConfig;
use nexus_core::models::*;
use nexus_core::service::Service;

fn main() {
    let mut args = std::env::args().skip(1);
    let (Some(dir), Some(key)) = (args.next(), args.next()) else {
        eprintln!("usage: demo_data DATA_DIR SSH_KEY");
        std::process::exit(2);
    };
    let config = AppConfig::in_dir(dir.into());
    std::fs::create_dir_all(&config.vault_dir).unwrap();
    std::fs::create_dir_all(&config.recordings_dir).unwrap();
    let svc = Service::open(config).unwrap();
    let user = std::env::var("USER").unwrap_or_else(|_| "andy".into());

    let mk = |proto, name: &str, host: &str, port: i64, folder: &str, fav: bool| {
        let mut p = Profile::new(proto);
        p.display_name = name.into();
        p.host = host.into();
        if port != 0 {
            p.port = port;
        }
        p.folder = folder.into();
        p.favourite = fav;
        p
    };
    let ssh = |name: &str, folder: &str, fav: bool, tags: &[&str]| {
        let mut p = mk(Protocol::Ssh, name, "127.0.0.1", 42022, folder, fav);
        p.username = user.clone();
        p.tags = tags.iter().map(|t| t.to_string()).collect();
        let s = p.ssh.as_mut().unwrap();
        s.private_key_path = key.clone();
        s.auth_methods = vec!["publickey".into()];
        s.host_key_policy = HostKeyPolicy::AcceptNew;
        p
    };

    let mut profiles = vec![];
    let mut atlas = ssh("atlas", "Production", true, &["web", "eu-west"]);
    atlas.description = "Primary web node".into();
    atlas.notes = "Deploys go out from here. Be nice to it.".into();
    profiles.push(atlas);
    let mut db = ssh("db-primary", "Production", false, &["db"]);
    db.description = "Postgres primary, via the bastion".into();
    db.ssh.as_mut().unwrap().jump_hosts = vec![JumpHost {
        host: "bastion.example.net".into(),
        port: 22,
        username: user.clone(),
        auth_method: "publickey".into(),
        secret_mode: SecretMode::Saved,
        private_key_path: key.clone(),
    }];
    db.ssh.as_mut().unwrap().port_forwards = vec![PortForward {
        id: new_id(),
        direction: ForwardDirection::Local,
        bind_host: "127.0.0.1".into(),
        bind_port: 15432,
        dest_host: "127.0.0.1".into(),
        dest_port: 5432,
        enabled: true,
        label: "Postgres".into(),
    }];
    profiles.push(db);
    let mut hello = ssh("hello-ssh", "Production", false, &["demo"]);
    hello.ssh.as_mut().unwrap().host_key_policy = HostKeyPolicy::Ask;
    hello.description = "Same box, but asks about its host key".into();
    profiles.push(hello);
    profiles.push(ssh("build-runner", "Lab", false, &["ci"]));

    let mut router = mk(Protocol::Telnet, "Core router", "192.0.2.1", 0, "Lab", false);
    router.description = "Out-of-band console".into();
    profiles.push(router);
    let mut console = mk(Protocol::Serial, "Switch console", "/dev/ttyUSB0", 0, "Lab", false);
    console.serial.as_mut().unwrap().baud_rate = 9600;
    profiles.push(console);
    let mut sensor = mk(Protocol::Raw, "Sensor feed", "127.0.0.1", 42023, "Lab", false);
    let raw = sensor.raw.as_mut().unwrap();
    raw.show_timestamps = true;
    raw.show_direction = true;
    raw.line_ending = LineEnding::Lf;
    raw.payload_presets = vec![
        PayloadPreset { id: new_id(), name: "Status".into(), payload: "U1RBVFVT".into(), format: "text".into() },
        PayloadPreset { id: new_id(), name: "Reset".into(), payload: "UkVTRVQ=".into(), format: "text".into() },
    ];
    profiles.push(sensor);

    let mut irc = mk(Protocol::Irc, "Libera Chat", "127.0.0.1", 42667, "Home", true);
    let ic = irc.irc.as_mut().unwrap();
    ic.nick = "andy".into();
    ic.real_name = "Andy Dixon".into();
    ic.use_tls = false;
    ic.auto_join_rooms = vec!["#nexus".into(), "#rust".into()];
    profiles.push(irc);
    let mut gh = mk(Protocol::Github, "GitHub", "github.com", 0, "Home", false);
    gh.github.as_mut().unwrap().base_url = "http://127.0.0.1:42080".into();
    profiles.push(gh);
    let mut vnc = mk(Protocol::Vnc, "Studio desktop", "127.0.0.1", 42900, "Home", false);
    vnc.vnc.as_mut().unwrap().vnc_host = "127.0.0.1".into();
    vnc.vnc.as_mut().unwrap().vnc_port = 42900;
    vnc.vnc.as_mut().unwrap().scale_to_fit = true;
    profiles.push(vnc);
    let mut rdp = mk(Protocol::Rdp, "Design workstation", "192.0.2.60", 0, "Home", false);
    rdp.username = "andy".into();
    rdp.rdp.as_mut().unwrap().domain = "STUDIO".into();
    profiles.push(rdp);

    for p in profiles {
        svc.create_profile(p).unwrap();
    }

    for (name, cmd, desc, folder) in [
        ("Disk usage", "df -h | sort -k5 -r | head", "Fullest filesystems first", "Diagnostics"),
        ("Who's hogging CPU", "ps aux --sort=-%cpu | head -n 8", "Top processes by CPU", "Diagnostics"),
        ("Tail a service", "journalctl -fu {{service}}", "Asks which service", "Logs"),
        ("Stamp the log", "echo '--- {{hostname}} {{date}} {{time}} ---'", "Drop a marker in the scrollback", "Logs"),
        ("Restart nginx", "sudo systemctl restart nginx && systemctl status nginx --no-pager", "", "Deploy"),
    ] {
        svc.save_snippet(Snippet {
            id: String::new(),
            name: name.into(),
            command: cmd.into(),
            description: desc.into(),
            folder: folder.into(),
            tags: vec![],
            created_at: now(),
            updated_at: now(),
        })
        .unwrap();
    }

    for (host, port, kind, fp) in [
        ("bastion.example.net", 22, "ssh-ed25519", "SHA256:x1rT0pV6c1m9QbHq4nWJcF5gk1Yt3uZ3w8sLrM2dE0Q"),
        ("192.0.2.40", 22, "ecdsa-sha2-nistp256", "SHA256:9Hq0b2VtZr6y1oQwA7mXJ4sEo5kP8fN3cLdT6uB2iRg"),
        ("git.example.org", 2222, "ssh-rsa", "SHA256:Qm7cV2pK9zR1tY5uW3xN8bL0aE6sD4fG2hJ1kM7nP5o"),
    ] {
        let n = now();
        svc.store
            .save_known_host(&KnownHost {
                id: String::new(),
                host: host.into(),
                port,
                key_type: kind.into(),
                fingerprint: fp.into(),
                public_key: String::new(),
                first_seen: n,
                last_seen: n,
                trusted: true,
            })
            .unwrap();
    }

    std::fs::write(
        svc.config.data_dir.join("triggers.json"),
        r#"[{"name":"Errors","pattern":"(?i)\\b(error|fatal|panic)\\b","isRegex":true,"action":"both","caseSensitive":false,"enabled":true},
{"name":"Build finished","pattern":"BUILD SUCCESSFUL","isRegex":false,"action":"sound","caseSensitive":true,"enabled":true},
{"name":"Disk nearly full","pattern":"9[0-9]%","isRegex":true,"action":"highlight","caseSensitive":false,"enabled":false}]"#,
    )
    .unwrap();

    let start = 1_791_633_486u64;
    for (i, (title, lines)) in [
        ("atlas — deploy", &["$ ./deploy.sh\r\n", "\x1b[32m✔\x1b[0m built in 41s\r\n", "\x1b[32m✔\x1b[0m rolled out to 4/4 nodes\r\n"][..]),
        ("Switch console", &["Switch> enable\r\n", "Switch# show version\r\n", "Uptime is 212 days\r\n"][..]),
        ("Local Machine", &["$ cargo test\r\n", "test result: \x1b[32mok\x1b[0m. 135 passed\r\n"][..]),
    ]
    .iter()
    .enumerate()
    {
        let mut body = format!(
            "{{\"version\":2,\"width\":100,\"height\":30,\"timestamp\":{},\"title\":\"{title}\",\"env\":{{\"TERM\":\"xterm-256color\"}}}}\n",
            start - i as u64 * 86_400 * 3
        );
        for (j, l) in lines.iter().enumerate() {
            body += &format!("[{}.5,\"o\",{}]\n", j * 2, serde_json::to_string(l).unwrap());
        }
        std::fs::write(svc.config.recordings_dir.join(format!("demo-{i}.cast")), body).unwrap();
    }
    println!("demo data written to {}", svc.config.data_dir.display());
}
