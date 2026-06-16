# Security Audit Report

## 1. Executive Summary

This fix-mode audit reviewed the Wails API surface, local proxy behavior, file import paths, SSH key management, GitHub integration, frontend DOM sinks, dependency manifests, tests, and release workflow.

Six concrete issues were fixed:

- The local VNC WebSocket proxy accepted any browser `Origin` and did not cap client-to-proxy WebSocket message size.
- SFTP browser uploads accepted the special filename `..`, allowing a Wails caller to escape the selected remote directory when the name was joined.
- SSH config import parsed line-by-line but had no total file or Host-block cap.
- Backend SSH key generation trusted caller-supplied key type and RSA bit count and had no process timeout.
- GitHub repo/ref values were interpolated into API paths without escaping, and archive downloads had no explicit stream cap.
- SSH key manager UI interpolated backend errors and filesystem-derived key names into `innerHTML`.

Verification passed with targeted Go tests, full Go tests, frontend regression checks, frontend build, `npm audit --omit=dev`, `govulncheck`, and `git diff --check`.

## 2. Scope Reviewed

Reviewed:

- Go backend packages under `internal/`: API bindings, VNC proxy, GitHub client, SSH config import, SSH key management, SFTP upload helpers, validation, sessions, storage, secrets, updater, recording, Leviathan parser bounds, and tests.
- Browser-facing JavaScript under `frontend/src/js`, especially SSH key management, SFTP upload flows, VNC integration, and dynamic HTML usage.
- Frontend package manifest, lockfile, build scripts, and regression script.
- Go module dependencies and reachable vulnerability state.
- GitHub Actions release workflow.

Not fully live-tested:

- A packaged Wails desktop binary.
- Real noVNC connections inside the packaged WebView after the new Origin policy.
- Real GitHub Enterprise archive downloads above normal repository sizes.

## 3. Methodology

The review used static analysis, targeted source searches, dependency/advisory tooling, and focused regression tests. I mapped attacker-controlled inputs from the renderer and local files into Go methods, network clients, local HTTP/WebSocket services, subprocesses, remote SFTP paths, and DOM sinks. I prioritized issues that could be fixed with small behavior-preserving changes and backed by tests.

Commands run:

- `go test ./internal/vncclient ./internal/api ./internal/sshconfig ./internal/githubclient -count=1`
- `npm --prefix frontend test`
- `git diff --check`
- `go test ./... -count=1`
- `npm --prefix frontend run build`
- `npm --prefix frontend audit --omit=dev`
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`

## 4. Risk Rating Method

Severity reflects exploit prerequisite, affected trust boundary, likely impact, and availability/confidentiality/integrity outcome:

- Critical: remote unauthenticated compromise or secret disclosure.
- High: practical compromise of credentials, host access, or arbitrary code execution.
- Medium: meaningful local, renderer, remote service, or connected-server abuse with realistic prerequisites.
- Low: local robustness issues, bounded DoS, or defense-in-depth gaps.
- Informational: observations and hardening opportunities.

Confidence reflects direct code evidence and testability. Findings fixed here are High confidence because they map to concrete files and new regression tests.

## 5. Attack Surface Map

Entry points:

- Wails-bound `api.Service` methods callable from the renderer.
- Local VNC WebSocket proxy bound to `127.0.0.1` on a random port and random token path.
- SSH, Telnet, RAW TCP, VNC, SFTP, GitHub, updater, and Leviathan binary-analysis workflows.
- Profile import/export, encrypted `.nexus` import, OpenSSH config import, recording playback, SSH key generation/deletion, and drag/drop upload flows.

Trust boundaries:

- Renderer to Go backend.
- Local browser/WebView to loopback VNC proxy.
- Local filesystem files selected or scanned by the app.
- Remote SSH/SFTP/VNC/GitHub/update endpoints.
- OS keychain/encrypted vault to profile storage and session runtime.

Attacker starting positions considered:

- Malicious local webpage attempting loopback WebSocket access.
- Malicious or compromised renderer script.
- Malicious local filename or selected config/import file.
- Malicious GitHub Enterprise/API endpoint or unusual repo/ref metadata.
- Malicious connected SSH/SFTP/VNC host.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---|---|---|---|---|
| SEC-20260616-1702-001 | Medium | High | VNC proxy allowed all origins and uncapped WebSocket frames | `internal/vncclient` | Fixed |
| SEC-20260616-1702-002 | Medium | High | SFTP upload name `..` escaped selected remote directory | `internal/api` SFTP upload helpers | Fixed |
| SEC-20260616-1702-003 | Low | High | SSH config import lacked total file and Host-block bounds | `internal/sshconfig` | Fixed |
| SEC-20260616-1702-004 | Low | High | SSH key generation allowed expensive caller-controlled options without timeout | `internal/api` SSH key management | Fixed |
| SEC-20260616-1702-005 | Medium | High | GitHub repo/ref path interpolation and uncapped archive stream | `internal/githubclient` | Fixed |
| SEC-20260616-1702-006 | Medium | High | SSH key manager rendered key names/errors through dynamic `innerHTML` | `frontend/src/js/sshkeys.js` | Fixed |

## 7. Detailed Findings

### Finding ID: SEC-20260616-1702-001

#### Title

VNC proxy allowed all origins and uncapped WebSocket frames

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/vncclient/vncclient.go`
- `internal/vncclient/vncclient_test.go`
- Local noVNC WebSocket proxy

#### Description

The VNC proxy was bound to loopback and protected by a random token path, but its Gorilla WebSocket upgrader accepted every `Origin`. It also read WebSocket messages without an explicit message size limit. If a proxy URL token leaked through logs, UI state, injected renderer code, or another local channel, a non-local browser origin could attempt to drive the active VNC TCP connection or send oversized frames that force memory allocation.

#### Evidence

Before the fix, `websocket.Upgrader.CheckOrigin` returned `true` for every request, and the proxy called `ws.ReadMessage()` without `SetReadLimit`.

#### Attack Scenario

A malicious local webpage or injected renderer script obtains an active `ws://127.0.0.1:<port>/<token>` URL. Because all origins were accepted, it could connect from a non-app origin and send very large WebSocket messages or VNC protocol bytes into the bridged TCP connection.

#### Impact

Potential local cross-origin abuse of active VNC sessions if the token is exposed, and avoidable memory pressure from oversized client-to-proxy WebSocket frames.

#### Recommendation

Allow only Wails and loopback browser origins, keep accepting missing origins for non-browser clients, and cap inbound WebSocket message size.

#### Safer Example

Implemented `allowProxyOrigin`, `isLoopbackHost`, and `ws.SetReadLimit(maxWebSocketMessageBytes)`.

#### Suggested Tests

Added `TestAllowProxyOrigin` for missing, Wails, loopback, remote, lookalike, and malformed origins.

#### References

- CWE-346: Origin Validation Error
- CWE-400: Uncontrolled Resource Consumption
- OWASP ASVS 14.4: HTTP security configuration

#### Assumptions / Limitations

The packaged Wails WebView should be manually tested to confirm its runtime Origin is covered by the `wails://` allow rule.

### Finding ID: SEC-20260616-1702-002

#### Title

SFTP upload name `..` escaped selected remote directory

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`
- `internal/api/api_test.go`
- `SFTPUploadData`
- `SFTPUploadStart`

#### Description

`safeSFTPUploadName` normalized slash variants and used `path.Base`, but did not reject the special basename `..`. `path.Join(remoteDir, "..")` resolves to the parent directory, so a Wails caller could escape the selected upload directory.

#### Evidence

The previous rejection covered empty, `.`, `/`, and NUL, but not `..`. The upload methods then used `path.Join(remoteDir, safeName)`.

#### Attack Scenario

A malicious renderer call invokes chunked upload with `remoteDir="/home/user/upload"` and `fileName=".."`. The destination path resolves to `/home/user`, outside the selected directory.

#### Impact

Unexpected remote writes/truncation attempts outside the user's selected SFTP directory, depending on server permissions and target type.

#### Recommendation

Reject `..` after basename normalization.

#### Safer Example

Implemented `safeName == ".."` rejection.

#### Suggested Tests

Added `TestSafeSFTPUploadNameRejectsParentDirectory`.

#### References

- CWE-22: Path Traversal
- OWASP ASVS 12.3: File path control

#### Assumptions / Limitations

The browser `File.name` API normally does not produce `..`, but Wails API methods are still a trust boundary and should validate independently.

### Finding ID: SEC-20260616-1702-003

#### Title

SSH config import lacked total file and Host-block bounds

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `internal/sshconfig/sshconfig.go`
- `internal/sshconfig/sshconfig_test.go`
- `ImportSSHConfig`

#### Description

The OpenSSH config parser scanned files line-by-line, but it did not cap total file size or the number of accumulated Host blocks. A very large selected config could allocate a large slice of parsed blocks and profiles.

#### Evidence

`ParseFile` opened the file and appended Host blocks to an in-memory slice until EOF.

#### Attack Scenario

A user imports a hostile or accidentally huge SSH config containing thousands of Host blocks. The app spends unnecessary CPU and memory parsing and converting entries.

#### Impact

Local denial of service or UI unresponsiveness during import.

#### Recommendation

Reject oversized config files, set a scanner line cap, and reject excessive Host-block counts.

#### Safer Example

Implemented `maxConfigFileBytes`, `maxConfigLineBytes`, and `maxHostBlocks`.

#### Suggested Tests

Added oversized-file and excessive-Host-block tests.

#### References

- CWE-400: Uncontrolled Resource Consumption

#### Assumptions / Limitations

The selected caps are conservative for normal OpenSSH config files. Extremely large enterprise configs may need batch import support later.

### Finding ID: SEC-20260616-1702-004

#### Title

SSH key generation allowed expensive caller-controlled options without timeout

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`
- `internal/api/api_test.go`
- `GenerateSSHKey`

#### Description

The frontend exposes fixed key-type and bit-size choices, but the backend method accepted caller-supplied values directly and ran `ssh-keygen` without a context timeout. A malformed renderer call could request unsupported types or excessive RSA bit sizes, tying up the subprocess.

#### Evidence

`GenerateSSHKey` passed `keyType` to `ssh-keygen -t` and only checked `bits > 0` for RSA before adding `-b`.

#### Attack Scenario

A malicious renderer call requests a very large RSA key size. `ssh-keygen` consumes CPU for an extended period or fails slowly.

#### Impact

Local CPU denial of service and poor recovery if the subprocess hangs.

#### Recommendation

Validate allowed key types and RSA sizes server-side, and execute `ssh-keygen` with a timeout.

#### Safer Example

Implemented `validateSSHKeyGenerationOptions` and `exec.CommandContext` with a 30 second timeout.

#### Suggested Tests

Added validation tests for defaults, RSA defaults, oversized RSA sizes, and unsupported key types.

#### References

- CWE-400: Uncontrolled Resource Consumption
- CWE-20: Improper Input Validation

#### Assumptions / Limitations

The timeout is a safety net, not a substitute for platform-level process controls. Real key generation should still be manually tested on slower hardware.

### Finding ID: SEC-20260616-1702-005

#### Title

GitHub repo/ref path interpolation and uncapped archive stream

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/githubclient/githubclient.go`
- `internal/githubclient/githubclient_test.go`
- `FetchLatestCommit`
- `DownloadRepoArchive`

#### Description

Repository full names and refs were interpolated into REST API paths without escaping. The archive download also streamed response bodies to disk without an explicit maximum. A malformed renderer call, unusual branch name, or compromised GitHub Enterprise endpoint could create surprising request paths or consume disk.

#### Evidence

The client built paths with `fmt.Sprintf("%s/repos/%s/commits/%s", ...)` and copied archives with `io.Copy(file, resp.Body)`.

#### Attack Scenario

A renderer calls archive download with a malformed repo full name containing extra path segments, or a malicious GitHub Enterprise endpoint returns an unbounded archive response.

#### Impact

Potential confused API requests using the user's GitHub token and local disk exhaustion from unbounded archive streams.

#### Recommendation

Require `owner/repo` format, URL-escape each API path segment, and cap archive streams.

#### Safer Example

Implemented `splitRepoFullName`, `endpointURL`, `copyLimitedArchive`, and a 2 GiB archive cap.

#### Suggested Tests

Added tests for escaped refs, malformed repo names, archive limit rejection, and at-limit copies.

#### References

- CWE-20: Improper Input Validation
- CWE-22: Path Traversal style path confusion
- CWE-400: Uncontrolled Resource Consumption

#### Assumptions / Limitations

The 2 GiB archive cap may reject unusually large repositories. A future preferences setting could make this user-configurable.

### Finding ID: SEC-20260616-1702-006

#### Title

SSH key manager rendered key names/errors through dynamic `innerHTML`

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `frontend/src/js/sshkeys.js`
- `frontend/scripts/test-regressions.js`
- SSH key manager modal

#### Description

The SSH key manager interpolated backend error text and confirmation title/message/button text into `innerHTML`. Key names are derived from local `~/.ssh/*.pub` filenames, so a malicious local filename could inject HTML into the delete confirmation modal.

#### Evidence

The previous code used `list.innerHTML = `<div ...>${err}</div>`` and built confirmation modal HTML with `${title}`, `${message}`, and `${confirmLabel}`.

#### Attack Scenario

A local attacker creates an SSH public key file with an HTML-like filename. When the user opens the key manager and deletes it, the filename is interpolated into modal HTML.

#### Impact

DOM XSS in the Wails renderer, with possible access to app UI actions exposed to the renderer.

#### Recommendation

Render dynamic strings with `textContent` or DOM node construction.

#### Safer Example

Implemented text-node rendering for key load errors and confirmation modal dynamic text.

#### Suggested Tests

Added frontend regression checks to prevent the vulnerable interpolation pattern from returning.

#### References

- CWE-79: Cross-Site Scripting
- OWASP ASVS 5.3: Output encoding

#### Assumptions / Limitations

Other static-template `innerHTML` use remains in the frontend and should continue to be reviewed when it accepts dynamic values.

## 8. Blackhat-Style Abuse Paths

Path 1: Token leak to VNC abuse

- Prerequisite: an active VNC proxy URL leaks to a non-app webpage or injected script.
- Old path: any Origin could connect and send arbitrary WebSocket frames to the VNC TCP bridge.
- Breakpoint added: Wails/loopback Origin allowlist plus message cap.

Path 2: Renderer-to-remote path escape

- Prerequisite: malicious renderer code or direct Wails call.
- Old path: call SFTP upload with filename `..`, causing `path.Join(remoteDir, "..")`.
- Breakpoint added: `..` is rejected after basename normalization.

Path 3: Local filename to renderer script injection

- Prerequisite: crafted key filename in `~/.ssh`.
- Old path: key name appears in confirmation modal HTML.
- Breakpoint added: key names and errors use `textContent`.

Path 4: Malicious GitHub Enterprise response to disk pressure

- Prerequisite: user connects to compromised or malicious API base URL allowed by profile validation.
- Old path: archive stream was copied until EOF.
- Breakpoint added: Content-Length precheck and streaming cap.

## 9. Denial-of-Service Review

Fixed resource-control issues:

- VNC inbound WebSocket message cap.
- SSH config file size, line size, and Host-block caps.
- SSH key generation input validation and subprocess timeout.
- GitHub archive stream cap.

Previously fixed and rechecked from recent audit context:

- GitHub/update JSON response caps.
- Leviathan bounded reads and chunked save/export paths.
- SFTP inline/chunk upload caps.
- Secret vault and SSH key file read caps.

Residual DoS considerations:

- SFTP directory listings depend on remote server behavior and can still return large lists.
- GitHub repository enrichment intentionally caps total repositories but still performs concurrent commit lookups.
- Very large legitimate GitHub archives above 2 GiB now fail fast.

## 10. Dependency and Supply-Chain Review

Results:

- `npm --prefix frontend audit --omit=dev`: 0 vulnerabilities.
- `govulncheck ./...`: no vulnerabilities found.

Reviewed manifests:

- `go.mod`
- `go.sum`
- `frontend/package.json`
- `frontend/package-lock.json`
- `.github/workflows/release-builds.yml`

No high-confidence dependency findings were found in this pass.

## 11. Secrets and Sensitive Data Review

Checked:

- No obvious private keys, GitHub tokens, cloud keys, or hardcoded credentials were found in source outside documentation/examples.
- Non-secret profile export redaction was already covered by tests.
- Secret access logging records secret type, not value.
- GitHub Authorization headers are set only in HTTP requests and not logged by this code.

Residual:

- Plain exports without secrets are safer, but encrypted export remains the recommended path for portable backups.
- Local audit logs should continue to avoid host/user secrets in future features.

## 12. Authentication and Authorisation Review

This is a local desktop application, so there are no server-side user roles or tenant boundaries. Relevant controls reviewed:

- SSH host-key callback requirement for VNC SSH tunnels.
- Known-host storage and verification paths.
- Secret retrieval via keychain/vault and prompt callbacks.
- GitHub token validation before repo fetch.

No missing multi-user authorization checks apply to the current architecture.

## 13. Input Validation and Output Encoding Review

Fixed:

- SFTP upload basename `..`.
- SSH key generation key type and RSA bit sizes.
- GitHub repo full-name format and path segment escaping.
- SSH key manager dynamic output encoding.

Previously present controls:

- Profile validation rejects invalid hosts, ports, secret modes, GitHub HTTP non-loopback base URLs, and malformed protocol configs.
- Import profiles are size-capped and validated before persistence.

## 14. Frontend HTML/JavaScript Robustness Review

Fixed:

- SSH key manager load errors now render via `textContent`.
- SSH key deletion confirmation modal dynamic strings now render via DOM nodes/text.
- Regression script now checks these patterns.

Checked:

- Wails service bridge ordering remains covered by regression checks.
- SFTP drag/drop upload still uses chunked methods.
- Direct VNC warning flow remains covered by regression checks.

Residual:

- Frontend still contains many static `innerHTML` templates. Future reviews should focus on any template that accepts local filesystem, remote host, profile, update, or GitHub data.

## 15. Infrastructure and Deployment Review

Reviewed:

- GitHub Actions release workflow.
- Wails build config.
- Frontend build scripts.

No new high-confidence release workflow finding was fixed. The workflow uses pinned major versions for common actions and default GitHub token permissions scoped to release attachment needs.

Residual:

- Consider adding checksums/SBOM generation for release artifacts.
- Consider a CI job for `go test ./...`, frontend regression tests, `npm audit --omit=dev`, and `govulncheck`.

## 16. Logging, Monitoring, and Error Handling Review

Fixed:

- SSH key manager no longer injects raw error strings into HTML.
- GitHub malformed repo names now fail before request construction.
- SSH keygen timeout returns a specific timeout error.

Checked:

- Error messages added in this pass do not include tokens, passwords, or key material.
- Existing audit logger records profile/session actions and secret access metadata.

## 17. Test Coverage and Security Regression Gaps

Added:

- VNC Origin allowlist tests.
- SFTP parent-directory upload name tests.
- SSH key generation option validation tests.
- SSH config oversized file and Host-block cap tests.
- GitHub URL escaping, malformed full-name, and archive cap tests.
- Frontend static regression checks for SSH key manager safe rendering.

Remaining gaps:

- No packaged Wails/noVNC integration test for Origin behavior.
- No E2E browser test for SSH key modal rendering with hostile filenames.
- No integration test for GitHub archive cap against an HTTP test server.

## 18. Prioritised Remediation Plan

Immediate:

- Manually test VNC connections in a packaged Wails app to verify the Origin allowlist.
- Keep the new security tests in CI.

Short-term:

- Add an E2E test for SSH key manager hostile filenames.
- Add an HTTP integration test for GitHub archive caps and escaped refs.
- Surface user-friendly errors for new caps in the UI.

Medium-term:

- Add CI security checks for `govulncheck`, `npm audit --omit=dev`, and `git diff --check`.
- Add release artifact checksums and an SBOM.

Long-term:

- Continue replacing dynamic frontend `innerHTML` where data provenance is mixed.
- Add configurable large-download limits for GitHub archives and SFTP operations.

## 19. False Positives / Needs Manual Verification

- VNC Origin policy should be validated in macOS, Windows, and Linux packaged Wails builds.
- The 2 GiB GitHub archive cap should be validated against expected user repository sizes.
- The SSH keygen 30 second timeout should be checked on slow hardware, especially for RSA 4096.

## 20. Final Notes

This pass focused on concrete, testable hardening rather than broad speculative rewrites. The remaining risk is mostly runtime validation and UX around newly explicit limits. The repo still has an unrelated sidebar CSS spacing change in the working tree that was not part of this security audit.
