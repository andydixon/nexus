#!/usr/bin/env bash
# Shared build helpers for local builds, release packaging, and CI snippets.

nexus_wails_version() {
    local go_mod="${1:-go.mod}"
    awk '$1 == "github.com/wailsapp/wails/v2" { print $2; exit }' "$go_mod"
}

nexus_build_tags() {
    if command -v pkg-config >/dev/null 2>&1 \
        && pkg-config --exists webkit2gtk-4.1 \
        && ! pkg-config --exists webkit2gtk-4.0; then
        echo "webkit2_41"
    fi
}

nexus_build_version() {
    local tag
    local commit
    tag="$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")"
    commit="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"
    echo "${tag}-${commit}"
}

nexus_build_ldflags() {
    local version="$1"
    local flags="-s -w -X github.com/andydixon/nexus/internal/api.appVersion=${version}"
    if [ -n "${NEXUS_UPDATE_URL:-}" ]; then
        flags="${flags} -X github.com/andydixon/nexus/internal/api.appUpdateURL=${NEXUS_UPDATE_URL}"
    fi
    echo "${flags}"
}
