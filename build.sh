#!/usr/bin/env bash
# build.sh — Build script for Nexus Terminal
# Provides a reproducible build process for CI and local builds.
# Usage: ./build.sh [dev|build|test|clean|deps]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
source "$SCRIPT_DIR/scripts/build-common.sh"

# Colours for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[nexus]${NC} $1"; }
warn() { echo -e "${YELLOW}[nexus]${NC} $1"; }
err()  { echo -e "${RED}[nexus]${NC} $1" >&2; }

MIN_GO_VERSION="1.25.0"
WAILS_VERSION="$(nexus_wails_version go.mod)"
WAILS_BIN=""
WAILS_USE_GO_RUN=0

if [ -x "$SCRIPT_DIR/.tools/go/bin/go" ]; then
    export PATH="$SCRIPT_DIR/.tools/go/bin:$PATH"
fi
export GOCACHE="${GOCACHE:-$SCRIPT_DIR/.cache/go-build}"
export GOPATH="${GOPATH:-$SCRIPT_DIR/.cache/go}"
mkdir -p "$GOCACHE" "$GOPATH"
export PATH="$GOPATH/bin:$PATH"

version_at_least() {
    local have="${1#go}"
    local want="$2"
    local IFS=.
    local have_parts want_parts
    read -r -a have_parts <<< "$have"
    read -r -a want_parts <<< "$want"
    for i in 0 1 2; do
        local h="${have_parts[$i]:-0}"
        local w="${want_parts[$i]:-0}"
        if (( h > w )); then return 0; fi
        if (( h < w )); then return 1; fi
    done
    return 0
}

check_go() {
    command -v go >/dev/null 2>&1 || { err "Go is not installed"; exit 1; }

    local version
    version="$(go env GOVERSION 2>/dev/null || go version | awk '{print $3}')"
    if ! version_at_least "$version" "$MIN_GO_VERSION"; then
        err "Go ${MIN_GO_VERSION}+ is required by go.mod; found ${version} at $(command -v go)"
        err "Install a newer Go, or place it at .tools/go/bin/go for this project."
        exit 1
    fi
}

resolve_wails() {
    if WAILS_BIN="$(command -v wails 2>/dev/null)"; then
        WAILS_USE_GO_RUN=0
        return
    fi

    local gopath
    gopath="$(go env GOPATH 2>/dev/null || true)"
    if [ -n "$gopath" ] && [ -x "$gopath/bin/wails" ]; then
        WAILS_BIN="$gopath/bin/wails"
        WAILS_USE_GO_RUN=0
        return
    fi

    if [ -n "$WAILS_VERSION" ]; then
        WAILS_BIN="github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
        WAILS_USE_GO_RUN=1
        warn "Wails CLI not found; using pinned ${WAILS_BIN} via go run"
        return
    fi

    err "Wails is not installed and no Wails version was found in go.mod"
    exit 1
}

wails_cmd() {
    if [ "$WAILS_USE_GO_RUN" -eq 1 ]; then
        go run "$WAILS_BIN" "$@"
    else
        "$WAILS_BIN" "$@"
    fi
}

ensure_upx() {
    if ! nexus_bool_enabled "${NEXUS_COMPRESS:-0}"; then
        return
    fi
    if nexus_skips_upx_for_target "$(host_os)"; then
        return
    fi
    if command -v upx >/dev/null 2>&1; then
        return
    fi
    err "NEXUS_COMPRESS=1 requires UPX. Install it first, for example: brew install upx"
    exit 1
}

check_linux_deps() {
    if [ "$(uname -s)" != "Linux" ]; then
        return
    fi

    command -v pkg-config >/dev/null 2>&1 || {
        err "pkg-config is required for Linux Wails builds."
        err "On Ubuntu/Pop!_OS install: sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev"
        exit 1
    }

    local missing=()
    pkg-config --exists gtk+-3.0 || missing+=("gtk+-3.0")
    if ! pkg-config --exists webkit2gtk-4.1 && ! pkg-config --exists webkit2gtk-4.0; then
        missing+=("webkit2gtk-4.1 or webkit2gtk-4.0")
    fi
    if ! pkg-config --exists ayatana-appindicator3-0.1 && ! pkg-config --exists appindicator3-0.1; then
        missing+=("ayatana-appindicator3-0.1 or appindicator3-0.1")
    fi

    if [ "${#missing[@]}" -gt 0 ]; then
        err "Missing Linux build dependencies: ${missing[*]}"
        err "On Ubuntu/Pop!_OS install: sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev"
        exit 1
    fi
}

build_tags() {
    nexus_build_tags
}

host_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux) echo "linux" ;;
        *) echo "unknown" ;;
    esac
}

build_version() {
    nexus_build_version
}

build_ldflags() {
    nexus_build_ldflags "$1"
}

# Check prerequisites
check_deps() {
    check_go
    command -v node >/dev/null 2>&1 || { err "Node.js is not installed"; exit 1; }
    command -v npm >/dev/null 2>&1 || { err "npm is not installed"; exit 1; }
    check_linux_deps
    resolve_wails
    ensure_upx

    log "All prerequisites found"
    log "Go:    $(go version)"
    log "Node:  $(node --version)"
    log "Wails: $(wails_cmd version 2>/dev/null | head -1)"
}

# Install dependencies
install_deps() {
    log "Installing Go dependencies…"
    go mod download

    log "Installing frontend dependencies…"
    if [ -f frontend/package-lock.json ]; then
        (cd frontend && npm ci)
    else
        (cd frontend && npm install)
    fi

    log "Dependencies installed"
}

# Development mode
dev() {
    check_deps
    install_deps
    log "Starting development mode…"
    wails_cmd dev
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
    nexus_configure_pure_go_build
    local build_args=("-trimpath")
    if nexus_bool_enabled "${NEXUS_COMPRESS:-0}"; then
        if nexus_skips_upx_for_target "$(host_os)"; then
            warn "Skipping UPX compression on macOS; distribute the zipped .app instead, or set NEXUS_FORCE_MACOS_UPX=1 with suitable NEXUS_UPX_FLAGS."
        else
            log "Using UPX compression"
            build_args+=("-upx")
            if [ -n "${NEXUS_UPX_FLAGS:-}" ]; then
                build_args+=("-upxflags" "$NEXUS_UPX_FLAGS")
            fi
        fi
    fi
    if [ -n "$tags" ]; then
        log "Using Wails build tags: ${tags}"
        wails_cmd build "${build_args[@]}" -tags "$tags" -ldflags "$(build_ldflags "${version}")"
    else
        wails_cmd build "${build_args[@]}" -ldflags "$(build_ldflags "${version}")"
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
    check_go
    log "Running tests…"
    go test ./... -v -count=1
    (cd frontend && npm test)
}

# Clean build artefacts
clean() {
    log "Cleaning build artefacts…"
    rm -rf build/bin
    rm -rf frontend/node_modules
    rm -rf frontend/src/js/lib
    if command -v go >/dev/null 2>&1; then
        go clean -testcache
    fi
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
