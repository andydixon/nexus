# Functionality and Next Steps Report

## 1. Executive Summary

This fix pass improved robustness around local proxying, file imports, remote downloads, SSH key management, and frontend rendering. The app now fails earlier and more predictably when inputs are malformed or too large.

Highest-impact next steps:

- Manually verify packaged VNC connections after the new Origin allowlist.
- Add UI copy for newly enforced limits.
- Add CI coverage for the security checks run in this pass.
- Continue frontend cleanup around dynamic `innerHTML`.

## 2. Current Functional Risks

VNC runtime compatibility:

- The backend now allows missing, `wails://`, and loopback HTTP(S) origins. This should work for Wails/noVNC, but packaged app validation is still needed.

Large-file and large-download behavior:

- GitHub archive downloads now stop at 2 GiB. Very large repositories will fail fast.
- SSH config imports now reject files above 5 MiB or more than 5000 Host blocks.

SSH key generation:

- RSA key generation is limited to 2048, 3072, and 4096 bits and times out after 30 seconds.
- Slow systems should be checked with RSA 4096 to ensure the timeout is not too tight.

Frontend:

- SSH key manager dynamic rendering is safer. Other static `innerHTML` templates remain and should be reviewed when they take dynamic data.

## 3. Code Quality Improvements

Completed:

- Added focused helper functions for VNC Origin validation.
- Centralized GitHub endpoint construction and owner/repo parsing.
- Added reusable archive stream limiting.
- Added backend SSH key generation option validation.
- Added import bounds to the SSH config parser.
- Replaced SSH key manager dynamic modal HTML with DOM construction.

Recommended refactors:

- Move file-size and stream-size caps into named settings or a shared limits package if more subsystems need them.
- Add a small frontend helper for safe modal construction so feature code stops hand-rolling modal `innerHTML`.
- Consider a common backend "safe basename" helper for local and remote file operations.

## 4. Frontend Interaction and Error-Handling Improvements

Recommended:

- Show a specific VNC connection error if the proxy rejects an unexpected Origin in packaged builds.
- Show clear messages for SSH config files that exceed size or Host-block limits.
- Show clear messages for GitHub archives above the cap.
- Add a hostile-filename E2E test for the SSH key delete confirmation modal.
- Continue converting dynamic modal content to DOM nodes or `textContent`.

## 5. Testing Roadmap

Added in this pass:

- VNC Origin allowlist tests.
- SFTP upload parent-directory rejection tests.
- SSH config file and Host-block limit tests.
- SSH key generation option validation tests.
- GitHub path escaping and archive cap tests.
- Frontend regression checks for SSH key manager text rendering.

Next tests:

- Packaged Wails VNC smoke test on macOS, Windows, and Linux.
- Browser/E2E test for SSH key filenames containing `<`, `>`, quotes, and ampersands.
- HTTP integration test for GitHub archive download caps and escaped branch refs.
- UI tests for limit-aware error messages.
- CI job that runs Go tests, frontend tests, frontend build, `npm audit --omit=dev`, `govulncheck`, and `git diff --check`.

## 6. Recommended New Functionality

Useful additions:

- Preferences for maximum GitHub archive download size.
- A pre-download confirmation for large GitHub archives when Content-Length is known.
- Import preview for OpenSSH config showing Host count before persistence.
- A diagnostics panel for VNC proxy status, including local URL redaction, Origin decisions, and connection state.
- A unified "Limits" help page that lists SFTP upload caps, GitHub archive cap, SSH config import cap, and Leviathan file caps.

## 7. Next-Level Opportunities

Architecture:

- Centralize resource limits and expose them to both Go and frontend UI copy.
- Add a secure modal builder and migrate risky frontend dialogs gradually.
- Add structured security regression tests for every Wails method that accepts file paths, filenames, URLs, or base64 payloads.

Operations:

- Generate checksums for release artifacts.
- Generate an SBOM during release.
- Publish a short security hardening checklist in `docs/build-and-release.md`.

Developer experience:

- Add a `make security-check` target that runs the same commands used in this audit.
- Add pre-commit or CI checks for `gofmt`, `go test`, frontend regression tests, and `git diff --check`.

## 8. Prioritised Action Plan

Immediate:

- Manually test packaged VNC on all supported platforms.
- Keep the security patch tests in CI before release.

Short-term:

- Add user-facing messages for the new SSH config, GitHub archive, and SSH keygen limits.
- Add E2E coverage for hostile SSH key filenames.
- Add an HTTP test around GitHub archive download behavior.

Medium-term:

- Centralize app resource limits and expose them to UI/help.
- Migrate high-risk frontend dynamic HTML patterns to safe modal helpers.
- Add release checksums and SBOM generation.

Long-term:

- Add progressive/streaming UX for large GitHub and SFTP transfers.
- Build a cross-platform packaged-app smoke test suite for Wails integrations.

## 9. Caveats and Validation Needed

- VNC Origin compatibility must be validated in packaged builds, not only unit tests.
- The 2 GiB GitHub archive cap may need product tuning for users with very large repositories.
- RSA 4096 key generation timeout should be checked on lower-powered machines.
- The frontend build passed, but this pass did not interactively click through the SSH key modal in a real browser/WebView.
