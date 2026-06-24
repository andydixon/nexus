# Security Audit Report

## 1. Executive Summary

This fix-mode audit reviewed the Nexus desktop app, VS Code extensions, local bridge process, frontend regression harness, prior audit findings, dependency manifests, and build/test configuration. The codebase is in a materially better posture than the earlier June audits: major Wails, SFTP, VNC proxy, GitHub API, import-size, SSH key generation, and frontend DOM issues were already fixed and covered by tests.

Two remaining issues were confirmed and fixed in commit `3a16a14`:

- The VS Code bridge's localhost HTTP server had no explicit header/body read timeouts and decoded JSON request bodies without a hard cap.
- VS Code webview CSP nonces were derived from `Date.now()`, which made a script allow-list nonce predictable rather than cryptographically random.

Verification passed with targeted bridge tests, full Go tests, frontend regression checks, frontend build, `npm audit --omit=dev`, `govulncheck`, JavaScript syntax checks, and `git diff --check`.

## 2. Scope Reviewed

Reviewed:

- Go backend packages under `internal/` and the `cmd/vscode-bridge` localhost JSON-RPC/SSE bridge.
- Browser-facing frontend JavaScript and regression checks under `frontend/`.
- VS Code extension webviews in `vscode/nexus/extension.js` and `vscode/leviathan/extension.js`.
- Dependency manifests: `go.mod`, `go.sum`, `frontend/package.json`, `frontend/package-lock.json`, VS Code extension package manifests.
- Build, release, and CI files including shell scripts and `.github/workflows/release-builds.yml`.
- Existing audit reports under `audits/` to avoid duplicate findings.

Not fully live-tested:

- Packaged VS Code extensions inside a real VS Code host after the CSP nonce change.
- Long-lived bridge event streams under deliberate slow-client conditions.
- Packaged Wails binaries on all target operating systems.

## 3. Methodology

I used static review, adversarial attack-surface mapping, dependency review, configuration review, DoS review, frontend/webview review, and targeted regression tests. I traced attacker-controlled inputs from webviews, the VS Code extension host, local HTTP requests, imported files, remote services, and session events into filesystem, process, network, DOM, and parser boundaries.

Commands run:

- `node --check vscode/nexus/extension.js`
- `node --check vscode/leviathan/extension.js`
- `node --check frontend/scripts/test-regressions.js`
- `go test ./cmd/vscode-bridge -count=1`
- `npm --prefix frontend test`
- `git diff --check`
- `go test ./... -count=1`
- `npm --prefix frontend run build`
- `npm --prefix frontend audit --omit=dev`
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`

## 4. Risk Rating Method

Severity reflects attacker position, exploit practicality, affected trust boundary, and confidentiality/integrity/availability impact. Confidence reflects direct source evidence and regression coverage.

- Critical: unauthenticated remote compromise or credential disclosure.
- High: practical credential theft, code execution, or privilege escalation.
- Medium: meaningful local, renderer, or connected-service abuse with realistic prerequisites.
- Low: defence-in-depth or bounded local robustness issue.
- Informational: hardening or validation gaps without a direct exploit path.

## 5. Attack Surface Map

Entry points:

- Wails-bound API methods callable from the renderer.
- VS Code extension webviews and their `postMessage` handlers.
- `cmd/vscode-bridge` localhost HTTP endpoints: `/health`, `/rpc`, `/events`, and `/runtime/event`.
- SSH, SFTP, Telnet, RAW TCP, VNC, GitHub, IRC, local shell, updater, recording, export/import, and Leviathan binary-analysis workflows.

Trust boundaries:

- Renderer/webview to Go backend.
- Local browser or extension host to localhost bridge.
- Local selected files and profile imports to parsers.
- Remote GitHub, SSH/SFTP/VNC/IRC/Telnet/RAW endpoints to client code.
- OS keychain/vault to profile/session runtime.

Attacker starting positions considered:

- Local unprivileged process attempting bridge resource exhaustion.
- Malicious or compromised extension webview state.
- Hostile remote service data rendered into webviews.
- Malicious selected local file or imported profile/export.
- Compromised connected host attempting terminal/session abuse.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---|---|---|---|---|
| SEC-20260624-0945-001 | Low | High | VS Code bridge accepted unbounded HTTP parser work | `cmd/vscode-bridge` | Fixed in `3a16a14` |
| SEC-20260624-0945-002 | Low | High | VS Code webview CSP nonces used predictable timestamps | `vscode/nexus`, `vscode/leviathan` | Fixed in `3a16a14` |

## 7. Detailed Findings

### Finding ID: SEC-20260624-0945-001

#### Title

VS Code bridge accepted unbounded HTTP parser work

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `cmd/vscode-bridge/main.go:120`
- `cmd/vscode-bridge/main.go:212`
- `cmd/vscode-bridge/main.go:230`
- `cmd/vscode-bridge/main.go:336`
- `cmd/vscode-bridge/main.go:346`
- `cmd/vscode-bridge/main_test.go:11`
- `cmd/vscode-bridge/main_test.go:28`
- `cmd/vscode-bridge/main_test.go:48`

#### Description

The bridge listens only on localhost and requires a random bearer token, but the HTTP server previously used default server timeouts and decoded JSON bodies directly from `r.Body`. A local process, or a compromised component with bridge token access, could keep connections open or send oversized JSON to force unnecessary parser work and memory use.

#### Evidence

Before the fix, `server := &http.Server{Handler: mux}` used default timeout values, and `/rpc` plus `/runtime/event` called `json.NewDecoder(r.Body).Decode(...)` without `http.MaxBytesReader`.

The fix adds explicit bridge limits at `cmd/vscode-bridge/main.go:50`, uses `newBridgeHTTPServer` at `cmd/vscode-bridge/main.go:126`, and applies `decodeLimitedJSON` in both POST handlers at `cmd/vscode-bridge/main.go:218` and `cmd/vscode-bridge/main.go:236`.

#### Attack Scenario

A local unprivileged process repeatedly connects to the bridge and sends slow headers or a large authenticated JSON body. Without timeouts and body caps, the bridge can spend disproportionate time and memory on unauthorised or oversized traffic.

#### Impact

Local denial of service against the VS Code extension bridge, causing sluggishness, failed RPCs, or event-stream disruption.

#### Recommendation

Set bounded HTTP server timeouts and header limits. Cap JSON request bodies before decoding. Keep long-lived SSE compatibility by avoiding a global `WriteTimeout` that would break event streams.

#### Safer Example

Implemented:

```go
r.Body = http.MaxBytesReader(w, r.Body, bridgeMaxJSONBodyBytes)
return json.NewDecoder(r.Body).Decode(dst)
```

#### Suggested Tests

Added tests that assert bridge timeout/header settings and oversized `/rpc` plus `/runtime/event` bodies are rejected before reflective service dispatch.

#### References

- CWE-400: Uncontrolled Resource Consumption
- OWASP ASVS 12.1: File and resource handling
- OWASP API Security Top 10 API4: Unrestricted Resource Consumption

#### Assumptions / Limitations

The practical attacker is local, because the bridge binds to `127.0.0.1`. The bearer token remains an important control, but it is not a substitute for parser and server resource limits.

### Finding ID: SEC-20260624-0945-002

#### Title

VS Code webview CSP nonces used predictable timestamps

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `vscode/nexus/extension.js:5`
- `vscode/nexus/extension.js:10`
- `vscode/nexus/extension.js:219`
- `vscode/nexus/extension.js:554`
- `vscode/nexus/extension.js:895`
- `vscode/nexus/extension.js:1061`
- `vscode/nexus/extension.js:1162`
- `vscode/nexus/extension.js:1260`
- `vscode/nexus/extension.js:1375`
- `vscode/nexus/extension.js:1618`
- `vscode/nexus/extension.js:1832`
- `vscode/leviathan/extension.js:5`
- `vscode/leviathan/extension.js:10`
- `vscode/leviathan/extension.js:189`
- `vscode/leviathan/extension.js:497`
- `vscode/leviathan/extension.js:773`
- `frontend/scripts/test-regressions.js:222`

#### Description

The VS Code webviews use CSP script nonces, but each nonce was previously `String(Date.now())`. CSP nonces are intended to be high-entropy, per-render allow-list values. A timestamp nonce is guessable and weakens CSP if an HTML injection primitive is introduced elsewhere.

#### Evidence

All `const nonce = String(Date.now());` occurrences in the VS Code extension files were replaced with `createCSPNonce()`, backed by `crypto.randomBytes(16).toString('base64')`.

#### Attack Scenario

An attacker finds or later introduces a webview HTML injection path that can emit a `<script nonce="...">` tag. A predictable timestamp nonce materially reduces the barrier to bypassing CSP, especially when the webview creation time is observable or inferable.

#### Impact

Defence-in-depth weakness for webview script execution controls. A full exploit still requires an injection path, but predictable nonces undermine the CSP layer that should contain that class of defect.

#### Recommendation

Generate CSP nonces with cryptographically secure random bytes for every webview render, and keep regression coverage preventing timestamp nonce reintroduction.

#### Safer Example

Implemented:

```js
function createCSPNonce() {
  return crypto.randomBytes(16).toString('base64');
}
```

#### Suggested Tests

Added regression assertions that both VS Code extension files use `crypto.randomBytes(16).toString('base64')` and that `const nonce = String(Date.now());` is absent under `vscode/`.

#### References

- CWE-345: Insufficient Verification of Data Authenticity
- CWE-693: Protection Mechanism Failure
- OWASP ASVS 14.4: Security headers

#### Assumptions / Limitations

I did not confirm a current exploitable webview injection path in these files during this pass. This fix preserves the CSP guarantee if a future DOM/HTML bug appears.

## 8. Blackhat-Style Abuse Paths

Path 1: Local bridge exhaustion.

Prerequisite: local code execution on the same machine. The attacker opens many slow or oversized bridge requests. Before the fix, default server settings and uncapped JSON decoding provided weaker resistance. Defensive breakpoint: `ReadHeaderTimeout`, `ReadTimeout`, `IdleTimeout`, `MaxHeaderBytes`, and `http.MaxBytesReader`.

Path 2: Future webview injection to script execution.

Prerequisite: an HTML injection sink in a VS Code webview. Predictable CSP nonces made it easier to craft a nonce-bearing script. Defensive breakpoint: cryptographic per-render nonces and continued escaping/text-node rendering.

## 9. Denial-of-Service Review

Fixed:

- Local bridge header/body parsing now has explicit limits.
- Oversized bridge JSON bodies are rejected before reflective RPC dispatch.

Previously fixed and still covered by tests:

- VNC WebSocket frame read limits.
- SFTP upload chunk and streaming size controls.
- SSH config import file and Host-block caps.
- GitHub archive stream cap.
- Update response caps.
- Leviathan and recording file-size limits in reviewed paths.

Residual DoS considerations:

- Long-lived `/events` streams intentionally remain open; they should be observed under slow-client conditions in VS Code.
- Remote SFTP operations still rely on remote server responsiveness and transport-level behaviour.
- Binary-analysis features should continue adding per-tool time and output caps as new parsers are added.

## 10. Dependency and Supply-Chain Review

`npm --prefix frontend audit --omit=dev` reported zero production vulnerabilities. `govulncheck` reported no reachable Go vulnerabilities. The release workflow uses pinned major versions of common GitHub Actions but still installs Wails dynamically from the module version; that is acceptable but should be monitored in release hardening.

## 11. Secrets and Sensitive Data Review

No new hard-coded credentials or private keys were found in the changed files. The bridge token remains generated with `crypto/rand` and is emitted to the extension process over stdout. Secret storage continues to use OS keychain and encrypted vault mechanisms reviewed in earlier audits.

## 12. Authentication and Authorisation Review

The bridge still requires `Authorization: Bearer <token>`. The fix does not change bridge authorisation semantics. No new auth bypass was identified in this pass. Host key handling, SFTP path validation, GitHub URL validation, and profile validation had existing regression coverage from prior fixes.

## 13. Input Validation and Output Encoding Review

Fixed:

- Bridge JSON input is bounded to 1 MiB before decoding.

Checked:

- VS Code webviews continue escaping interpolated profile, repository, SFTP, and Leviathan state before `innerHTML` usage in reviewed areas.
- Frontend regression tests include prior checks for SSH key manager error rendering and SFTP upload name handling.

## 14. Frontend HTML/JavaScript Robustness Review

Fixed:

- VS Code webview CSP nonces are now cryptographically random.

Checked:

- Existing frontend regression script still passes.
- JavaScript syntax checks pass for both VS Code extension files and the regression script.

Manual validation still needed:

- Open the Nexus and Leviathan VS Code webviews in a real VS Code host and confirm all panels load under the stricter nonce generation.

## 15. Infrastructure and Deployment Review

The GitHub Actions release workflow was reviewed for obvious command injection or secret leakage. No new finding was fixed here. It still uses broad `contents: write` permissions for release attachment, which is appropriate for the release job but should remain isolated to tag/manual workflows.

## 16. Logging, Monitoring, and Error Handling Review

Bridge oversized-body failures return structured JSON errors through the existing RPC error path. No sensitive values are logged by the new code. The bridge still logs startup/server failures through `log`.

## 17. Test Coverage and Security Regression Gaps

Added:

- Bridge server resource-limit tests.
- Oversized `/rpc` body test.
- Oversized `/runtime/event` body test.
- Regression assertions preventing timestamp CSP nonces in VS Code webviews.

Remaining gaps:

- No automated VS Code host integration test exercises all webview panels.
- No slowloris-style integration test measures bridge timeout behaviour over real sockets.
- No e2e test validates the long-lived SSE event stream after server timeout changes.

## 18. Prioritised Remediation Plan

Immediate:

- Manually smoke-test the packaged VS Code extension panels and bridge event stream.

Short-term:

- Add a small VS Code extension integration smoke test for webview creation and bridge RPC.
- Add a socket-level bridge test for slow headers and idle connections.

Medium-term:

- Centralise VS Code webview HTML helpers so CSP, escaping, and nonce generation are not duplicated.
- Add response-size caps and request timeouts in the Node extension bridge client.

Long-term:

- Add release-time security checks for packaged VSIX contents, binary hashes, and expected CSP coverage.

## 19. False Positives / Needs Manual Verification

- The CSP nonce issue is a defence-in-depth finding, not evidence of a currently exploitable XSS.
- Bridge DoS impact is local because the bridge binds to localhost.
- VS Code runtime behaviour was not manually validated inside the actual editor during this audit.

## 20. Final Notes

The confirmed issues were fixed and committed in `3a16a14`. The residual risk is mainly operational validation: verify the VS Code extension panels in a real host and keep expanding integration coverage for bridge and webview behaviour.
