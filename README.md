# Nexus Terminal

**A native desktop client for SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local terminals — with secure credential handling and a focused, multi-session workflow.**

Nexus Terminal helps you manage many connections at once, keep terminal work organised, and move files with confidence. It is a single Rust application with no webview, so it starts quickly and stays responsive under heavy terminal output.

## Highlights

- SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local terminal sessions in one app
- Tabbed sessions with split panes, pinning and drag-to-reorder
- SFTP browser with drag-and-drop uploads and a permission-aware directory picker
- SSH jump hosts, SOCKS5/HTTP proxies, local and remote port forwards, agent forwarding and TOTP auto-fill
- Serial consoles with full line settings (baud, data bits, parity, stop bits, flow control)
- Native VNC and RDP viewers; RDP with NLA, certificate pinning, and SOCKS5/HTTP proxy or SSH tunnel routes
- Disconnected overlay with one-click reconnect, and auto-reconnect with backoff
- Broadcast input, snippets, trigger rules and terminal search
- Session recording (asciicast v2) and playback
- Secure credential storage with the OS keychain first and an encrypted vault fallback

## Install

**macOS** (Homebrew):

```sh
brew install --cask andydixon/tap/nexusterminal
```

**Debian, Ubuntu** (22.04 and later):

```sh
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://repo.dixon.cx/dixon.gpg | sudo tee /etc/apt/keyrings/dixon.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/dixon.gpg] https://repo.dixon.cx/apt stable main" |
    sudo tee /etc/apt/sources.list.d/dixon.list
sudo apt update && sudo apt install nexusterminal
```

**Fedora, RHEL, openSUSE:**

```sh
sudo curl -fsSL -o /etc/yum.repos.d/dixon.repo https://repo.dixon.cx/rpm/dixon.repo
sudo dnf install nexusterminal        # or: sudo zypper install nexusterminal
```

**Arch Linux:**

```sh
curl -fsSL https://repo.dixon.cx/dixon.asc | sudo pacman-key --add -
sudo pacman-key --lsign-key C22FF7330668417C62C7A304CBC7951D7F2234D1
printf '\n[dixon]\nServer = https://repo.dixon.cx/arch/$arch\n' | sudo tee -a /etc/pacman.conf
sudo pacman -Sy nexusterminal
```

**Windows**, or any platform by hand: download a zip from
[GitHub Releases](https://github.com/andydixon/nexus/releases) and check it against `SHA256SUMS`.
Builds aren't code-signed yet, so macOS and Windows ask you to confirm the first launch.

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

## License

Copyright (C) 2026 Andy Dixon.

Nexus Terminal is free software: you can redistribute it and/or modify it under the terms of the
[GNU Affero General Public License, version 3](LICENSE) as published by the Free Software
Foundation. It is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY;
without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
license for details.

Third-party crates (including the patched copies in `rust/vendor/`) keep their own licenses,
which are listed in each crate.

