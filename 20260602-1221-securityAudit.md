# Security Audit Report

## 1. Executive Summary

This audit reviewed Nexus Terminal, a Wails desktop SSH/Telnet/RAW/VNC/SFTP client with a Go backend and JavaScript frontend. Fix mode was requested, so confirmed issues were patched, regression-tested, and prepared for commit.

The highest-risk issues were SSH trust failures: regular SSH sessions were not wired to the existing known-host verification flow, jump-host SSH connections ignored host keys, and VNC SSH tunnels also ignored host keys. These could allow machine-in-the-middle attacks against SSH credentials and terminal/VNC traffic. This run wired all SSH paths to known-host verification and prompt handling.

Other fixed issues include a local noVNC WebSocket proxy access-control weakness, SSH key-name path traversal in public-key reads, unsafe update-result DOM rendering, inline secret leakage in non-secret profile exports, and current vulnerable dependency versions in `golang.org/x/crypto` and `esbuild`.

Residual risk remains in user-selected insecure modes such as direct unencrypted VNC, user-configured `HostKeyInsecure`, and large local file transfer workflows that still process complete files in memory.

## 2. Scope Reviewed

Reviewed:

- Go backend packages under `internal/`: API bindings, sessions, SSH, VNC, SFTP, storage, secrets, updater, raw/telnet clients, recording, validation, known hosts, config, app startup.
- Frontend JavaScript under `frontend/src/js`: settings/update UI, host-key event handling, terminal and SFTP/VNC/GitHub surfaces, common DOM helpers.
- Dependency manifests and lockfiles: `go.mod`, `go.sum`, `frontend/package.json`, `frontend/package-lock.json`.
- Build and release scripts: `Makefile`, `build.sh`, `release.sh`, Wails config.
- Tests under `internal/**/*_test.go`.

Not in scope:

- Live network penetration testing of remote hosts.
- Reverse engineering packaged binaries.
- Manual verification of Wails runtime origin values in a built app.

## 3. Methodology

- Static source review of trust boundaries, file operations, SSH/VNC flows, Wails API bindings, frontend DOM sinks, update checks, storage, crypto, and secret handling.
- Dependency review with `govulncheck` and `npm audit`.
- Configuration review of Wails, build scripts, update manifests, and release paths.
- Threat modelling of desktop renderer compromise, local web-origin attacks, remote SSH/VNC attackers, and malicious update metadata.
- Denial-of-service review of network timeouts, WebSocket proxy behavior, transfer paths, and file ingestion.
- Regression tests added for fixed Go issues.

## 4. Risk Rating Method

Severity considered exploitability, attacker position, affected security property, and impact:

- Critical: practical remote compromise or credential disclosure without meaningful preconditions.
- High: credential/session compromise or machine-in-the-middle exposure on primary connection paths.
- Medium: meaningful local, renderer, or network-assisted exploit with constraints.
- Low: hardening issue, local DoS, or user-assisted exposure.
- Informational: checked area or non-security note.

Confidence reflects direct code evidence and verification coverage.

## 5. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---:|---:|---|---|---|
| SEC-001 | High | High | SSH sessions and jump hosts bypassed known-host verification | SSH sessions | Fixed |
| SEC-002 | High | High | VNC SSH tunnels accepted any SSH host key | VNC over SSH | Fixed |
| SEC-003 | Medium | High | Local noVNC WebSocket proxy used a guessable root endpoint | VNC WebSocket proxy | Fixed |
| SEC-004 | Medium | High | SSH public-key read accepted traversal key names | SSH key API | Fixed |
| SEC-005 | Medium | High | Update metadata rendered through dynamic HTML and inline handler | Settings/update UI | Fixed |
| SEC-006 | Medium | High | Non-secret profile export leaked inline SSH secrets | Profile export | Fixed |
| SEC-007 | High | High | `golang.org/x/crypto` vulnerable in reachable SSH code | Go dependencies | Fixed |
| SEC-008 | Medium | High | `esbuild` dev dependency matched current npm advisory | Frontend dependency | Fixed |
| SEC-009 | Low | Medium | Large local file upload paths remain memory-heavy | File transfer UX | Open |
| SEC-010 | Low | Medium | Direct VNC remains unencrypted by protocol choice | VNC direct mode | Open |

## 6. Detailed Findings

### Finding ID: SEC-001

#### Title

SSH sessions and jump hosts bypassed known-host verification

#### Severity

High

#### Confidence

High

#### Affected Files / Components

- `internal/sessions/manager.go`, SSH connection setup, now fixed at lines 657 and 1450-1502.
- `internal/sshclient/sshclient.go`, jump-host config, now fixed at lines 599 and 646-652.
- `frontend/src/js/app.js`, host-key response correlation, now fixed at lines 334-342.

#### Description

The project already contained known-host storage, a host-key verifier, and frontend prompt UI, but the main SSH session manager previously called the SSH client's default `Connect` path. That default path used `ssh.InsecureIgnoreHostKey()`. Jump hosts also built their own `ssh.ClientConfig` with `ssh.InsecureIgnoreHostKey()`.

#### Evidence

The fix now calls `ConnectWithHostKeyCallback(...)` for regular SSH sessions and propagates the final host-key callback into jump-host configs. A request ID is included in host-key prompt responses to prevent cross-session prompt confusion.

#### Attack Scenario

An attacker on the network path presents a fake SSH server or jump host. Without host-key verification, Nexus Terminal would accept the attacker's key, allowing credential capture, terminal tampering, or traffic interception.

#### Impact

Possible SSH credential theft, terminal command tampering, and loss of server authenticity.

#### Recommendation

Always use the known-host verifier for every SSH hop. Unknown host keys should follow the configured profile policy: strict, ask, accept-new, or explicit insecure.

#### Safer Example

Implemented: `Manager.hostKeyCallback` builds `sshclient.NewHostKeyVerifier(...)`, emits a prompt when needed, and `connectSSH` calls `ConnectWithHostKeyCallback(...)`.

#### Suggested Tests

Added regression coverage:

- `TestHostKeyCallbackAcceptNewStoresHost`
- `TestHostKeyCallbackStrictRejectsUnknownHost`
- `TestJumpHostClientConfigUsesFinalHostKeyCallback`

#### References

- CWE-295: Improper Certificate Validation, analogous host-key trust failure.
- OWASP ASVS 9.1.2: server certificate validation principles.

#### Assumptions / Limitations

Profiles may still explicitly choose `HostKeyInsecure`; that is a user-selected insecure mode and remains visible as residual risk.

### Finding ID: SEC-002

#### Title

VNC SSH tunnels accepted any SSH host key

#### Severity

High

#### Confidence

High

#### Affected Files / Components

- `internal/vncclient/vncclient.go`, VNC SSH config, now fixed at lines 49 and 89-95.
- `internal/sessions/manager.go`, VNC SSH callback wiring, now fixed at line 1016.

#### Description

VNC SSH tunnels established SSH connections with `ssh.InsecureIgnoreHostKey()`, allowing a network attacker to impersonate the SSH tunnel endpoint.

#### Evidence

The VNC client now requires `SSHHostKeyCallback` for tunneled SSH. The session manager supplies the same known-host/prompt path used by SSH sessions.

#### Attack Scenario

An attacker controlling DNS, Wi-Fi, or a gateway presents a fake SSH tunnel host for VNC traffic. The prior client accepted the key and tunneled VNC traffic through the attacker.

#### Impact

Possible VNC credential exposure, remote desktop traffic interception, and remote interaction tampering.

#### Recommendation

Require known-host verification for VNC SSH tunnels and reject tunnel setup if no callback is configured.

#### Safer Example

Implemented: `vncclient.Connect` returns an error if `cfg.SSHHostKeyCallback` is nil, and uses the provided callback in `ssh.ClientConfig`.

#### Suggested Tests

Covered indirectly by session host-key callback tests and VNC package tests.

#### References

- CWE-295.
- OWASP ASVS 9.1.2.

#### Assumptions / Limitations

VNC tunnel host-key policy defaults to ask mode because VNC profiles do not currently expose a separate host-key policy field.

### Finding ID: SEC-003

#### Title

Local noVNC WebSocket proxy used a guessable root endpoint

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/vncclient/vncclient.go`, WebSocket proxy setup, now fixed at lines 171-183 and 255-260.

#### Description

The local VNC WebSocket proxy listened on `127.0.0.1` with a random port, but accepted upgrades on `/` and allowed any Origin. A malicious local web page could scan localhost ports and attempt to connect to an active proxy.

#### Evidence

The proxy now generates a 128-bit random token and exposes only `ws://127.0.0.1:<port>/<token>` to the Wails frontend.

#### Attack Scenario

A user visits a malicious web page while a VNC session is active. The page scans localhost WebSocket ports and connects to the proxy endpoint, potentially reading or injecting VNC traffic.

#### Impact

Possible local cross-origin access to VNC session traffic if the port is discovered.

#### Recommendation

Require an unguessable per-session URL token and continue binding only to loopback.

#### Safer Example

Implemented: `newProxyToken()` uses `crypto/rand` and the proxy route is registered only at the token path.

#### Suggested Tests

Added `TestNewProxyTokenIsRandomHex`.

#### References

- CWE-346: Origin Validation Error.
- OWASP ASVS 14.4.3: cross-origin resource restrictions.

#### Assumptions / Limitations

`CheckOrigin` still returns true because Wails/noVNC origin behavior needs built-app verification. The unguessable path materially reduces exposure.

### Finding ID: SEC-004

#### Title

SSH public-key read accepted traversal key names

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`, SSH key API, now fixed at lines 1653-1708.

#### Description

`GetSSHPublicKey(name)` previously built `~/.ssh/<name>.pub` directly from a renderer-provided name. Unlike key generation, it did not restrict names to simple key basenames. Names such as `../somefile` could read `~/.ssh/../somefile.pub`.

#### Evidence

The fix centralizes key name validation through `sshKeyPairPaths(...)`, allowing only alphanumeric characters, hyphens, and underscores.

#### Attack Scenario

If renderer-side JavaScript is compromised or a malicious plugin/flow can invoke the Wails API, it can request public-key contents outside the intended `.ssh` key namespace if the target path ends in `.pub`.

#### Impact

Local file disclosure for matching `.pub` paths under paths reachable through traversal.

#### Recommendation

Treat key names as basenames, not paths. Apply the same validation to generate, read, and delete operations.

#### Safer Example

Implemented: `GetSSHPublicKey`, `GenerateSSHKey`, and `DeleteSSHKey` now all use `sshKeyPairPaths`.

#### Suggested Tests

Added:

- `TestSSHKeyPairPathsAcceptsSafeName`
- `TestSSHKeyPairPathsRejectsTraversal`
- `TestSSHKeyPairPathsRejectsNestedPath`

#### References

- CWE-22: Path Traversal.

#### Assumptions / Limitations

The API still intentionally lists and reads legitimate SSH public keys in `~/.ssh`.

### Finding ID: SEC-005

#### Title

Update metadata rendered through dynamic HTML and inline handler

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `frontend/src/js/settings.js`, update-check UI, now fixed at lines 940-958.

#### Description

Update metadata from GitHub or a configured update manifest was inserted into `innerHTML`, including an inline `onclick` that interpolated `info.releaseURL`. A malicious or compromised update metadata source could inject script into the Wails renderer.

#### Evidence

The fix constructs DOM nodes, sets text via `textContent`, and attaches a click listener with `addEventListener`.

#### Attack Scenario

An attacker controlling a self-hosted update manifest returns a release URL containing a quote and JavaScript payload. The prior inline handler interpolation could execute renderer JavaScript.

#### Impact

Renderer code execution. In a Wails app, renderer execution can call exposed backend APIs, increasing impact.

#### Recommendation

Never render remote metadata through string-built HTML. Use `textContent` and event listeners.

#### Safer Example

Implemented in `settings.js` with `createElement`, `document.createTextNode`, and `addEventListener`.

#### Suggested Tests

No frontend unit harness exists. Verified with `npm run build`.

#### References

- CWE-79: Cross-Site Scripting.
- OWASP ASVS 5.3: output encoding.

#### Assumptions / Limitations

Other static modal templates still use `innerHTML`; reviewed high-risk dynamic remote metadata paths first.

### Finding ID: SEC-006

#### Title

Non-secret profile export leaked inline SSH secrets

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`, Wails export wrapper, now fixed at lines 683-684.
- `internal/storage/store.go`, storage export, now fixed at lines 356-358 and 726-738.

#### Description

The plain profile export path accepted `includeSecrets=false` but profiles can still contain inline `ssh.proxyPassword` and `ssh.totpSecret` fields. Those fields could be serialized despite a non-secret export request.

#### Evidence

The fix redacts `ProxyPassword` and `TOTPSecret` for non-secret exports in both API and storage layers.

#### Attack Scenario

A user creates a plain profile export believing secrets are excluded. The exported JSON contains proxy credentials or TOTP secrets that are then shared, backed up, or stored insecurely.

#### Impact

Credential leakage and potential 2FA bypass for affected profiles.

#### Recommendation

Redact inline secret fields whenever `includeSecrets=false`. Prefer encrypted export for any export that intentionally includes secrets.

#### Safer Example

Implemented `redactProfileSecrets(...)` in storage and API packages.

#### Suggested Tests

Added:

- `TestExportProfilesRedactsInlineSSHSecrets`
- `TestRedactProfileSecretsClearsInlineSSHSecrets`

#### References

- CWE-200: Exposure of Sensitive Information.
- OWASP ASVS 8.3: sensitive data protection.

#### Assumptions / Limitations

The encrypted export path intentionally includes selected profile data and is password-protected.

### Finding ID: SEC-007

#### Title

`golang.org/x/crypto` vulnerable in reachable SSH code

#### Severity

High

#### Confidence

High

#### Affected Files / Components

- `go.mod`, `golang.org/x/crypto`, now fixed at line 14.
- `go.mod`, indirect `golang.org/x/sys`, now updated at line 45.
- Reachable SSH code in `internal/sshclient`, `internal/vncclient`, `internal/sftpclient`.

#### Description

`govulncheck` reported seven reachable vulnerabilities in `golang.org/x/crypto@v0.51.0`, including SSH DoS/panic issues, FIDO/U2F security-key interaction bypass risk, and SSH agent forwarding constraint handling issues.

#### Evidence

`govulncheck` identified GO-2026-5033, GO-2026-5020, GO-2026-5019, GO-2026-5018, GO-2026-5017, GO-2026-5013, and GO-2026-5006 as reachable before the update. The dependency is now `v0.52.0`, and rerun output reports: "No vulnerabilities found. Your code is affected by 0 vulnerabilities."

#### Attack Scenario

A malicious or compromised SSH server, SSH agent, key material, or protocol peer triggers vulnerable SSH library behavior during connection, channel use, key parsing, or agent forwarding.

#### Impact

Possible client panic, denial of service, weakened security-key interaction, or agent forwarding constraint mishandling depending on the CVE path.

#### Recommendation

Keep `golang.org/x/crypto` at or above `v0.52.0` and run `govulncheck` in CI.

#### Safer Example

Implemented: `go get golang.org/x/crypto@v0.52.0`.

#### Suggested Tests

Ran `govulncheck` and `go test ./...`.

#### References

- https://pkg.go.dev/vuln/GO-2026-5033
- https://pkg.go.dev/vuln/GO-2026-5020
- https://pkg.go.dev/vuln/GO-2026-5019
- https://pkg.go.dev/vuln/GO-2026-5018
- https://pkg.go.dev/vuln/GO-2026-5017
- https://pkg.go.dev/vuln/GO-2026-5013
- https://pkg.go.dev/vuln/GO-2026-5006

#### Assumptions / Limitations

`govulncheck` also noted vulnerabilities in required modules that are not called by this code.

### Finding ID: SEC-008

#### Title

`esbuild` dev dependency matched current npm advisory

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `frontend/package.json`, `esbuild`, now fixed at line 22.
- `frontend/package-lock.json`.

#### Description

`npm audit` reported GHSA-67mh-4wv8-2f99 for `esbuild <=0.24.2`: any website could send requests to the dev server and read responses under affected versions.

#### Evidence

The frontend previously resolved `esbuild@0.24.2`. It now uses `^0.28.0`, and `npm audit --json` reports zero vulnerabilities.

#### Attack Scenario

During local development, a malicious website could interact with the esbuild dev server and read responses if a developer runs an affected dev server.

#### Impact

Possible local development data exposure.

#### Recommendation

Use a fixed esbuild release and keep npm audit in CI.

#### Safer Example

Implemented: `npm install esbuild@0.28.0`.

#### Suggested Tests

Ran `npm audit --json` and `npm run build`.

#### References

- https://github.com/advisories/GHSA-67mh-4wv8-2f99

#### Assumptions / Limitations

This is a devDependency, so production exploitability is lower than runtime dependencies.

### Finding ID: SEC-009

#### Title

Large local file upload paths remain memory-heavy

#### Severity

Low

#### Confidence

Medium

#### Affected Files / Components

- `frontend/src/js/terminal.js`, dropped-file paste/upload flows.
- `internal/api/api.go`, `SFTPUploadData`.

#### Description

Some upload paths read whole files into memory and send base64 payloads to Go before uploading. This can be expensive for very large local files.

#### Evidence

`SFTPUploadData` decodes the entire base64 payload before calling session upload. The frontend drag/drop paste path also reads file contents into strings.

#### Attack Scenario

A local user or compromised renderer submits very large files, causing high memory use and potential application unresponsiveness.

#### Impact

Local denial of service.

#### Recommendation

Add size limits for paste-as-text, prefer path-based streaming upload for large files, and surface user-facing warnings for files above a threshold.

#### Safer Example

Use file path streaming upload where possible and reject in-memory uploads above a configured maximum.

#### Suggested Tests

Add boundary tests for maximum accepted in-memory upload size and UI tests for large-file warnings.

#### References

- CWE-400: Uncontrolled Resource Consumption.

#### Assumptions / Limitations

This is primarily local/user-assisted. It was left open to avoid changing UX and transfer semantics during the security fix pass.

### Finding ID: SEC-010

#### Title

Direct VNC remains unencrypted by protocol choice

#### Severity

Low

#### Confidence

Medium

#### Affected Files / Components

- `internal/vncclient/vncclient.go`, direct TCP VNC mode.
- VNC profile settings.

#### Description

Direct VNC connections use raw TCP to the VNC server. VNC is commonly unencrypted unless tunneled or separately protected.

#### Evidence

The direct branch calls `net.DialTimeout` to the VNC endpoint. SSH tunnel mode is available and now host-key verified.

#### Attack Scenario

On an untrusted network, direct VNC traffic may expose screen contents or credentials depending on server configuration.

#### Impact

Sensitive remote desktop data exposure.

#### Recommendation

Warn users before direct VNC connections and prefer SSH tunnel mode by default.

#### Safer Example

Default new VNC profiles to SSH tunnel where feasible, or show an explicit "unencrypted direct VNC" warning.

#### Suggested Tests

Add UI tests that direct VNC profiles show a warning before connection.

#### References

- OWASP ASVS 9.1: communications security.

#### Assumptions / Limitations

Some VNC servers may provide their own encryption; this audit did not test remote server configurations.

## 7. Denial-of-Service Review

Fixed:

- Updated `golang.org/x/crypto` to resolve reachable SSH panic/deadlock/DoS vulnerabilities.
- Added random-token access control to the local VNC WebSocket proxy, reducing local cross-origin abuse.

Reviewed and not changed:

- HTTP clients for GitHub and update checks use finite timeouts.
- SSH/Telnet/RAW connection setup uses timeouts.
- VNC WebSocket bridge is intentionally long-lived and uses no read/write timeout.
- Large in-memory file upload/paste paths remain a local DoS risk; see SEC-009.

## 8. Dependency and Supply-Chain Review

Checks run:

- `govulncheck`: fixed reachable `golang.org/x/crypto` vulnerabilities by updating to `v0.52.0`; rerun found zero reachable vulnerabilities.
- `npm audit --json`: fixed `esbuild` advisory by updating to `0.28.0`; rerun found zero vulnerabilities.
- `npm run build`: passed after dependency update.

Notes:

- `xterm` packages are deprecated upstream in favor of `@xterm/*`, but no current audit advisory was reported in this run.
- `go.mod` still has transitive modules with vulnerabilities that `govulncheck` reports as not called by this code.

## 9. Secrets and Sensitive Data Review

Fixed:

- Non-secret profile export now redacts inline `ProxyPassword` and `TOTPSecret`.
- SSH host-key verification now protects credential submission from basic MITM paths.

Reviewed:

- Vault uses Argon2id and AES-256-GCM with random salts/nonces.
- Vault file is written with `0600` permissions and store directories with `0700`.
- OS keychain is preferred where available.

Residual:

- Secret values still exist in process memory while sessions need them.
- Proxy password is still present in profile model state when entered inline.

## 10. Authentication and Authorisation Review

Fixed:

- SSH, jump host, and VNC-over-SSH authentication now has host authenticity checks before credential use.
- SSH host-key prompt responses are request-correlated.

Reviewed:

- SSH/TOTP flows use profile-specific secret callbacks.
- Agent forwarding is explicit profile configuration.
- Known-host policies include strict, ask, accept-new, and insecure.

Residual:

- `HostKeyInsecure` remains available for user-selected insecure connections.

## 11. Input Validation and Output Encoding Review

Fixed:

- SSH key names now reject traversal and nested path separators.
- Update metadata is rendered with DOM APIs instead of string-built HTML.
- Non-secret export redacts inline secret fields.

Reviewed:

- SQL operations use parameterized queries.
- Profile validation checks host, port, username, protocol-specific fields, and port-forwarding basics.
- Frontend frequently uses `textContent` or `escapeHtml`; remaining `innerHTML` static templates should still be reviewed during future frontend hardening.

## 12. Infrastructure and Deployment Review

Reviewed:

- Wails app binds Go services directly to the renderer. This makes frontend XSS more impactful than in a browser-only app.
- Build scripts use `set -euo pipefail`.
- Update checking is opt-in and does not auto-install binaries.

Residual:

- No CI configuration was present in the reviewed file list, so dependency scanning is not currently enforced by repository automation.

## 13. Logging, Monitoring, and Error Handling Review

Reviewed:

- Audit logger records profile create/update/delete, connection events, secret access, import/export.
- Errors are generally returned to the frontend with enough context for troubleshooting.

Residual:

- Some error messages include file paths, which is acceptable for a local desktop tool but should be considered if telemetry is later added.

## 14. Test Coverage and Security Regression Gaps

Added tests:

- API SSH key path validation and export redaction.
- Session host-key callback storage and strict rejection behavior.
- Jump-host host-key callback propagation.
- Storage export redaction.
- VNC proxy token generation.

Remaining gaps:

- No frontend unit test harness for DOM injection regressions.
- No integration test for full Wails host-key prompt round trip.
- No VNC WebSocket integration test verifying token-only connection behavior.
- No large-file upload boundary tests.

## 15. Prioritised Remediation Plan

Immediate:

- Keep the fixes in this audit and ensure they are merged.
- Add dependency scanning to CI: `govulncheck ./...` and `npm audit --audit-level=moderate`.

Short-term:

- Add a frontend test harness for update UI and other dynamic DOM surfaces.
- Add direct VNC warning UX and default to SSH tunnel where possible.
- Add explicit in-memory upload size limits.

Medium-term:

- Expose host-key policy for VNC SSH tunnels instead of always using ask mode.
- Review all Wails-exposed methods under a compromised-renderer threat model.
- Migrate deprecated `xterm` packages to maintained `@xterm/*` packages.

Long-term:

- Consider capability gating or confirmation for high-risk renderer-callable APIs such as file deletion, SSH key deletion, local shell selection, and profile import/export.
- Add signed update metadata if self-hosted update manifests become a production channel.

## 16. False Positives / Needs Manual Verification

- Wails/noVNC Origin behavior in a packaged app should be manually verified. Tokenized VNC WebSocket paths are in place, but Origin policy may still be tightened later.
- Direct VNC encryption depends on remote VNC server configuration and network placement.
- `HostKeyInsecure` is intentionally user-configurable; confirm UI wording makes the risk clear.
- Large-file memory usage thresholds need product decisions before enforcement.

## 17. Final Notes

Fix mode completed with code changes, tests, and dependency updates. The strongest residual concern is the general Wails threat model: a frontend XSS can call powerful local backend APIs. This audit fixed the confirmed dynamic update UI injection path and several backend trust-boundary issues, but future work should continue reducing string-built HTML and adding capability checks around sensitive local operations.
