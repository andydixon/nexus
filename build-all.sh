#!/usr/bin/env bash
# build-all.sh — Cross-build helper for Alchementary Link (macOS/Linux hosts)
# Builds all supported targets where the required toolchains are installed.
# Usage: ./build-all.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
source "$SCRIPT_DIR/scripts/build-common.sh"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${GREEN}[link]${NC} $1"; }
warn() { echo -e "${YELLOW}[link]${NC} $1"; }
err()  { echo -e "${RED}[link]${NC} $1" >&2; }

MIN_GO_VERSION="1.25.12"
WAILS_VERSION="$(awk '$1 == "github.com/wailsapp/wails/v2" { print $2; exit }' go.mod)"
WAILS_BIN=""
WAILS_USE_GO_RUN=0
BUILT_TARGETS=0
SKIPPED_TARGETS=0

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
    if ! link_bool_enabled "${LINK_COMPRESS:-0}"; then
        return
    fi
    if command -v upx >/dev/null 2>&1; then
        return
    fi
    warn "UPX is not installed; compressed targets will be skipped unless the target does not use UPX."
}

linux_deps_ok() {
    command -v pkg-config >/dev/null 2>&1 || return 1
    pkg-config --exists gtk+-3.0 || return 1
    if ! pkg-config --exists webkit2gtk-4.1 && ! pkg-config --exists webkit2gtk-4.0; then
        return 1
    fi
    if ! pkg-config --exists ayatana-appindicator3-0.1 && ! pkg-config --exists appindicator3-0.1; then
        return 1
    fi
}

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
    local flags="-s -w -X github.com/andydixon/link/internal/api.appVersion=${version}"
    if [ -n "${LINK_UPDATE_URL:-}" ]; then
        flags="${flags} -X github.com/andydixon/link/internal/api.appUpdateURL=${LINK_UPDATE_URL}"
    fi
    echo "${flags}"
}

check_deps() {
    check_go
    command -v node >/dev/null 2>&1 || { err "Node.js is not installed"; exit 1; }
    command -v npm >/dev/null 2>&1 || { err "npm is not installed"; exit 1; }
    resolve_wails
    ensure_upx

    log "Go:    $(go version)"
    log "Node:  $(node --version)"
    log "Wails: $(wails_cmd version 2>/dev/null | head -1)"
}

install_deps() {
    log "Installing Go dependencies…"
    go mod download

    log "Installing frontend dependencies…"
    if [ -f frontend/package-lock.json ]; then
        (cd frontend && npm ci)
    else
        (cd frontend && npm install)
    fi
}

host_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux) echo "linux" ;;
        *) echo "unknown" ;;
    esac
}

host_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "unknown" ;;
    esac
}

build_target() {
    local target_os="$1"
    local target_arch="$2"
    local host="$3"
    local host_arch_value="$4"
    local version="$5"
    local platform="${target_os}/${target_arch}"
    link_configure_pure_go_build
    local ldflags
    ldflags="$(build_ldflags "${version}")"
    local tags
    tags="$(build_tags)"
    local build_args=("-platform" "$target_os/$target_arch" "-trimpath")
    if link_use_upx_for_target "$target_os"; then
        if ! command -v upx >/dev/null 2>&1; then
            warn "Skipping ${platform} (LINK_COMPRESS=1 requires UPX)"
            ((SKIPPED_TARGETS += 1))
            return 0
        fi
        build_args+=("-upx")
        if [ -n "${LINK_UPX_FLAGS:-}" ]; then
            build_args+=("-upxflags" "$LINK_UPX_FLAGS")
        fi
    elif link_skips_upx_for_target "$target_os"; then
        warn "Skipping UPX compression for ${platform}; distribute the zipped .app instead, or set LINK_FORCE_MACOS_UPX=1 with suitable LINK_UPX_FLAGS."
    fi
    if [ -n "$tags" ]; then
        build_args+=("-tags" "$tags")
    fi
    build_args+=("-ldflags" "$ldflags")

    if [ "$target_os" = "linux" ] && ! linux_deps_ok; then
        warn "Skipping ${platform} (missing Linux GTK/WebKit build dependencies)"
        ((SKIPPED_TARGETS += 1))
        return 0
    fi

    log "Building ${platform}…"
    CGO_ENABLED=0 wails_cmd build "${build_args[@]}"
    ((BUILT_TARGETS += 1))
}

main() {
    check_deps
    install_deps

    local version
    version="$(build_version)"

    local host
    host="$(host_os)"
    local arch
    arch="$(host_arch)"
    if [ "$host" = "unknown" ] || [ "$arch" = "unknown" ]; then
        err "Unsupported host platform: $(uname -s)/$(uname -m)"
        exit 1
    fi

    build_target "$host" "$arch" "$host" "$arch" "$version"

    # Cross-build Linux and Windows targets where toolchains are available
    if [ "$host" = "darwin" ] || [ "$host" = "linux" ]; then
        for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
            local target_os="${target%%/*}"
            local target_arch="${target##*/}"
            if [ "$target_os" = "$host" ] && [ "$target_arch" = "$arch" ]; then
                continue
            fi
            build_target "$target_os" "$target_arch" "$host" "$arch" "$version"
        done
    fi

    if [ "$BUILT_TARGETS" -eq 0 ]; then
        err "No targets were built; ${SKIPPED_TARGETS} target(s) were skipped because required system toolchains are missing."
        exit 1
    fi

    log "Builds complete (${BUILT_TARGETS} built, ${SKIPPED_TARGETS} skipped). Output: build/bin/"
}

main "$@"
