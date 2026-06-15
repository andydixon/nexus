# Functionality and Next Steps Report

## 1. Executive Summary

This fix-mode audit improved immediate security and robustness around profile validation, imports, drag-drop upload, frontend encoding, dependencies, and release reproducibility.

The most useful next work is to turn these fixes into durable product quality: add frontend interaction tests, make large file upload streaming and consistent, warn users about intentionally insecure protocol choices, and strengthen release provenance.

## 2. Current Functional Risks

Direct VNC can still be used without SSH tunnelling. That is valid for trusted networks, but the UI should make the risk visible when profiles are saved or connected.

Drag-drop SFTP upload is now capped at 25 MiB because it still uses an in-memory browser-to-Go base64 path. Larger files should use the existing native SFTP upload flow or a future streaming drag-drop implementation.

Profile import now rejects malformed or unsafe profiles before storage. That is safer, but older third-party exports with missing defaults may need a clearer import error UI or a migration/repair preview.

RAW session history and recordings can still grow over long sessions. Users need limits, retention controls, and clear storage management.

## 3. Code Quality Improvements

Profile validation is now centralized enough for create/update/import, but profile normalization is still split between frontend defaults, Go defaults, and import-time cleanup. A schema-versioned import normalizer would make future protocol additions safer.

Frontend still has many `innerHTML` templates. Some are static or escaped, but a small helper pattern for safe modal construction would reduce future XSS mistakes.

Release scripts and CI now use pinned Wails and `npm ci`; follow-up work should avoid duplicate release logic between `build.sh`, `release.sh`, and GitHub Actions.

## 4. Frontend Interaction and Error-Handling Improvements

Add Wails/browser interaction tests for:

- Dropping a file with HTML metacharacters in its filename.
- Dropping a file over 25 MiB.
- Importing malformed and oversized profile bundles.
- Saving GitHub profiles with HTTP and HTTPS API base URLs.
- Creating direct VNC profiles and seeing a warning.

Improve import UX by showing which profile entry failed validation without exposing secret values.

## 5. Testing Roadmap

Immediate tests to add:

- Frontend DOM regression for file-drop title rendering.
- API tests for successful valid GitHub Enterprise HTTPS profile creation.
- Import tests for mixed valid/invalid profile arrays.

Short-term tests:

- End-to-end Wails smoke tests for create profile, connect prompt flow, import/export, SFTP upload, and VNC tunnel setup.
- Load tests for large SFTP directories, RAW captures, and recordings.
- CI security job with `npm audit`, govulncheck, and secret scanning.

Longer-term tests:

- Release workflow dry-run or artifact validation tests.
- Migration tests for profile schema changes.

## 6. Recommended New Functionality

Add a direct-VNC safety warning and a per-profile “prefer SSH tunnel” nudge.

Add streaming drag-drop SFTP upload for larger files, or route drag-drop files above 25 MiB into the native upload flow.

Add import preview: list profile names, protocols, validation warnings, and duplicate handling before writing.

Add recording retention settings: max recording size, max age, and one-click cleanup.

Add security diagnostics in settings: dependency check status, known-host summary, direct insecure profile count, and last audit-log events.

## 7. Next-Level Opportunities

Release hardening:

- Sign and notarize release artifacts where platform-appropriate.
- Publish checksums and provenance.
- Pin or cache dependency sources in CI.

Observability:

- Add structured non-sensitive debug logs for import rejection, update checks, VNC proxy lifecycle, and SFTP transfer failures.
- Add audit entries for validation failures that indicate suspicious imports, without storing payload contents.

Developer experience:

- Consolidate release/build dependency resolution into one reusable script.
- Add a frontend lint rule or custom check for unsafe `innerHTML` with dynamic values.

Accessibility/product quality:

- Ensure modal error states are keyboard reachable and announced.
- Add clearer status messages for blocked uploads and import validation failures.

## 8. Prioritised Action Plan

Immediate:

- Add direct VNC warning copy and tests.
- Add frontend file-drop XSS and oversize tests.
- Add CI jobs for `go test ./...`, `npm run build`, `npm audit`, and govulncheck.

Short-term:

- Replace deprecated `xterm` packages with maintained `@xterm/*` packages.
- Build import preview and validation repair UI.
- Add recording and RAW capture storage limits.

Medium-term:

- Implement streaming drag-drop upload.
- Add release signing, checksums, and provenance.
- Add schema-versioned profile import/export.

Long-term:

- Build a security/settings dashboard for insecure profiles, known hosts, audit status, and update health.
- Add comprehensive Wails end-to-end smoke coverage for core workflows.

## 9. Caveats and Validation Needed

The fixes were verified with automated local checks, but not with live Wails UI automation.

The new import validation may reject malformed legacy profiles that were previously accepted. That is intentional for safety, but the UX should help users repair old data.

The 25 MiB drag-drop upload cap is a guardrail, not a final large-file strategy. Streaming support would be the better long-term user experience.

Direct VNC was left open because some users need it on trusted networks; the next step is product warning and safer defaults, not silent removal.
