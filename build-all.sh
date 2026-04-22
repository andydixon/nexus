#!/usr/bin/env bash
# build-all.sh — Cross-build helper for Nexus Terminal (macOS/Linux hosts)
# Builds all supported targets where the required toolchains are installed.
# Usage: ./build-all.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${GREEN}[nexus]${NC} $1"; }
warn() { echo -e "${YELLOW}[nexus]${NC} $1"; }
err()  { echo -e "${RED}[nexus]${NC} $1" >&2; }

check_deps() {
    command -v go >/dev/null 2>&1 || { err "Go is not installed"; exit 1; }
    command -v node >/dev/null 2>&1 || { err "Node.js is not installed"; exit 1; }
    command -v npm >/dev/null 2>&1 || { err "npm is not installed"; exit 1; }

    if ! command -v wails >/dev/null 2>&1; then
        if [ -f "$HOME/go/bin/wails" ]; then
            export PATH="$PATH:$HOME/go/bin"
        else
            err "Wails is not installed. Run: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
            exit 1
        fi
    fi

    log "Go:    $(go version)"
    log "Node:  $(node --version)"
    log "Wails: $(wails version 2>/dev/null | head -1)"
}

install_deps() {
    log "Installing Go dependencies…"
    go mod download

    log "Installing frontend dependencies…"
    cd frontend && npm install && cd ..
}

host_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux) echo "linux" ;;
        *) echo "unknown" ;;
    esac
}

pick_cc() {
    local target_os="$1"
    local target_arch="$2"

    if [ "$target_os" = "windows" ]; then
        if [ "$target_arch" = "amd64" ]; then
            echo "x86_64-w64-mingw32-gcc"
        else
            echo "aarch64-w64-mingw32-gcc"
        fi
        return
    fi

    if [ "$target_os" = "linux" ]; then
        if [ "$target_arch" = "amd64" ]; then
            echo "x86_64-linux-gnu-gcc"
        else
            echo "aarch64-linux-gnu-gcc"
        fi
        return
    fi

    echo ""
}

build_target() {
    local target_os="$1"
    local target_arch="$2"
    local host="$3"

    local platform="${target_os}/${target_arch}"
    local cc=""

    if [ "$target_os" != "$host" ]; then
        cc="$(pick_cc "$target_os" "$target_arch")"
        if [ -n "$cc" ] && ! command -v "$cc" >/dev/null 2>&1; then
            warn "Skipping ${platform} (missing C compiler: ${cc})"
            return 0
        fi
    fi

    log "Building ${platform}…"
    if [ -n "$cc" ]; then
        CC="$cc" CGO_ENABLED=1 wails build -platform "$platform" -ldflags "-s -w"
    else
        CGO_ENABLED=1 wails build -platform "$platform" -ldflags "-s -w"
    fi
}

main() {
    check_deps
    install_deps

    local host
    host="$(host_os)"
    if [ "$host" = "unknown" ]; then
        err "Unsupported host OS for this script. Use macOS or Linux."
        exit 1
    fi

    # Build host targets first
    build_target "$host" "amd64" "$host"
    build_target "$host" "arm64" "$host"

    # Cross-build Linux and Windows targets where toolchains are available
    if [ "$host" = "darwin" ] || [ "$host" = "linux" ]; then
        build_target "linux" "amd64" "$host"
        build_target "linux" "arm64" "$host"
        build_target "windows" "amd64" "$host"
        build_target "windows" "arm64" "$host"
    fi

    log "Builds complete. Output: build/bin/"
}

main "$@"
