#!/usr/bin/env bash
# Shared build helpers for local builds, release packaging, and CI snippets.

link_wails_version() {
    local go_mod="${1:-go.mod}"
    awk '$1 == "github.com/wailsapp/wails/v2" { print $2; exit }' "$go_mod"
}

link_bool_enabled() {
    case "${1:-}" in
        1|true|TRUE|yes|YES|on|ON) return 0 ;;
        *) return 1 ;;
    esac
}

link_use_upx_for_target() {
    local target_os="$1"
    link_bool_enabled "${LINK_COMPRESS:-0}" || return 1
    if [ "$target_os" = "darwin" ] && ! link_bool_enabled "${LINK_FORCE_MACOS_UPX:-0}"; then
        return 1
    fi
    return 0
}

link_skips_upx_for_target() {
    local target_os="$1"
    link_bool_enabled "${LINK_COMPRESS:-0}" || return 1
    [ "$target_os" = "darwin" ] || return 1
    ! link_bool_enabled "${LINK_FORCE_MACOS_UPX:-0}"
}

link_configure_pure_go_build() {
    export CGO_ENABLED=0
    unset CGO_CFLAGS
    unset CGO_CXXFLAGS
}

link_build_tags() {
    if command -v pkg-config >/dev/null 2>&1 \
        && pkg-config --exists webkit2gtk-4.1 \
        && ! pkg-config --exists webkit2gtk-4.0; then
        echo "webkit2_41"
    fi
}

link_build_version() {
    local tag
    local commit
    tag="$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")"
    commit="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
    echo "${tag}-${commit}"
}

link_build_ldflags() {
    local version="$1"
    local flags="-s -w -buildid= -X github.com/andydixon/link/internal/api.appVersion=${version}"
    if [ -n "${LINK_UPDATE_URL:-}" ]; then
        flags="${flags} -X github.com/andydixon/link/internal/api.appUpdateURL=${LINK_UPDATE_URL}"
    fi
    echo "${flags}"
}
