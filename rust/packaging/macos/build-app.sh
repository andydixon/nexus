#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Andy Dixon
# Build NexusTerminal.app, a macOS application bundle. Run on a Mac with Xcode command-line tools:
#
#   rust/packaging/macos/build-app.sh            # universal (Apple Silicon + Intel) if both
#                                               # targets are installed, else this Mac's arch
#   rust/packaging/macos/build-app.sh --dmg      # also wrap it in a .dmg
#
# Output: rust/target/macos/NexusTerminal.app (and NexusTerminal-<version>.dmg).
# Set NEXUS_BIN=/path/to/binary to bundle an existing build instead of compiling.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
cd "$ROOT"
VERSION=${NEXUS_VERSION:-$(sed -n 's/^version = "\(.*\)"/\1/p' Cargo.toml | head -1)}
OUT="$ROOT/target/macos"
APP="$OUT/NexusTerminal.app"

if [ -n "${NEXUS_BIN:-}" ]; then
  BIN="$NEXUS_BIN"
else
  targets=""
  for t in aarch64-apple-darwin x86_64-apple-darwin; do
    if rustup target list --installed 2>/dev/null | grep -qx "$t"; then targets="$targets $t"; fi
  done
  [ -n "$targets" ] || targets=" $(rustc -vV | sed -n 's/^host: //p')"
  bins=""
  for t in $targets; do
    echo "building $t"
    cargo build --release -p nexus --target "$t"
    bins="$bins target/$t/release/nexus-terminal"
  done
  mkdir -p "$OUT"
  set -- $bins
  if [ $# -gt 1 ]; then
    lipo -create "$@" -output "$OUT/nexus-terminal-universal"
    BIN="$OUT/nexus-terminal-universal"
  else
    BIN="$1"
  fi
fi

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/NexusTerminal"
chmod +x "$APP/Contents/MacOS/NexusTerminal"
cp "$HERE/NexusTerminal.icns" "$APP/Contents/Resources/NexusTerminal.icns"
cp "$HERE/../../../LICENSE" "$APP/Contents/Resources/LICENSE"
sed "s/__VERSION__/$VERSION/g" "$HERE/Info.plist" > "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

if command -v plutil >/dev/null; then plutil -lint "$APP/Contents/Info.plist"; fi
# Ad-hoc sign so Apple Silicon runs it. For distribution, sign with a Developer ID and notarize:
#   codesign --force --options runtime --sign "Developer ID Application: …" NexusTerminal.app
#   xcrun notarytool submit … --wait && xcrun stapler staple NexusTerminal.app
if command -v codesign >/dev/null; then codesign --force --deep --sign - "$APP"; fi

if [ "${1:-}" = "--dmg" ] && command -v hdiutil >/dev/null; then
  DMG="$OUT/NexusTerminal-$VERSION.dmg"
  rm -f "$DMG"
  hdiutil create -volname "Nexus Terminal" -srcfolder "$APP" -ov -format UDZO "$DMG"
  echo "built $DMG"
fi
echo "built $APP"
