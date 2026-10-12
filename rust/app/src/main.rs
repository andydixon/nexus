//! Nexus Terminal: SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminals in one
//! native window. `nexus-terminal [--theme pro|midnight|light] [--screenshot out.png] [--size WxH]`.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod app;
mod chrome;
mod forwards;
mod github_view;
mod irc_view;
mod profile_editor;
mod quick_connect;
mod raw_view;
mod recordings;
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
mod vnc_view;
mod widgets;

use nexus_core::config::AppConfig;
use nexus_core::service::Service;
use std::sync::Arc;

fn main() -> eframe::Result {
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
                    "usage: nexus-terminal [--theme pro|midnight|light] [--data-dir DIR] [--connect NAME]... [--open WINDOW] [--do PALETTE-COMMAND]... [--answer SECRET]... [--shot-delay MS] [--run-local CMD] [--screenshot out.png] [--size WxH] [--version]"
                );
                return Ok(());
            }
        }
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
