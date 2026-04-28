#!/usr/bin/env bash
# release.sh — Build and package release artefacts for Nexus Terminal.
# Produces per-platform zips plus a manifest.json and version.txt.
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
    command -v zip >/dev/null 2>&1 || { err "zip is not installed"; exit 1; }
    command -v shasum >/dev/null 2>&1 || { err "shasum is not installed"; exit 1; }

    if ! command -v wails >/dev/null 2>&1; then
        if [ -f "$HOME/go/bin/wails" ]; then
            export PATH="$PATH:$HOME/go/bin"
        else
            err "Wails is not installed. Run: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
            exit 1
        fi
    fi
}

install_deps() {
    log "Installing Go dependencies..."
    go mod download

    log "Installing frontend dependencies..."
    cd frontend && npm install && cd ..
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
    local version="$3"
    local out_dir="$4"

    local platform="${target_os}/${target_arch}"
    local cc
    cc="$(pick_cc "$target_os" "$target_arch")"

    if [ -n "$cc" ] && ! command -v "$cc" >/dev/null 2>&1; then
        warn "Skipping ${platform} (missing C compiler: ${cc})"
        return 0
    fi

    local output_base="NexusTerminal-${target_os}-${target_arch}"
    local output_path
    if [ "$target_os" = "darwin" ]; then
        output_path="${out_dir}/${output_base}.app"
    elif [ "$target_os" = "windows" ]; then
        output_path="${out_dir}/${output_base}.exe"
    else
        output_path="${out_dir}/${output_base}"
    fi

    log "Building ${platform}..."
    local ldflags
    ldflags="$(build_ldflags "$version")"
    if [ -n "$cc" ]; then
        CC="$cc" CGO_ENABLED=1 wails build -platform "$platform" -ldflags "$ldflags" -o "$output_path"
    else
        CGO_ENABLED=1 wails build -platform "$platform" -ldflags "$ldflags" -o "$output_path"
    fi

    local zip_name="${out_dir}/${output_base}.zip"
    if [ "$target_os" = "darwin" ]; then
        (cd "$out_dir" && zip -r "${output_base}.zip" "${output_base}.app" >/dev/null)
    elif [ "$target_os" = "windows" ]; then
        (cd "$out_dir" && zip -j "${output_base}.zip" "${output_base}.exe" >/dev/null)
    else
        (cd "$out_dir" && zip -j "${output_base}.zip" "${output_base}" >/dev/null)
    fi

    echo "$target_os|$target_arch|$output_base.zip" >> "${out_dir}/.release-files"
}

write_manifest() {
    local version="$1"
    local out_dir="$2"
    local manifest_path="$3"
    local version_path="$4"
    local base_url="${NEXUS_RELEASE_BASE_URL:-}"

    local published_at
    published_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    echo "$version" > "$version_path"

    {
        echo "{"
        echo "  \"latestVersion\": \"${version}\","
        echo "  \"publishedAt\": \"${published_at}\","
        if [ -n "$base_url" ]; then
            echo "  \"releaseURL\": \"${base_url}/\","
        else
            echo "  \"releaseURL\": \"\","
        fi
        echo "  \"files\": ["

        local first=1
        while IFS='|' read -r os arch filename; do
            [ -z "$os" ] && continue
            local file_path="${out_dir}/${filename}"
            local sha
            sha="$(shasum -a 256 "$file_path" | awk '{print $1}')"
            local size
            size="$(wc -c < "$file_path" | tr -d ' ')"
            local url=""
            if [ -n "$base_url" ]; then
                url="${base_url}/${filename}"
            fi

            if [ $first -eq 0 ]; then
                echo ","
            fi
            first=0
            printf "    {\"platform\":\"%s\",\"arch\":\"%s\",\"filename\":\"%s\",\"url\":\"%s\",\"sha256\":\"%s\",\"size\":%s}" "$os" "$arch" "$filename" "$url" "$sha" "$size"
        done < "${out_dir}/.release-files"
        echo
        echo "  ]"
        echo "}"
    } > "$manifest_path"

    rm -f "${out_dir}/.release-files"
}

main() {
    check_deps
    install_deps

    local version
    version="$(build_version)"

    if [ -z "${NEXUS_UPDATE_URL:-}" ]; then
        warn "NEXUS_UPDATE_URL is not set; updates will default to GitHub in this build"
    fi

    local release_root="build/releases"
    local out_dir="${release_root}/${version}"
    mkdir -p "$out_dir"

    log "Building release ${version}..."
    : > "${out_dir}/.release-files"

    build_target "darwin" "amd64" "$version" "$out_dir"
    build_target "darwin" "arm64" "$version" "$out_dir"
    build_target "linux" "amd64" "$version" "$out_dir"
    build_target "linux" "arm64" "$version" "$out_dir"
    build_target "windows" "amd64" "$version" "$out_dir"
    build_target "windows" "arm64" "$version" "$out_dir"

    local manifest_path="${release_root}/manifest.json"
    local version_path="${release_root}/version.txt"
    write_manifest "$version" "$out_dir" "$manifest_path" "$version_path"

    log "Release artefacts written to ${out_dir}"
    log "Manifest: ${manifest_path}"
    log "Version: ${version_path}"
}

main "$@"
