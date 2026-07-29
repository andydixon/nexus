# Functionality and Next Steps Report

## 1. Executive Summary

This fix-mode pass makes long-running Nexus processes more predictable:
reconnect prompts no longer accumulate runtime listeners, unfinished SFTP
uploads are bounded and cancelled with their owning sessions, and application
shutdown drains prompt, listener, session, and upload state. IPv6 literals now
work in the reviewed VNC, IRC tunnel, and connection-test paths.

The full backend, race, frontend, build, syntax, and dependency checks pass.
The highest-value next step is a packaged long-duration soak test using real
local and remote tabs. Unit tests prove the new ownership rules, but only a live
test will show whether CPU, memory, goroutine, descriptor, and remote-session
counts remain flat across thousands of open/close/reconnect cycles.

## 2. Current Functional Risks

- Application-level cleanup is now explicit, but remote libraries and OS
  resources need live soak validation with real SSH, SFTP, VNC, IRC, Telnet,
  RAW, and local shell sessions.
- SFTP partial-file removal is best-effort. If a transport is already dead,
  Nexus can close its local writer but may be unable to remove the remote
  partial file.
- External Leviathan extractors can run subprocesses without a shared timeout,
  output-byte cap, or file-count cap. This path should remain unexposed until
  those controls exist.
- Parser test coverage varies by format. Complex hostile files may still find
  correctness or performance edge cases not represented by current fixtures.
- Several frontend areas still render trusted templates through `innerHTML`.
  Existing dynamic values are escaped in reviewed paths, but DOM-based
  construction would be less fragile for future edits.
- Four Go module versions have advisories in code paths Nexus does not call.
  They are not current reachable vulnerabilities, but routine dependency
  refresh will reduce future uncertainty.
- POSIX permission tests do not establish equivalent Windows ACL behaviour.

## 3. Code Quality Improvements

- Make ownership visible in types and naming. Sessions should own reconnect
  contexts, uploads, protocol clients, terminal resources, and timers through a
  single idempotent close path.
- Introduce a small lifecycle registry for application-level Wails listeners.
  The new stored unsubscribe functions are correct; a shared helper would make
  future listeners harder to leak.
- Replace `time.After` in long-lived or high-frequency paths with owned timers
  when cancellation or reuse matters. The current reconnect prompt has bounded
  frequency, but an owned timer makes resource lifetime more explicit.
- Return structured cleanup results for unfinished uploads: local writer
  closed, remote partial removed, or remote removal unavailable. This would
  improve diagnostics without turning cleanup failure into a tab-close
  blocker.
- Centralise network endpoint formatting so every protocol consistently uses
  `net.JoinHostPort` and one port-validation helper.
- Keep frontend regression assertions aligned with tracked source. The audit
  removed assertions for the intentionally deleted VS Code tree and replaced
  the obsolete CGO flag check with the current pure-Go invariant.

## 4. Frontend Interaction and Error-Handling Improvements

- When a tab closes during an upload, display a brief non-blocking message that
  the upload was cancelled and whether a remote partial file may remain.
- Make credential-prompt cancellation explicit in the UI so a reconnect
  cancellation is distinguishable from a wrong password or timeout.
- Add an unobtrusive diagnostics panel showing counts only: open tabs, active
  protocol sessions, reconnect workers, pending prompts, streaming uploads,
  goroutines, and recent cleanup failures.
- Disable or debounce reconnect controls while a reconnect is already in
  progress.
- Prefer `textContent`, element creation, and event delegation for dynamic UI
  state. Keep `innerHTML` for reviewed static templates only.
- Ensure every async action rechecks that its tab/session still exists before
  updating the DOM. This prevents late remote responses from reviving stale
  state or producing confusing errors.
- Never put passwords, vault contents, tokens, private keys, bridge tokens, or
  terminal contents in diagnostics or frontend logs.

## 5. Testing Roadmap

Immediate automated coverage:

- Add tests that call `Service.Shutdown` and `Manager.Shutdown` twice to enforce
  idempotence.
- Add a race test where an SFTP chunk write, session close, and shutdown overlap.
- Add tests for a capacity race where two upload starts pass the pre-check and
  only one may claim the final slot.
- Add address-table tests for IPv4, DNS names, bare IPv6, and scoped IPv6
  literals.

Packaged soak coverage:

- Start with zero tabs and record CPU, RSS, goroutines, descriptors, and
  listener/upload counts.
- Repeat local terminal open/write/close and SSH open/reconnect/close at least
  1,000 times.
- Repeat cancelled credential prompts and interrupted SFTP uploads.
- Leave two idle tabs open for several hours and confirm resource counts and
  CPU settle rather than grow.
- Shut down and verify all subprocesses, sockets, PTYs, uploads, and runtime
  listeners terminate.

Protocol integration coverage:

- Real SFTP server: finish, cancel, disconnect mid-chunk, reconnect, dead
  transport, and remote partial-file cleanup.
- Real IPv6 endpoints for VNC and SSH-tunnelled IRC/VNC.
- Host-key accept/reject/cancel and secret prompt cancel/timeout.
- Slow or malicious peers for protocol read limits and cancellation.

Parser and frontend coverage:

- Fuzz the PE, ELF, Mach-O, SquashFS, PDF, archive, and transform entry points.
- Add DOM tests for tab close during asynchronous callbacks.
- Keep `npm test`, frontend build, `go vet`, focused race tests,
  `govulncheck`, and `npm audit` as release gates.

## 6. Recommended New Functionality

- Add a resource diagnostics view with current and peak counts for tabs,
  sessions, reconnects, prompts, uploads, goroutines, file descriptors where
  available, and local child processes.
- Add an optional "delete partial remote upload on disconnect" status/history
  entry, including a retry action after reconnect.
- Add a developer soak command that scripts repeated open/reconnect/close
  cycles against safe fixture servers and emits a machine-readable resource
  summary.
- Add an export-permission warning when the destination filesystem cannot
  enforce private file access.
- Add a dependency-health command for development builds that reports the Go
  toolchain version and last audit result without contacting external services
  during normal application use.

## 7. Next-Level Opportunities

- Build one lifecycle coordinator that owns application listeners, service
  uploads, sessions, reconnect workers, protocol clients, timers, and local
  subprocesses. A small dependency graph would make shutdown ordering
  testable.
- Add pprof-enabled development builds and automated before/after heap,
  goroutine, mutex, and CPU profile comparisons during soak tests.
- Introduce fault injection around remote writes, EOF, connection resets,
  prompt cancellation, delayed events, and shutdown.
- Wrap external analysis tools in a sandbox-oriented runner with a context
  deadline, process-group kill, private working directory, output byte/file
  limits, and a minimal environment.
- Add signed, reproducible release metadata and verify the expected Go
  toolchain, module graph, frontend lock file, and artefact hashes in CI.

## 8. Prioritised Action Plan

Immediate:

- Run a packaged local/remote tab soak test and capture resource graphs before
  and after `b5c7528`.
- Validate interrupted SFTP uploads against a real server.
- Confirm shutdown leaves no Nexus child process, PTY, listener, or open
  connection.

Short-term:

- Add shutdown-idempotence and overlapping upload/close race tests.
- Add resource counters and a development diagnostics view.
- Refresh modules with unreachable advisories under a dedicated compatibility
  change.
- Add live IPv6 integration tests.

Medium-term:

- Add parser fuzzing and external extractor limits.
- Reduce dynamic HTML construction and add DOM lifecycle tests.
- Add fault-injected protocol integration tests.

Long-term:

- Make packaged soak, vulnerability, dependency, race, and artefact checks part
  of release qualification.
- Track resource-count baselines so gradual lifecycle regressions fail before
  release.

## 9. Caveats and Validation Needed

- Automated checks passed, but no packaged Wails application was opened during
  this audit.
- No real remote protocol server was used, so remote partial-file behaviour and
  third-party client cleanup still need live confirmation.
- The CPU symptom was not reproduced under a profiler in this pass. The
  reconnect listener leak and upload leak were confirmed from source and fixed,
  but they should not be claimed as the only possible causes of historical
  high CPU without a profile from an affected long-running process.
- Filesystem permission behaviour was validated on the current POSIX host, not
  Windows ACLs or every network filesystem.
- `clear` reduces the lifetime of mutable sensitive byte slices but does not
  guarantee complete memory erasure in Go.
- `govulncheck` reports no reachable vulnerability after the toolchain update;
  its four module-only advisories still deserve normal upgrade testing.
- The deleted VS Code extension sources were not audited as current code. Only
  the still-tracked Go bridge was reviewed.
