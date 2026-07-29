# Security Audit Report

## 1. Executive Summary

This fix-mode audit reviewed the current Nexus desktop application, its local
VS Code bridge process, browser-facing frontend, session and upload lifecycles,
credential handling, network clients, encrypted storage and export, Leviathan
file analysis, build/release configuration, dependencies, and earlier audit
reports.

No Critical or High severity vulnerability was confirmed. Four
evidence-backed findings were fixed in commit `b5c7528`:

- Auto-reconnect credential prompts registered a permanent Wails listener on
  every prompt. Long-running processes with repeated reconnects accumulated
  callbacks and unnecessary event-dispatch work.
- Interrupted streaming SFTP uploads could retain open remote writers and
  in-memory upload entries after their session closed. Concurrent unfinished
  uploads were not bounded.
- The accepted Go toolchain range included versions affected by a reachable
  standard-library TLS privacy vulnerability.
- Encrypted exports and Leviathan carve output did not consistently enforce
  private local file permissions. Short-lived plaintext and derived key byte
  slices were also retained until garbage collection.

The same commit fixed malformed IPv6 host/port construction in VNC, IRC SSH
tunnels, and the connection tester, and repaired stale frontend regression
assertions left behind when the VS Code extension tree and CGO build were
removed.

All Go tests, focused race tests, Go vet, the complete frontend test suite,
frontend production build, JavaScript and shell syntax checks, PHP syntax
check, `npm audit`, and the reachable Go vulnerability scan pass. Live remote
protocol testing and packaged desktop testing remain manual validation items.

## 2. Scope Reviewed

Reviewed:

- Go packages under `internal/`, including sessions, SSH, SFTP, VNC, IRC,
  Telnet, RAW TCP, local shells, storage, vault/keychain integration,
  recording, updater, GitHub client, import/export, and Leviathan.
- The localhost service bridge in `cmd/vscode-bridge`.
- Frontend source, lifecycle handling, DOM construction, timers, Wails event
  subscriptions, build scripts, and regression harness under `frontend/`.
- Network address construction, host-key verification, connection setup, and
  session shutdown paths.
- Dependency manifests and lock files.
- Build, release, update, and CI configuration.
- Earlier reports under `audits/`, to distinguish regressions from already
  fixed findings.

Not live-tested:

- Packaged Wails applications on every supported operating system.
- Real SSH, SFTP, VNC, IRC, Telnet, RAW, GitHub, or update endpoints.
- Malicious network peers under sustained load.
- External Leviathan extractor tools. Their extraction path is not currently
  exposed by the active Wails document response.
- A VS Code extension runtime, because the `vscode/` source tree was
  intentionally removed in commit `4fc3b19`.

## 3. Methodology

The review combined:

- Repository and Git-history inspection.
- Trust-boundary and attacker-starting-position mapping.
- Static review of authentication, authorisation, secrets, cryptography,
  filesystem operations, command execution, parsers, network clients, event
  listeners, goroutines, timers, and cleanup paths.
- Adversarial resource-exhaustion review, with particular attention to a
  process that has run for a long time while tabs and sessions are repeatedly
  opened, reconnected, and closed.
- Frontend sink/source review for unsafe HTML, event-listener accumulation,
  timers, stale state, and privileged Wails calls.
- Dependency and supply-chain scanning.
- Focused regression tests followed by broad test, race, build, syntax, and
  static-analysis checks.

Principal validation commands:

- `go test ./... -count=1`
- `go test -race ./internal/sessions ./internal/api ./internal/exportimport ./internal/vncclient ./cmd/vscode-bridge -count=1`
- `go vet ./...`
- `go run golang.org/x/vuln/cmd/govulncheck@latest -show verbose ./...`
- `go run github.com/securego/gosec/v2/cmd/gosec@latest ./...`
- `npm test` in `frontend/`
- `npm run build` in `frontend/`
- `npm audit --json` in `frontend/`
- `node --check` for changed JavaScript
- `bash -n build.sh build-all.sh release.sh scripts/build-common.sh`
- `php -l updates/index.php`
- `git diff --check`

## 4. Risk Rating Method

Severity reflects attacker position, prerequisites, exploit repeatability,
trust-boundary impact, and confidentiality, integrity, and availability
consequences.

- Critical: practical unauthenticated remote compromise or broad secret
  disclosure.
- High: practical code execution, credential theft, or privilege escalation.
- Medium: meaningful local, renderer, connected-peer, or privacy impact with
  realistic prerequisites.
- Low: bounded local exposure, defence-in-depth weakness, or correctness issue
  with a security consequence.
- Informational: hardening or validation work without a confirmed exploit.

Confidence reflects the strength of source evidence, reproduction, scanner
evidence, and regression coverage. Severity is not increased merely because a
scanner reports a possible pattern.

## 5. Attack Surface Map

Entry points:

- Wails-bound service methods invoked by the desktop renderer.
- Runtime events crossing between frontend JavaScript and Go.
- The token-authenticated localhost bridge endpoints.
- User-selected SSH, SFTP, VNC, IRC, Telnet, RAW TCP, GitHub, and update
  endpoints.
- Imported profiles, OpenSSH configuration, encrypted Nexus exports,
  recordings, and files selected for Leviathan analysis.
- Local shell commands and SSH key generation.

Trust boundaries:

- Renderer to privileged Go backend.
- Local bridge client to localhost HTTP/RPC service.
- OS keychain or encrypted vault to runtime session credentials.
- Remote network data to protocol clients and frontend rendering.
- User-selected or hostile files to parsers and external analysis tooling.
- Release workflow and update manifest to installed application binaries.

Attacker starting positions considered:

- A malicious or compromised remote service.
- A hostile local file or imported configuration.
- A local unprivileged process targeting the localhost bridge or filesystem
  artefacts.
- A renderer injection or future renderer compromise invoking bound methods.
- An observer of TLS traffic.
- A legitimate local user accidentally creating unbounded resource state over
  a long-running process.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---|---|---|---|---|
| SEC-20260729-1237-001 | Medium | High | Reconnect credential prompts accumulated permanent runtime listeners | `internal/sessions` | Fixed in `b5c7528` |
| SEC-20260729-1237-002 | Medium | High | Interrupted SFTP uploads retained resources without a concurrency bound | `internal/api` | Fixed in `b5c7528` |
| SEC-20260729-1237-003 | Medium | High | Build accepted a Go toolchain with a reachable TLS privacy vulnerability | `go.mod`, build scripts | Fixed in `b5c7528` |
| SEC-20260729-1237-004 | Low | High | Sensitive local artefacts did not consistently enforce private permissions | Export/import and Leviathan | Fixed in `b5c7528` |

## 7. Detailed Findings

### Finding ID: SEC-20260729-1237-001

#### Title

Reconnect credential prompts accumulated permanent runtime listeners

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/sessions/manager.go:110`
- `internal/sessions/manager.go:676`
- `internal/sessions/manager.go:2577`
- `internal/sessions/manager.go:2615`
- `internal/sessions/manager.go:2654`
- `internal/sessions/manager_test.go:383`
- `cmd/vscode-bridge/main.go:187`
- `internal/app/app.go:118`

#### Description

Every credential request created during automatic reconnect registered another
`session:secret:response` Wails listener. The listener was never unregistered,
including after a response, timeout, cancelled reconnect, session close, or
application shutdown.

The request ID check prevented one prompt from accepting another prompt's
value, but all historical callbacks were still invoked for every future
credential response. A process left open for a long time could therefore
accumulate event callbacks in proportion to its reconnect history. A closed
session could also leave a credential prompt waiting for its full timeout.

#### Evidence

Before the fix, `buildInteractiveSecretCallback` called
`wailsRuntime.EventsOn` inside the callback created for each prompt and
discarded the returned unsubscribe function.

The fix registers one manager-level response router from `SetContext`, stores
the unsubscribe function, routes responses through a request-ID map, honours
frontend cancellation, and selects on the reconnect context. `Shutdown`
unregisters both host-key and credential listeners and releases all waiters.
Application and bridge shutdown now call the complete service and manager
shutdown methods.

#### Attack Scenario

A remote SSH or IRC service repeatedly drops connections after authentication,
or a user repeatedly opens and reconnects profiles that require an interactive
secret. Historical event listeners accumulate. Later secret responses fan out
to all callbacks, increasing CPU work and retained memory even though old tabs
are closed.

#### Impact

Progressive memory and CPU consumption in a long-running process, sluggish
credential prompts, delayed shutdown, and eventual application availability
loss. The request-ID routing means no cross-session secret disclosure was
confirmed.

#### Recommendation

Use one application-lifetime event listener per event type, route responses by
opaque request ID, bind every waiter to a cancellation context, and explicitly
unsubscribe and release waiters during shutdown.

#### Safer Example

Implemented in outline:

```go
cancel := wailsRuntime.EventsOn(ctx, "session:secret:response", m.handleSecretResponse)
m.secretListenerCancel = cancel

select {
case response := <-ch:
    return response.value, cancellationError(response)
case <-ctx.Done():
    return "", ctx.Err()
}
```

#### Suggested Tests

Added tests verify request-ID routing, reconnect-context cancellation, frontend
cancellation, pending-map cleanup, and shutdown waiter release. The focused
race suite passes.

#### References

- CWE-400: Uncontrolled Resource Consumption
- CWE-772: Missing Release of Resource after Effective Lifetime

#### Assumptions / Limitations

No packaged Wails runtime stress test was run. The defect and fix are directly
visible in source, and the routing/cancellation logic is covered at Go level.

### Finding ID: SEC-20260729-1237-002

#### Title

Interrupted SFTP uploads retained resources without a concurrency bound

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go:52`
- `internal/api/api.go:117`
- `internal/api/api.go:469`
- `internal/api/api.go:475`
- `internal/api/api.go:515`
- `internal/api/api.go:1569`
- `internal/api/api.go:2189`
- `internal/api/api.go:2202`
- `internal/api/api_test.go:145`

#### Description

Streaming SFTP upload state held an open remote writer in the service upload
map. Finish and explicit cancel removed it, but disconnecting, reconnecting, or
closing the owning session did not. Application shutdown also did not drain
unfinished uploads. There was no limit on the number of simultaneously open
streaming uploads.

#### Evidence

The earlier lifecycle paths called the session manager directly without
visiting the upload map. `SFTPUploadStart` inserted every new writer without a
capacity check.

The fix:

- Caps active streaming uploads at 16.
- Checks capacity before opening a remote writer and atomically rechecks before
  insertion.
- Removes and closes uploads belonging to a session before disconnect or
  reconnect.
- Drains every upload during close-all and service shutdown.
- Best-effort removes the partial remote file.
- Releases secret-prompt waiters and the service's Wails listener during
  shutdown as part of the same lifecycle hardening.

#### Attack Scenario

A compromised renderer, automation loop, or repeated user action starts
uploads and closes the associated tabs or sessions without finishing them.
Each iteration can retain a local map entry, SFTP request state, remote file
handle, and partially written file. Repetition eventually exhausts descriptors,
memory, or server-side resources.

#### Impact

Local and remote resource exhaustion, failed subsequent uploads, leaked partial
files, and degraded session availability.

#### Recommendation

Make resource ownership explicit: uploads must be owned by a session and
cancelled when that session ends. Bound concurrent work and keep a race-safe
second capacity check after expensive setup.

#### Safer Example

Implemented:

```go
func (s *Service) DisconnectSession(sessionID string) error {
    s.cancelSFTPUploads(sessionID)
    return s.sessions.Disconnect(sessionID)
}
```

#### Suggested Tests

Added tests verify session-scoped cancellation, unrelated-upload preservation,
the active-upload limit, writer closure, and shutdown cleanup. Full and race
tests pass.

#### References

- CWE-400: Uncontrolled Resource Consumption
- CWE-404: Improper Resource Shutdown or Release
- CWE-772: Missing Release of Resource after Effective Lifetime

#### Assumptions / Limitations

No real SFTP server was used to verify partial-file deletion after a transport
failure. Removal is best-effort because the session may already be unavailable;
local writer and map cleanup do not depend on remote deletion succeeding.

### Finding ID: SEC-20260729-1237-003

#### Title

Build accepted a Go toolchain with a reachable TLS privacy vulnerability

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `go.mod:3`
- `go.mod:5`
- `build.sh:21`
- `build-all.sh:20`

#### Description

The build accepted Go 1.25.0 and the local build used Go 1.26.4.
`govulncheck` identified GO-2026-5856 / CVE-2026-42505 in a reachable
`crypto/tls` path. Affected clients could expose TLS PSK identities in a
plaintext ClientHello while Encrypted Client Hello was in use, allowing a
passive observer to undermine ECH privacy.

#### Evidence

The pre-fix scan reported the standard-library vulnerability as reachable.
The [official Go vulnerability record](https://pkg.go.dev/vuln/GO-2026-5856)
states that Go 1.25 is fixed in 1.25.12 and Go 1.26 is fixed in 1.26.5.

The fix raises the module minimum to Go 1.25.12, requests toolchain Go 1.26.5,
and aligns both Unix build scripts with the new minimum. The post-fix verbose
scan used the Go 1.26.5 standard library and reported zero symbol or package
vulnerabilities affecting Nexus.

#### Attack Scenario

Where ECH and PSK resumption are used, a passive network observer records the
client's plaintext ClientHello and correlates an exposed PSK identity with the
protected destination.

#### Impact

Conditional network privacy disclosure. No private key or application
credential extraction was demonstrated. Nexus does not explicitly configure
ECH, so actual exposure depends on TLS configuration and runtime behaviour.

#### Recommendation

Enforce fixed toolchain floors in `go.mod`, local build scripts, and CI. Keep
`govulncheck` in release validation so standard-library vulnerabilities cannot
be missed by module-only scanners.

#### Safer Example

Implemented:

```text
go 1.25.12
toolchain go1.26.5
```

#### Suggested Tests

Run `go version` and verbose `govulncheck` in release CI, and fail if a
reachable symbol-level vulnerability is reported.

#### References

- GO-2026-5856
- CVE-2026-42505
- CWE-200: Exposure of Sensitive Information

#### Assumptions / Limitations

The scanner established a reachable TLS symbol path, not that Nexus actively
negotiated ECH with a vulnerable server during this audit. Severity therefore
reflects conditional exploitability.

### Finding ID: SEC-20260729-1237-004

#### Title

Sensitive local artefacts did not consistently enforce private permissions

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `internal/exportimport/exportimport.go:68`
- `internal/exportimport/exportimport.go:111`
- `internal/exportimport/exportimport.go:116`
- `internal/exportimport/exportimport.go:170`
- `internal/exportimport/exportimport_test.go:108`
- `internal/leviathan/binsnoop/binsnoop.go:137`
- `internal/leviathan/binsnoop/binsnoop.go:141`
- `internal/leviathan/binsnoop/extract.go:17`
- `internal/leviathan/binsnoop/extract.go:20`
- `internal/leviathan/binsnoop/common_test.go:25`

#### Description

Encrypted profile exports were created through `os.Create`, which normally
uses mode `0666` filtered by the process umask. Overwriting an existing file
also preserved its existing mode. Leviathan carved binary data and extraction
directories used `0644` and `0755`. Carved content can include credentials,
private keys, firmware configuration, or other forensic secrets.

Export encryption keys, password byte slices, and decrypted plaintext byte
slices also remained live until garbage collection after use.

#### Evidence

The fix opens exports with mode `0600` and explicitly applies `Chmod(0600)` so
an existing permissive destination is tightened. Leviathan-owned carve files
now use `0600`, and directories use `0700`. Export/import makes a best-effort
`clear` of transient password, derived-key, and plaintext byte slices.

#### Attack Scenario

On a multi-user host, another local account reads a carved private key or
configuration from a world-readable output file. For an export destination
that already had permissive permissions, another account can copy the
encrypted profile bundle for offline password guessing.

#### Impact

Local confidentiality exposure. The export remains protected by Argon2id and
AES-GCM, which significantly reduces its immediate sensitivity; raw carve
output has no equivalent encryption.

#### Recommendation

Create sensitive artefacts and parent directories with private modes, tighten
existing destinations explicitly, and minimise the lifetime of mutable
plaintext and key buffers. Document that Go strings and internal copies cannot
be guaranteed to be erased.

#### Safer Example

Implemented:

```go
f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
if err == nil {
    err = f.Chmod(0600)
}
```

#### Suggested Tests

POSIX-only regression tests verify `0600` export and carve files and a `0700`
carve directory, including overwriting a pre-existing `0644` export.

#### References

- CWE-276: Incorrect Default Permissions
- CWE-316: Cleartext Storage of Sensitive Information in Memory

#### Assumptions / Limitations

POSIX mode bits are not equivalent on Windows, so permission tests skip there.
`clear` is best-effort in Go and cannot erase immutable password strings,
copies held by libraries, or compiler-created copies. External extraction
tools may choose their own modes for files they create beneath the private
directory.

## 8. Blackhat-Style Abuse Paths

### Path 1: Reconnect churn to process exhaustion

Prerequisite: a profile needs an interactive credential during automatic
reconnect. A hostile endpoint repeatedly drops the connection. Before the fix,
each prompt left another Wails listener behind. Defensive breakpoint:
application-level listener routing, context cancellation, and shutdown
unsubscribe.

### Path 2: Renderer-driven SFTP handle exhaustion

Prerequisite: renderer access to bound service methods. Start streaming uploads
without finishing them, then close or reconnect sessions repeatedly. Before the
fix, writers and upload entries survived. Defensive breakpoint: the 16-upload
cap and session-owned cancellation.

### Path 3: Passive observation of vulnerable TLS ECH

Prerequisite: affected Go runtime, ECH/PSK behaviour, and network visibility.
Observe plaintext ClientHello state and correlate exposed PSK identity.
Defensive breakpoint: Go 1.25.12 or 1.26.5 and later.

### Path 4: Local recovery of carved secrets

Prerequisite: another local account can traverse the selected output location.
Read a `0644` carved file containing a private key or configuration secret.
Defensive breakpoint: `0700` directories and `0600` files.

No practical chain to unauthenticated remote code execution was identified.

## 9. Denial-of-Service Review

Fixed in this audit:

- Reconnect secret listeners no longer grow with historical prompt count.
- Cancelled reconnect prompts stop immediately instead of waiting up to 60
  seconds.
- Session close/reconnect and application shutdown drain streaming uploads.
- Concurrent streaming uploads are capped at 16.
- Runtime listeners and prompt waiters are released at shutdown.
- IPv6 literals are joined with ports correctly, preventing connection failure
  and misleading repeated retry behaviour.

Previously hardened and rechecked:

- SFTP inline/chunk/total upload size caps.
- VNC WebSocket frame limits.
- GitHub archive and update-response size limits.
- SSH configuration and public-key input caps.
- Recording and Leviathan file-size limits in exposed paths.
- Session timers, latency pings, frontend intervals, and closed-tab lifecycle
  cleanup covered by the current regression tests.

Residual considerations:

- Network protocols ultimately depend on remote peer responsiveness.
- External Leviathan tools lack a shared timeout/output-budget wrapper, but
  that path is not currently exposed by the active UI API.
- Hostile binary parsers deserve continued fuzzing even where current bounds
  checks reject malformed offsets.

## 10. Dependency and Supply-Chain Review

`npm audit --json` reported zero vulnerabilities across 33 frontend
dependencies.

Post-fix `govulncheck -show verbose` reported:

- Zero vulnerabilities in symbols Nexus calls.
- Zero vulnerabilities in imported package call paths.
- Four advisories in required module versions that Nexus does not appear to
  call:
  - GO-2026-5970 in `golang.org/x/text@v0.37.0`.
  - GO-2026-5942 in `golang.org/x/net@v0.55.0`.
  - GO-2026-5932 for the unmaintained `x/crypto/openpgp` package.
  - GO-2026-5841 in `github.com/klauspost/compress/s2@v1.17.11`.

Those versions should be refreshed through normal dependency maintenance, but
they were not forcibly upgraded in this fix because the scanner found no
Nexus call path and broad transitive churn would require separate compatibility
testing.

The release workflow uses common GitHub Actions by major version and a
write-capable release job. Pinning actions to immutable commit SHAs would
further reduce supply-chain drift. Release permissions should remain scoped to
the release job and tag/manual triggers.

## 11. Secrets and Sensitive Data Review

No hard-coded production credential, private key, token, or certificate was
found. Test secrets were confined to test fixtures.

Reviewed controls:

- OS keychain integration.
- Argon2id-derived AES-GCM vault and export encryption.
- SSH host-key verification and known-host handling.
- Bridge bearer token generation and comparison.
- Secret prompt routing and cancellation.
- Sensitive file permissions.
- Logging and frontend error paths for obvious secret disclosure.

Fixed:

- Best-effort clearing of mutable export/import plaintext, password bytes, and
  derived keys.
- Private export and carve modes.
- Prompt waiters and listeners released during shutdown.

Residual:

- Go strings and library copies cannot be reliably zeroed.
- Users may explicitly choose plaintext Telnet, RAW TCP, or non-TLS VNC.
- A user can explicitly select insecure SSH host-key behaviour; that choice
  remains a visible compatibility option and should not be the default.

## 12. Authentication and Authorisation Review

Nexus is a single-user desktop client rather than a multi-tenant server. Its
primary authorisation boundary is the logged-in OS account plus the Wails
renderer/backend boundary.

Checked:

- Local bridge endpoints retain bearer-token authentication.
- SSH host-key callbacks are required in the reviewed direct and tunnel paths.
- GitHub and remote-session credentials are retrieved from configured secret
  stores or interactive prompts.
- Sensitive service methods validate session identifiers through the manager
  and validate paths, sizes, or option allow-lists where applicable.

No new authentication bypass or cross-user authorisation defect was confirmed.
A future renderer compromise would still have substantial privileges because
Wails-bound methods are designed for the trusted desktop UI; maintaining output
encoding and a narrow method surface therefore remains important.

## 13. Input Validation and Output Encoding Review

Reviewed:

- Profile fields, ports, hostnames, paths, filenames, import strategies,
  archive/update URLs, SSH key options, recording files, binary offsets, and
  base64 upload chunks.
- HTML construction and text insertion in frontend modules.
- Local subprocess arguments for shell, `ssh-keygen`, and Leviathan extraction.

Fixed:

- `net.JoinHostPort` now constructs IPv4, hostname, and IPv6 endpoints in VNC,
  IRC SSH tunnels, and the connection tester.
- Existing SFTP validation is now combined with a concurrency limit and
  lifecycle cleanup.

Scanner findings for MD5/SHA-1 in Leviathan are intentional forensic hash
identifiers, not password or signature protection. Representative integer
conversion and slice warnings in hostile-file parsers were manually traced to
existing bounds checks; no concrete out-of-bounds path was confirmed. Parser
fuzzing remains preferable to broad speculative casting changes.

## 14. Frontend HTML/JavaScript Robustness Review

The frontend contains numerous event listeners because each tab and modal owns
interactive DOM. Reviewed tab/session paths attach handlers to ephemeral nodes
that become collectable when their owning DOM is removed, while application
listeners and intervals have explicit one-time or cleanup behaviour.

Current regression coverage verifies session cleanup, active latency timer
behaviour, closed-tab release, safe DOM patterns, upload constraints, build
hardening, and other earlier fixes.

The aggregate test harness still referenced VS Code files deleted in
`4fc3b19` and a CGO flag removed by the same pure-Go migration. This caused
`npm test` to fail before reaching its current assertions. The stale VS Code
checks were removed, and the obsolete `-O2 -g0` assertion was replaced with a
check that the current build retains `CGO_ENABLED=0`. Both frontend suites now
pass.

No confirmed exploitable current DOM injection was found. Some `innerHTML`
usage remains and relies on escaping helpers or trusted static markup; future
changes should prefer `textContent` and DOM construction for dynamic data.

## 15. Infrastructure and Deployment Review

Reviewed:

- Shell, PowerShell, Makefile, and shared build helpers.
- GitHub Actions release configuration.
- Updater endpoint and PHP manifest delivery.
- Cross-platform build flags and Go toolchain selection.

Fixed:

- Unix build scripts now reject Go versions older than 1.25.12.
- `go.mod` requests Go 1.26.5 while keeping 1.25.12 as the minimum supported
  language/toolchain floor.
- Stale regression coverage now reflects the pure-Go build.

The updater remains a high-value trust boundary. Production release validation
should verify HTTPS, hashes/signatures where supported, expected artefact
names, and least-privilege release credentials. No new command-injection or
manifest-escaping defect was confirmed.

## 16. Logging, Monitoring, and Error Handling Review

The new cleanup paths do not log credentials or secret responses. Upload
cleanup treats remote partial-file deletion as best-effort because the
underlying session may already be broken.

Useful future operational signals:

- Active and rejected streaming-upload counts.
- Reconnect attempt count and pending credential prompts.
- Active runtime listener and session counts in a diagnostics view.
- Resource snapshots during long-running open/close soak tests.

These should expose counts and identifiers only, never passwords, tokens,
private keys, terminal contents, or bridge bearer tokens.

## 17. Test Coverage and Security Regression Gaps

Added:

- Credential response routing by request ID.
- Reconnect cancellation releases its secret prompt immediately.
- Frontend cancellation is honoured.
- SFTP cleanup is scoped to the owning session.
- Active streaming uploads are bounded.
- Service shutdown releases uploads and prompt waiters.
- Existing permissive export files are tightened to `0600`.
- Leviathan carve directories/files use `0700`/`0600`.

Passing:

- Full Go test suite.
- Focused Go race suite.
- Go vet.
- Complete frontend lifecycle and regression suite.
- Frontend production build.
- JavaScript, shell, and PHP syntax checks.
- `npm audit`.
- Reachable Go vulnerability scan.

Remaining gaps:

- No packaged Wails UI smoke or long-duration tab/reconnect soak test.
- No real SFTP server test proves partial-file deletion after a broken
  transport.
- No live IPv6 protocol endpoint was used.
- Hostile-file parser coverage is uneven; fuzzing should be expanded.
- Windows ACL behaviour is not verified by POSIX permission tests.
- External Leviathan extractors lack integration tests for time and output
  limits.

## 18. Prioritised Remediation Plan

Immediate:

- Soak-test a packaged app while repeatedly opening, reconnecting, and closing
  local and remote tabs. Monitor process CPU, memory, goroutines, file
  descriptors, and listener/upload counts.
- Exercise interrupted SFTP uploads against a real server and verify local
  handle cleanup plus remote partial-file behaviour.

Short-term:

- Add a packaged Wails lifecycle smoke test.
- Add fuzz targets for the highest-complexity Leviathan parsers.
- Refresh the four module versions with unreachable advisories and run protocol
  compatibility tests.
- Add metrics or a diagnostics screen for active sessions, reconnects,
  uploads, prompts, and event subscriptions.

Medium-term:

- Wrap every external Leviathan extractor with context timeout, process-group
  termination, and output-size/file-count budgets before exposing extraction
  through the UI.
- Replace remaining dynamic `innerHTML` paths with text nodes or narrowly
  reviewed rendering helpers.
- Add real IPv6 SSH/VNC/IRC tunnel integration coverage.

Long-term:

- Add release CI gates for full tests, race-focused tests, `govulncheck`,
  frontend audit/build, packaged smoke checks, and signed artefact validation.
- Pin third-party GitHub Actions to reviewed immutable revisions.

## 19. False Positives / Needs Manual Verification

- Gosec MD5 and SHA-1 reports in Leviathan are forensic digest features, not
  security authentication or password hashing.
- Gosec subprocess reports include user-selected local shells, allow-listed SSH
  key generation options, and an external extraction path not exposed by the
  current document API. The extraction path still needs limits before future
  exposure.
- File-parser integer-conversion and slice warnings were reviewed
  representatively; existing range checks rejected the paths inspected.
  Fuzzing is required for stronger assurance.
- The four remaining Go module advisories are present in required versions,
  but `govulncheck` found no called symbol or imported vulnerable package path.
- The fixed TLS issue had a reachable standard-library path, but actual ECH
  exposure was not reproduced.
- Sensitive-buffer clearing is best-effort and must not be described as
  guaranteed memory erasure in Go.

## 20. Final Notes

The confirmed issues and their regression tests are committed in `b5c7528`.
The most important improvement for the reported long-running-process behaviour
is that credential event listeners, prompt waiters, and unfinished upload
writers now have explicit owners and terminal cleanup paths.

Closing a session now cancels its reconnect work and SFTP uploads; closing all
sessions drains all uploads; application or bridge shutdown also unregisters
runtime listeners and releases pending prompts. This materially improves
cleanup, but live soak testing is still required before claiming that every OS,
third-party library, and remote-peer resource remains flat indefinitely.
