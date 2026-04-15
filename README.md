# Nexus Terminal

**A cross-platform desktop client for SSH, Telnet, RAW TCP, and GitHub — with encrypted credential storage and a dark Material-inspired interface.**

Built with [Wails v2](https://wails.io/) (Go backend, HTML/CSS/JS frontend, [xterm.js](https://xtermjs.org/) terminal emulator).

---

### Feature Highlights

- ✅ SSH with password, public key, keyboard-interactive, and SSH agent authentication
- ✅ Telnet with option negotiation, encoding, and local echo
- ✅ RAW TCP with text, hex, and split views
- ✅ GitHub repository browser grouped by organisation
- ✅ Tabbed multi-session with split panes, pinning, and reordering
- ✅ SFTP file browser overlay for SSH sessions
- ✅ Session recording and playback (asciicast v2)
- ✅ Encrypted vault (AES-256-GCM + Argon2id) and OS keychain integration
- ✅ Jump hosts, port forwarding, and SSH agent forwarding
- ✅ OpenSSH `~/.ssh/config` import
- ✅ Encrypted profile export/import (`.nexus` format)
- ✅ 8 built-in colour themes and command palette
- ✅ Automatic update checking via GitHub Releases

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Keyboard Shortcuts](#keyboard-shortcuts)
3. [Connection Types](#connection-types)
4. [Security](#security)
5. [Architecture](#architecture)
6. [Building & Testing](#building--testing)
7. [Data Locations](#data-locations)
8. [Troubleshooting](#troubleshooting)
9. [Licence](#licence)

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
git clone https://github.com/andydixon/nexus-terminal.git
cd nexus-terminal
make deps    # install Go modules and npm packages
make dev     # launch with hot reload
```

The first build may take a minute or two while dependencies are fetched. Once it's running you'll see the Nexus Terminal window appear — ready to create your first connection profile.

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
| F11 | Toggle fullscreen (Windows/Linux) |

---

## Connection Types

### SSH

Full-featured SSH client with support for:

- **Authentication** — password, public key, keyboard-interactive, SSH agent, and agent forwarding
- **Jump hosts** — connect through one or more bastion servers (multi-hop)
- **Port forwarding** — local, remote, and dynamic (SOCKS) tunnels
- **SFTP** — built-in file browser that slides open as a right-side overlay panel
- **Host key verification** — four policies: strict, ask, accept-new, and insecure
- **Session recording** — capture terminal output to asciicast v2 files for later playback
- **OpenSSH config import** — reads your existing `~/.ssh/config` to create connection profiles

### Telnet

- Option negotiation, configurable character encoding (UTF-8 and others)
- Local echo and line mode settings
- Configurable line endings

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
  sshclient/     — SSH client with agent support
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
  src/js/        — 15 IIFE-wrapped JS modules (no framework, no bundler)
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
