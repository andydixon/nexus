# Nexus Terminal

**A premium desktop SSH, Telnet, and RAW TCP client** built with [Wails v2](https://wails.io/) (Go backend, web frontend).

Nexus Terminal provides a modern, tabbed interface for managing remote connections over SSH, Telnet, and raw TCP — with encrypted credential storage, host key verification, audit logging, and a dark Material-inspired design.

---

## Features

### Implemented

- ✅ **SSH connections** — password, public key, and keyboard-interactive authentication
- ✅ **Telnet** — option negotiation, character encoding, local echo, configurable line modes
- ✅ **RAW TCP** — binary-safe text, hex, and split views with payload presets
- ✅ **Saved connection profiles** — tags, folders, environments, favourites
- ✅ **Encrypted secret storage** — AES-256-GCM with Argon2id key derivation
- ✅ **OS keychain integration** — macOS Keychain, Linux Secret Service, Windows Credential Manager
- ✅ **Known hosts verification** — configurable policies (strict / ask / accept-new / insecure)
- ✅ **Tabbed multi-session management** — rename, reorder, pin, context menu
- ✅ **xterm.js terminal** — search, web links, fit-to-container
- ✅ **Profile import/export** — JSON format, secrets excluded by default
- ✅ **Command palette** — quick access via Ctrl+K / Cmd+K
- ✅ **Dark charcoal Material-inspired UI**
- ✅ **Audit logging** — timestamped records of connections and actions
- ✅ **Input validation** — comprehensive profile and protocol validation

### In Progress

- 🔨 **Jump host / bastion support** — architecture in place, single-hop implemented
- 🔨 **Session transcript export** — partial implementation

### Planned

- 📋 Port forwarding (extension point architected)
- 📋 SFTP file transfer (extension point architected)
- 📋 Split panes
- 📋 Theme customisation
- 📋 OpenSSH config import (partial parser exists)
- 📋 Encrypted profile export

---

## Screenshots

> _Screenshots will be added once the UI is feature-complete._

---

## Prerequisites

| Tool       | Version | Install                                                           |
|------------|---------|-------------------------------------------------------------------|
| Go         | 1.21+   | [golang.org/dl](https://golang.org/dl/)                           |
| Node.js    | 18+     | [nodejs.org](https://nodejs.org/)                                 |
| Wails CLI  | v2      | `go install github.com/wailsapp/wails/v2/cmd/wails@latest`       |

> **Note:** On Linux you may also need GTK, WebKit, and related system libraries.
> Run `wails doctor` to verify your environment.

---

## Quick Start

```bash
git clone https://github.com/your-org/nexus-terminal.git
cd nexus-terminal
make deps    # install Go modules and frontend npm packages
make dev     # launch in development mode with hot reload
```

Or using the shell script:

```bash
./build.sh deps
./build.sh dev
```

---

## Build

| Command            | Description                              |
|--------------------|------------------------------------------|
| `make deps`        | Install all dependencies                 |
| `make dev`         | Run in development mode with hot reload  |
| `make build`       | Production build                         |
| `make test`        | Run Go tests                             |
| `make test-cover`  | Run tests with HTML coverage report      |
| `make clean`       | Remove build artefacts and caches        |
| `make lint`        | Run golangci-lint                        |
| `make package`     | Build and package for distribution       |
| `make help`        | Show all available targets               |

The production binary is written to `build/bin/NexusTerminal`.

---

## Architecture Overview

```
copilot-ssh-client/
├── cmd/
│   └── app/
│       └── main.go              # Application entry point (Wails bootstrap)
├── frontend/
│   ├── dist/
│   │   └── index.html           # HTML entry point (embedded by Wails)
│   ├── scripts/
│   │   ├── build.js             # Frontend build & watch script
│   │   └── copy-assets.js       # Copies xterm assets from node_modules
│   ├── src/
│   │   ├── assets/icons/        # Application icons
│   │   ├── components/          # UI components (planned)
│   │   ├── css/                 # Stylesheets
│   │   │   └── lib/             # Vendored CSS (xterm.css — auto-copied)
│   │   └── js/                  # JavaScript source
│   │       └── lib/             # Vendored JS (xterm — auto-copied)
│   └── package.json
├── internal/
│   ├── api/                     # Wails-bound API methods (planned)
│   ├── app/
│   │   └── app.go               # Application lifecycle management
│   ├── audit/                   # Audit log service (planned)
│   ├── config/
│   │   └── config.go            # Platform-specific path resolution
│   ├── knownhosts/              # Known hosts management (planned)
│   ├── models/
│   │   ├── models.go            # Core domain types (Profile, Session, etc.)
│   │   └── defaults.go          # Default values and factory functions
│   ├── preferences/             # Preferences service (planned)
│   ├── rawclient/
│   │   └── encoding.go          # Hex, escaped, and character encoding utilities
│   ├── secrets/
│   │   ├── keychain.go          # OS keychain integration
│   │   ├── redact.go            # Secret redaction for logs and UI
│   │   └── secrets.go           # AES-256-GCM encrypted vault
│   ├── sessions/                # Session lifecycle management (planned)
│   ├── sshclient/
│   │   └── hostkey.go           # SSH host key verification with policies
│   ├── storage/
│   │   ├── migrations/
│   │   │   ├── 001_initial.sql  # Initial database schema
│   │   │   └── migrations.go    # Embedded migration files
│   │   └── storage.go           # SQLite persistence layer
│   ├── telnetclient/            # Telnet client (planned)
│   └── validation/
│       └── validation.go        # Profile and protocol validation
├── build.sh                     # Shell build script for CI
├── Makefile                     # Make-based build targets
├── wails.json                   # Wails v2 project configuration
├── go.mod                       # Go module definition
└── README.md
```

### Key Packages

| Package            | Purpose                                                                |
|--------------------|------------------------------------------------------------------------|
| `cmd/app`          | Wails application bootstrap — window size, title, lifecycle hooks      |
| `internal/app`     | Wires together all subsystems; manages startup and shutdown            |
| `internal/config`  | Resolves platform-specific data, database, and vault paths             |
| `internal/models`  | Domain types shared across all packages — profiles, sessions, enums    |
| `internal/storage` | SQLite repository with WAL mode, migrations, and import/export         |
| `internal/secrets` | Encrypted vault (AES-256-GCM + Argon2id) and OS keychain abstraction  |
| `internal/sshclient` | SSH host key verification with strict / ask / accept-new policies   |
| `internal/rawclient` | Hex dump, escape parsing, and character encoding for raw TCP         |
| `internal/validation` | Multi-field validation for profiles and protocol-specific configs  |

### Frontend–Backend Interaction

Wails v2 exposes Go methods to the frontend as JavaScript functions. The `App` struct (and future API types) are bound in `main.go`. Frontend calls arrive as synchronous RPC; asynchronous data (terminal output, session events) flows via the Wails event system.

```
┌─────────────┐   Wails bindings   ┌──────────────┐
│  Frontend    │ ◄────────────────► │  Go backend   │
│  (xterm.js)  │   Wails events     │  (App struct) │
└─────────────┘                     └──────────────┘
       │                                    │
       │ xterm.js writes                    │ golang.org/x/crypto/ssh
       │ to terminal                        │ net.Dial (telnet, raw)
       ▼                                    ▼
   Browser DOM                        Remote hosts
```

---

## Security

### Secret Storage

Nexus Terminal uses a two-tier approach to credential storage:

1. **OS keychain (preferred)** — macOS Keychain, Linux Secret Service (via D-Bus), Windows Credential Manager. Probed at startup; used automatically when available.
2. **Encrypted file vault (fallback)** — AES-256-GCM encrypted JSON file at `<data-dir>/vault`. Requires a master password to unlock.

### Encryption Details

| Parameter       | Value                             |
|-----------------|-----------------------------------|
| Cipher          | AES-256-GCM                       |
| Key derivation  | Argon2id (time=3, mem=64 KB, threads=4) |
| Key length      | 256 bits (32 bytes)               |
| Salt            | 32 random bytes (per vault)       |
| Nonce           | Random, prepended to ciphertext   |

### Threat Model

**Protects against:**
- Secrets exposed via disk theft or backup exfiltration
- Accidental secret leakage in logs, exports, or UI (redaction layer)
- Credential access without the master password

**Does NOT protect against:**
- A compromised operating system or process with memory access
- Memory forensics whilst the vault is unlocked
- Keyloggers capturing the master password
- Weak master passwords (no strength enforcement currently)

### Secret Key Format

Secrets are keyed by profile: `profile:<uuid>:password`, `profile:<uuid>:passphrase`. The `ListKeys()` method exposes keys but never values.

### Export Handling

Profile exports exclude secrets by default. The JSON export format contains connection metadata but no passwords or private key passphrases unless explicitly requested (future encrypted export).

---

## Protocol Support

| Feature                          | SSH | Telnet | RAW TCP |
|----------------------------------|:---:|:------:|:-------:|
| Password authentication          | ✅  | —      | —       |
| Public key authentication        | ✅  | —      | —       |
| Keyboard-interactive auth        | ✅  | —      | —       |
| Host key verification            | ✅  | —      | —       |
| Keepalive                        | ✅  | —      | —       |
| Option negotiation               | —   | ✅     | —       |
| Local echo                       | —   | ✅     | —       |
| Character encoding (UTF-8, etc.) | —   | ✅     | ✅      |
| Text view                        | ✅  | ✅     | ✅      |
| Hex view                         | —   | —      | ✅      |
| Split (text + hex) view          | —   | —      | ✅      |
| Payload presets                  | —   | —      | ✅      |
| Configurable line endings        | —   | ✅     | ✅      |
| Auto-reconnect                   | —   | —      | ✅      |
| Jump hosts / bastions            | 🔨  | —      | —       |
| Port forwarding                  | 📋  | —      | —       |
| SFTP                             | 📋  | —      | —       |

**Legend:** ✅ implemented · 🔨 in progress · 📋 planned · — not applicable

---

## Storage

### Database

Nexus Terminal stores profiles, known hosts, preferences, and audit entries in a SQLite database with WAL (Write-Ahead Logging) mode for concurrent read performance.

| Platform | Default location                                       |
|----------|--------------------------------------------------------|
| macOS    | `~/Library/Application Support/NexusTerminal/nexus.db` |
| Linux    | `~/.config/NexusTerminal/nexus.db`                     |
| Windows  | `%APPDATA%\NexusTerminal\nexus.db`                     |

### Schema Versioning

Migrations are embedded at compile time via `embed.FS` and applied sequentially at startup. Each migration records its version in the `schema_version` table to prevent re-application.

Current schema (v001):
- `profiles` — connection profiles with JSON-encoded protocol configs
- `known_hosts` — SSH host key fingerprints with trust state
- `preferences` — key-value application preferences (JSON)
- `audit_log` — timestamped action records
- `schema_version` — applied migration tracking

---

## Import / Export

### JSON Export

```bash
# Via the application UI: Profiles → Export
```

The export format is a JSON array of profile objects. Each profile includes connection settings, terminal preferences, and protocol-specific configuration — but **not** secrets (passwords, passphrases) by default.

### Import Strategies

| Strategy    | Behaviour                                                     |
|-------------|---------------------------------------------------------------|
| `skip`      | Skip profiles whose ID already exists                         |
| `overwrite` | Replace existing profiles with the imported version           |
| `duplicate` | Import as a new profile with a fresh UUID                     |

### Format

```json
{
  "version": 1,
  "exported_at": "2024-01-15T10:30:00Z",
  "profiles": [
    {
      "id": "uuid",
      "display_name": "Production Server",
      "protocol": "ssh",
      "host": "prod.example.com",
      "port": 22,
      "username": "deploy",
      "tags": ["production", "web"],
      "folder": "Servers",
      "ssh_config": { "..." : "..." }
    }
  ]
}
```

---

## Developer Notes

### Adding a New Protocol

1. Create a new package under `internal/` (e.g., `internal/mystclient/`).
2. Define a protocol-specific config struct in `internal/models/models.go`.
3. Add the protocol constant to the `Protocol` type.
4. Update `internal/models/defaults.go` with default values and a factory case in `NewProfile()`.
5. Add validation rules in `internal/validation/validation.go`.
6. Wire the new client into `internal/app/app.go` and `internal/sessions/`.
7. Update the database migration if new columns are required.

### Adding Tests

Tests live alongside the code they exercise:

```bash
go test ./internal/validation/ -v          # run validation tests
go test ./internal/secrets/ -v             # run secrets tests
go test ./... -v -count=1                  # run all tests
go test ./... -coverprofile=coverage.out   # with coverage
```

Use `_test.go` files in the same package for white-box tests, or in a `_test` package for black-box tests. Table-driven tests are preferred.

### Event System

The Wails runtime provides an event bus for asynchronous communication:

- **Backend → Frontend:** `runtime.EventsEmit(ctx, "event:name", payload)`
- **Frontend → Backend:** `runtime.EventsOn("event:name", callback)`

Key events:
- `backend:ready` — emitted when the Go backend has finished initialisation
- `session:data` — terminal output data for a specific session
- `session:state` — session state changes (connecting, connected, disconnected, error)

---

## Limitations and Future Work

### Known Limitations

- **Single-hop jump hosts only** — the jump host architecture supports chaining, but only one hop is currently implemented.
- **No SFTP** — the extension point is designed but file transfer is not yet available.
- **No port forwarding** — local/remote/dynamic forwarding is planned but not implemented.
- **No split panes** — single terminal per tab; split view is planned.
- **No theme customisation** — the dark charcoal theme is currently the only option.
- **No master password strength enforcement** — the vault accepts any master password.
- **Argon2id memory parameter is conservative** — 64 KB is low for production use; will be increased in a future release.
- **No SSH agent forwarding** — authentication uses direct key/password only.
- **Linux recursive file watch** — the development watch script may not detect all changes on Linux due to `fs.watch` limitations.

### Planned Improvements

- Increase Argon2id memory to 64 MB+ for stronger key derivation
- SFTP file browser and transfer
- Local, remote, and dynamic port forwarding
- Multi-hop jump host chains
- Split pane terminal layout
- Custom themes and colour schemes
- OpenSSH `~/.ssh/config` import
- Session recording and playback
- SSH agent forwarding
- Encrypted profile export/import
- Master password strength meter
- Automatic update checking

---

## Licence

This project is licensed under the MIT Licence. See [LICENCE](LICENCE) for details.

---

<sub>Built with ❤ using [Wails](https://wails.io/), [xterm.js](https://xtermjs.org/), and [Go](https://go.dev/).</sub>
