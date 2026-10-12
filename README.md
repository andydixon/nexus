# Nexus Terminal

**A native desktop client for SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminals — with secure credential handling and a focused, multi-session workflow.**

Nexus Terminal helps you manage many connections at once, keep terminal work organised, and move files with confidence. It is a single Rust application with no webview, so it starts quickly and stays responsive under heavy terminal output.

## Highlights

- SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local terminal sessions in one app
- Tabbed sessions with split panes, pinning and drag-to-reorder
- SFTP browser with drag-and-drop uploads and a permission-aware directory picker
- SSH jump hosts, SOCKS5/HTTP proxies, local and remote port forwards, agent forwarding and TOTP auto-fill
- Serial consoles with full line settings (baud, data bits, parity, stop bits, flow control)
- Native VNC viewer, optionally tunnelled over SSH
- Disconnected overlay with one-click reconnect, and auto-reconnect with backoff
- Broadcast input, snippets, trigger rules and terminal search
- Session recording (asciicast v2) and playback
- Secure credential storage with the OS keychain first and an encrypted vault fallback

## Quick start

1. Launch Nexus Terminal.
2. Click **+** at the bottom of the sidebar (or press **Cmd+N** / **Ctrl+N**) and create a connection profile.
3. Click **Create & Connect** to save it and open a session in a new tab. Afterwards, click the profile in the sidebar to connect.

Prefer a one-off session? Use **Cmd+Shift+Q** / **Ctrl+Shift+Q** to quick connect, or click **Local Machine** in the sidebar for a local shell.

If a session drops, use the **Reconnect** button in the disconnected overlay.

## Documentation

- Product overview: [docs/overview.md](docs/overview.md)
- Quick start (shareable): [docs/quick-start.md](docs/quick-start.md)
- User manual: [docs/user-manual.md](docs/user-manual.md)
- Illustrated user guide (PDF): [docs/guide/nexus-guide.pdf](docs/guide/nexus-guide.pdf), built from LaTeX in [docs/guide](docs/guide) (`make` there; `tools/screenshots.sh` regenerates the screenshots)
- Build and release: [docs/build-and-release.md](docs/build-and-release.md)

## Keyboard shortcuts

All shortcuts use **Cmd** on macOS and **Ctrl** on Windows/Linux.

| macOS Shortcut    | Windows/Linux Shortcut | Action                           |
| ----------------- | ---------------------- | -------------------------------- |
| Cmd + K           | Ctrl + K               | Open command palette             |
| Cmd + N           | Ctrl + N               | New connection                   |
| Cmd + W           | Ctrl + W               | Close current tab                |
| Cmd + Tab         | Ctrl + Tab             | Next tab                         |
| Cmd + Shift + Tab | Ctrl + Shift + Tab     | Previous tab                     |
| Cmd + 1–9         | Ctrl + 1–9             | Switch to tab by number          |
| Cmd + ,           | Ctrl + ,               | Open settings                    |
| Cmd + F           | Ctrl + F               | Find in terminal                 |
| Cmd + Shift + D   | Ctrl + Shift + D       | Split pane right (side by side)  |
| Cmd + Shift + E   | Ctrl + Shift + E       | Split pane down (top and bottom) |
| Cmd + Shift + R   | Ctrl + Shift + R       | Toggle session recording         |
| Cmd + Shift + B   | Ctrl + Shift + B       | Toggle broadcast input mode      |
| Cmd + Shift + S   | Ctrl + Shift + S       | Open snippet library             |
| Cmd + Shift + Q   | Ctrl + Shift + Q       | Quick connect                    |
| F11               | F11                    | Toggle fullscreen                |

## Building from source

Nexus Terminal is a Rust workspace in `rust/`. With [rustup](https://rustup.rs) installed:

```bash
cd rust
cargo run --release
```

See [docs/build-and-release.md](docs/build-and-release.md) for platform prerequisites, tests and release packaging.
