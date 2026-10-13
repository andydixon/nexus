#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Andy Dixon
# release.sh — build and package Nexus Terminal release artefacts with cargo.
#
# Produces, under build/releases/<version>/ (repo root):
#   NexusTerminal-<os>-<arch>[.exe|.app] and NexusTerminal-<os>-<arch>.zip
# and next to them manifest-<os>-<arch>.json + version-<os>-<arch>.txt for the update server
# (updates/index.php), in the same format the Wails build produced.
#
# Targets default to this machine's native one; set NEXUS_RELEASE_TARGETS to a comma list such as
# "linux/amd64,windows/amd64" (windows/* from Linux needs the mingw-w64 toolchain and rustup target).
# CI (.github/workflows/release-builds.yml) builds every platform natively.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
cd "$HERE"

DEFAULT_UPDATE_URL="https://dixon.cx/downloads/nexus/manifest-{platform}-{arch}.json"
DEFAULT_RELEASE_BASE_URL="https://dixon.cx/downloads/nexus"

log()  { echo -e "\033[0;32m[nexus]\033[0m $1"; }
warn() { echo -e "\033[1;33m[nexus]\033[0m $1"; }

host_target() {
    local os arch
    case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; MINGW*|MSYS*|CYGWIN*) os=windows ;; *) os=unknown ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) arch=unknown ;; esac
    echo "$os/$arch"
}

rust_triple() {
    case "$1" in
        linux/amd64) echo x86_64-unknown-linux-gnu ;;
        linux/arm64) echo aarch64-unknown-linux-gnu ;;
        darwin/amd64) echo x86_64-apple-darwin ;;
        darwin/arm64) echo aarch64-apple-darwin ;;
        windows/amd64) if [ "$(uname -s)" = Linux ]; then echo x86_64-pc-windows-gnu; else echo x86_64-pc-windows-msvc; fi ;;
        windows/arm64) echo aarch64-pc-windows-msvc ;;
        *) return 1 ;;
    esac
}

VERSION="${NEXUS_VERSION:-$(git describe --tags --abbrev=0 2>/dev/null || echo dev)-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
export NEXUS_VERSION="$VERSION"
export NEXUS_UPDATE_URL="${NEXUS_UPDATE_URL:-$DEFAULT_UPDATE_URL}"
BASE_URL="${NEXUS_RELEASE_BASE_URL:-$DEFAULT_RELEASE_BASE_URL}"
RELEASE_ROOT="${NEXUS_RELEASE_ROOT:-$ROOT/build/releases}"
OUT="$RELEASE_ROOT/$VERSION"
mkdir -p "$OUT"
: > "$OUT/.release-files"

IFS=',' read -r -a TARGETS <<< "${NEXUS_RELEASE_TARGETS:-$(host_target)}"
errors=0
for target in "${TARGETS[@]}"; do
    os="${target%%/*}"; arch="${target##*/}"
    triple="$(rust_triple "$target")" || { warn "unknown target $target"; errors=$((errors+1)); continue; }
    base="NexusTerminal-${os}-${arch}"
    log "Building $target ($triple), version $VERSION"
    rustup target add "$triple" >/dev/null 2>&1 || true
    if ! cargo build --release --locked -p nexus --target "$triple"; then
        warn "build failed for $target"; errors=$((errors+1)); continue
    fi
    bin="target/$triple/release/nexus-terminal"
    case "$os" in
        darwin)
            NEXUS_BIN="$bin" packaging/macos/build-app.sh
            rm -rf "$OUT/$base.app"
            cp -R target/macos/NexusTerminal.app "$OUT/$base.app"
            (cd "$OUT" && rm -f "$base.zip" && zip -qry "$base.zip" "$base.app" && zip -qj "$base.zip" "$ROOT/LICENSE")
            ;;
        windows)
            cp "$bin.exe" "$OUT/$base.exe"
            (cd "$OUT" && rm -f "$base.zip" && zip -qj "$base.zip" "$base.exe" "$ROOT/LICENSE")
            ;;
        *)
            cp "$bin" "$OUT/$base"
            strip "$OUT/$base" 2>/dev/null || true
            stage="$(mktemp -d)"
            cp "$OUT/$base" "$stage/nexus-terminal"
            cp packaging/linux/nexus-terminal.desktop packaging/linux/install.sh "$ROOT/LICENSE" "$stage/"
            mkdir -p "$stage/app/assets/icon" && cp app/assets/icon/icon-256.png app/assets/icon/icon-512.png "$stage/app/assets/icon/"
            sed -i 's|\$HERE/../../app|$HERE/app|; s|\$HERE/../../target/release/nexus-terminal|$HERE/nexus-terminal|' "$stage/install.sh"
            (cd "$stage" && rm -f "$OUT/$base.zip" && zip -qr "$OUT/$base.zip" .)
            rm -rf "$stage"
            ;;
    esac
    echo "$os|$arch|$base.zip" >> "$OUT/.release-files"
done

published_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
while IFS='|' read -r os arch filename; do
    [ -z "$os" ] && continue
    sha="$(shasum -a 256 "$OUT/$filename" 2>/dev/null || sha256sum "$OUT/$filename")"; sha="${sha%% *}"
    size="$(wc -c < "$OUT/$filename" | tr -d ' ')"
    echo "$VERSION" > "$RELEASE_ROOT/version-${os}-${arch}.txt"
    cat > "$RELEASE_ROOT/manifest-${os}-${arch}.json" <<JSON
{
  "latestVersion": "${VERSION}",
  "publishedAt": "${published_at}",
  "releaseURL": "${BASE_URL}/",
  "files": [
    {"platform":"${os}","arch":"${arch}","filename":"${filename}","url":"${BASE_URL}/${filename}","sha256":"${sha}","size":${size}}
  ]
}
JSON
done < "$OUT/.release-files"
rm -f "$OUT/.release-files"
log "Artefacts in $OUT"
exit $errors
