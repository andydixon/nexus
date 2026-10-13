# Functionality and Next Steps Report

Nexus Terminal 3.0.1, reviewed alongside `20261012-2201-securityAudit.md`.

## 1. Executive Summary

The product is broad and mostly solid: nine protocols, sync, restore, shell integration, inline images, RDP devices and a 71-page guide. The highest-impact improvements now:

- **Make the UI thread untouchable.** Several remote- or device-driven paths still do blocking or unbounded work on the UI thread:
  - serial writes;
  - inline image decode;
  - find-bar recomputation;
  - recording seek;
  - the unbounded event queue.
  Moving them to workers, or adding budgets, removes a whole class of "one bad peer freezes everything" reports.
- **Finish the trust story for sync.** Timestamps are clipped and retargeted credentials are forgotten. What's still missing:
  - a visible "what changed" summary;
  - approval for security-relevant fields;
  - eventually per-machine signatures.
- **Credentials out of profile data.** Login-script passwords are the last secrets stored as profile data.
- **Signed updates and releases**, as already planned by the owner.
- **One fixed functional break:** link opening never worked, because eframe's `links` feature was missing. It is fixed now, but it shows the UI has no automated coverage for "does the click do something".

## 2. Current Functional Risks

| Risk | Where | Effect |
|---|---|---|
| Find bar recomputes matches over the whole scrollback (≤100k lines) on every output chunk while open | `app/src/terminal.rs` find (≈1035-1075) | A chatty session plus an open find bar pins the UI thread |
| Recording seek re-feeds from byte 0 on every slider frame | `app/src/recordings.rs` `seek` | Dragging the slider on a big recording wedges the UI |
| `.cast` ids: `trim_end_matches(".cast")` strips repeatedly and `trim()` changes names | `core/src/recording.rs` | `a.cast.cast` plays or deletes `a.cast`; names with leading spaces break |
| A failed recording load closes the browser before the error toast | `app/src/recordings.rs` | The user loses their place |
| `Dialog` is a single `Option`; terminal responses and menus overwrite an open dialog | `tabs.rs`, `app.rs` | A pending Delete confirmation can silently disappear |
| A remote forward with `bind_port == 0` can't be stopped (cancel uses port 0) | `core/src/clients/ssh.rs` `shutdown_forward` | Forward stays listed and active on the server |
| `sftp()` session cached forever; a dead subsystem breaks SFTP until reconnect | `core/src/clients/ssh.rs` | "SFTP stopped working" reports |
| SFTP `list()` treats symlinks to directories as files | `core/src/clients/sftp.rs` | Can't navigate into linked directories |
| No SFTP transfer cancellation | `core/src/clients/sftp.rs` | Large accidental downloads can't be stopped |
| ssh-config import keeps only the first `ProxyJump` hop; `Host !foo` becomes a profile; `=` split on the first `=` anywhere | `core/src/sshconfig.rs` | Imported profiles connect differently from `ssh` |
| Jump-host `auth_method`/`secret_mode` ignored (key/agent only) | `core/src/clients/ssh.rs` `via_jumps` | Password-only bastions can't be used |
| IPv6 literals break KDC and gateway host:port parsing | `rdp.rs`, `ironrdp-mstsgu` | `[::1]:443` doesn't work |
| RD Gateway consent message aborts the connection | vendored `ironrdp-mstsgu` | Enterprise gateways with a banner fail |
| Kerberos hostname comes from `$HOSTNAME`, which is rarely exported | `rdp.rs` | Computer name is almost always "nexus" |
| `is_newer` ignores pre-release suffixes | `core/src/updater.rs` | An `-rc` build isn't offered the final release |
| Swallowed errors: tombstone write failure, vault delete failure, `triggers.json` read failure | `service.rs`, `secrets.rs`, `triggers.rs` | Silent data inconsistency |
| `has_sync_password` was a keychain round trip per frame (now cached for 5 s) | `service.rs` | Fixed; included here because macOS keychain prompts were possible |

## 3. Code Quality Improvements

- **One GCM helper.** `secrets::seal/open` and `exportimport::seal/open` duplicate the AES-GCM wrapping with different layouts. Merge them into one helper that takes AAD; that also closes SEC-049 at the next format bump.
- **Writer threads for every byte-stream client.** VNC already has a bounded queue. Telnet/RAW now have a write timeout, but writes still run on the caller's thread, and Serial and local PTY writes block under locks. One pattern for all of them removes a recurring bug class.
- **Dead code:** `Store::import_profiles` (no callers), `TermResponse.title` / `TermEvent::Title` plumbing (never read), the stray doc comment in `updater.rs`, the stale `.gitignore` whitelist for `rust/core/tests/fixtures/**`, and "Alchementary Link" strings left in `migrations/001_initial.sql` and `updates/index.php`.
- **`parse_col`** drops an unreadable config column, and the next save overwrites it with defaults. Keep the raw JSON and merge, so a config written by a newer build survives an older one.
- **Dependency hygiene:** move nexus-core to `rand 0.9`, `argon2 0.6`, `aes-gcm 0.11` and `sha2 0.11` to match the rest of the graph, collapsing duplicate crypto crates.
- **Vendored crates:** commit `.patch` files plus upstream tarball sha256, and add a CI check that the patches still apply cleanly to the pinned upstream versions.

## 4. Frontend Interaction and Error-Handling Improvements

(The UI is native egui. These are its equivalents of the frontend event and error items.)

- **Keep heavy work off the UI thread:**
  - decode inline images on a worker;
  - debounce find to ≤10 Hz, or restrict it to visible rows plus N;
  - seek recordings on `drag_stopped`;
  - write serial data on a writer thread.
- **Bound the event queue:** a per-session byte budget, coalescing consecutive `Data` events.
- **Dialogs:** make `Dialog` a small stack, or refuse to replace a confirmation dialog, so destructive confirmations can't vanish.
- **A sync change summary:** after a pull, list the changed profiles and fields (host, user, key policy, scripts, forwards, shared folder), with approve/revert for the security-relevant ones.
- **Explain withheld credentials:** IRC now does this for plaintext servers. Do the same for any future "held back for safety" behaviour, ideally with a one-click fix (enable TLS).
- **Clipboard settings:** consider defaulting "Allow remote clipboard writes" to off, or prompting on multi-line writes. The toast is now honest about line counts.
- **Debug logs:** before a user shares `RUST_LOG=debug` logs, the Diagnostics page should warn that debug output from the RDP libraries can contain authentication blobs and shared-folder data.

## 5. Testing Roadmap

- **Unit:**
  - RDPDR read clamp;
  - oversized RDP desktop;
  - `.cast` id mapping;
  - remote forward with port 0 stop;
  - `parse_col` with an unknown future field;
  - import strategies (skip/overwrite/duplicate, duplicate ids in one batch, size caps).
- **Integration:**
  - two-sshd jump chain proving a jump host can't open agent or forwarded-tcpip channels (SEC-008);
  - a stalled SSH server proving every phase hits its timeout;
  - a fake Telnet peer that floods and never reads, proving `close()` returns quickly.
- **Migration fixtures:** real schema-1 and schema-7 databases opened by the current build, including one with Go-era `proxyPassword` keys.
- **Fuzzing:**
  - new targets for `.cast` parsing, `known_hosts::parse`, `exportimport::open` and the JSON profile importer;
  - seed the VNC corpus with valid ZRLE/Tight zlib streams;
  - keep `-timeout` (now 10 s) so slow-input bugs surface.
- **UI smoke:**
  - a headless run per protocol using the existing screenshot automation (`--connect`, `--do`, `--screenshot`) as a CI job, asserting no panic and a non-blank frame;
  - "does this click do something" checks (link open, Copy, Paste) would have caught the missing `links` feature.
- **Load:** stream 512 MiB through a RAW session with the UI paused and assert bounded RSS (SEC-044).

## 6. Recommended New Functionality

- **Secret placeholders in login scripts** (`{{password}}`, `{{totp}}`) resolved from the keychain or vault at run time. This removes plaintext secrets from profiles (SEC-020) and improves the feature.
- **Sync change review and per-machine trust:** pair machines once by exchanging ed25519 public keys and sign sync records. Then the sync password is no longer the only key.
- **Signed update checks:** a minisign signature over the manifest, with the public key embedded, and verification in the updater. Pairs with the signing work already planned.
- **RDP NLA memory:** remember that a host did NLA and refuse a TLS-only downgrade later (SEC-019). Show "no NLA" visibly in the toolbar when it is in use.
- **VNC "unauthenticated session" indicator**, shown when the server picks security type None.
- **IRC TLS certificate pinning** for private-CA networks, so users aren't nudged to turn TLS off.
- **SFTP:** transfer cancellation and following symlinked directories.
- **Jump hosts with passwords:** honour the existing `auth_method` and `secret_mode` fields.

## 7. Next-Level Opportunities

- **Observability:** a "Copy debug bundle" action that redacts known-sensitive upstream log targets automatically, instead of relying on the user's `RUST_LOG` choice.
- **Architecture:** a single I/O runtime per session (tokio already runs SSH) for Telnet, RAW, Serial and VNC. That gives consistent cancellation, timeouts and back-pressure, and removes several thread-per-connection blocking patterns.
- **Release engineering:** build provenance attestations, signed tags, and reproducible-build notes. Releases are already split into build (read-only) and publish (write) jobs.
- **Accessibility:** egui AccessKit is enabled. Audit screen-reader labels on the desktop viewers and terminal (password fields are already masked for AccessKit).
- **Performance:** the OSC 8 underline painter issues up to cols/3 `hline` calls per cell per frame, and the RAW hex row cache is bounded by count, not bytes. Both are cheap wins.

## 8. Prioritised Action Plan

| When | Action | Why | Risk |
|---|---|---|---|
| Immediate | Fix the GitHub Actions billing block so CI and the v3.0.1 release builds run | No CI signal and no release artefacts right now | None |
| Short term | Event budget, find debounce, recording seek on drag-stop, serial writer thread | Remove the remaining UI-freeze vectors | Low; contained changes |
| Short term | Sync change summary plus approval for security-relevant fields | Closes SEC-021 | Medium; UX design needed |
| Short term | RDP NLA memory (SEC-019) | Downgrade protection | Low; needs a small known-hosts field |
| Medium | Login-script secret placeholders | Removes the last plaintext secrets | Medium; changes the script editor and migration |
| Medium | Merge the GCM helpers with AAD, bump crypto crates, vendored patch checks | Hygiene and integrity | Low |
| Medium | Fuzz targets and migration fixtures listed in §5 | Regression safety | None |
| Long term | Signed manifests, binaries and tags; per-machine sync signatures | Supply-chain and sync trust | Medium; key management |

## 9. Caveats and Validation Needed

- **Not exercised against real systems:**
  - Windows RDP with NLA and Display Control;
  - RD Gateway;
  - Kerberos;
  - macOS/Windows keychains (after the `Zeroizing` API change);
  - real plaintext IRC networks (behaviour changed: passwords are now held back there).
- **Release ordering:** the release workflow now publishes only after every platform builds, so one failing platform blocks the release. Confirm that is the preferred trade-off over partial releases.
- **Sync re-prompts:** forgetting credentials on retargeted sync/import means a host edit on one machine makes the others re-prompt once. That is intended, but users should be told (it's in the manual now).
- **Profile ids:** ids are now limited to `[A-Za-z0-9._-]{1,64}`. Every id this app generates is a UUID, but hand-made JSON imports with unusual ids will be refused rather than rewritten. Rewriting them on import is a possible follow-up.
