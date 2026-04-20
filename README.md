# Nexus Terminal

**A cross-platform desktop client for SSH, Telnet, RAW TCP, and GitHub — with encrypted credential storage and a dark Material-inspired interface.**

Built with [Wails v2](https://wails.io/) (Go backend, HTML/CSS/JS frontend, [xterm.js](https://xtermjs.org/) terminal emulator).

---

### Feature Highlights

- ✅ SSH with password, public key, keyboard-interactive, SSH agent, and TOTP/2FA
- ✅ Telnet with option negotiation, encoding, and local echo
- ✅ RAW TCP with text, hex, and split views
- ✅ GitHub repository browser grouped by organisation
- ✅ Tabbed multi-session with split panes, pinning, and reordering
- ✅ SFTP file browser overlay for SSH sessions
- ✅ Session recording and playback (asciicast v2)
- ✅ Encrypted vault (AES-256-GCM + Argon2id) and OS keychain integration
- ✅ Jump hosts, port forwarding, and SSH agent forwarding
- ✅ SOCKS5 and HTTP CONNECT proxy support for SSH
- ✅ Auto-reconnect with exponential backoff
- ✅ Auto-login scripts (expect-style send/wait sequences)
- ✅ Broadcast input — type into multiple sessions at once
- ✅ Snippet/command library for frequently-used commands
- ✅ Terminal search with regex and case-sensitivity toggles
- ✅ Right-click context menu on terminal
- ✅ Quick connect without saving a full profile
- ✅ OpenSSH `~/.ssh/config` import
- ✅ Encrypted profile export/import (`.nexus` format)
- ✅ 8 built-in colour themes and command palette
- ✅ Automatic update checking via GitHub Releases

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Keyboard Shortcuts](#keyboard-shortcuts)
3. [Connection Types](#connection-types)
4. [Power Features](#power-features)
5. [Security](#security)
6. [Architecture](#architecture)
7. [Building & Testing](#building--testing)
8. [Data Locations](#data-locations)
9. [Troubleshooting](#troubleshooting)
10. [Licence](#licence)

---

## Getting Started

### What You'll Need

| Tool | Version | Where to get it |
|------|---------|-----------------|
| Go | 1.23+ | [golang.org/dl](https://golang.org/dl/) |
| Node.js | 18+ | [nodejs.org](https://nodejs.org/) |
| Wails CLI | v2 | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |

> **Linux users:** you'll also need GTK and WebKit system libraries. Run `wails doctor` to check your setup.

### Clone and Run

```bash
git clone https://github.com/andydixon/nexus.git
cd nexus
make deps    # install Go modules and npm packages
make dev     # launch with hot reload
```

The first build may take a minute or two while dependencies are fetched. Once it's running you'll see the Nexus Terminal window appear — ready to create your first connection profile.

### Quick Connect

If you just want to connect to a server without setting up a full profile, press **Cmd+Shift+Q** (macOS) or **Ctrl+Shift+Q** (Windows/Linux). Choose a protocol (SSH, Telnet, or RAW), enter the host and port, and you're connected immediately. The session won't be saved as a profile unless you choose to save it afterwards.

---

## Keyboard Shortcuts

All shortcuts use **Cmd** on macOS and **Ctrl** on Windows/Linux.

| Shortcut | Action |
|----------|--------|
| Cmd + K | Open command palette |
| Cmd + N | New connection |
| Cmd + W | Close current tab |
| Cmd + Tab | Next tab |
| Cmd + Shift + Tab | Previous tab |
| Cmd + 1–9 | Switch to tab by number |
| Cmd + , | Open settings |
| Cmd + F | Find in terminal |
| Cmd + Shift + D | Split pane vertically |
| Cmd + Shift + E | Split pane horizontally |
| Cmd + Shift + R | Toggle session recording |
| Cmd + Shift + B | Toggle broadcast input mode |
| Cmd + Shift + S | Open snippet library |
| Cmd + Shift + Q | Quick connect |
| F11 | Toggle fullscreen (Windows/Linux) |

---

## Connection Types

### SSH

Full-featured SSH client with support for:

- **Authentication** — password, public key, keyboard-interactive, SSH agent, and agent forwarding
- **TOTP/2FA** — auto-fill time-based one-time passwords during keyboard-interactive auth (configure a TOTP secret in the profile editor)
- **Jump hosts** — connect through one or more bastion servers (multi-hop)
- **Port forwarding** — local, remote, and dynamic (SOCKS) tunnels
- **Proxy support** — route connections through SOCKS5 or HTTP CONNECT proxies (with optional authentication)
- **SFTP** — built-in file browser that slides open as a right-side overlay panel (click the 📁 icon on the tab)
- **Auto-login scripts** — define expect-style send/wait sequences to automate login flows after connecting
- **Auto-reconnect** — automatically reconnect with exponential backoff when the connection drops unexpectedly
- **Host key verification** — four policies: strict, ask, accept-new, and insecure
- **Session recording** — capture terminal output to asciicast v2 files for later playback
- **OpenSSH config import** — reads your existing `~/.ssh/config` to create connection profiles

### Telnet

- Option negotiation, configurable character encoding (UTF-8 and others)
- Local echo and line mode settings
- Configurable line endings
- Auto-login scripts (expect-style send/wait sequences)
- Auto-reconnect with exponential backoff

### RAW TCP

- Binary-safe connection with three view modes: text, hex, and split (text + hex side by side)
- Payload presets for quick sending of common data
- Configurable character encoding and line endings
- Auto-reconnect on disconnection

### GitHub

- Browse your repositories grouped by organisation
- Sorted by most recent commit, with automatic refresh
- Quick access from the sidebar alongside your other connections

---

## Power Features

### Broadcast Input

Press **Cmd+Shift+B** (or click the broadcast button in the tab bar) to toggle broadcast mode. When active, everything you type is sent to **all connected sessions** simultaneously — ideal for running the same command across multiple servers. The broadcast button pulses red to remind you it's active.

### Snippet Library

Press **Cmd+Shift+S** to open the snippet library. Save frequently-used commands (e.g. `sudo systemctl restart nginx`) with a name, description, and optional folder/tags. You can send a snippet to the active session with one click, or access snippets from the right-click context menu.

### Terminal Search

Press **Cmd+F** to open the search overlay on the active terminal. Features include:

- Case-sensitive toggle (**Aa** button)
- Regular expression toggle (**.*** button)
- Match count indicator
- Navigate through matches with up/down arrows or Enter/Shift+Enter

### Right-Click Context Menu

Right-click anywhere in the terminal to access:

- **Copy** — copy selected text to clipboard
- **Paste** — paste from clipboard
- **Select All** — select all terminal content
- **Find** — open the search overlay
- **Send Snippet** — quickly send a saved command
- **Clear** / **Reset** — clear the terminal buffer

### Auto-Login Scripts

For SSH and Telnet connections, you can define expect-style login scripts in the profile editor. Each script is a sequence of **wait** (match text from the server) and **send** (send text + newline) steps that run automatically after connection. Useful for:

- Automating password prompts on bastion hosts
- Navigating menu-driven systems
- Running initial setup commands

Each wait step has a configurable timeout (default 10 seconds).

### Auto-Reconnect

Enable auto-reconnect in the profile's Advanced section. When an SSH or Telnet connection drops unexpectedly, Nexus Terminal will automatically attempt to reconnect using exponential backoff (1s, 2s, 4s, 8s… up to 30s between attempts). You can configure the maximum number of attempts (default 5).

### Quick Connect

Press **Cmd+Shift+Q** to open a lightweight connect dialog. Choose SSH, Telnet, or RAW; enter the host and port; and connect immediately without creating a saved profile.

---

## Security

Nexus Terminal takes credential storage seriously. Your passwords and private key passphrases are never stored in plain text.

### Two-Tier Credential Storage

1. **OS keychain (preferred)** — on macOS this is Keychain, on Linux it's Secret Service via D-Bus, and on Windows it's Credential Manager. If your system's keychain is available, Nexus Terminal uses it automatically.

2. **Encrypted file vault (fallback)** — if the OS keychain isn't available, credentials are stored in a local vault file encrypted with AES-256-GCM. You set a master password when creating the vault, and a strength metre helps you choose a good one.

### How the Encryption Works

| Detail | Value |
|--------|-------|
| Cipher | AES-256-GCM |
| Key derivation | Argon2id — 128 MiB memory, 3 iterations, 4 threads |
| Key length | 256 bits (32 bytes) |
| Salt | 32 random bytes (unique per vault) |
| Nonce | Random, prepended to ciphertext |

### TOTP/2FA

For SSH servers that use two-factor authentication, you can store a TOTP secret in the profile's encrypted vault. During keyboard-interactive authentication, Nexus Terminal detects 2FA prompts (verification code, OTP, authenticator, etc.) and automatically generates and submits a 6-digit time-based code. You can test the secret from the profile editor before connecting.

### Known Hosts

When you connect to an SSH server for the first time, Nexus Terminal can remember the server's fingerprint. You choose one of four policies per profile:

- **Strict** — reject any server whose key doesn't match a saved fingerprint
- **Ask** — prompt you to accept or reject unknown keys
- **Accept new** — automatically trust first-time connections, reject changes
- **Insecure** — accept any key (not recommended)

### Profile Export & Import

Profiles can be exported as encrypted `.nexus` bundles (AES-256-GCM, same as the vault). By default, exports exclude secrets — passwords and passphrases are only included when you choose an encrypted export. Secrets are keyed internally by profile: `profile:<uuid>:password`, `profile:<uuid>:passphrase`.

---

## Architecture

> This section is aimed at developers who want to understand or contribute to the codebase.

Nexus Terminal is a [Wails v2](https://wails.io/) desktop application: a Go backend handles all connection logic and data storage, whilst an HTML/CSS/JS frontend (rendered in a native webview) provides the interface.

```
internal/
  app/           — lifecycle bootstrap: opens database, initialises secrets, wires everything together
  api/           — single Service struct bound to Wails; every public method is callable from JS
  sessions/      — manages concurrent sessions; owns SSH/Telnet/RAW/GitHub client lifecycles
  storage/       — SQLite repository (WAL mode) with embedded sequential migrations
  secrets/       — two-tier credential storage (OS keychain + encrypted file vault)
  models/        — shared domain types, protocol configs, and defaults
  sshclient/     — SSH client with agent, proxy, and TOTP support
  telnetclient/  — Telnet with option negotiation
  rawclient/     — RAW TCP with hex encoding
  githubclient/  — GitHub API integration
  sftpclient/    — SFTP file operations
  sshconfig/     — OpenSSH config parser
  recording/     — asciicast v2 session recording
  exportimport/  — encrypted profile bundles (.nexus format)
  updater/       — GitHub Releases update checker
  knownhosts/    — SSH host key management
  validation/    — profile and protocol validation
  audit/         — timestamped audit logging

frontend/
  src/js/        — 18 IIFE-wrapped JS modules (no framework, no bundler)
  src/css/       — modular stylesheets with design tokens (tokens.css)
```

### How the Frontend and Backend Communicate

- **Frontend → Backend:** calling `window.go.api.Service.MethodName()` returns a Promise. Every public method on the Go `Service` struct is available this way.
- **Backend → Frontend:** the Go side pushes events via `wailsRuntime.EventsEmit()`. The frontend listens with `window.runtime.EventsOn()`.
- **Interactive prompts** (e.g. password requests, host key verification) use a request/response event pattern with a unique `requestID`, so the Go side can block on a channel until the user responds in the UI.

---

## Building & Testing

| Command | What it does |
|---------|-------------|
| `make deps` | Install Go modules and npm packages |
| `make build` | Production build → `build/bin/nexus-terminal.app` (macOS) |
| `make dev` | Development mode with hot reload |
| `make test` | Run all Go tests (`go test ./... -v -count=1`) |
| `make lint` | Run golangci-lint |

To run tests for a single package:

```bash
go test ./internal/storage/ -v -count=1 -run TestProfileCRUD
```

The frontend has no separate build step — `npm run build` (called by Wails) copies xterm vendor libraries from `node_modules` into the frontend source tree. All frontend assets are embedded at compile time.

### Cross-Build Helper (macOS/Linux)

If you want to build multiple targets from a macOS or Linux host, use the helper script:

```bash
./build-all.sh
```

It builds the host platform plus Linux and Windows (both `amd64` and `arm64`) **where a suitable C compiler is available**. SQLite requires cgo, so missing toolchains will cause a target to be skipped. Common compiler names:

- Linux: `x86_64-linux-gnu-gcc`, `aarch64-linux-gnu-gcc`
- Windows: `x86_64-w64-mingw32-gcc`, `aarch64-w64-mingw32-gcc`

### Windows Builds

SQLite requires cgo on Windows, which means you need a C compiler in `PATH` (for example MSYS2 with mingw-w64). Use the Windows build script to set `CGO_ENABLED=1` and run the right steps:

```powershell
.\build.ps1 deps
.\build.ps1 build
```

If the build fails with a cgo/SQLite error, install MSYS2, then install the toolchain:

```powershell
pacman -S --needed mingw-w64-x86_64-toolchain
```

Then reopen your terminal so `gcc` is on `PATH` and retry `build.ps1`.

---

## Data Locations

| Platform | Database | Vault | Recordings |
|----------|----------|-------|------------|
| macOS | `~/Library/Application Support/NexusTerminal/nexus.db` | `.../vault` | `.../recordings/` |
| Linux | `~/.config/NexusTerminal/nexus.db` | `.../vault` | `.../recordings/` |
| Windows | `%APPDATA%\NexusTerminal\nexus.db` | `...\vault` | `...\recordings\` |

The database uses SQLite in WAL (Write-Ahead Logging) mode. Migrations are embedded at compile time and applied automatically on startup.

---

## Troubleshooting

**The app won't start on Linux**
You likely need GTK and WebKit system libraries. Run `wails doctor` to see what's missing, then install the packages it recommends for your distribution.

**"Vault locked" on every launch**
If you're using the encrypted file vault (rather than the OS keychain), you'll need to enter your master password each time. If your system supports a keychain, Nexus Terminal will use it instead and you won't see this prompt.

**SSH connection fails with "host key verification failed"**
Your profile's host key policy may be set to **strict** and the server's key has changed (or hasn't been saved yet). Open the profile settings and choose **ask** if you'd like to be prompted, or verify the server's key and update your known hosts.

**SSH connection fails through a proxy**
Check that your proxy host, port, and type (SOCKS5 or HTTP CONNECT) are correct in the profile's Proxy section. If the proxy requires authentication, make sure the username and password are filled in. SOCKS5 defaults to port 1080; HTTP CONNECT defaults to port 8080.

**TOTP/2FA code is rejected**
Ensure the TOTP secret is correct (base32-encoded) and that your system clock is accurate. Time-based codes are only valid for 30-second windows. Use the "Test" button in the TOTP section of the profile editor to verify the secret generates valid codes.

**Auto-reconnect keeps failing**
The connection must have been successfully established at least once. Auto-reconnect uses the same credentials as the original connection. If the server is genuinely unreachable, reconnection attempts will exhaust after the configured maximum (default 5 attempts).

**Login script times out**
Each "wait" step has a configurable timeout (default 10 seconds). If the server takes longer to produce the expected text, increase the timeout in the profile editor. Check that the wait pattern matches exactly — it's a plain text substring match, not a regex.

**Terminal text looks wrong or garbled**
Check the character encoding setting on the connection profile. Most modern servers use UTF-8, but some older systems may need a different encoding.

**Build fails with "wails: command not found"**
Install the Wails CLI:
```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```
Make sure `$GOPATH/bin` (or `$HOME/go/bin`) is in your `PATH`.

---

## Licence

This project is licensed under the MIT Licence. See [LICENCE](LICENCE) for details.

**Bundle identifier:** `com.andydixon.nexusterminal`
**Author:** Andy Dixon — [www.dixon.cx/nexus](https://www.dixon.cx/nexus)

---

<sub>Built with [Wails](https://wails.io/), [xterm.js](https://xtermjs.org/), and [Go](https://go.dev/).</sub>
