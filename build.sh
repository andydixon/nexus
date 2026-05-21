#!/usr/bin/env bash
# build.sh — Build script for Nexus Terminal
# Provides a reproducible build process for CI and local builds.
# Usage: ./build.sh [dev|build|test|clean|deps]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Colours for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[nexus]${NC} $1"; }
warn() { echo -e "${YELLOW}[nexus]${NC} $1"; }
err()  { echo -e "${RED}[nexus]${NC} $1" >&2; }

build_tags() {
    if command -v pkg-config >/dev/null 2>&1 \
        && pkg-config --exists webkit2gtk-4.1 \
        && ! pkg-config --exists webkit2gtk-4.0; then
        echo "webkit2_41"
    fi
}

build_version() {
    local tag
    local commit
    tag="$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")"
    commit="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
    echo "${tag}-${commit}"
}

build_ldflags() {
    local version="$1"
    local flags="-s -w -X github.com/andydixon/nexus/internal/api.appVersion=${version}"
    if [ -n "${NEXUS_UPDATE_URL:-}" ]; then
        flags="${flags} -X github.com/andydixon/nexus/internal/api.appUpdateURL=${NEXUS_UPDATE_URL}"
    fi
    echo "${flags}"
}

# Check prerequisites
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

    log "All prerequisites found"
    log "Go:    $(go version)"
    log "Node:  $(node --version)"
    log "Wails: $(wails version 2>/dev/null | head -1)"
}

# Install dependencies
install_deps() {
    log "Installing Go dependencies…"
    go mod download

    log "Installing frontend dependencies…"
    cd frontend && npm install && cd ..

    log "Dependencies installed"
}

# Development mode
dev() {
    check_deps
    install_deps
    log "Starting development mode…"
    wails dev
}

# Production build
build() {
    check_deps
    install_deps
    log "Building production binary…"
    local version
    version="$(build_version)"
    local tags
    tags="$(build_tags)"
    if [ -n "$tags" ]; then
        log "Using Wails build tags: ${tags}"
        wails build -tags "$tags" -ldflags "$(build_ldflags "${version}")"
    else
        wails build -ldflags "$(build_ldflags "${version}")"
    fi

    # Replace Wails-generated icon with custom app icon on macOS.
    if [[ "$OSTYPE" == darwin* ]]; then
        local app_bundle="build/bin/nexus-terminal.app"
        local custom_icns="build/darwin/AppIcon.icns"
        if [ -d "$app_bundle" ] && [ -f "$custom_icns" ]; then
            cp "$custom_icns" "$app_bundle/Contents/Resources/iconfile.icns"
            log "Replaced app icon with custom AppIcon.icns"
        fi
    fi

    log "Build complete: build/bin/"
}

# Run tests
run_tests() {
    log "Running tests…"
    go test ./... -v -count=1
}

# Clean build artefacts
clean() {
    log "Cleaning build artefacts…"
    rm -rf build/bin
    rm -rf frontend/node_modules
    rm -rf frontend/src/js/lib
    rm -rf frontend/src/css/lib
    go clean -testcache
    log "Clean complete"
}

# Main
case "${1:-build}" in
    dev)     dev ;;
    build)   build ;;
    test)    run_tests ;;
    clean)   clean ;;
    deps)    check_deps && install_deps ;;
    *)
        echo "Usage: $0 [dev|build|test|clean|deps]"
        exit 1
        ;;
esac
