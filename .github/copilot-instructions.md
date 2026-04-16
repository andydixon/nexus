# Copilot Instructions — Nexus Terminal

## Build & Test

```bash
make deps              # go mod download + npm install
make build             # production build → build/bin/nexus-terminal.app
make dev               # wails dev with hot reload
make test              # go test ./... -v -count=1
make lint              # golangci-lint run ./...

# Single package test
go test ./internal/storage/ -v -count=1 -run TestProfileCRUD
```

The frontend has no separate build step — `npm run build` (called by Wails) copies xterm vendor libs from `node_modules` into `frontend/src/js/lib/` and `frontend/src/css/lib/`. All frontend assets are embedded at compile time via `frontend/embed.go`.

## Architecture

Wails v2 desktop app: Go backend + HTML/CSS/JS frontend rendered in a native webview (WKWebView on macOS).

**Go backend** (`internal/`):
- `app/` — lifecycle bootstrap: opens SQLite, inits secret stores, wires everything together
- `api/` — single `Service` struct bound to Wails; every public method is callable from JS as `window.go.api.Service.MethodName()`
- `sessions/` — manages concurrent protocol sessions; owns SSH/Telnet/RAW/GitHub client lifecycles; handles auto-reconnect, login scripts, broadcast
- `storage/` — SQLite repository (WAL mode) with embedded sequential migrations in `storage/migrations/`
- `secrets/` — two-tier credential storage: OS keychain (preferred) → AES-256-GCM encrypted file vault (fallback)
- `models/` — shared domain types and defaults; protocol configs are JSON-encoded in the DB
- `sshclient/` — SSH client with agent, TOTP/2FA, and SOCKS5/HTTP proxy support
- `telnetclient/`, `rawclient/`, `githubclient/` — protocol implementations
- `sftpclient/` — SFTP file operations (browse, upload, download, rename, delete)
- `sshconfig/` — OpenSSH `~/.ssh/config` parser and importer
- `recording/` — asciicast v2 session recording and playback
- `exportimport/` — encrypted profile bundles (.nexus format)
- `updater/` — GitHub Releases update checker
- `knownhosts/` — SSH host key management
- `validation/` — profile and protocol validation
- `audit/` — timestamped audit logging

**Frontend** (`frontend/`):
- Plain HTML/CSS/JS — no framework, no bundler, no ES modules
- `index.html` is the app shell; loads CSS then JS in strict order
- `src/js/` — 18 modules, each IIFE-wrapped, exporting to `window.NexusTerminal`
- `src/css/` — modular stylesheets; `tokens.css` defines all design tokens
- Vendor libs (xterm.js, addons) live in `src/js/lib/` and `src/css/lib/` — auto-copied, gitignored

**Go ↔ JS communication:**
- **Synchronous:** Frontend calls `window.go.api.Service.*()` (returns Promise)
- **Asynchronous:** Backend pushes events via `wailsRuntime.EventsEmit()`; frontend listens with `window.runtime.EventsOn()`
- **Bidirectional events** for secret prompts and host key verification: backend emits a request event with a `requestID`, frontend shows a modal, then emits a response event with the same ID. The Go side blocks on a channel keyed by that ID.

Key events: `session:data`, `session:state`, `session:closed`, `session:raw:data`, `session:github:repos`, `session:secret:prompt`/`session:secret:response`, `session:hostkey:verify`/`session:hostkey:response`.

## WKWebView Constraints (Critical)

The macOS webview silently blocks or breaks several standard web APIs. These constraints affect every frontend change:

1. **No `window.confirm()`, `window.prompt()`, `window.alert()`** — they either return false/null silently or do nothing. All user prompts must use the custom modal system (DOM-based backdrop + modal with `is-visible` class toggling).

2. **No ES modules** — `<script type="module">` fails silently. All JS files use plain `<script>` tags and IIFE wrappers: `(function() { ... })();`

3. **Click swallowing on text-selectable elements** — WKWebView initiates a text selection gesture on `mousedown` for `<div>` elements, which swallows the first `click` event (appears to require double-click). Fix: add `mousedown` handler with `e.preventDefault()` on any clickable non-input element (section headers, list items, etc.).

4. **Modal visibility pattern** — both `.modal-backdrop` AND `.modal` elements start with `opacity: 0; visibility: hidden` in CSS. You must add the `is-visible` class to **both** elements, not just the backdrop.

5. **Wails runtime injection** — `window.go` and `window.runtime` are injected by Wails before scripts execute. Do NOT add explicit script tags for Wails scripts. The frontend polls for their availability at startup (max 5s).

## Conventions

**Language:** All code comments in British English (colour, initialise, behaviour, etc.).

**Go patterns:**
- String-typed enums: `type Protocol string` with `const ProtocolSSH Protocol = "ssh"` etc.
- Protocol configs are pointer fields on `Profile` (`SSH *SSHConfig`, `Telnet *TelnetConfig`, etc.) — nil when not applicable
- `NewProfile(protocol)` factory in `defaults.go` populates sensible defaults per protocol
- Storage uses `nullableJSON()` helper to write nil for unused config columns
- Tests use `t.TempDir()` for isolation, `t.Fatalf()` for setup failures

**Frontend patterns:**
- Every JS file is IIFE-wrapped and exports to `window.NexusTerminal` namespace
- Script load order matters (defined in `index.html`) — utils → state → notifications → sidebar → tabs → themes → terminal → profiles → raw → github → settings → commands → secrets → recordings → sftp → splitpane → snippets → app
- DOM helpers: `createElement(tag, className, attrs)` from utils.js (supports `textContent`, `innerHTML`, `dataset`, `on*` event handlers)
- Profile editor uses `_renderSection(title, fields)` pattern with `_field(type, name, label, value, opts)` helper for form generation
- Login script editor uses `_renderLoginScriptSection(prefix, steps)` for expect-style step lists
- TOTP section uses `_renderTOTPSection(profile)` with encrypted vault storage
- `protocolColour()` returns CSS class names like `proto-ssh`; tab badges use `badge-ssh` class naming (different pattern)

**CSS patterns:**
- Design tokens in `tokens.css`: `--bg-base`, `--bg-surface`, `--bg-elevated`, `--text-primary`, `--accent`, `--protocol-ssh`, etc.
- Dark charcoal palette — not Material Design literally, but Material-inspired layering
- Protocol colours: SSH=teal `#4ecdc4`, Telnet=amber `#ffa726`, RAW=purple `#ab47bc`, GitHub=indigo `#7c8aff`

**Adding a new protocol:**
1. Add `Protocol` constant + config struct in `models/models.go`
2. Add defaults in `models/defaults.go` `NewProfile()` switch
3. Create client package in `internal/<proto>client/`
4. Add `connect<Proto>()` in `sessions/manager.go` + case in `Connect()` switch
5. Add API methods in `api/api.go` if needed
6. Add DB migration in `storage/migrations/` (update CHECK constraint, add config column)
7. Update `storage/store.go`: all SELECT/INSERT/UPDATE queries + both scan functions
8. Frontend: add DEFAULTS in `profiles.js`, render sections, collect form values, protocol tab
9. Frontend: add viewer in new JS file (IIFE-wrapped), include in `index.html`, wire in `app.js`
10. Add CSS: protocol colour token, sidebar indicator, tab badge, viewer styles

**Database migrations:**
- Numbered SQL files: `001_initial.sql`, `002_github_protocol.sql`, etc.
- Each must `INSERT INTO schema_version (version, applied_at) VALUES (N, datetime('now'))` at the end
- SQLite has no `ALTER COLUMN` — use the recreate-table pattern (CREATE new → INSERT INTO new SELECT FROM old → DROP old → ALTER TABLE RENAME)
- Every query in `store.go` that touches the profiles table must be updated when columns change (there are ~8 SELECT column lists + 2 scan functions)
