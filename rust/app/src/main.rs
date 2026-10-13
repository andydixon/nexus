// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Nexus Terminal: SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminals in one
//! native window. `nexus-terminal [--theme pro|midnight|light] [--screenshot out.png] [--size WxH]`.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod app;
mod chrome;
mod desktop_view;
mod forwards;
mod github_view;
mod irc_view;
mod osc;
mod profile_editor;
mod quick_connect;
mod raw_view;
mod recordings;
mod restore;
mod settings;
mod sftp;
mod sidebar;
mod snippets;
mod splits;
mod ssh_keys;
mod state;
mod tabs;
mod terminal;
mod terminal_themes;
mod theme;
mod transfer;
mod triggers;
mod views;
mod widgets;

use nexus_core::config::AppConfig;
use nexus_core::service::Service;
use std::sync::Arc;

fn main() -> eframe::Result {
    harden_process();
    // A panic on a worker thread (a session's reader, a transfer) ends only that thread; say so in
    // the UI rather than letting the session just stop.
    let default_hook = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        default_hook(info);
        let what =
            info.payload().downcast_ref::<&str>().copied().or_else(|| info.payload().downcast_ref::<String>().map(String::as_str)).unwrap_or("unknown error");
        let thread = std::thread::current().name().unwrap_or("worker").to_string();
        app::post_error_nonblocking(format!("Internal error in {thread} thread: {what}. Other sessions are unaffected; reconnect the affected one."));
    }));
    let mut opts = app::Options {
        screenshot: None,
        theme: None,
        run_local: None,
        open: None,
        connect: vec![],
        actions: vec![],
        answers: vec![],
        shot_delay: std::time::Duration::from_millis(600),
    };
    let mut size = [1360.0, 860.0];
    let mut data_dir = None;
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--theme" => opts.theme = args.next(),
            "--run-local" => opts.run_local = args.next(),
            "--open" => opts.open = args.next(),
            "--connect" => opts.connect.extend(args.next()),
            "--do" => opts.actions.extend(args.next()),
            "--answer" => opts.answers.extend(args.next()),
            "--shot-delay" => {
                if let Some(ms) = args.next().and_then(|s| s.parse().ok()) {
                    opts.shot_delay = std::time::Duration::from_millis(ms);
                }
            }
            "--screenshot" => opts.screenshot = args.next().map(Into::into),
            // Use another data directory (testing, screenshots, portable installs).
            "--data-dir" => data_dir = args.next(),
            "--size" => {
                if let Some((w, h)) = args.next().as_deref().and_then(|s| s.split_once('x')) {
                    size = [w.parse().unwrap_or(size[0]), h.parse().unwrap_or(size[1])];
                }
            }
            "-V" | "--version" => {
                println!("Nexus Terminal {}", nexus_core::service::VERSION);
                return Ok(());
            }
            _ => {
                println!(
                    "usage: nexus-terminal [--theme pro|midnight|light] [--data-dir DIR] [--connect NAME]... [--open WINDOW] [--do PALETTE-COMMAND]... [--answer SECRET]... (or NEXUS_ANSWERS=one-per-line) [--shot-delay MS] [--run-local CMD] [--screenshot out.png] [--size WxH] [--version]"
                );
                return Ok(());
            }
        }
    }
    // Automation answers: prefer NEXUS_ANSWERS (one per line), which stays out of the process list
    // and shell history; it's removed so local shells don't inherit it.
    if let Ok(v) = std::env::var("NEXUS_ANSWERS") {
        opts.answers.extend(v.lines().map(String::from));
        // SAFETY: still single-threaded here; no other thread can be reading the environment.
        unsafe { std::env::remove_var("NEXUS_ANSWERS") };
    }
    // They answer every credential prompt in order (and --answer is visible in the process list),
    // so they're only honoured for screenshot runs.
    if !opts.answers.is_empty() && opts.screenshot.is_none() {
        eprintln!("--answer/NEXUS_ANSWERS only work together with --screenshot (they auto-fill credential prompts)");
        std::process::exit(2);
    }
    let config = match data_dir {
        Some(d) => {
            let c = AppConfig::in_dir(d.into());
            nexus_core::config::create_private_dir(&c.data_dir).and_then(|_| nexus_core::config::create_private_dir(&c.vault_dir)).map(|_| c)
        }
        None => AppConfig::load(),
    };
    let svc = match config.map_err(|e| e.to_string()).and_then(|c| Service::open(c).map_err(|e| e.to_string())) {
        Ok(s) => Arc::new(s),
        Err(e) => {
            eprintln!("Nexus Terminal couldn't open its data: {e}");
            rfd::MessageDialog::new().set_title("Nexus Terminal").set_description(format!("Couldn't open the application data:\n{e}")).show();
            std::process::exit(1);
        }
    };
    init_logging(&svc.config.data_dir);
    tracing::info!(version = nexus_core::service::VERSION, "starting");
    let headless = opts.screenshot.is_some();
    let native = eframe::NativeOptions {
        persist_window: !headless,
        viewport: egui::ViewportBuilder::default()
            .with_title("Nexus Terminal")
            .with_app_id("nexus-terminal")
            .with_icon(window_icon())
            .with_inner_size(size)
            .with_min_inner_size([820.0, 520.0])
            .with_decorations(false)
            .with_transparent(false)
            .with_drag_and_drop(true),
        ..Default::default()
    };
    let svc2 = svc.clone();
    let r = eframe::run_native("nexus-terminal", native, Box::new(move |cc| Ok(Box::new(app::App::new(cc, svc2, opts)))));
    svc.sessions.close_all();
    svc.secrets.vault.lock();
    r
}

fn window_icon() -> egui::IconData {
    let img = image::load_from_memory(chrome::LOGO_PNG).map(|i| i.to_rgba8()).unwrap_or_default();
    egui::IconData { width: img.width(), height: img.height(), rgba: img.into_raw() }
}

/// Diagnostics go to `nexus.log` in the data directory (rotated at 5 MiB), and to stderr too when
/// RUST_LOG is set. RUST_LOG also sets the level (default: info for Nexus, warn for libraries).
/// Connection lifecycle, host-key checks and errors are logged; credentials and terminal data never.
fn init_logging(data_dir: &std::path::Path) {
    use tracing_subscriber::prelude::*;
    let path = data_dir.join("nexus.log");
    if std::fs::metadata(&path).is_ok_and(|m| m.len() > 5 << 20) {
        let _ = std::fs::rename(&path, data_dir.join("nexus.log.1"));
    }
    let mut opts = std::fs::OpenOptions::new();
    opts.create(true).append(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
    let file = opts.open(&path).ok().map(|f| tracing_subscriber::fmt::layer().with_ansi(false).with_writer(std::sync::Mutex::new(f)));
    let stderr = std::env::var_os("RUST_LOG").map(|_| tracing_subscriber::fmt::layer().with_writer(std::io::stderr));
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn,nexus_terminal=info,nexus_core=info"));
    let _ = tracing_subscriber::registry().with(filter).with(file).with(stderr).try_init();
}

/// Release builds keep secrets out of core dumps and away from same-user debuggers: no core
/// files, and on Linux the process is non-dumpable (also blocks ptrace attach by other processes
/// of the same user). Debug builds skip this so they can still be debugged.
fn harden_process() {
    #[cfg(unix)]
    if !cfg!(debug_assertions) {
        // SAFETY: plain syscalls on this process with valid arguments; failure is harmless.
        unsafe {
            let none = libc::rlimit { rlim_cur: 0, rlim_max: 0 };
            libc::setrlimit(libc::RLIMIT_CORE, &none);
            #[cfg(target_os = "linux")]
            libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0);
        }
    }
}
