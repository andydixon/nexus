# Rust port plan: Nexus Terminal, a native egui app with no Go and no webview

Goal: rebuild Alchementary Link in Rust as **Nexus Terminal**, a single Rust application in the
style of Leviathan Desktop (`../leviathan/rust`).
- **UI:** the Wails webview (HTML/CSS/JS, xterm.js) is replaced by an **eframe/egui 0.36** shell with
  Leviathan's design system: `Tokens` themes, Inter and JetBrains Mono, Phosphor icons, a custom title
  bar, a navigator rail, document tabs, a dock, a status bar, and the Ctrl/Cmd+K palette over one `Cmd`
  enum. Full functional parity is required.
- **Backend:** the Go core is ported to Rust. At the end, Go, Node and Wails are gone from the repo.
- **Rename:** back to **Nexus Terminal** (§7). Existing Alchementary Link data is migrated on first
  run.
- **Removed:**
  - the hidden **Leviathan** mode (`internal/leviathan/*`, `leviathan.js`, the Konami toggle). It now lives in Leviathan Desktop.
  - the **VS Code bridge** (`cmd/vscode-bridge`).
- **New:** a Serial protocol (§4).
- **VNC is included, not deferred.** Rust has a usable RFB client crate (§6a).

### Crates

| Need | Crate | Notes |
|---|---|---|
| UI | `eframe`/`egui` 0.36, `egui-phosphor`, `rfd`, `image` | Same versions and features as Leviathan (`glow`, `wayland`, `x11`, `accesskit`, `persistence`) |
| Async runtime | `tokio` (multi-thread, 2 workers) | Network sessions only. The UI thread never blocks on it |
| SSH, SFTP | `russh`, `russh-sftp`, `ssh-key` | Strict-kex (Terrapin) support; client-side **agent forwarding** (`server_channel_open_agent_forward`); `direct-tcpip` for jump hosts; `tcpip-forward` for remote forwards |
| Terminal emulation | `alacritty_terminal` | VT parser, grid, scrollback, selection and regex search (the same engine Zed uses). We draw it with egui's painter (§6) |
| Local shell | `portable-pty` | `forkpty` on Unix, ConPTY on Windows |
| Serial | `serialport` | Baud/bits/parity/stop, RTS/CTS and XON/XOFF, DTR/RTS, break, port enumeration |
| IRC | `irc` (tokio, rustls) | Registration, PING, CTCP, NickServ via PRIVMSG; reconnect and channel-state persistence are ours |
| VNC | `vnc-rs` | RFB handshake, VNC auth, Raw/CopyRect/Tight/ZRLE → egui texture |
| HTTP (GitHub, updater) | `ureq` 3 | Blocking calls on `spawn_blocking` |
| SQLite | `rusqlite` (`bundled`) | SQLite compiled in, so no system library is needed |
| Vault, export | `argon2`, `aes-gcm`, `rand` | Native Argon2id at 128 MiB takes about 0.3 s |
| Keychain | `keyring` 3 | Must read entries written by go-keyring (§3) |
| TOTP | `totp-rs` | |
| JSON | `serde`, `serde_json` | Protocol configs stay JSON columns |
| Telnet, RAW, recordings | none (tokio `TcpStream`, serde) | Small ports of the Go code |

There are **no system C libraries**. SQLite is compiled in, and TLS uses rustls. The Linux binary needs
only the X11/Wayland/GL libraries that eframe loads, as Leviathan does.

## 1. Architecture

Like Leviathan, the project is engine-first: a UI-free core crate, and the egui app is one client of it.

```
rust/
├── core/    nexus-core: storage, secrets, models, protocol clients, SessionManager, Service. No egui.
└── app/     nexus: the egui shell
```

- **`Service`** keeps the **same method names** as today's `api.Service` (`connect_session`,
  `sftp_list`, …), and **`Event`** is one enum whose variants match today's event names
  (`SessionData`, `SessionState`, `SecretPrompt`, `HostKeyVerify`, …). The checklist in §10 refers to
  these names. They are now plain Rust calls with no serialisation.
- **The UI thread** runs egui and owns all UI state. Each frame it drains a
  `crossbeam_channel::Receiver<Event>`. `Service` methods that touch SQLite are fast and run inline
  (rusqlite behind a `Mutex<Connection>`, in WAL mode).
- **One tokio task per session** (SSH, Telnet, RAW, IRC, GitHub refresh, VNC). Serial and local-shell
  sessions each get a blocking thread (`serialport` and `portable-pty` have blocking APIs). Each session
  has an `mpsc` outbound queue (keystrokes, resize, break, DTR/RTS). Inbound bytes are batched per read
  and sent as one `Event::SessionData`, followed by `ctx.request_repaint()`. Sending raw bytes instead
  of base64 JSON is the main performance gain over the Wails app.
- **Credential prompts:** when a session task needs a secret or a host-key decision while connecting,
  it sends `Event::SecretPrompt { reply: oneshot::Sender }` and awaits the answer. Closing the session
  drops the receiver, which cancels the prompt. This is the same contract as the Go channel.
- **Long jobs** (SFTP transfers, archive download, `test_connection`, key generation) are tokio tasks
  or `spawn_blocking`, and report progress through events.

## 2. SSH (russh)

| Feature | Approach |
|---|---|
| Interactive shell | `channel.request_pty(term type from the profile)`, env vars, `request_shell`, `window_change` |
| Auth | password, public key (files, with passphrase), agent (`SSH_AUTH_SOCK`; Pageant and the Windows OpenSSH pipe through russh's agent client), keyboard-interactive, with TOTP answering the prompts |
| Host keys | `Handler::check_server_key` against **our** SQLite known-hosts table (strict/ask/accept-new/insecure). Never `~/.ssh/known_hosts` |
| Keepalive | russh `keepalive_interval`/`keepalive_max` from the profile |
| Jump hosts | Hop N opens `channel_open_direct_tcpip`, and its stream is passed to hop N+1 as the transport (`connect_stream`). No loopback socket is needed |
| SOCKS5 and HTTP proxies | `tokio-socks` for SOCKS5; HTTP CONNECT is about 40 lines. The resulting stream goes to `connect_stream` |
| Local forwards | A `TcpListener` per forward → `channel_open_direct_tcpip` → `copy_bidirectional` |
| Remote forwards | `tcpip_forward`, plus `Handler::server_channel_open_forwarded_tcpip` |
| Agent forwarding | `agent_forward` request plus `server_channel_open_agent_forward`, bridged to the local agent. ⚠ Prove this in the phase 0 spike |
| SFTP | `russh-sftp` on a second channel: list/stat/mkdir/rename/remove/upload/download, with progress events |
| Key manager | `ssh-key`: generate ed25519, RSA 2048/4096 and ECDSA; write OpenSSH private keys (encrypted with bcrypt-pbkdf + aes256-ctr); public-key export; `file_is_private_key`. The key comment becomes `name@nexus-terminal` |
| `~/.ssh/config` import | Port of `sshconfig` (~300 lines of Go) |
| Compression | russh `zlib@openssh.com` when the profile enables it |

The fallback, if a russh gap blocks the spike, is `ssh2` (libssh2 with vendored OpenSSL). It has no
API for accepting agent-forwarding channels, and it brings C back into the build.

## 3. Porting the Go backend

Sizes are Go lines, excluding tests. The Go tree is 41k lines. Leviathan (25.5k), VNC's Go side and the bridge are not
ported, which leaves about 15k lines to port.

| Go package | Lines | Rust replacement |
|---|---|---|
| `storage` + `migrations/*.sql` | 949 | rusqlite. **Embed the same `.sql` files** with `include_str!` and keep the same migrations table, so existing databases open unchanged |
| `models`, `validation`, `preferences`, `audit`, `knownhosts`, `config` | ~1.5k | Plain modules. Protocol configs stay JSON columns, read as `serde_json::Value` or, where the UI edits them, as `#[derive(Deserialize)]` structs with `#[serde(default)]`. Leviathan preferences (`leviathanEnabled`, …) are ignored on read |
| `secrets` (vault + keychain) | 460 | Vault: `argon2` + `aes-gcm`. Keychain: `keyring`, with explicit target names to match go-keyring (see below) |
| `exportimport` | 209 | Same envelope and the same crypto; `.nexus` extension (§7) |
| `sessions` + `api` | ~6k | `SessionManager` + `Service` (§1): reconnect with attempts and backoff, login scripts, broadcast, recording hooks, secret resolution (saved / session-only / ask every time). The Leviathan methods are dropped |
| `sshclient`, `sftpclient`, `sshconfig`, `terminalenv` | ~1.7k | §2 |
| `telnetclient` | 603 | tokio `TcpStream` and the option state machine (echo, SGA, NAWS, TTYPE, BRK) |
| `rawclient` | 561 | tokio; history, capture, and hex/escaped encoders |
| `ircclient` (on `girc`) | 833 | `irc` crate plus our own state: rooms, NAMES, LIST, topic, reconnect, channel-state persistence (migration 006) |
| `githubclient` | 408 | `ureq` + serde; periodic refresh; archive download |
| `localshell` | 294 | `portable-pty` |
| `recording` | 538 | asciicast v2 writer and reader; the files are the same |
| `updater` | 303 | `ureq`; same manifest format; URLs per §7 |
| `vncclient` | 340 | §6a |
| `leviathan/*` | 25.5k | **Deleted.** |
| `cmd/vscode-bridge` | 357 | **Deleted.** |

### Data compatibility (hard requirement)

Existing users upgrade in place. **Before any Go is deleted**, generate fixtures from the Go code and
commit them under `rust/core/tests/fixtures/`:
- a database with every protocol, snippets and known hosts, migrated through 006
- a vault file with a known password
- `.link` exports
- recordings
- keychain entries written by go-keyring, on each OS in CI

Rust must read every fixture byte for byte. The formats are:
- **Vault:** JSON `{version, salt(b64, 32 bytes), secrets: key → b64(nonce12 ‖ ciphertext ‖ tag)}`;
  Argon2id with t=3, m=128 MiB, lanes=4, and a 32-byte key.
- **Export:** `[1 byte version][32 bytes salt][12 bytes nonce][ciphertext]`, with the Argon2 parameters
  from `exportimport.go`. Only the extension changes (`.link` → `.nexus`).
- **Keychain:** ⚠ go-keyring and `keyring` don't store entries the same way by default:
  - **macOS:** both use a generic password stored under service and account, so they match.
  - **Windows:** go-keyring's target is `service:user`, but `keyring` defaults to `user.service`. Use `Entry::new_with_target`.
  - **Linux:** both use the Secret Service, but with different attribute sets. Search by `service` + `username`.

  The phase 0 spike proves all three against the fixtures.

## 4. New protocol: Serial

- **Library:** `serialport`.
- **Migration:** `007_serial_protocol.sql` adds `serial` to the protocol CHECK, plus a `serial_config` JSON column.
- **Config fields and defaults:** `baudRate 115200, dataBits 8, parity none|odd|even, stopBits 1|2,
  flowControl none|xonxoff|rtscts, lineEnding cr|lf|crlf, localEcho false, dtrOnConnect true,
  rtsOnConnect true, encoding, autoReconnect true, maxReconnectTries 5`.
  Mark/space parity and 1.5 stop bits are left out because `serialport` doesn't support them. If they're needed, add them
  through `termios`/`DCB` on the raw handle (about 30 lines per OS).
- **Device path:** stored in `host`, with `port` 0, so the sidebar, search and palette work unchanged.
- **Session:** a terminal session through `TermView`, so recording, triggers, broadcast, find, snippets
  and split panes all work. Unplugging the adapter sets the session to disconnected, and auto-reconnect re-opens the path.
  New service methods: `list_serial_ports`, `set_serial_lines(dtr, rts)`, `send_break`.
- **UI:**
  - an editor page (device combo with refresh, line settings, behaviour settings)
  - a Quick Connect entry
  - Send Break and DTR/RTS toggles on the tab
  - a badge colour
  - error hints: the `dialout`/`uucp` group on Linux, `/dev/cu.*` on macOS, and busy ports
  - a Help topic

## 5. Toolchain and platforms

- **Toolchain:** pin the same `rust-toolchain.toml` as Leviathan (`1.99.0`), edition 2024.
- **Targets:** keep the current release matrix in full: darwin amd64 and arm64 (one universal `.app`),
  linux amd64 and arm64, and windows amd64 and arm64.
- **UI code from Leviathan:** copy these files into `app/src/` once, then let them evolve
  independently. There is no dependency on the Leviathan repo:
  - `theme.rs` (Tokens, fonts; 278 lines)
  - `widgets.rs` (198 lines)
  - the title bar, command palette and frame layout from `app.rs`
  - `hexview.rs`, for the RAW hex view
  - bundled fonts
  - the `packaging/` layout

## 5a. UI mapping: webview module → egui

egui is immediate mode. Lists are virtualised with `ScrollArea::show_rows`, so the 1000/200/5000-row caps go away.
Terminal views are repainted only when their session receives data, and for input and cursor blink.

| Current (JS) | egui replacement |
|---|---|
| `index.html` grid layout | Leviathan's frame: custom title bar (Nexus mark, menus, active-session title, ⌘K launcher, window controls); a **navigator rail** holding the connection sidebar (resizable 180–500); a central **tab strip + workspace** with an empty state; a right **dock** for SFTP and the profile editor; a **status bar** |
| Shortcuts (`app.js`) | The `Cmd` enum plus a shortcut table. `Modifiers::COMMAND` maps to ⌘ on macOS and Ctrl elsewhere. The palette enumerates every `Cmd` |
| `tabs.js` | A painted tab strip (Leviathan's tab style) with a protocol badge, status dot, activity marker, pin and 📁; pinned tabs first; drag to reorder; scroll with edge fades; context menu; tooltip with "Connected since"; close confirmation |
| `terminal.js` + xterm.js | `TermView` (§6) |
| `splitpane.js` | A binary tree of `Pane::{Leaf(session), Split{dir, ratio, a, b}}` laid out by rect, with draggable dividers (minimum 100 px). A placeholder leaf shows the connection picker. Child sessions get no tab |
| `sidebar.js` | Collapsing sections (Local Machine, ★ Favourites, Recent 5, folders, Ungrouped), a protocol dot and ★, Ctrl/Cmd-click multi-select, a context menu, a bulk-action bar, and fuzzy search |
| `profiles.js` drawer | A dock panel: protocol selector plus one section per protocol. List editors (jump hosts, forwards, RAW presets, login script) are `egui::Grid` rows with ▲▼✕ |
| `settings.js` | Settings window with tabs: General, Terminal, Security, Known Hosts, Help, About |
| `secrets.js` | Modal windows: secret prompt, master password (strength meter using the same formula), host-key dialog |
| `sshkeys.js` | SSH Keys window: list, generate, copy public key, delete |
| `sftp.js` | A dock panel per session: breadcrumb, table, bookmarks, and a transfer queue with progress bars. Dialogs via `rfd` |
| Drop files on terminal | `ctx.input(\|i\| i.raw.dropped_files)` gives real paths. The File Drop dialog stays. Upload by path, so the 768 KiB base64 chunking goes away |
| `raw.js` | Text view, hex view (the copied `hexview`, read-only), or split; a send bar (format combo, input, quick-byte buttons, presets); Save via `rfd` |
| `irc.js` | Room tabs + a read-only `TermView` fed by the ported mIRC→ANSI formatter + a composer + side lists (rooms, room list, users) |
| `github.js` | Painted repo cards under group headers; a filter; Refresh; ZIP and Visit buttons |
| `vnc.js` | `VncView` (§6a) |
| `commands.js` | The copied palette widget, with Nexus's sections and a port of `fuzzyMatch` |
| `snippets.js`, `triggers.js`, `recordings.js` | Windows. Playback uses its own `TermView`, with a scrubber, speed control and autoplay |
| `notifications.js` | A toast stack painted top-right in an `egui::Area`, auto-dismissed after 4 s (6 s for errors). About 60 lines; no crate needed |
| `themes.js` | App chrome uses `Tokens` (Pro/Midnight/Light). `terminal_themes.rs` is a const table of the 11 terminal palettes (charcoal … rose-pine) |
| Help tab HTML | The help topics minus Leviathan, as `include_str!` text with simple heading and paragraph styling |
| `BrowserOpenURL`, clipboard, fullscreen | `ctx.open_url`, `ctx.copy_text` and egui paste events, `ViewportCommand::Fullscreen` |

**App theme:** every widget is drawn by egui, so the theme applies everywhere.

## 6. TermView (the critical path)

- **Model:** one `alacritty_terminal::Term` per view, behind a `FairMutex`, fed by
  `vte::ansi::Processor`. Session data events are fed straight in as bytes; UTF-8 is the parser's job.
- **Drawing:** for each visible row, group cells into runs with the same attributes, then draw one
  `painter.text` call per run, plus background rectangles, cursor, selection and search highlights.
  Cache each row's layout by a hash of its content. Use the profile's font, falling back to the
  preferences (JetBrains Mono is bundled). Wide and emoji cells take the terminal's cell width.
- **Scrollback:** `Config.scrolling_history`, from the profile, falling back to `defaultScrollback`
  (0–100000).
- **Input:**
  - egui `Key` events map to escape sequences, respecting the terminal mode (application cursor keys, keypad).
  - `Event::Text` and `Event::Ime(Commit)` send the text, so IME input works.
  - Paste is bracketed when the application enables it.
  - Output goes to `write_session` and to the broadcast targets.
- **Mouse:**
  - When the application enables mouse reporting, send SGR/X10 reports.
  - Otherwise: drag selects, double-click selects a word, triple-click selects a line.
  - The wheel scrolls the scrollback. On the alternate screen it sends ↑/↓ once per notch (the existing tmux behaviour).
  - Ctrl/Cmd-click on a URL opens it.
- **Behaviours to keep:**
  - **Clipboard:** Ctrl/Cmd+C copies when there is a selection and otherwise sends SIGINT; Ctrl/Cmd+V pastes.
  - **Resize:** debounced; recompute columns and rows, then `term.resize` and `resize_session`.
  - **Right-click menu:** all 11 items.
  - **Find bar:** alacritty `RegexSearch`, with case and regex options, "i of N", Enter and Shift+Enter.
  - **Overlays:** the disconnect overlay with Reconnect, and the local shell's "[Exited - press enter to restart]".
  - **Cursor:** style (block, underline or bar) and blink.
- **Performance target:** `cat` of a 50 MB file stays responsive, at parity with or better than xterm.js.
  Parsing runs on the UI thread inside the event drain. If profiling shows that's too slow, feed `Term` on the session
  thread and keep only drawing on the UI thread (the `FairMutex` already allows this).
- **Reference:** `egui_term` (alacritty_terminal + egui) is prior art. Read it, but don't depend on it,
  because it lags egui releases.

## 6a. VNC

- `vnc-rs` on a tokio task, with an optional SSH tunnel through §2's `direct-tcpip`.
- Decoded rectangles are applied to an RGBA buffer, and dirty frames update a `TextureHandle`, scaled to fit.
- Input: pointer → `PointerEvent`; keys → X11 keysyms (port the keysym map from `vnc.js`).
- If `vnc-rs` doesn't work with the servers users run, the fallback is our own RFB client: Raw, CopyRect and ZRLE
  in about 600 lines, plus Tight's JPEG via the `image` crate.

## 7. Rename to Nexus Terminal and migrate data

The rebrand to Alchementary Link (commit `1a380a0`) renamed every path and **migrated nothing**. This rename
undoes it and migrates the data.

| Item | Alchementary Link (now) | Nexus Terminal |
|---|---|---|
| Product and window title | Alchementary Link | Nexus Terminal |
| Binary, bundle and app id | `AlchementaryLink` | `NexusTerminal` / `nexus-terminal` |
| Config dir | `…/AlchementaryLink` | `…/NexusTerminal` |
| Database and log | `link.db`, `link.log` | `nexus.db`, `nexus.log` |
| Keychain service | `AlchementaryLink` | `NexusTerminal` |
| Export file | `.link`, "Link Export" | `.nexus`, "Nexus Export" (import accepts both) |
| Updater User-Agent and GitHub fallback | `AlchementaryLink-UpdateChecker`, `alchementary-link` | `NexusTerminal-UpdateChecker`, `nexus-terminal` |
| Update and release URLs | `alchementary.co.uk/download/link/…` | ⚠ **Decision needed:** the new URL for the manifest and downloads |
| Icons and brand mark | `alchementary-link-*.png`, ">_ AlchementaryLink" | Restore the `nexus-terminal-*` icons from git history (pre-`1a380a0`), redrawn for the egui title bar |

**First-run migration** (in `nexus-core`, before the database is opened):
1. If `NexusTerminal/nexus.db` exists, use it. This covers users from before the rebrand, whose data was never moved.
2. Otherwise, if `AlchementaryLink/link.db` exists, **copy** (don't move) `link.db` → `nexus.db`, plus the
   `vault/` directory and the recordings, into `NexusTerminal/`. Write a `migrated-from` marker.
   The old directory is left untouched, so the Wails build still works if a user rolls back.
3. **Keychain:** when a read under `NexusTerminal` misses, try `AlchementaryLink`. If it's found there, write it under
   `NexusTerminal`. Old entries are not deleted.
4. If both directories hold a database (a user who ran both builds), Nexus wins, and the toast says so.

The old update channel must point existing Alchementary Link installs at the first Nexus release. That's one
manifest entry on the update server, so existing users get the Rust build.

## 8. Phases and milestones

Each phase ends with a runnable build and its slice of the §10 checklist ticked off. **The Wails app keeps
shipping until phase 7**, and Go is deleted only then.

| # | Phase | Delivers | Milestone |
|---|---|---|---|
| 0 | Fixtures and spikes | The Go-generated fixtures (§3). Spikes, each answering one question: (a) a russh PTY shell against OpenSSH with an ed25519 key, strict-kex, a jump host and **agent forwarding**; (b) `TermView` on alacritty_terminal with vim, tmux, htop, IME input, and `cat` of 50 MB; (c) `keyring` reads go-keyring entries on all three OSes; (d) `vnc-rs` against TigerVNC and macOS Screen Sharing | Go/no-go on each risk in §2, §3 and §6a |
| 1 | Core foundation | `nexus-core`: storage and migrations, models, validation, preferences, audit, known hosts, vault, keychain, export/import, `Service`/`Event`, and the **§7 migration**. App skeleton in Leviathan's frame: rail, sidebar, tabs, status bar, toasts, and the secret, master-password and host-key modals | Opens an existing Alchementary Link user's data as Nexus; profiles listed; lock and unlock |
| 2 | Terminal | `TermView` (§6), terminal themes, local shell, Telnet, **Serial**, state handling (reconnect toast, error classifier, "Connection Already Open") | A usable local, Telnet and serial terminal |
| 3 | SSH | §2 in full, plus SFTP and the key manager | Usable as a daily SSH client |
| 4 | Forms | Profile editor (every field in §10.3), Quick Connect, Settings, Known Hosts, SSH Keys, encrypted export/import, update check | All profile CRUD and settings |
| 5 | Power features | Split panes, broadcast, snippets, triggers, recording and playback, port-forward dialog, SFTP dock and file drop, palette, shortcuts, fullscreen | Every terminal-session feature |
| 6 | RAW, IRC, GitHub, VNC | Rust clients and their views | Every protocol |
| 7 | Ship; delete Go | Packaging and CI (§9). Delete `go.mod`, `go.sum`, `main.go`, `internal/`, `cmd/`, `frontend/`, `wails.json`, the Wails `build/` assets, and the Go build scripts (`build*.sh`, `build.ps1`, `Makefile`, `build-all.sh`). Rewrite the docs, README and `.github/copilot-instructions.md` as Nexus Terminal. Publish the update-server entry that moves users across (§7) | Nexus Terminal released from a tag; no Go or JS left |

## 9. Build and release

- **Build:** `cargo build --release --target …`.
  - Version: `CARGO_PKG_VERSION`, set by `release.sh` in `Cargo.toml`.
  - Update URL: `option_env!("NEXUS_UPDATE_URL")`, defaulting to the URL decided in §7. This replaces Go's `-ldflags -X`.
- **Packaging:** Leviathan's `packaging/` layout, renamed:
  - **macOS:** a universal `NexusTerminal.app` via `build-app.sh`, with Developer ID signing and notarisation.
  - **Windows:** an `.exe` with an icon and version info from `build.rs`/`winresource`, using the GUI subsystem.
  - **Linux:** a tarball, plus `install.sh` and a `.desktop` file.
- **CI:** rewrite `release-builds.yml` with `dtolnay/rust-toolchain` and a native runner per OS and arch.
  Drop Go, Node, Wails and `libwebkit2gtk`. Run `cargo test` on every platform.
- **Unchanged:** the update server (`updates/index.php`) and the manifest format. `release.sh` gets the new names.

## 10. Parity checklist

This is the acceptance list, built from a full read of `frontend/src/js/*`. A feature is done when it works in the native build.

### 10.1 Shell, tabs, sessions
- [ ] Layout: sidebar, tab strip (+ New, Broadcast: left-click toggles, right-click picks targets), workspace empty state (New Connection, Quick Connect), status bar
- [ ] Sidebar footer: New, ⌘K, Import, Export, Recordings, Settings
- [ ] Shortcuts: ⌘K, ⌘N, ⌘W, ⌘Tab/⇧⌘Tab (wrap), ⌘1–9, ⌘, (Settings), ⌘F, ⇧⌘D, ⇧⌘E, ⇧⌘R, ⇧⌘B, ⇧⌘S, ⇧⌘Q, F11; Esc closes palette and exits fullscreen
- [ ] Events handled: `session:data`, `session:state` (with attempt/maxRetries/delaySec toast), `session:closed`, `session:raw:data`, `session:github:*`, `session:irc:event`, `session:forward:started/stopped/error` toasts, `session:recording:started/stopped`, `session:sftp:progress`, `session:secret:prompt`, `session:hostkey:verify`, `app:open-about`
- [ ] Connect flow: "Connection Already Open" (Cancel / New Connection / Use Existing), "Connecting to X…" toast, refit and resize at 50/300/800 ms after connect
- [ ] Error classifier: auth errors → Close / Edit Profile / Retry; host-key errors also offer Known Hosts
- [ ] Tabs:
  - [ ] Visuals: protocol badge, status dot, activity marker cleared on activate, pinned-first order
  - [ ] Interaction: drag reorder; wheel scroll with overflow fades; scroll the active tab into view; hover hint (name + "Connected since"); close activates the nearest neighbour
  - [ ] Context menu: Rename, Duplicate, Pin/Unpin, Move Left/Right, Reconnect, Close
  - [ ] Close confirm when the pref is on (not for GitHub tabs)
- [ ] Session close is idempotent and cleans up splits, SFTP, triggers, recordings and viewers
- [ ] Local shell: `ConnectLocal`, exit and restart-on-Enter
- [ ] Quick Connect: SSH/Telnet/RAW/IRC/Serial, default ports, username (SSH/IRC), nick (IRC), Enter/Esc
- [ ] Broadcast: toggle toast, target picker with Select All (all selected means null, i.e. all sessions)
- [ ] Status bar latency: <100 good, <250 warn, else bad; stops on error
- [ ] Toasts: 4 types, × dismiss, 4 s / 6 s auto-dismiss

### 10.2 Terminal
- [ ] §6 in full; 11 themes; switching theme updates every open terminal
- [ ] Find bar (case, regex, count, next/prev, clear highlights)
- [ ] Context menu: all 11 items, with the shortcut labels for the platform
- [ ] File drop: Paste contents / Upload to current directory (SSH only) via the directory picker (breadcrumb, filter, Up, Refresh, READ ONLY / NO READ badges, "Use this folder" disabled when the directory isn't writable, keyboard navigation)
- [ ] Port Forwards dialog: list, Stop, add (L/R, bind, dest, label)
- [ ] Split panes: split the focused pane right/down, placeholder picker (Local Machine + profiles), divider drag with live refit, per-pane ✕ promotes the sibling, late connects to a closed pane are disconnected, closing the tab closes child sessions
- [ ] Recording toggle and a visible tab indicator

### 10.3 Profiles, sidebar, settings, security
- [ ] Sidebar:
  - [ ] Sections: Local Machine, Favourites, Recent 5, folders (sorted), Ungrouped / "All Connections"; collapsible
  - [ ] Search: fuzzy over name, host, tags and protocol; empty states
  - [ ] Mouse: click connects; Ctrl/Cmd-click multi-selects; double-click edits
  - [ ] Context menu: Connect, Edit, Duplicate, Favourite toggle, Delete (confirm)
  - [ ] Bulk bar: Connect all, Disconnect (confirm), Tags add/replace, Move Group, Clear
- [ ] Profile editor, common:
  - [ ] Protocol switch keeps common fields
  - [ ] Validation: host, port, IRC nick and rooms, IRC SSH tunnel
  - [ ] Buttons: Test (Reachable — N ms), Delete (confirm), Create/Save, Save & Connect
  - [ ] General fields: Display Name, Host, Port, Username, Group (autocomplete), Description
  - [ ] Terminal fields: font, size 8–36, scrollback 0–100000, cursor style
  - [ ] Tags and Notes
- [ ] SSH fields:
  - [ ] Password mode, auth methods
  - [ ] Key path with `ListSSHKeys` autocomplete, Browse, and checks (reject `~`, `FileExists`, `FileIsPrivateKey`); passphrase mode
  - [ ] Host key policy (strict/ask/accept-new/insecure)
  - [ ] Jump hosts list, port forwards list
  - [ ] Startup command, env KEY=VALUE, login script (wait/send, timeout 1–120, reorder)
  - [ ] Agent auth and forwarding
  - [ ] TOTP: show/hide, Test (code shown 5 s), Save/Delete, placeholder when a secret is saved
  - [ ] Proxy (none/socks5/http, host, port 1080, user, password)
  - [ ] Advanced: terminalType, keepalive interval/count, compression, autoReconnect, maxReconnectTries 1–20
- [ ] Telnet fields: localEcho, lineMode, terminalType, encoding, line ending, negotiationDebug, login script, reconnect
- [ ] RAW fields: encoding, line ending, showTimestamps, showDirection, payload presets (name/payload/format), timeouts, autoReconnect
- [ ] GitHub fields: API base URL, token storage, refresh interval 60–3600
- [ ] IRC fields: nick, realName, TLS, server password and mode, NickServ, auto-join rooms, SSH tunnel, reconnect
- [ ] VNC fields: every current field, saved round-trip
- [ ] Settings:
  - [ ] General: theme, default protocol, confirm on close/disconnect, status bar, sidebar width, local shell (Browse)
  - [ ] Terminal: theme, font, size, scrollback, cursor
  - [ ] Security: init/unlock/lock
  - [ ] Known Hosts: table, Remove
  - [ ] Help: 13 topics
  - [ ] About: version, Check for Updates, View release, Import ~/.ssh/config
- [ ] Master password dialog: strength meter (Weak/Fair/Good/Strong), confirm match
- [ ] Secret prompt (Esc cancels → `cancelled:true`)
- [ ] Host key dialog (Reject / Accept & Save)
- [ ] SSH Key Manager: list (name, type, bits), copy public key, delete, generate (ed25519/rsa 2048|4096/ecdsa, passphrase)
- [ ] Encrypted export (`.nexus`; import also accepts `.link`) (profile checkboxes, password + confirm) and import (password, skip/overwrite/duplicate)
- [ ] Command palette:
  - [ ] Sections: Connections, Actions, Tabs, Application, Security (lock/unlock), Terminal (Trigger Rules; split/clear/reset when a session is active)
  - [ ] Fuzzy ranking, ↑↓ Enter Esc
- [ ] Snippets: search, CRUD (name, description, command, folder), send to the active connected session, `{{hostname|date|time|timestamp}}` built-ins plus prompted variables
- [ ] Triggers: CRUD; name, pattern, regex, case-sensitive, enabled; actions notify/highlight/both/sound; line-buffered matching with CSI stripped
- [ ] Recordings:
  - [ ] Browser: list, storage info, delete, cleanup by age (7/30/90) and size (1 GB / 5 GB)
  - [ ] Playback: play/pause, restart, scrubber seek (replay to the target), m:ss, speed 0.5–10×, autoplay

### 10.4 SFTP, RAW, IRC, GitHub, VNC
- [ ] SFTP panel:
  - [ ] Layout: toggle, resizable, breadcrumb, Go to path, New folder, Upload, Refresh, Close
  - [ ] File list: folders first, `..` entry, extension icons, size with modified-time hint, loading/empty/error + Retry
  - [ ] Actions: multi-select, Download (files), Rename (single), Delete (confirm)
  - [ ] Bookmarks
  - [ ] Transfer queue with progress; refresh when an upload is done; toast when the panel is closed
- [ ] RAW:
  - [ ] Views: Text/Hex/Split, Pause/Resume, Clear, Save capture, byte counter, 5000-entry buffer
  - [ ] Prefixes: timestamps and direction per the profile
  - [ ] Send: Text/Hex/Escaped formats, NUL/CR/LF/ETX/EOT, presets, Enter sends, local echo of the sent entry
- [ ] IRC:
  - [ ] Room tabs: Server tab, unread markers, 2000-line buffers
  - [ ] Composer: /commands via `IRCHandleInput`, Tab nick completion, click a nick to insert `@nick`
  - [ ] Room lists: join box, joined rooms with topic hint, room list (filter, click to join)
  - [ ] Users list with prefixes stripped
  - [ ] Formatting: all event types formatted as today, nick colour hash, full mIRC formatting (^B ^] ^^ ^_ ^V ^O ^C fg,bg ^D hex)
- [ ] GitHub:
  - [ ] Status line, filter, Refresh
  - [ ] Ordering: owner groups ordered by newest repo (own login first on ties)
  - [ ] Card contents: Private/Fork/Archived badges, last commit and committer, description, language colour, stars, relative pushed time
  - [ ] Actions: ZIP (save dialog → `DownloadGitHubRepoArchiveToPath`), Visit
- [ ] VNC: connect (direct or SSH-tunnelled), scaled framebuffer, mouse and keyboard, clipboard, disconnect overlay

### 10.4a Serial
- [ ] Profiles: create, edit and duplicate, and export/import round-trip including `serialConfig`
- [ ] Editor: device list and refresh
- [ ] Line settings: all baud, data-bit, parity, stop-bit and flow-control options applied
- [ ] Session behaviour:
  - [ ] data in the terminal
  - [ ] local echo, line ending and encoding
  - [ ] DTR/RTS on connect and as toggles
  - [ ] Send Break
  - [ ] XON/XOFF and RTS/CTS flow control honoured
  - [ ] unplug → disconnected overlay; auto-reconnect when the device returns
- [ ] Quick Connect Serial; recording, triggers, broadcast, snippets, find and split panes all work on serial sessions
- [ ] Permission-denied and busy-port hints

### 10.5 Rename and migration
- [ ] Every user-visible string, window title, bundle, binary and icon says Nexus Terminal
- [ ] First run migrates `AlchementaryLink/` → `NexusTerminal/`, copying the database, vault and recordings, and leaves the old directory intact
- [ ] Pre-rebrand `NexusTerminal/nexus.db` is used as-is
- [ ] Keychain entries under `AlchementaryLink` are found and re-saved under `NexusTerminal`
- [ ] Import accepts `.link` and `.nexus`; export writes `.nexus`
- [ ] An existing Alchementary Link install updates to Nexus through the update server


## 11. Existing quirks: default is to fix during the port

| Quirk | Native default |
|---|---|
| RAW Save passes the capture content as a file path (`raw.js:217` → `SaveSessionCapture(sid, path)`) | Save dialog, pass the path (fixes the bug) |
| Rename prompt's OK button says "Connect" | "Rename" |
| Recording tab dot never shows (wrong selectors) | Indicator drawn by the tab strip |
| `#status-protocol` never filled | Show the active session's protocol |
| Adding or removing a jump host, forward or preset discards unsaved edits | Grid editing keeps them |
| `folder` dropped when switching protocol | Keep it |
| RAW decodes received data as Latin-1 | Decode as UTF-8 in text view (hex view unchanged) |
| SFTP bookmarks keyed by session id (lost on disconnect) | Key by profile id |
| Help says sidebar supports drag between groups (it doesn't) | Fix the Help text; use "Move Group" |
| Trigger "sound" uses a WebAudio 880 Hz beep | the system bell |

Client-side state that is **not migrated**: trigger rules and SFTP bookmarks live in WebKit localStorage. Users recreate them; everything else is in the SQLite DB, the vault and the keychain, which are read as-is (§3).

## 12. Tests

- **Before Go is deleted:** the §3 fixtures.
- **`cargo test` in `nexus-core`:**
  - **Compatibility:** every §3 fixture read and round-tripped: database, vault, `.link` exports, recordings, and the keychain on each OS.
  - **Migration (§7):** old dir only → copied; new dir only → untouched; both → Nexus wins; keychain fallback.
  - **Ported Go test tables:** `validation`, `secrets`, `storage`, `exportimport`, `sshconfig`, `knownhosts`,
    `terminalenv`, `rawclient` encoding and history, `models` defaults, and `updater` manifest parsing.
  - **Ported JS tests:** IRC mIRC→ANSI (`test-irc.js`), and split-tree lifecycle with late-connect
    rejection (`test-session-lifecycle.js`).
  - **Pure logic:** fuzzy match, snippet variables, trigger line buffering, and password strength.
  - **Serial:** a pty pair on Linux, covering data, echo, line endings and XON/XOFF.
- **SSH integration:** a CI job against an `openssh-server` container, covering password, key,
  keyboard-interactive with TOTP, jump host, local and remote forwards, agent forwarding, an SFTP round trip,
  and a host-key mismatch.
- **Screenshots:** copy Leviathan's `--screenshot out.png` flag, and check each view under `xvfb-run` in CI.
- `test-regressions.js` is retired; §10 replaces it.
