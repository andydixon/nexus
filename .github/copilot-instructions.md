# Copilot Instructions — Nexus Terminal

Nexus Terminal is a native desktop client (SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub, local shell) written in Rust with an eframe/egui UI. All the code lives in the Cargo workspace under `rust/`.

## Build & Test

```bash
cd rust
cargo run --release                                     # the app (binary `nexus-terminal`)
cargo test --workspace                                  # unit and integration tests
cargo clippy --workspace --all-targets -- -D warnings   # must be clean
cargo fmt --all --check                                 # must be clean

# Single test
cargo test -p nexus-core --lib storage::
cargo test -p nexus-core --test ssh_integration
```

CI (`.github/workflows/ci.yml`) runs `cargo test --locked`, `cargo fmt --check` and `cargo clippy --all-targets -D warnings` on Linux, macOS and Windows. The toolchain is pinned in `rust/rust-toolchain.toml`. Platform prerequisites and release packaging are in `docs/build-and-release.md`.

## Architecture

The project is engine-first: a UI-free core crate, and the egui app is one client of it.

```
rust/
├── core/   nexus-core: storage, secrets, models, protocol clients, session Manager, Service. No egui.
│   ├── migrations/       numbered SQL migrations, embedded with include_str!
│   └── tests/            sessions.rs, ssh_integration.rs, fixtures/
└── app/    nexus: the egui shell (binary `nexus-terminal`)
```

**Core (`rust/core/src/`):**
- `service.rs` — `Service`, the app-facing API: profiles, connect/reconnect, credentials, known hosts, preferences, snippets, import/export, SSH keys, SFTP, updates. File dialogs live in the UI; methods here take paths.
- `sessions.rs` — `Manager`: concurrent sessions, auto-reconnect with backoff, login scripts, broadcast writes, recording hooks, secret resolution (saved / session-only / ask every time), host-key checks. It reports through one `Event` enum (`Data`, `State`, `Closed`, `RawData`, `Irc`, `Github*`, `Forward*`, `Recording*`, `SftpProgress`, `SecretPrompt`, `HostKeyVerify`, `Vnc*`).
- `clients/` — one module per protocol (`ssh`, `sftp`, `telnet`, `raw`, `serial`, `local`, `irc`, `github`, `vnc`). Read the conventions at the top of `clients/mod.rs` before touching one: `connect()` blocks its (worker) thread and returns once usable; the client then owns its reader thread(s) and reports through `Callbacks` (data, fatal error, close exactly once); `write`/`resize`/`close` are callable from any thread and `close` is idempotent. russh-based clients use the shared tokio runtime from `clients::rt()`; the rest use plain threads.
- `storage.rs` (SQLite via rusqlite, WAL), `secrets.rs` (keychain + vault), `config.rs` (data directories and Alchementary Link migration), `models.rs`, `validation.rs`, `exportimport.rs`, `recording.rs` (asciicast v2), `sshconfig.rs`, `updater.rs`, `ircformat.rs`, `terminalenv.rs`, `keyfiles.rs`.

**App (`rust/app/src/`):**
- `main.rs` parses flags, opens the `Service` and starts eframe. `app.rs` owns `App`: it installs an event sink that forwards `Event`s over a channel and calls `request_repaint`, drains them each frame (`on_event`), handles `Request`s, the keyboard shortcuts (`shortcuts()`), and lays out the frame.
- `state.rs` — `Shared` (service handle, cached profiles and preferences, tabs, panes, toasts) and the `Request` queue features use to ask the main window to act.
- `views.rs` — `View::{Terminal, Raw, Irc, Github, Vnc}`, one per pane.
- `terminal.rs` (`TermView`: alacritty_terminal drawn with egui's painter), `tabs.rs` (tab strip, workspace, find bar, overlays), `splits.rs` (split tree), `sidebar.rs`, `chrome.rs` (title bar, status bar, command palette `Cmd` enum, prompts, dialogs, toasts).
- Feature windows: `profile_editor.rs`, `quick_connect.rs`, `settings.rs` (including the Help topics), `ssh_keys.rs`, `transfer.rs` (import/export), `sftp.rs`, `forwards.rs`, `snippets.rs`, `triggers.rs`, `recordings.rs`.
- Protocol views: `raw_view.rs`, `irc_view.rs`, `github_view.rs`, `vnc_view.rs`.

**Flow:** UI → `Service` / `Manager` method (plain Rust call) → protocol client on a worker thread → `Callbacks` → `Manager` emits `Event` → app channel → `App::on_event` on the next frame. Credential and host-key questions are `Event::SecretPrompt` / `Event::HostKeyVerify` carrying an `mpsc::Sender` reply; the connecting thread blocks on the receiver, and dropping the sender (closing the prompt or the session) cancels it.

## Conventions

- **British English** in comments, docs and UI strings (colour, initialise, behaviour, favourite).
- **Never block the UI thread.** Anything that touches the network, the keychain, `ssh-keygen` or slow disk work runs on a worker thread: `Shared::spawn` (errors become toasts), `settings::Job` (poll a result each frame), or a module's own inbox. Call `ctx.request_repaint()` when the work finishes. Quick SQLite calls through `Service` are fine inline.
- **Colours come from `Tokens`** (`theme.rs`, `Tokens::get(ctx)`): `text`, `text_dim`, `text_faint`, `accent`, `success`, `warning`, `danger`, `field`, `card`, `dock`, … Don't hard-code colours; all three app themes (Pro, Midnight, Light) must work. Protocol colours and badges come from `views::protocol_color` / `protocol_badge`; terminal palettes from `terminal_themes.rs`.
- **Icons are Phosphor** (`egui_phosphor::regular as icon`), usually as `format!("{}  Label", icon::X)`. Reuse the helpers in `widgets.rs` (`primary`, `button`, `icon_button`, `hint`, `empty`, `chip`, `section`, `kv`) and `chrome::modal` for modal dialogs.
- **Shortcut labels** go through `state::keys("Mod+Shift+K")`, which renders ⌘ on macOS and Ctrl elsewhere. A new shortcut needs an entry in `App::shortcuts()`, the command palette (`chrome::commands`), the menu, the Help "Keyboard Shortcuts" topic and the README table.
- **Formatting:** `rustfmt.toml` sets `max_width = 160` with `use_small_heuristics = "Max"`, so long single-line expressions are normal. Run `cargo fmt --all` rather than wrapping by hand.
- Errors are `nexus_core::Result<T>` (`Box<dyn Error + Send + Sync>`); use `?` and `format!` messages that read well in a toast.
- Protocol configs are `Option<…Config>` fields on `Profile` (`ssh`, `telnet`, `raw`, `github`, `vnc`, `irc`, `serial`), stored as JSON columns; `Profile::new(protocol)` fills the defaults, and the structs use `#[serde(default)]` so older rows still load.
- A `ponytail:` comment marks a deliberate simplification and names its limit; keep them accurate when you change that code.

**Adding a protocol** touches: `models.rs` (`Protocol`, config struct, defaults, labels), `validation.rs`, a new migration (CHECK constraint and config column) plus the column lists in `storage.rs`, a client in `clients/`, the `Manager` connect/close/write paths in `sessions.rs`, the profile editor section, Quick Connect if relevant, a view (or `TermView` for terminal protocols), `views::protocol_badge`/`protocol_color`, the palette, and a Help topic.

## Data compatibility (hard rules)

Existing users upgrade in place, and their data came from the Go app first. Nexus Terminal must keep reading it.

- **Never change the database, vault, export or recording formats incompatibly.** The vault (`vault/vault.json`: Argon2id t=3, m=128 MiB, 4 lanes; AES-256-GCM per secret), the export envelope (`[version][32-byte salt][12-byte nonce][ciphertext]`, `.nexus`; `.link` must still import) and asciicast v2 recordings are fixed.
- **Fixtures** generated by the old Go app live in `rust/core/tests/fixtures/` (`link.db`, `vault/`, `profiles.link`, `recordings/`). Tests in `storage.rs`, `secrets.rs`, `exportimport.rs` and `recording.rs` read them. Don't regenerate or edit them to make a test pass.
- **Schema changes are new SQL files** in `rust/core/migrations/` (`008_….sql`, …), added to `MIGRATIONS` in `storage.rs`. Each ends with `INSERT INTO schema_version (version, applied_at) VALUES (N, datetime('now'));`. Never edit an applied migration. SQLite has no `ALTER COLUMN`; use the recreate-table pattern.
- **Keychain** entries are stored under the service `NexusTerminal`; reads fall back to `AlchementaryLink` and copy forward. Don't change key names (`profile:<id>:<kind>`).
- **Data directory** names (`NexusTerminal`, `nexus.db`, `vault/`, `recordings/`) and the first-run migration from `AlchementaryLink/` in `config.rs` are user-facing; keep them stable.

## Testing tips

- **Never point a test or manual run at your real data.** Use `--data-dir DIR` (a temporary directory) for manual runs; tests create their own directory under `std::env::temp_dir()`, as the existing ones do.
- **App flags** (`rust/app/src/main.rs`):
  - `--data-dir DIR` — use another data directory.
  - `--screenshot out.png` — render, save a PNG and exit (works under `xvfb-run`); combine with `--size WxH` and `--theme pro|midnight|light`.
  - `--open WINDOW` — open a window at startup: `editor[:protocol|id]`, `quick`, `settings[:terminal|security|knownhosts|help|about]`, `keys`, `snippets`, `triggers`, `recordings`, `palette`, `export`, `import`.
  - `--connect NAME` — connect a saved profile by display name or id.
  - `--run-local CMD` — open a local terminal and type `CMD` into it once connected.

  For example: `cargo run -- --data-dir /tmp/nx --open settings:help --screenshot /tmp/help.png`.
- **SSH integration tests** (`core/tests/ssh_integration.rs`) start a throwaway `sshd` as the current user on a random port and skip themselves when OpenSSH isn't installed. They **must never open an interactive login shell** — the user's shell profile can auto-attach tmux/byobu to a real session. Set the profile's startup command to `/bin/sh -i`, as the existing `profile()` helper does.
- `core/tests/sessions.rs` drives the `Manager` end to end against local TCP listeners; follow its `manager()` and `wait()` helpers for event-driven tests.
- UI logic that can be tested without a window (parsers, fuzzy matching, split trees, snippet variables, trigger matching) has `#[cfg(test)]` unit tests next to it; add one with any non-trivial change.
