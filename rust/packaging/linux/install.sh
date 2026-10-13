#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Andy Dixon
# Install Nexus Terminal for the current user so it appears in the app launcher.
# Usage: rust/packaging/linux/install.sh [path/to/nexus-terminal]   (default: target/release/nexus-terminal)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-"$HERE/../../target/release/nexus-terminal"}
PREFIX=${XDG_DATA_HOME:-$HOME/.local/share}
install -Dm755 "$BIN" "$HOME/.local/bin/nexus-terminal"
install -Dm644 "$HERE/nexus-terminal.desktop" "$PREFIX/applications/nexus-terminal.desktop"
install -Dm644 "$HERE/../../app/assets/icon/icon-256.png" "$PREFIX/icons/hicolor/256x256/apps/nexus-terminal.png"
install -Dm644 "$HERE/../../app/assets/icon/icon-512.png" "$PREFIX/icons/hicolor/512x512/apps/nexus-terminal.png"
update-desktop-database "$PREFIX/applications" 2>/dev/null || true
gtk-update-icon-cache -f -t "$PREFIX/icons/hicolor" 2>/dev/null || true
echo "installed: ~/.local/bin/nexus-terminal (make sure ~/.local/bin is on PATH)"
