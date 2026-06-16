# Security Audit Report

## 1. Executive Summary

This fix-mode audit focused on file-reading and response-reading paths that could allocate complete files or unbounded remote bodies in memory.

The main risk fixed was local denial of service from large user-selected files, imported Leviathan artefacts, recordings, SSH key files, vault files, and remote update/GitHub API responses. Where the codebase already had a chunked document model, saves and exports now use it. Where a parser still requires a complete byte slice, the code now rejects oversized inputs before allocation.

Verification passed with targeted package tests, `go test ./...`, frontend regression tests, frontend build, and `git diff --check`.

## 2. Scope Reviewed

Reviewed and changed:

- Leviathan file-backed document save, case export, spectrogram image/audio conversion, PDF parsing, and BinSnoop file helpers.
- Wails API helpers that read SSH public keys and Leviathan image/audio inputs.
- SSH and VNC tunnel private-key loading.
- GitHub client and update-checker HTTP response handling.
- Recording playback parsing.
- Encrypted secrets vault loading.

Not live-tested:

- A packaged Wails desktop binary.
- Real remote SSH/VNC/GitHub endpoints.
- Very large real-world media corpora beyond automated guard tests.

## 3. Methodology

I searched for `ReadFile`, `ReadAll`, and whole-document `Range(0, size)` patterns, then classified each as chunked, bounded, embedded/test-only, or unsafe. Fixes were kept to low-risk local changes:

- Prefer `Document.ForEachChunk` for writing/exporting working file bytes.
- Stream line-oriented recordings with `bufio.Scanner`.
- Limit HTTP response reads with `io.LimitReader`.
- Add `os.Stat` size checks before unavoidable bounded `os.ReadFile` calls.
- Add parser-specific caps before whole-document reads.

## 4. Findings Summary

| ID | Severity | Confidence | Title | Component | Status |
|---|---:|---:|---|---|---|
| SEC-20260616-001 | Medium | High | Leviathan save/export and spectrogram tools could materialize large files | Leviathan API, spectrogram | Fixed |
| SEC-20260616-002 | Medium | High | Remote API/update responses were read without size limits | GitHub client, updater | Fixed |
| SEC-20260616-003 | Medium | High | Local app data/key reads lacked size caps | SSH keys, VNC keys, vault, BinSnoop | Fixed |
| SEC-20260616-004 | Low | High | Recording playback loaded the whole capped recording file | Recording playback | Fixed |
| SEC-20260616-005 | Low | Medium | Some parsers still require complete bounded buffers | PDF, cert/key scanners, media decoders | Accepted with caps |

## 5. Detailed Findings

### SEC-20260616-001: Leviathan full-file allocation paths

Leviathan save/export and spectrogram operations could duplicate entire working files in memory. Case export also built the zip archive in memory before writing it.

Fixed in:

- `internal/api/leviathan.go`
- `internal/leviathan/spectrogram/spectrogram.go`
- `internal/leviathan/pdf/pdf.go`

Changes:

- Working-copy save writes via `Document.ForEachChunk`.
- Case export streams `working.bin` directly into `zip.Writer` backed by the destination file.
- Spectrogram encode/view/insert rejects oversized image/audio documents before bounded decode.
- Image decoding checks compressed byte size and decoded pixel dimensions before full image decode.
- PDF parsing rejects files above 128 MiB before whole-document structural parsing.
- Long-running spectrogram/export tools no longer hold the Leviathan engine lock while doing heavy work or native save dialogs.

### SEC-20260616-002: Unbounded remote response reads

GitHub and update-check response bodies could be read without an explicit maximum.

Fixed in:

- `internal/githubclient/githubclient.go`
- `internal/updater/updater.go`

Changes:

- GitHub JSON responses are capped at 4 MiB.
- GitHub error bodies are capped at 64 KiB.
- Update release/manifest responses are capped at 1 MiB.
- Repository archive downloads continue to stream to disk instead of memory.

### SEC-20260616-003: Local key/vault/helper reads lacked caps

Several local helper paths read files directly even though the expected files are small.

Fixed in:

- `internal/api/api.go`
- `internal/sshclient/sshclient.go`
- `internal/sessions/manager.go`
- `internal/secrets/secrets.go`
- `internal/leviathan/binsnoop/common.go`
- `internal/leviathan/binsnoop/binsnoop.go`

Changes:

- SSH public keys are capped at 64 KiB.
- SSH private keys for SSH and VNC tunnel auth are capped at 1 MiB.
- Secret vault files are capped at 10 MiB.
- BinSnoop direct file helpers are capped at 64 MiB; the UI path already uses a bounded buffer.

### SEC-20260616-004: Recording playback whole-file read

Recording playback had a 100 MiB file cap, but still read and split the full file in memory.

Fixed in:

- `internal/recording/recorder.go`

Changes:

- Playback now opens the recording and scans line-by-line.
- Individual playback lines are capped at 8 MiB.
- Existing malformed-event tolerance is preserved.

## 6. Residual Risk

Some operations still need bounded complete buffers because downstream parsers or decoders require byte slices: image/audio spectrogram conversion, PDF structural parsing, certificate/key scanning, and small SSH/vault/public-key parsing. These now fail before allocation when inputs exceed explicit caps.

Truly streaming versions of every binary parser would be a larger Leviathan architecture project. The current fixes remove the unbounded allocation behavior while preserving tool behavior for normal files.

## 7. Verification

Passed:

- `go test ./internal/api ./internal/leviathan/spectrogram ./internal/leviathan/pdf ./internal/leviathan/binsnoop ./internal/updater ./internal/githubclient ./internal/secrets ./internal/recording ./internal/sshclient ./internal/sessions -count=1`
- `go test ./... -count=1`
- `npm --prefix frontend test`
- `npm --prefix frontend run build`
- `git diff --check`

New tests cover oversized GitHub/update bodies, oversized spectrogram images, bounded Leviathan document reads, chunked patched-file writes, oversized public keys, oversized vault files, oversized PDFs, and BinSnoop file caps.
