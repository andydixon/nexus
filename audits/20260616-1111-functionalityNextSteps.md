# Functionality and Next Steps Report

## 1. Executive Summary

This fix pass moves Nexus closer to predictable large-file behavior: file-backed Leviathan documents stay file-backed for save/export, recordings parse incrementally, and unavoidable in-memory parsers now advertise and enforce clear limits.

The most useful next work is to surface these limits in the Leviathan UI and add user-facing guidance when an operation needs a smaller selection or a streaming implementation.

## 2. Current Functional Risks

Leviathan spectrogram conversion still uses bounded in-memory audio/image buffers because the current encoder/decoder APIs operate on byte slices. Very large images, MP3s, WAVs, or decoded MP3 output will now be rejected with an error rather than processed.

PDF, certificate, and key scanners still operate on bounded whole-document buffers for structural parsing. The cap avoids memory denial of service, but users working with very large firmware or document corpora may need range-based scans in future.

Recording playback is now line-streamed, but the returned API shape still collects parsed events before sending them to the frontend. Very long recordings are still protected by the playback cap; true progressive playback would require an API contract change.

## 3. Code Quality Improvements Completed

Chunk-first behavior:

- Leviathan working-copy save now writes with `Document.ForEachChunk`.
- Leviathan case export now streams `working.bin` into the zip file on disk.
- Recording playback parses line-by-line instead of reading and splitting the whole file.

Bounded read behavior:

- Spectrogram image/audio inputs reject oversize data before decode.
- Image decode checks dimensions before allocating decoded pixels.
- HTTP response helpers cap JSON and error bodies.
- SSH key, VNC key, vault, public-key, PDF, and BinSnoop file reads have explicit limits.

Responsiveness:

- Heavy Leviathan spectrogram/export operations no longer hold the engine lock while processing or showing native save dialogs.

## 4. Frontend Interaction and Error Handling

Recommended UI follow-up:

- Show limit-aware messages for spectrogram tools, such as image max size, audio max size, and decoded image dimension limits.
- For PDF/cert/key tools, show “scan a smaller file or selected range” guidance when caps are hit.
- Add a small status line in Leviathan for whether an operation is streaming, bounded-buffer, or selection-based.
- For recording playback, consider progressive playback loading so long recordings do not need all parsed events returned at once.

## 5. Testing Roadmap

Completed in this pass:

- Oversized spectrogram image dimensions are rejected before full decode.
- Leviathan bounded document reads reject oversize documents.
- Patched working-file writes preserve changed bytes.
- Oversized GitHub/update responses are rejected.
- Oversized SSH public keys, vault files, PDFs, and BinSnoop files are rejected.

Recommended next tests:

- Wails UI tests for Leviathan oversized image/audio/PDF error messages.
- A case-export test that opens a sparse file-backed document and verifies the zip is produced without materializing `working.bin`.
- A recording playback test with malformed lines and a long valid event stream.
- Browser interaction tests for the Leviathan tab while a long analysis is running.

## 6. Recommended New Functionality

Add range-based versions of whole-document Leviathan tools:

- `keys.scanRange`
- `certs.scanRange`
- `pdf.quickScan` for headers/xref/trailer without full structural recovery
- `spectrogram.renderRange` for selected audio windows

Add streaming media support later:

- Decode audio from file/range readers instead of complete byte slices.
- Stream WAV output directly to disk for spectrogram generation.
- Stream image insertion output to disk when carrier audio is large.

## 7. Caveats

The new caps trade permissiveness for stability. Files above the limits now fail fast instead of attempting best-effort processing.

The frontend build and regression checks passed, but I did not run a packaged Wails app manually in this pass.
