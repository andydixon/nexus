#!/usr/bin/env bash
#
# Build platform-specific VSIX packages for the Nexus and Leviathan VSCode
# extensions, covering Windows, macOS and Linux on both x64 and arm64.
#
# Each extension embeds a Go "bridge" binary that is OS/arch specific, so we
# produce one VSIX per target using VSCode's platform-specific extension
# mechanism (`vsce package --target <target>`). VSCode then installs the VSIX
# matching the user's platform automatically (e.g. from the Marketplace, or via
# `code --install-extension <file>.vsix`).
#
# The bridge is pure Go (CGO_ENABLED=0), so every target cross-compiles from any
# host with just the Go toolchain — no C compilers or per-OS toolchains needed.
#
# Usage:
#   vscode/package-all.sh                 # build every target
#   vscode/package-all.sh linux-x64 ...   # build only the named vsce target(s)
#
# Optional macOS code signing + notarization (darwin targets only):
#   Runs only on a macOS host with Xcode command line tools, and only when the
#   relevant env vars are set — otherwise it is skipped with a warning and the
#   darwin VSIXs are produced unsigned (fine for local use, blocked by Gatekeeper
#   when distributed to other machines).
#
#   MACOS_SIGN_IDENTITY   "Developer ID Application: Name (TEAMID)" — enables signing.
#
#   Then, to also notarize, provide EITHER a stored notarytool profile:
#     MACOS_NOTARY_PROFILE   profile name from `xcrun notarytool store-credentials`
#   OR an Apple ID + app-specific password:
#     MACOS_NOTARY_APPLE_ID  Apple ID email
#     MACOS_NOTARY_TEAM_ID   Apple Developer team id
#     MACOS_NOTARY_PASSWORD  app-specific password
#
#   Example:
#     MACOS_SIGN_IDENTITY="Developer ID Application: Andy Dixon (AB12CD34EF)" \
#     MACOS_NOTARY_PROFILE="nexus-notary" \
#     vscode/package-all.sh darwin-x64 darwin-arm64
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VSCODE_DIR="$ROOT_DIR/vscode"
OUT_DIR="$VSCODE_DIR/dist"
EXTENSIONS=(nexus leviathan)

# vsce target  ->  GOOS/GOARCH
ALL_TARGETS=(
  "win32-x64:windows/amd64"
  "win32-arm64:windows/arm64"
  "darwin-x64:darwin/amd64"
  "darwin-arm64:darwin/arm64"
  "linux-x64:linux/amd64"
  "linux-arm64:linux/arm64"
)

# Optional positional filter: only build the vsce targets passed as arguments.
SELECTED=("$@")
target_selected() {
  [ ${#SELECTED[@]} -eq 0 ] && return 0
  local want="$1"
  for t in "${SELECTED[@]}"; do [ "$t" = "$want" ] && return 0; done
  return 1
}

run_vsce() {
  if command -v vsce >/dev/null 2>&1; then
    vsce "$@"
  else
    npx --yes @vscode/vsce "$@"
  fi
}

# Codesign (hardened runtime) and optionally notarize a single darwin bridge
# binary. Opt-in via env vars and skipped with a warning when it can't run, so
# the cross-platform build still succeeds on non-macOS hosts. Signing the staged
# binary before it is copied into each extension means both VSIXs inherit the
# signature (and notarization, which is keyed to the binary's hash).
macos_sign() {
  local bin="$1"

  if [ -z "${MACOS_SIGN_IDENTITY:-}" ]; then
    echo "[vscode]   (skip signing: MACOS_SIGN_IDENTITY not set — shipping unsigned)"
    return 0
  fi
  if [ "$(uname -s)" != "Darwin" ] || ! command -v codesign >/dev/null 2>&1; then
    echo "[vscode]   WARNING: cannot sign darwin bridge here (needs macOS + Xcode tools); shipping unsigned"
    return 0
  fi

  echo "[vscode]   codesign ($MACOS_SIGN_IDENTITY)"
  codesign --force --options runtime --timestamp --sign "$MACOS_SIGN_IDENTITY" "$bin"
  codesign --verify --strict --verbose=2 "$bin"

  # Notarize only when credentials are provided.
  local notary_args=()
  if [ -n "${MACOS_NOTARY_PROFILE:-}" ]; then
    notary_args=(--keychain-profile "$MACOS_NOTARY_PROFILE")
  elif [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] && [ -n "${MACOS_NOTARY_TEAM_ID:-}" ] && [ -n "${MACOS_NOTARY_PASSWORD:-}" ]; then
    notary_args=(--apple-id "$MACOS_NOTARY_APPLE_ID" --team-id "$MACOS_NOTARY_TEAM_ID" --password "$MACOS_NOTARY_PASSWORD")
  else
    echo "[vscode]   (skip notarization: no notary credentials set)"
    return 0
  fi
  if ! command -v xcrun >/dev/null 2>&1; then
    echo "[vscode]   WARNING: xcrun not found; cannot notarize"
    return 0
  fi

  echo "[vscode]   notarize (submitting to Apple, waiting for result…)"
  local zip="${bin}.notarize.zip"
  # notarytool requires an archive; a bare Mach-O binary cannot be submitted directly.
  ditto -c -k --keepParent "$bin" "$zip"
  xcrun notarytool submit "$zip" "${notary_args[@]}" --wait
  rm -f "$zip"
  # A standalone executable cannot be stapled (stapler only supports app/dmg/pkg);
  # Gatekeeper verifies the notarization online via the now-registered hash, which
  # matches the exact bytes embedded in every VSIX.
  echo "[vscode]   notarized (online check; standalone binaries are not stapleable)"
}

echo "[vscode] Building frontend assets (platform independent)"
if [ ! -d "$ROOT_DIR/frontend/node_modules" ]; then
  npm --prefix "$ROOT_DIR/frontend" install
fi
npm --prefix "$ROOT_DIR/frontend" run build

echo "[vscode] Staging shared media assets"
cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm.min.js"            "$VSCODE_DIR/nexus/media/xterm.min.js"
cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm-addon-fit.min.js"  "$VSCODE_DIR/nexus/media/xterm-addon-fit.min.js"
cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm-addon-unicode11.min.js" "$VSCODE_DIR/nexus/media/xterm-addon-unicode11.min.js"
cp "$ROOT_DIR/frontend/dist/src/css/lib/xterm.css"              "$VSCODE_DIR/nexus/media/xterm.css"
cp "$ROOT_DIR/frontend/dist/src/js/lib/novnc.min.js"            "$VSCODE_DIR/nexus/media/novnc.min.js"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

cd "$ROOT_DIR"
VERSION="$(node -p "require('$VSCODE_DIR/nexus/package.json').version")"

for entry in "${ALL_TARGETS[@]}"; do
  VSCE_TARGET="${entry%%:*}"
  GOPLATFORM="${entry##*:}"
  GOOS="${GOPLATFORM%%/*}"
  GOARCH="${GOPLATFORM##*/}"

  if ! target_selected "$VSCE_TARGET"; then
    continue
  fi

  BIN_NAME="nexus-vscode-bridge"
  [ "$GOOS" = "windows" ] && BIN_NAME="nexus-vscode-bridge.exe"

  echo "[vscode] Building bridge for $VSCE_TARGET ($GOOS/$GOARCH)"
  STAGE_BIN="$VSCODE_DIR/bin/$VSCE_TARGET/$BIN_NAME"
  mkdir -p "$(dirname "$STAGE_BIN")"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 \
    go build -trimpath -o "$STAGE_BIN" ./cmd/vscode-bridge

  # Sign + notarize macOS bridges before they are copied into the extensions.
  if [ "$GOOS" = "darwin" ]; then
    macos_sign "$STAGE_BIN"
  fi

  for EXT in "${EXTENSIONS[@]}"; do
    EXT_BIN_DIR="$VSCODE_DIR/$EXT/bin"
    # Ship exactly one binary per VSIX so the package can't carry a wrong-arch
    # (or wrong-OS) bridge alongside the right one.
    rm -rf "$EXT_BIN_DIR"
    mkdir -p "$EXT_BIN_DIR"
    cp "$STAGE_BIN" "$EXT_BIN_DIR/$BIN_NAME"
    chmod 0755 "$EXT_BIN_DIR/$BIN_NAME"

    OUT_VSIX="$OUT_DIR/$EXT-$VERSION-$VSCE_TARGET.vsix"
    ( cd "$VSCODE_DIR/$EXT" && run_vsce package --target "$VSCE_TARGET" \
        --no-dependencies --allow-missing-repository -o "$OUT_VSIX" >/dev/null )
    echo "[vscode]   wrote $(basename "$OUT_VSIX")"
  done
done

# Leave the working tree with a host-native bridge staged so a plain
# `code --install-extension` of an unpacked extension still works locally.
HOST_OS="$(go env GOOS)"; HOST_ARCH="$(go env GOARCH)"
HOST_BIN="nexus-vscode-bridge"; [ "$HOST_OS" = "windows" ] && HOST_BIN="nexus-vscode-bridge.exe"
for EXT in "${EXTENSIONS[@]}"; do
  rm -rf "$VSCODE_DIR/$EXT/bin"; mkdir -p "$VSCODE_DIR/$EXT/bin"
  GOOS="$HOST_OS" GOARCH="$HOST_ARCH" CGO_ENABLED=0 \
    go build -trimpath -o "$VSCODE_DIR/$EXT/bin/$HOST_BIN" ./cmd/vscode-bridge
done

echo "[vscode] Done. Packages in $OUT_DIR:"
ls -1 "$OUT_DIR"
