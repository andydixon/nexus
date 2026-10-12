# Functionality and Next Steps Report

**Project:** Nexus Terminal — Rust port (`rust/` workspace)
**Branch / base commit:** `rust-port` @ `8766846` (after this run: `aac986d`)
**Date:** 2026-10-12 07:23 (local)
**Companion:** `audits/20261012-0723-securityAudit.md` (SEC-20261012-NNN references below)

## 1. Executive Summary

The port is functionally complete relative to the Go/Wails app and is in good shape: the core crate is cleanly separated from the UI, every protocol client follows one lifecycle contract (blocking connect, own reader thread, `on_close` exactly once), the test suite (142 tests) exercises real sockets, a real `sshd`, PTYs and fake IRC/VNC/GitHub servers, and `clippy -D warnings` is clean.

The highest-impact improvements now are reliability and user-experience rather than new protocols:

1. **Credential UX** — a mistyped “saved” password is persisted before it is verified and then blocks every reconnect with no re-prompt (SEC-015); store failures are silent.
2. **Resilience** — a panic on any worker thread poisons shared mutexes and takes the whole app down (`lock().unwrap()` everywhere); there is no panic hook, no crash message and no recovery of open sessions.
3. **Product/privacy switches** — update checks, remote clipboard writes and multi-line paste all happen without a preference or notice.
4. **Operability** — recording metadata, audit-log growth and the IRC state model scale poorly with long-running use.
5. **Developer experience** — fuzz targets, a CI audit step and a documented `cargo clean` note after moving the checkout would have saved time in this run.

## 2. Current Functional Risks

| # | Risk | Where | Effect on users |
|---|---|---|---|
| F1 | Saved credential stored before auth succeeds; no path to replace it on failure other than editing the profile | `core/src/sessions.rs` `secret_fn` | Typo → every connect fails silently with the wrong saved password |
| F2 | `Secrets::set` swallows errors (`let _ =`) | `core/src/secrets.rs` | “Save securely” silently doesn't save when the keychain refuses and the vault is locked |
| F3 | Mutex poisoning: ~60 `lock().unwrap()` sites; any panic in a callback thread (e.g. a decoder bug) crashes the UI on the next lock | `sessions.rs`, `app.rs`, clients | One bad session kills all sessions, recordings stop mid-write |
| F4 | No panic hook / crash dialog; `eframe` aborts silently | `app/src/main.rs` | Users see the window vanish with no explanation |
| F5 | Quick Connect (SSH) allows an empty username; connection then fails at auth with an opaque server error | `app/src/quick_connect.rs`, `service.rs` `connect_quick` (unvalidated) | Confusing failure |
| F6 | `import_ssh_config` writes profiles without `validate_profile` (e.g. empty `User`) | `core/src/service.rs` | Profiles that fail validation when later edited |
| F7 | Unknown enum value in a stored JSON column (`hostKeyPolicy`, `lineEnding`, …) makes `parse_col` return `None`; the profile silently reverts that protocol's config to defaults | `core/src/storage.rs` `parse_col` | Lost settings after a downgrade/edit with a newer app |
| F8 | Empty vault accepts any master password; a typo on first use creates entries under the wrong key (SEC-016) | `core/src/secrets.rs` | Unrecoverable secrets |
| F9 | Recording listing reads every `.cast` fully (up to 250 MiB each) on the UI's worker thread (SEC-020) | `core/src/recording.rs` `read_meta` | Recordings window stalls with large files |
| F10 | macOS `capture_cwd` runs `/usr/sbin/lsof` on every Enter in a local shell | `core/src/clients/local.rs` | Noticeable latency on macOS local tabs |
| F11 | Keychain probe writes and deletes an item at every launch | `core/src/secrets.rs` `Keychain::available` | Keychain prompts/noise on macOS; D-Bus wake-ups on Linux |
| F12 | Keychain legacy read-through copies `AlchementaryLink` items forward on every miss | `Keychain::get` | Repeated writes for keys that only exist in the legacy service |
| F13 | GitHub refresh performs up to 5000 commit lookups every refresh interval with 5 threads | `core/src/clients/github.rs` | Token rate-limit exhaustion; 403s surface as “auth failed” |
| F14 | IRC: channels/users tracked for any JOIN/NAMES seen, UI targets created for any sender (SEC-014) | `irc.rs`, `irc_view.rs` | Memory growth on busy networks |
| F15 | `check_github` in `updater.rs` is unused (dead code) and hardcodes an owner/repo that may not exist | `core/src/updater.rs` | Misleading; GitHub fallback never runs |
| F16 | Update check at every launch, no preference, docs say opt-in (SEC-013) | `app/src/app.rs` | Privacy expectation mismatch |
| F17 | Audit log has no cap/rotation | `storage.rs` `add_audit` | DB grows indefinitely |
| F18 | Stale `target/` after moving the checkout bakes old `CARGO_MANIFEST_DIR` into fixture tests (4 failures until `cargo clean`) | build hygiene | Confusing CI-vs-local mismatch |
| F19 | `Secrets::delete_profile` relies on a static list of kinds (`PROFILE_SECRET_KINDS`); the profile editor and session manager still use string literals for kinds | `secrets.rs`, `profile_editor.rs`, `sessions.rs` | A new kind added in one place is missed by the purge |
| F20 | Reconnect credential prompt timeout is 60 s vs 300 s on first connect | `sessions.rs` `schedule_reconnect` | Reconnects fail if the user is away briefly |

## 3. Code Quality Improvements

- **Secret kinds as a type**: replace the `&'static str` kinds (`"password"`, `"github-token"`, `"vnc-ssh-passphrase"`, …) scattered across `sessions.rs`, `profile_editor.rs` and `secrets.rs` with an enum (`SecretKind`) that has `as_str()` and `ALL`. Why: F19 and SEC-001 depend on the list staying complete. Safe step: enum + `From<&str>`; keep the string form for storage keys.
- **Poison-tolerant locking**: introduce a small `fn lock<T>(m: &Mutex<T>) -> MutexGuard<T>` that recovers from poisoning (`unwrap_or_else(|e| e.into_inner())`, as `irc.rs`/`vnc.rs` already do) and use it in `sessions.rs`, `ssh.rs`, `app.rs`. Why: F3. Mechanical refactor; existing tests cover behaviour.
- **Panic hook**: install `std::panic::set_hook` in `main.rs` that logs the payload (no locals) and posts a toast via `post_error`; keep worker panics from killing the UI. Why: F4.
- **`parse_col` should not swallow errors**: log the parse error and keep the raw JSON (or surface a toast once) instead of returning `None`. Why: F7. Add `#[serde(other)]` variants to the enums so unknown values map to a named `Unknown` instead of failing the whole struct.
- **`sessions.rs` (1 300 lines)**: split per-protocol connect functions (`connect_ssh/irc/vnc/github`) and the reconnect state machine into modules; the file is the hardest to test in isolation. Behaviour-preserving move; `tests/sessions.rs` guards it.
- **Thread-per-event**: `on_close` spawns a thread per close and each connect spawns one; fine for a desktop app but makes ordering hard to reason about. Consider one `std::sync::mpsc` worker for lifecycle events.
- **Duplicated defaults**: `Profile::new`, `Profile::normalize`, `profile_editor::prep` and `SshConfig::defaults` each know the default port/keepalive/retry values. Centralise in `models.rs` constants (already partly done) and have `prep` call `normalize`.
- **`run_command("pwd")`** is the only use of exec for state; SFTP `realpath(".")` already gives the home — consider dropping the shell call to avoid shells that print banners.
- **`Keychain::available` probe**: cache the result in preferences with a timestamp; re-probe weekly or on explicit request. Why: F11.
- **Dead code**: `updater::check_github`, `Protocol::Local` branches in validation (`unsupported protocol`). Remove or wire up.
- **`Store::migrate`**: wrap each migration in a transaction and record `schema_version` explicitly in code rather than relying on the SQL files to insert it (verify each file does).

## 4. Frontend Interaction and Error-Handling Improvements

The UI is native egui; the equivalent of “event firing / async failures / console diagnostics” was reviewed:

- **Async failures surface**: every worker reports through `Job`, `Inbox`, `post_error` or `Shared::spawn`; the pattern is consistent. Gap: `Secrets::set` (F2) and `let _ = self.store.update_profile(&p)` in `connect_profile` discard errors — toast them.
- **Modal keyboard handling**: `prompts()` and `dialogs()` read `Key::Enter`/`Escape` globally; with two modals stacked (a credential prompt while the Delete dialog is open) one key can act on both. Route keys only to the top-most modal (check `app.prompts.is_empty()` before dialogs read Enter).
- **Credential prompt**: show which credential kind and profile; add “Save this credential” checkbox when the profile is in ask-every-time mode; after an auth failure with a saved secret, offer “Re-enter”.
- **Host-key prompt**: offer “Copy fingerprint” and show the port; on key change, show both fingerprints side by side with a “Remove old key” action (today the user must go to Settings → Known Hosts).
- **Clipboard from remote** (SEC-010): toast with the first line whenever OSC 52 or VNC sets the clipboard; preference to disable.
- **Multi-line paste** (SEC-017): confirmation preview when the terminal is not in bracketed-paste mode.
- **Transfers**: SFTP upload stops at the first failure and drops the rest of the queue (by design) — show which files were skipped.
- **Console diagnostics**: adopt `tracing` with `RUST_LOG` support for connection lifecycle (host, protocol, state, error) — never credentials, never terminal data; today only `eprintln!` for CLI misuse exists.
- **Crash fallback** (F4): panic hook + “Nexus Terminal hit an internal error; sessions were kept open where possible” dialog.
- **Screenshot automation flags** (`--answer`): read from `NEXUS_ANSWERS` env/file instead of argv (SEC-018) and print a warning when used without `--screenshot`.

## 5. Testing Roadmap

- **Unit**: `secret_fn` ordering (store after success), `Vault` verifier (once added), `parse_col` unknown-enum behaviour, IRC state caps, `read_meta` tail parsing, `export_json` redaction of login-script sends.
- **Integration**: extend `tests/ssh_integration.rs` with password + keyboard-interactive auth (sshd with `PasswordAuthentication yes` and a PAM-less test user is awkward; alternatively a russh server fixture), host-key change detection against the known-hosts table, remote forward teardown, SFTP rename/mkdir/remove.
- **Abuse-case / fuzz**: `cargo-fuzz` targets for `vnc::read_update` (+ decoders), `telnet::Inner::process`, `irc::parse_line` + `ircformat::format_event`, `recording::Store::get`, `sshconfig::parse`, `updater::parse_manifest`. Run in CI nightly with a corpus from the existing fake-server tests.
- **UI interaction**: the `--screenshot --do --answer` automation already drives the app headlessly; turn it into a smoke test matrix in CI (connect to the in-test sshd, open SFTP, run a palette command, assert the PNG is non-empty and the process exits 0).
- **Regression**: keep the Go-fixture compatibility tests; add a fixture written by *this* version so future format changes are caught both ways.
- **Load**: the ignored `perf::feed_throughput` test (50 MiB through the VT parser) should run in release mode in CI with a threshold; add a VNC decode benchmark on a synthetic 4K frame.
- **Smoke**: `cargo audit` and `cargo deny check licenses advisories` jobs; `cargo test --locked` already runs on three OSes.

## 6. Recommended New Functionality

- **Preferences**: check-for-updates on/off (SEC-013), allow remote clipboard writes (SEC-010), confirm multi-line paste (SEC-017), confirm before `insecure` host-key policy connects.
- **Credential management**: a Settings → Credentials page listing stored kinds per profile with “forget” buttons; master-password change (re-encrypt vault); export/import of the vault.
- **Known hosts**: import from `~/.ssh/known_hosts` and export to it; SSH certificate CA trust (`@cert-authority`) since russh already parses certificates.
- **Update integrity**: signed manifest (minisign/Ed25519), verified before display; optional in-app download with sha256 + signature check.
- **IRC**: SASL PLAIN/EXTERNAL (modern networks reject NickServ IDENTIFY over plaintext), IRCv3 message tags for timestamps, per-network ISUPPORT line length.
- **Serial**: session logging to file, hex view like RAW, break duration setting.
- **SFTP**: recursive directory upload/download, drag-out to the desktop, resume on reconnect.
- **Session**: “Send to all tabs in group”, per-session environment overrides for local shells, custom shell arguments.
- **Operability**: audit-log viewer with cap/rotation; recordings metadata cache; “Diagnostics” page (keychain status, data dir, versions of russh/rustls, last update check).

## 7. Next-Level Opportunities

- **Observability**: `tracing` + optional file log with redaction; connection timing metrics in the status bar; opt-in crash reports (local file only).
- **Resilience architecture**: supervisor-style session threads that catch panics per session (`std::panic::catch_unwind` around client callbacks) so one protocol bug cannot end the app.
- **Security architecture**: move all secret-bearing fields out of `Profile` into the secret store behind a migration (keeps `.nexus` compatibility by exporting them inline only when “include secrets” is chosen); `Zeroizing`/`SecretString` throughout; Linux `PR_SET_DUMPABLE` while the vault is unlocked.
- **Distribution**: signed, notarised builds; Flatpak/AppImage with sandboxing (`--filesystem=home:ro` + explicit `~/.ssh`), winget/Homebrew casks; SLSA provenance from the release workflow.
- **Performance**: VNC — upload only dirty rows to the texture (`TextureHandle::set_partial`), decode JPEG on the I/O thread (already) but avoid the full `local.clone()` on resize; terminal — batch paint runs per style (already) and cache galleys per row.
- **Accessibility**: egui `accesskit` is enabled; audit focus order in modals and provide screen-reader labels for icon-only buttons (`widgets::icon_button` has hover text only).
- **Developer experience**: `justfile`/`make` targets for `test`, `clippy`, `audit`, `screenshot-smoke`; a `CONTRIBUTING.md` note about `cargo clean` after moving the checkout (F18); fuzz corpus checked in.

## 8. Prioritised Action Plan

**Immediate (this week)** — low risk, high value
- Add the three preferences (update check, remote clipboard, paste confirmation) with safe defaults. Risk: trivial.
- Toast on `Secrets::set` failure; store saved credentials after auth success (F1/F2). Risk: touches the auth path; covered by sshd tests — add a keyboard-interactive case.
- Poison-tolerant lock helper + panic hook (F3/F4). Risk: mechanical.
- Add `cargo audit` to CI with SEC-012 ignored and dated (outside `rust/`, owner's call).

**Short term (next release)**
- IRC state caps and joined-only tracking (F14).
- `parse_col` error visibility and `Unknown` enum variants (F7).
- Vault verifier entry (F8); keychain probe caching (F11).
- Known-hosts import/export; host-key change dialog improvements.
- Recording metadata from file tail (F9).

**Medium term**
- Secret-kind enum and credential management page (F19).
- Fuzz targets + nightly job; screenshot smoke matrix.
- Signed update manifest; release signing.
- SASL for IRC; SFTP recursive transfers.

**Long term**
- Move profile-embedded secrets into the store with migration; `SecretString` everywhere.
- Per-session panic isolation; `tracing`-based diagnostics.
- Sandboxed distribution and provenance.

## 9. Caveats and Validation Needed

- Keychain behaviour (purge on delete, probe cost, legacy copy-forward) was verified by reading `keyring` usage, not by running against macOS Keychain, Windows Credential Manager or a live Secret Service; test on each OS before release.
- The in-process key generator (`ssh::generate_key_pair`) was tested for Ed25519 and ECDSA; RSA generation runs the `rsa` crate's keygen (slow in debug builds — verify the UI stays responsive and that OpenSSH accepts the files on all three platforms).
- `--data-dir` now creates directories 0700; if any user relies on sharing a portable data directory between accounts this changes behaviour.
- The VNC size cap (64 Mpx) is above any real display; confirm with multi-monitor spanning setups (e.g. 3×4K = 37 Mpx) before lowering it.
- Preference defaults for update checks and remote clipboard are product decisions; both have compatibility implications (vim/tmux yank over SSH relies on OSC 52).
- UI event flows were validated by code review and the existing automation flags, not by an interactive run in this environment.
