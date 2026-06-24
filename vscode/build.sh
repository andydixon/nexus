#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VSCODE_DIR="$ROOT_DIR/vscode"
OUT_DIR="$VSCODE_DIR/dist"

cd "$ROOT_DIR"

echo "[vscode] Building frontend assets"
if [ ! -d "$ROOT_DIR/frontend/node_modules" ]; then
  npm --prefix "$ROOT_DIR/frontend" install
fi
npm --prefix "$ROOT_DIR/frontend" run build

echo "[vscode] Building Go bridge"
mkdir -p "$VSCODE_DIR/bin"
BRIDGE_NAME="nexus-vscode-bridge"
if [ "$(go env GOOS)" = "windows" ]; then
  BRIDGE_NAME="nexus-vscode-bridge.exe"
fi
go build -trimpath -o "$VSCODE_DIR/bin/$BRIDGE_NAME" ./cmd/vscode-bridge

echo "[vscode] Staging extension assets"
for EXT in nexus leviathan; do
  mkdir -p "$VSCODE_DIR/$EXT/bin"
  cp "$VSCODE_DIR/bin/$BRIDGE_NAME" "$VSCODE_DIR/$EXT/bin/$BRIDGE_NAME"
done

cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm.min.js" "$VSCODE_DIR/nexus/media/xterm.min.js"
cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm-addon-fit.min.js" "$VSCODE_DIR/nexus/media/xterm-addon-fit.min.js"
cp "$ROOT_DIR/frontend/dist/src/js/lib/xterm-addon-unicode11.min.js" "$VSCODE_DIR/nexus/media/xterm-addon-unicode11.min.js"
cp "$ROOT_DIR/frontend/dist/src/css/lib/xterm.css" "$VSCODE_DIR/nexus/media/xterm.css"
cp "$ROOT_DIR/frontend/dist/src/js/lib/novnc.min.js" "$VSCODE_DIR/nexus/media/novnc.min.js"

echo "[vscode] Packaging VSIX-compatible archives"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

package_extension() {
  local ext="$1"
  local out="$2"
  if command -v vsce >/dev/null 2>&1; then
    vsce package --no-dependencies --allow-missing-repository -o "$out"
  else
    npx --yes @vscode/vsce package --no-dependencies --allow-missing-repository -o "$out"
  fi
}

for EXT in nexus leviathan; do
  (
    cd "$VSCODE_DIR/$EXT"
    package_extension "$EXT" "$OUT_DIR/$EXT-0.1.0.vsix"
  )
  echo "[vscode] Wrote $OUT_DIR/$EXT-0.1.0.vsix"
done

echo "[vscode] Build complete"
