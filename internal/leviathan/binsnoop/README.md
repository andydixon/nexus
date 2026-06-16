# binsnoop

A pure-Go binary analysis library for detecting and extracting embedded files, firmware images, compressed data, filesystems, and more from binary blobs. Ported from the [binwalk](https://github.com/ReFirmLabs/binwalk) Rust codebase with like-for-like scanning logic.

**Zero external dependencies** — everything is self-contained, including the Aho-Corasick multi-pattern matcher, CRC32 validation, and binary structure parsing.

## Features

- **111 built-in signatures** covering archives, compression, filesystems, images, executables, firmware headers, cryptographic constructs, and more
- **Aho-Corasick** multi-pattern scanning with overlapping match support
- **Signature validation** — every magic byte match is verified by a dedicated parser that checks headers, checksums, and structural integrity
- **Confidence scoring** — results are ranked Low / Medium / High so callers can filter noise
- **Extraction support** — carve identified objects out of a binary, or invoke external tools (7z, unsquashfs, etc.) to fully extract them
- **Filtering** — include/exclude signatures by name, restrict short signatures to offset 0, or add custom signatures
- **JSON-friendly** — all result types carry `json` struct tags for easy serialisation

## Installation

```bash
go get binsnoop
```

Or add to your `go.mod`:

```
require binsnoop v0.0.0
```

## Quick Start

```go
package main

import (
    "fmt"
    "binsnoop"
)

func main() {
    scanner := binsnoop.New()

    // Scan a file
    results, err := scanner.ScanFile("firmware.bin")
    if err != nil {
        panic(err)
    }

    for _, r := range results {
        fmt.Printf("[0x%08X] %s (confidence: %d, size: %d)\n",
            r.Offset, r.Description, r.Confidence, r.Size)
    }
}
```

## API Reference

### Creating a Scanner

```go
// Default scanner with all 111 signatures
scanner := binsnoop.New()

// Scanner with options
scanner, err := binsnoop.NewWithOptions(binsnoop.Options{
    Include:    []string{"gzip", "squashfs", "uimage"},  // only these signatures
    Exclude:    []string{"copyright"},                    // remove these signatures
    FullSearch: true,                                     // match short sigs at any offset
    AdditionalSignatures: []binsnoop.Signature{...},      // add custom signatures
})
```

### Scanning

```go
// Scan raw bytes
results := scanner.ScanBytes(data)

// Scan a file on disk
results, err := scanner.ScanFile("/path/to/firmware.bin")
```

Both return `[]SignatureResult` sorted by offset.

### Analysis (Scan + Extraction)

```go
// Analyse raw bytes (with optional extraction)
analysis := scanner.AnalyzeBytes(data, "firmware.bin", true)

// Analyse a file on disk
analysis, err := scanner.AnalyzeFile("/path/to/firmware.bin", true)
```

Returns `*AnalysisResults` containing the file map and extraction outcomes.

### Carving

Extract a single identified object from raw data:

```go
err := binsnoop.CarveResult(data, results[0], "/tmp/output")
```

This writes the bytes at the result's offset/size to a file in the output directory.

### Listing Signatures

```go
names := binsnoop.ListSignatures()  // []string of all registered signature names
```

### Scanner Info

```go
scanner.SignatureCount()  // number of registered signatures (111)
scanner.PatternCount()    // total magic byte patterns across all signatures
```

## Types

### `SignatureResult`

```go
type SignatureResult struct {
    Offset             int    `json:"offset"`              // byte offset in the input data
    ID                 string `json:"id"`                  // unique identifier for this match
    Size               int    `json:"size"`                // size in bytes (0 = unknown)
    Name               string `json:"name"`                // signature name (e.g. "gzip")
    Confidence         uint8  `json:"confidence"`          // 0=Low, 128=Medium, 250=High
    Description        string `json:"description"`         // human-readable description
    AlwaysDisplay      bool   `json:"always_display"`      // show even if low confidence
    ExtractionDeclined bool   `json:"extraction_declined"` // parser advises against extraction
    PreferredExtractor *Extractor `json:"-"`               // extractor to use for this result
}
```

### `Options`

```go
type Options struct {
    Include              []string    // restrict to these signature names
    Exclude              []string    // remove these signature names
    FullSearch           bool        // short sigs match at any offset
    AdditionalSignatures []Signature // user-defined signatures
}
```

### `AnalysisResults`

```go
type AnalysisResults struct {
    FilePath    string                       `json:"file_path"`
    FileMap     []SignatureResult             `json:"file_map"`
    Extractions map[string]*ExtractionResult  `json:"extractions"`
}
```

### Confidence Levels

| Constant | Value | Meaning |
|----------|-------|---------|
| `ConfidenceLow` | 0 | Magic matched but weak validation |
| `ConfidenceMedium` | 128 | Header parsed and mostly valid |
| `ConfidenceHigh` | 250 | Full structural validation passed |

## Custom Signatures

You can add your own signatures at scanner creation:

```go
mySignature := binsnoop.Signature{
    Name:  "my_format",
    Magic: [][]byte{[]byte("MYMAGIC")},
    Parser: func(data []byte, offset int) (*binsnoop.SignatureResult, error) {
        // Validate the match and return a result
        if offset+16 > len(data) {
            return nil, fmt.Errorf("insufficient data")
        }
        return &binsnoop.SignatureResult{
            Offset:      offset,
            Size:        1024,
            Description: "My custom format, size: 1024 bytes",
            Confidence:  binsnoop.ConfidenceHigh,
        }, nil
    },
    Description: "My custom file format",
}

scanner, _ := binsnoop.NewWithOptions(binsnoop.Options{
    AdditionalSignatures: []binsnoop.Signature{mySignature},
})
```

## Supported Signatures

### Archives (12)
gzip, zip, tarball, 7zip, rar, cab, deb, cpio, arj, iso9660, dahua\_zip

### Compression (10)
lzma, bzip2, xz, zstd, lz4, lzop, lzfse, zlib, compress'd

### Filesystems (13)
squashfs, cramfs, jffs2, yaffs, romfs, ext, fat, ntfs, apfs, btrfs, logfs, ubi, ubifs

### Images & Media (7)
png, jpeg, bmp, gif, svg, riff, pdf

### Executables (2)
elf, pe

### Firmware (32)
uimage, uboot, trx, tplink, tplink\_rtos, dlob, arcadyan, chk, seama, cfe, jboot\_arm, jboot\_stag, jboot\_sch2, packimg, binhdr, autel, android\_sparse, android\_bootimg, dtb, dlink\_tlv, dlke, shrs, dkbs, dms, encfw, encrpted\_img, wince, mh01, csman, dxbc, matter\_ota, rtk

### Cryptographic (17)
aes\_sbox, aes\_forward\_table, aes\_reverse\_table, aes\_rcon, aes\_acceleration\_table, rsa, openssl, gpg\_signed, pem\_certificate, pem\_public\_key, pem\_private\_key, pkcs\_der\_hash, luks, crc32, sha256, md5

### Miscellaneous (18)
copyright, wind\_kernel, vxworks\_symtab, ecos, linux\_kernel, linux\_boot\_image, linux\_arm\_zimage, linux\_arm64\_boot\_image, efigpt, mbr, uefi\_pi\_volume, uefi\_capsule, pchrom, pcapng, pjl, srecord, dmg, qnx\_ifs, dpapi, qcow

## Extraction

Many signatures have associated extractors that invoke external command-line tools. When you call `AnalyzeFile` or `AnalyzeBytes` with `extract: true`, binsnoop will:

1. Carve the identified data to a temporary file
2. Run the configured external tool (e.g. `7z x`, `unsquashfs`, `jefferson`)
3. Return the extraction results including output directory and success/failure status

**Common external tools used:**

| Tool | Signatures |
|------|-----------|
| `7z` | zip, cpio, arj, iso9660, xz, lzma, squashfs, dmg, efigpt |
| `gzip` | gzip |
| `bzip2` | bzip2 |
| `zstd` | zstd |
| `lz4` | lz4 |
| `unsquashfs` | squashfs |
| `jefferson` | jffs2 |
| `unyaffs` | yaffs |
| `tsk_recover` | ext, fat, ntfs |
| `uefi-firmware-parser` | uefi\_pi\_volume, uefi\_capsule, pchrom |
| `simg2img` | android\_sparse |
| `dtc` | dtb |

## How It Works

### Scanning Algorithm

1. **Short-signature pass** — signatures marked `Short` (e.g. zlib, MBR) are checked only at offset 0 (unless `FullSearch` is enabled)
2. **Aho-Corasick scan** — all magic byte patterns are compiled into an Aho-Corasick automaton for efficient multi-pattern matching. When a high-confidence sized match is found, scanning restarts from the end of that match
3. **Validation** — every magic match is passed to its signature parser which validates headers, checksums, and structure
4. **Post-processing** — results are sorted by offset, overlapping/duplicate matches are resolved (higher confidence wins), and unknown sizes are estimated based on the next significant match or EOF

### Architecture

```
binsnoop.go       Public API (New, ScanBytes, ScanFile, AnalyzeFile, CarveResult, etc.)
scanner.go        Core scan engine (Aho-Corasick integration, post-processing)
match.go          Self-contained Aho-Corasick trie with overlapping match support
parse.go          Generic binary structure parser (u8/u16/u24/u32/u64, big/little endian)
types.go          Core types (SignatureResult, Signature, Extractor, Options, etc.)
common.go         Utility functions (CRC32, endian helpers, string helpers)
extract.go        Carving and external tool execution
magic.go          Signature registry — wires all 111 signatures together
sig_archives.go   Archive signature parsers
sig_compression.go Compression signature parsers
sig_filesystems.go Filesystem signature parsers
sig_images.go     Image/media signature parsers
sig_executables.go Executable signature parsers
sig_firmware.go   Firmware header signature parsers
sig_crypto.go     Cryptographic signature parsers
sig_misc.go       Miscellaneous signature parsers
```

## License

This project is a Go port of [binwalk](https://github.com/ReFirmLabs/binwalk) by ReFirmLabs. See the original project for licensing details.
