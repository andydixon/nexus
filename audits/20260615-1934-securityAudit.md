# Security Audit Report

## 1. Executive Summary

This fix-mode audit reviewed Nexus Terminal, a Wails desktop client with a Go backend and JavaScript frontend for SSH, Telnet, RAW TCP, VNC, SFTP, GitHub, recordings, local shells, encrypted secrets, and profile import/export.

The highest-risk issues fixed in this run were unsafe profile persistence/import validation, a local DOM XSS trigger in the file-drop modal, memory-exhaustion paths in import and drag-drop upload flows, vulnerable dependency versions, and release tooling that installed unpinned build dependencies. I also removed the stale insecure SSH connection helper so future code cannot silently bypass host-key verification.

Post-fix verification passed: `go test ./...`, focused Go tests, `npm run build`, `npm audit --json`, `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`, `bash -n build.sh release.sh`, and `git diff --check`.

Residual risk remains where users intentionally choose insecure protocols or workflows, especially direct VNC without SSH tunnelling. No high-confidence secrets or private keys were found in the repository search performed during this audit.

## 2. Scope Reviewed

Reviewed code and configuration:

- Go backend under `internal/`: API bindings, sessions, SSH, VNC, SFTP, storage, secrets, export/import, validation, updater, recordings, local shell, GitHub client, raw and telnet clients.
- Frontend JavaScript under `frontend/src/js`, especially file drop, terminal, settings, GitHub, profile, SFTP, VNC, notification, and import/export flows.
- Dependency manifests and lockfiles: `go.mod`, `go.sum`, `frontend/package.json`, `frontend/package-lock.json`.
- Build/release configuration: `build.sh`, `release.sh`, `.github/workflows/release-builds.yml`, `wails.json`.
- Existing tests under `internal/**/*_test.go`.

Not reviewed with live external testing:

- Real SSH/VNC/GitHub endpoints.
- Packaged binaries and platform installers.
- Manual UI testing inside a running Wails app.

## 3. Methodology

I used a two-pass review:

- Attack-surface mapping of Wails API methods, import/export paths, file handling, DOM sinks, dependency/build chain, protocol clients, local proxy behavior, and credential flows.
- Defensive validation from source and tests, followed by smallest safe fixes with regression tests.

Tools/checks used:

- Static review with `rg`, direct source reads, and line evidence.
- `go test ./...`.
- Focused tests for API, storage, export/import, validation, SSH client, and sessions.
- `npm audit --json`.
- `npm run build`.
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- `bash -n build.sh release.sh`.
- `git diff --check`.

## 4. Risk Rating Method

Severity considered attacker position, exploit preconditions, likely impact, and whether sensitive credentials or code execution could be affected.

- Critical: practical remote compromise or credential disclosure with minimal preconditions.
- High: credible dependency, credential, or supply-chain compromise.
- Medium: user-assisted or renderer/local attack that can affect confidentiality, integrity, or availability.
- Low: hardening, defense-in-depth, or intentionally insecure user-selected modes.
- Informational: verified area, non-exploitable issue, or test gap.

Confidence is High where source evidence and regression checks directly support the finding.

## 5. Attack Surface Map

Entry points:

- Wails-bound Go API methods in `internal/api/api.go`.
- Profile create/update/import/export and encrypted `.nexus` imports.
- Drag-and-drop file upload/paste in `frontend/src/js/terminal.js`.
- SSH, Telnet, RAW TCP, VNC, SFTP, GitHub API, and local shell sessions.
- Local VNC WebSocket proxy on loopback.
- Build/release automation and dependency installation.

Trust boundaries:

- Frontend renderer to Wails API.
- User-selected/imported local files to backend parsers.
- Saved/imported profiles to outbound network clients.
- Remote hosts and GitHub/GitHub Enterprise APIs to frontend rendering.
- Local filesystem/keychain/vault to application process.
- CI/release environment to dependency registries and Go module proxy.

Attacker starting positions considered:

- Malicious imported profile bundle.
- Crafted local file or filename dropped into the terminal.
- Compromised renderer or malicious frontend input reaching Wails APIs.
- Network attacker on SSH/VNC/GitHub paths.
- Dependency registry or release-time supply-chain manipulation.

## 6. Findings Summary Table

| ID | Severity | Confidence | Title | Component | Status |
|---|---:|---:|---|---|---|
| SEC-20260615-001 | Medium | High | Profile persistence and imports accepted unsafe or malformed profiles | API, validation, import | Fixed |
| SEC-20260615-002 | Medium | High | Import and drag-drop upload paths allowed memory exhaustion | Import/export, SFTP upload | Fixed |
| SEC-20260615-003 | Medium | High | Dropped filenames were rendered through `innerHTML` | Frontend terminal file-drop modal | Fixed |
| SEC-20260615-004 | Medium | High | Unused SSH helper still defaulted to insecure host-key verification | SSH client | Fixed |
| SEC-20260615-005 | High | High | Dependency advisories affected `esbuild` and required `x/net` module versions | Dependencies | Fixed |
| SEC-20260615-006 | Medium | High | Release automation installed unpinned build dependencies | CI/release supply chain | Fixed |
| SEC-20260615-007 | Low | High | Direct VNC remains unencrypted when SSH tunnelling is disabled | VNC client | Open |

## 7. Detailed Findings

### Finding ID: SEC-20260615-001

#### Title

Profile persistence and imports accepted unsafe or malformed profiles

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`, `CreateProfile`, `UpdateProfile`, `ImportProfiles`, `ImportProfilesEncrypted`, lines 94-131, 715-728, 1176-1189, 1754-1791.
- `internal/validation/validation.go`, profile, GitHub, and VNC validation, lines 51-140 and 325-410.
- `internal/storage/store.go`, `ImportProfiles`, lines 365-380.

#### Description

The API comment said profile creation validates input, but the Wails API previously persisted profiles without calling the validation package. Plain JSON import also decoded into `[]*models.Profile` and dereferenced entries without rejecting `null`. A malicious or corrupt import could therefore crash the app or store profiles with unsafe GitHub API base URLs that would later receive a saved or prompted token.

#### Evidence

The fixed code now calls `validation.ValidateProfile` before create/update and validates every imported profile before writing. GitHub API base URLs must be HTTPS unless loopback, must be absolute, and must not include URL credentials. Import strategy and null entries are rejected before persistence.

Regression tests were added in `internal/api/api_test.go` and `internal/storage/storage_test.go`.

#### Attack Scenario

A user imports a profile JSON containing a GitHub profile with `http://evil.example/api/v3` as the API base URL, or a `null` profile entry. Before the fix, malformed data could be saved or crash import handling. If the malicious GitHub profile were later used, the app could send a token to an attacker-controlled endpoint.

#### Impact

Possible token exposure through malicious profile configuration, app crash during import, and fragile persisted state.

#### Recommendation

Validate all Wails-bound persistence inputs and imported profiles before storage. Reject non-HTTPS remote GitHub API URLs and malformed entries.

#### Safer Example

Implemented:

- `CreateProfile` and `UpdateProfile` call `validation.ValidateProfile`.
- `ValidateGitHubConfig` rejects remote HTTP and URL credentials.
- Import helpers reject oversized, null, or invalid entries before writes.

#### Suggested Tests

Implemented:

- Unsafe GitHub base URL rejection.
- Null import entry rejection.
- Oversized JSON import rejection.
- Storage-level null import entry rejection.
- GitHub and VNC validation cases.

#### References

- CWE-20: Improper Input Validation.
- CWE-200: Exposure of Sensitive Information to an Unauthorized Actor.
- OWASP API Security Top 10: API3 Broken Object Property Level Authorization / unsafe object properties.

#### Assumptions / Limitations

GitHub Enterprise over non-loopback HTTP is now rejected. If a legitimate deployment needs HTTP, it should be placed behind local loopback or HTTPS.

### Finding ID: SEC-20260615-002

#### Title

Import and drag-drop upload paths allowed memory exhaustion

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/api/api.go`, plain import and SFTP drag-drop upload, lines 715-718 and 1336-1350.
- `internal/exportimport/exportimport.go`, encrypted import file read, lines 37 and 131-144.
- `frontend/src/js/terminal.js`, drag-drop file upload cap, lines 10 and 829-849.

#### Description

Several local file paths loaded attacker-controlled or user-selected content fully into memory without a size cap. Plain profile JSON import accepted arbitrary string size from the renderer. Encrypted `.nexus` import used `os.ReadFile` on the selected path. Drag-drop SFTP upload read each browser `File` into an `ArrayBuffer`, converted it to base64, and sent it to the backend, where it was decoded into another byte slice.

#### Evidence

The fixed code adds:

- 5 MiB cap for plain profile JSON import.
- 10 MiB cap for encrypted `.nexus` import files.
- 25 MiB frontend and backend caps for inline drag-drop SFTP uploads.

Regression tests cover oversized plain import, encrypted import, and backend upload payload rejection.

#### Attack Scenario

A user drops or imports a very large crafted file. Before the fix, the renderer and backend could allocate multiple full copies, causing high memory pressure or an app crash.

#### Impact

Local denial of service against the desktop application.

#### Recommendation

Keep bounded inline payload paths and prefer streaming APIs for larger transfers. The existing native SFTP upload path streams local files and should remain the recommended path for large uploads.

#### Safer Example

Implemented size checks before decode/read where practical:

- `base64.StdEncoding.DecodedLen` guard before decoding drag-drop upload payloads.
- `os.Stat` size check before encrypted import `os.ReadFile`.

#### Suggested Tests

Implemented:

- `TestImportProfilesRejectsOversizedJSON`.
- `TestDecryptRejectsOversizedFile`.
- `TestSFTPUploadDataRejectsOversizedPayloadBeforeSessionLookup`.

#### References

- CWE-400: Uncontrolled Resource Consumption.
- OWASP ASVS 12.4: file and resource limits.

#### Assumptions / Limitations

The caps are conservative application-level limits. Larger profile bundles or drag-drop uploads now require a streaming implementation or the existing native SFTP upload flow.

### Finding ID: SEC-20260615-003

#### Title

Dropped filenames were rendered through `innerHTML`

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `frontend/src/js/terminal.js`, file-drop modal, lines 720-742.

#### Description

The file-drop modal interpolated the dropped filename into an `innerHTML` template. Local files can have names containing HTML metacharacters. In a Wails desktop app, DOM script execution is higher impact than a normal static page because the renderer can call privileged Wails APIs.

#### Evidence

The fixed modal escapes the file-drop title with `escapeHtml(title)` before injecting it into the template.

#### Attack Scenario

A user downloads or receives a file with a crafted name such as an HTML tag with an event handler, then drops it into a terminal. Before the fix, the modal could parse the filename as markup.

#### Impact

Potential renderer-side script execution and access to Wails-bound APIs from malicious DOM content.

#### Recommendation

Do not put user-controlled strings into `innerHTML`. Use `textContent` or escape before insertion.

#### Safer Example

Implemented `escapeHtml(title)` in the file-drop modal.

#### Suggested Tests

Add a frontend interaction test that drops a file named with `<img ...>`-style metacharacters and asserts the modal text is rendered literally. Manual Wails/browser verification was not run in this audit.

#### References

- CWE-79: Improper Neutralization of Input During Web Page Generation.
- OWASP ASVS 5.3: output encoding.

#### Assumptions / Limitations

This fix covers the reviewed file-drop title sink. Other `innerHTML` templates were spot-checked; future work should add an automated DOM-sink lint or test pass.

### Finding ID: SEC-20260615-004

#### Title

Unused SSH helper still defaulted to insecure host-key verification

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `internal/sshclient/sshclient.go`, `Connect`, lines 72-75.
- `internal/sshclient/sshclient_test.go`, regression test, lines 12-19.

#### Description

Current production session code uses explicit host-key callbacks, and no remaining reviewed SSH/VNC path calls `ssh.InsecureIgnoreHostKey`. However, the internal `sshclient.Connect` helper still defaulted to an insecure host-key callback. That made future misuse easy.

#### Evidence

The helper now fails closed and instructs callers to use `ConnectWithHostKeyCallback`. `rg` found no remaining `InsecureIgnoreHostKey` references after the change.

#### Attack Scenario

A future feature calls the convenience method instead of `ConnectWithHostKeyCallback`. Before the fix, that new path would silently accept any SSH host key.

#### Impact

Potential SSH machine-in-the-middle exposure if the helper were reused.

#### Recommendation

Keep host-key policy explicit on every SSH path. Do not provide insecure convenience APIs in internal packages.

#### Safer Example

Implemented: `Connect` returns `sshclient: host key callback is required`.

#### Suggested Tests

Implemented `TestConnectRequiresHostKeyCallback`.

#### References

- CWE-295: Improper Certificate Validation, analogous to SSH host-key validation.
- OWASP ASVS 9.1.2: server authenticity validation.

#### Assumptions / Limitations

This is a preventive fix. The reviewed production paths already used explicit callbacks.

### Finding ID: SEC-20260615-005

#### Title

Dependency advisories affected `esbuild` and required `x/net` module versions

#### Severity

High

#### Confidence

High

#### Affected Files / Components

- `frontend/package.json`, `esbuild`, line 22.
- `frontend/package-lock.json`, `esbuild` and platform packages.
- `go.mod`, `golang.org/x/net`, line 15.
- `go.sum`.

#### Description

`npm audit` reported `esbuild 0.28.0` affected by a high-severity advisory and a low-severity advisory. `govulncheck -show verbose` reported six vulnerabilities in required `golang.org/x/net v0.54.0` modules, though no reachable symbols were called by this code.

#### Evidence

The fixed versions are:

- `esbuild` bumped to `^0.28.1`.
- `golang.org/x/net` bumped to `v0.55.0`.

Post-fix `npm audit --json` reports zero vulnerabilities and `govulncheck` reports `No vulnerabilities found.`

#### Attack Scenario

For `esbuild`, a compromised or manipulated package installation environment could exploit known build-tool weaknesses. For `x/net`, future code changes could start calling vulnerable symbols if the module stayed on an affected version.

#### Impact

Supply-chain compromise risk during build/development and latent dependency risk.

#### Recommendation

Keep audited dependency patch versions current and include `npm audit` and `govulncheck` in regular release checks.

#### Safer Example

Implemented package/module upgrades and lockfile updates.

#### Suggested Tests

Implemented verification:

- `npm audit --json`.
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- `go test ./...`.
- `npm run build`.

#### References

- CWE-494: Download of Code Without Integrity Check.
- CWE-1104: Use of Unmaintained Third Party Components.
- Go vulnerability database advisories for `golang.org/x/net`.
- GitHub Advisory Database for `esbuild`.

#### Assumptions / Limitations

The `x/net` advisories were not reachable before the bump, according to govulncheck. The upgrade removes the latent module-level finding anyway.

### Finding ID: SEC-20260615-006

#### Title

Release automation installed unpinned build dependencies

#### Severity

Medium

#### Confidence

High

#### Affected Files / Components

- `.github/workflows/release-builds.yml`, Wails and npm install steps, lines 66-70.
- `release.sh`, Wails and npm install logic, lines 76-99.

#### Description

The release workflow installed the Wails CLI with `@latest` and used `npm install` rather than the lockfile-enforcing `npm ci`. Local release scripting also instructed or performed unpinned dependency installation in some paths.

#### Evidence

The fixed workflow installs the Wails CLI at the version resolved from `go.mod` and uses `npm ci`. `release.sh` now installs the pinned Wails CLI version from `go.mod` when needed and uses `npm ci` when `frontend/package-lock.json` exists.

#### Attack Scenario

A release build runs after a compromised or breaking upstream release, or after lockfile drift. Before the fix, release outputs could include unreviewed dependency changes.

#### Impact

Build reproducibility loss and supply-chain risk in release artifacts.

#### Recommendation

Use pinned module versions and lockfile-enforced installs in CI/release paths.

#### Safer Example

Implemented:

- `go install ...@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)`.
- `npm ci` in CI and release script.

#### Suggested Tests

Implemented:

- `bash -n build.sh release.sh`.
- Frontend build after `npm ci`.

#### References

- SLSA build integrity principles.
- CWE-494.

#### Assumptions / Limitations

The workflow still allows network dependency retrieval during CI. Stronger controls would include dependency caching, provenance, and artifact signing.

### Finding ID: SEC-20260615-007

#### Title

Direct VNC remains unencrypted when SSH tunnelling is disabled

#### Severity

Low

#### Confidence

High

#### Affected Files / Components

- `internal/sessions/manager.go`, direct VNC branch, lines 1012-1062.
- `internal/vncclient/vncclient.go`, direct TCP dial, lines 139-144.

#### Description

When VNC SSH tunnelling is disabled, the client makes a direct TCP VNC connection. Classic VNC deployments often provide weak or no transport encryption.

#### Evidence

The VNC client intentionally supports both SSH-tunnelled and direct modes. The direct branch uses `net.DialTimeout` to the VNC address.

#### Attack Scenario

A user connects directly to VNC over an untrusted network. A network attacker observes or tampers with remote desktop traffic, depending on the VNC server's negotiated security type.

#### Impact

Potential confidentiality and integrity loss for remote desktop sessions.

#### Recommendation

Keep direct VNC available for trusted networks, but clearly warn users when SSH tunnelling is disabled and consider requiring explicit confirmation for saved direct-VNC profiles.

#### Safer Example

Prefer enabling `vnc.sshTunnel` and verifying the SSH host key for the tunnel host.

#### Suggested Tests

Add a UI test that direct VNC profiles show a warning, and an integration test that tunnelled VNC requires a host-key callback.

#### References

- OWASP ASVS 9.1: secure channel requirements.

#### Assumptions / Limitations

This was not changed in this run because direct VNC can be a deliberate user choice for isolated networks.

## 8. Blackhat-Style Abuse Paths

Path A: malicious profile import to token exfiltration, now blocked.

1. Provide a profile bundle with a GitHub API base URL pointing to attacker infrastructure.
2. User imports and connects.
3. App prompts for or retrieves a token and sends it to the configured endpoint.
4. Defensive breakpoints now added: profile validation, HTTPS requirement for remote GitHub APIs, import validation before storage.

Path B: crafted dropped filename to renderer script execution, now blocked.

1. User receives a file with HTML metacharacters in the filename.
2. User drops it into a terminal.
3. Filename is rendered in the file-drop modal.
4. Defensive breakpoint now added: title is escaped before `innerHTML` insertion.

Path C: oversized file to app memory exhaustion, now blocked for reviewed inline paths.

1. User imports or drops an extremely large file.
2. Renderer/backend allocate full file contents and base64 copies.
3. App becomes slow or crashes.
4. Defensive breakpoints now added: import and inline upload size caps.

Path D: direct VNC over hostile network, still open by design.

1. User disables SSH tunnel and connects to a VNC server over an untrusted network.
2. Network attacker observes/tampers with VNC traffic if protocol security is weak.
3. Defensive breakpoint: use SSH tunnel; future UI warning recommended.

## 9. Denial-of-Service Review

Fixed DoS controls:

- Plain profile import now rejects payloads over 5 MiB.
- Encrypted profile import now rejects files over 10 MiB before `os.ReadFile`.
- Drag-drop SFTP upload now rejects files over 25 MiB in the frontend and backend before base64 decode.
- Null profile entries no longer panic import paths.

Checked and not found:

- SQL queries use parameters for profile and audit operations reviewed.
- GitHub repo fetch has timeouts and caps repository listing at 5000 repos.
- SSH/Telnet/RAW connection setup uses dial timeouts.
- VNC local proxy binds to loopback and uses a per-session token in the current code.

Residual DoS risks:

- RAW session history and terminal recordings can grow with long sessions.
- SFTP directory listing still depends on remote server behavior and could return very large directories.
- Direct VNC proxy bridges long-lived streams by design.

## 10. Dependency and Supply-Chain Review

Fixed:

- `esbuild` upgraded to `^0.28.1`; `npm audit --json` now reports zero vulnerabilities.
- `golang.org/x/net` upgraded to `v0.55.0`; govulncheck now reports no vulnerabilities.
- Release workflow uses `npm ci` and pinned Wails CLI version.
- `release.sh` uses `npm ci` when a lockfile exists and installs the Wails CLI at the version from `go.mod`.

Checked:

- `go.mod`, `go.sum`, `frontend/package.json`, `frontend/package-lock.json`.
- `.github/workflows/release-builds.yml`, `build.sh`, `release.sh`.

Notes:

- npm warns that `xterm` packages are deprecated in favor of the newer `@xterm/*` packages. That is not an active vulnerability from `npm audit`, but it is a maintenance risk.

## 11. Secrets and Sensitive Data Review

Checked:

- Repository search for private key markers, common cloud tokens, GitHub token prefixes, and obvious password assignments.
- Secret store uses AES-GCM with Argon2id-derived keys and writes vault data with `0600`.
- Profile export redacts inline SSH proxy/TOTP secrets when non-secret export mode is used.

No high-confidence committed secrets or private keys were found.

Residual notes:

- GitHub and remote connection tokens are still available to the running app process by design.
- Audit logging should continue to avoid logging raw secrets; reviewed audit messages do not include secret values.

## 12. Authentication and Authorisation Review

This is a desktop app, so there are no multi-user server authz roles in the reviewed code.

Checked/fixed:

- SSH host-key bypass helper removed by making `sshclient.Connect` fail closed.
- Current SSH and VNC tunnel paths use explicit host-key callbacks.
- GitHub API base URL validation now reduces token leakage to remote HTTP endpoints.

Residual:

- Users can still choose insecure protocols/modes such as direct VNC or legacy Telnet/RAW TCP.

## 13. Input Validation and Output Encoding Review

Fixed:

- API create/update now validates profiles before persistence.
- Plain and encrypted import validate every profile before storage.
- GitHub API URLs now require HTTPS except loopback and reject URL credentials.
- VNC validation supports VNC-specific host fallback and validates tunnel fields.
- File-drop filename is escaped before template insertion.

Checked:

- SSH key management already rejects path traversal key names with alphanumeric, hyphen, and underscore constraints.
- SFTP dropped filenames use `filepath.Base` before remote path construction.

## 14. Frontend HTML/JavaScript Robustness Review

Fixed:

- File-drop modal filename sink now uses `escapeHtml`.
- Drag-drop upload rejects files over 25 MiB before FileReader allocation.

Checked:

- Notifications render messages with `textContent`.
- GitHub repository card fields reviewed use `textContent` or escaping for remote metadata.

Not fully exercised:

- No live Wails UI/browser automation was run in this audit. Add frontend interaction tests for file-drop, import/export, and update UI in future work.

## 15. Infrastructure and Deployment Review

Fixed:

- GitHub Actions release workflow now uses pinned Wails CLI and `npm ci`.
- Local `release.sh` now uses pinned Wails CLI and `npm ci`.

Checked:

- No Docker, Kubernetes, Terraform, or cloud deployment config was present in the reviewed file list.
- Wails config was reviewed for frontend build/install commands.

Residual:

- Release artifacts are not signed/notarized in the reviewed workflow.
- CI workflow has `contents: write`, required for release attachment, but should remain limited to release jobs.

## 16. Logging, Monitoring, and Error Handling Review

Checked:

- Audit log records profile changes, import/export counts, connects/disconnects, and secret access without raw secret values.
- Fixed import validation returns actionable errors rather than panics.
- Frontend upload errors now surface a user-safe oversized-file message.

Residual:

- Some protocol errors are emitted directly to the UI. Continue avoiding credentials in error strings from remote services.
- Consider adding structured debug logs for rejected imports without dumping payloads.

## 17. Test Coverage and Security Regression Gaps

Added/updated tests:

- API rejects unsafe GitHub base URL.
- API rejects null and oversized imports.
- API rejects oversized inline SFTP upload before session lookup.
- Export/import rejects oversized encrypted files.
- Storage import rejects null profile entries and invalid strategy before looping.
- Validation covers GitHub URL and VNC host/tunnel cases.
- SSH client `Connect` requires explicit host-key callback.

Remaining gaps:

- Browser/Wails interaction tests for file-drop XSS and upload cap.
- Load tests for very large SFTP directories, RAW history, and recordings.
- End-to-end import/export tests through the actual Wails UI.
- Release workflow provenance/signing tests.

## 18. Prioritised Remediation Plan

Immediate:

- Keep the fixed tests in CI.
- Add `npm audit` and govulncheck to CI if not already present.
- Warn users when saving or connecting direct VNC profiles without SSH tunnelling.

Short-term:

- Replace deprecated `xterm` packages with maintained `@xterm/*` packages.
- Add frontend tests for DOM injection via filenames and imported profile metadata.
- Add streaming drag-drop upload support or route large drops through the native file upload path.

Medium-term:

- Add release artifact signing/notarization and checksums with provenance.
- Add application-level limits for RAW history, terminal recording size, SFTP listing size, and GitHub error body reads.

Long-term:

- Add a security-focused CI job: tests, `npm audit`, govulncheck, secret scanning, and static DOM-sink checks.
- Introduce a profile schema version and migration/normalization layer for imports.

## 19. False Positives / Needs Manual Verification

- `govulncheck -show verbose` initially reported module-level `x/net` advisories that were not reachable. The module was still upgraded to remove latent risk.
- Direct VNC is intentionally supported; risk depends on network trust and VNC server security type.
- File-drop XSS was fixed by code inspection; manual Wails UI verification should still be added.
- No live external service testing was performed.

## 20. Final Notes

The repo is in a materially better state after this run: profile data is validated before storage, local resource-consumption paths are bounded, the file-drop DOM sink is escaped, dependency scanners are clean, and release dependency installation is more reproducible.

The main residual security posture issue is user-selected insecure connectivity: Telnet/RAW/direct VNC remain useful but should be presented with clear safety context.
