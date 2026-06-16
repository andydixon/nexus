#!/usr/bin/env bash
# release.sh — Build and package release artefacts for Nexus Terminal.
# Produces per-platform zips plus a manifest.json and version.txt.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
source "$SCRIPT_DIR/scripts/build-common.sh"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

DEFAULT_UPDATE_URL="https://dixon.cx/downloads/nexus/manifest-{platform}-{arch}.json"
DEFAULT_RELEASE_BASE_URL="https://dixon.cx/downloads/nexus"
WAILS_VERSION="$(nexus_wails_version go.mod)"

log()  { echo -e "${GREEN}[nexus]${NC} $1"; }
warn() { echo -e "${YELLOW}[nexus]${NC} $1"; }
err()  { echo -e "${RED}[nexus]${NC} $1" >&2; }

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

build_tags() {
    nexus_build_tags
}

default_targets() {
    local os
    local arch
    os="$(host_os)"
    arch="$(host_arch)"

    if [ "$os" = "unknown" ] || [ "$arch" = "unknown" ]; then
        err "Unsupported host platform: $(uname -s)/$(uname -m)"
        exit 1
    fi

    echo "${os}/${arch}"
}

release_targets() {
    if [ -n "${NEXUS_RELEASE_TARGETS:-}" ]; then
        echo "$NEXUS_RELEASE_TARGETS" | tr ',' '\n' | sed '/^[[:space:]]*$/d'
        return
    fi

    default_targets
}

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
            if [ -z "$WAILS_VERSION" ]; then
                err "Wails is not installed and no pinned Wails version was found in go.mod"
                exit 1
            fi
            log "Installing pinned Wails CLI ${WAILS_VERSION}..."
            go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
            export PATH="$PATH:$(go env GOPATH)/bin"
        fi
    fi
}

install_deps() {
    log "Installing Go dependencies..."
    go mod download

    log "Installing frontend dependencies..."
    if [ -f frontend/package-lock.json ]; then
        (cd frontend && npm ci)
    else
        (cd frontend && npm install)
    fi
}

build_version() {
    nexus_build_version
}

build_ldflags() {
    nexus_build_ldflags "$1"
}

pick_cc() {
    local target_os="$1"
    local target_arch="$2"

    if [ "$target_os" = "windows" ]; then
        if command -v zig >/dev/null 2>&1; then
            echo "zig:${target_arch}"
            return
        fi
        if [ "$target_arch" = "amd64" ]; then
            echo "x86_64-w64-mingw32-gcc"
        else
            echo "aarch64-w64-mingw32-gcc"
        fi
        return
    fi

    if [ "$target_os" = "linux" ]; then
        if command -v zig >/dev/null 2>&1; then
            echo "zig:${target_arch}"
            return
        fi
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

    local zig_target=""
    if [[ "$cc" == zig:* ]]; then
        zig_target="${cc#zig:}"
        if [ "$zig_target" = "amd64" ]; then
            zig_target="x86_64"
        elif [ "$zig_target" = "arm64" ]; then
            zig_target="aarch64"
        fi
        cc="zig"
    fi

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
    local tags
    tags="$(build_tags)"
    if [ -n "$cc" ]; then
        local cc_cmd="$cc"
        if [ -n "$zig_target" ]; then
            if [ "$target_os" = "linux" ]; then
                cc_cmd="zig cc -target ${zig_target}-linux-gnu"
            elif [ "$target_os" = "windows" ]; then
                cc_cmd="zig cc -target ${zig_target}-windows-gnu"
            fi
        fi
        if [ -n "$tags" ]; then
            if ! CC="$cc_cmd" CGO_ENABLED=1 wails build -platform "$platform" -tags "$tags" -ldflags "$ldflags" -o "$output_path"; then
                warn "Build failed for ${platform}"
                return 1
            fi
        elif ! CC="$cc_cmd" CGO_ENABLED=1 wails build -platform "$platform" -ldflags "$ldflags" -o "$output_path"; then
            warn "Build failed for ${platform}"
            return 1
        fi
    else
        if [ -n "$tags" ]; then
            if ! CGO_ENABLED=1 wails build -platform "$platform" -tags "$tags" -ldflags "$ldflags" -o "$output_path"; then
                warn "Build failed for ${platform}"
                return 1
            fi
        elif ! CGO_ENABLED=1 wails build -platform "$platform" -ldflags "$ldflags" -o "$output_path"; then
            warn "Build failed for ${platform}"
            return 1
        fi
    fi

    local expected_path=""
    local build_bin="build/bin"
    if [ "$target_os" = "darwin" ]; then
        expected_path="${out_dir}/${output_base}.app"
        if [ ! -d "$expected_path" ]; then
            local source_app
            source_app="$(find "$build_bin" -type d -name "*.app" -print0 | xargs -0 ls -td 2>/dev/null | head -1)"
            if [ -z "$source_app" ]; then
                warn "Built .app not found for ${platform}"
                return 1
            fi
            log "Using app bundle: ${source_app}"
            cp -R "$source_app" "$expected_path"
        fi
        if ! (cd "$out_dir" && zip -r "${output_base}.zip" "${output_base}.app" >/dev/null); then
            warn "Zip failed for ${platform}"
            return 1
        fi
    elif [ "$target_os" = "windows" ]; then
        expected_path="${out_dir}/${output_base}.exe"
        if [ ! -f "$expected_path" ]; then
            local source_exe
            source_exe="$(find "$build_bin" -type f -name "${output_base}.exe" -print0 | xargs -0 ls -t 2>/dev/null | head -1)"
            if [ -z "$source_exe" ]; then
                warn "Built .exe not found for ${platform}"
                return 1
            fi
            log "Using executable: ${source_exe}"
            cp "$source_exe" "$expected_path"
        fi
        if ! (cd "$out_dir" && zip -j "${output_base}.zip" "${output_base}.exe" >/dev/null); then
            warn "Zip failed for ${platform}"
            return 1
        fi
    else
        expected_path="${out_dir}/${output_base}"
        if [ ! -f "$expected_path" ]; then
            local source_bin
            source_bin="$(find "$build_bin" -type f -name "${output_base}" -print0 | xargs -0 ls -t 2>/dev/null | head -1)"
            if [ -z "$source_bin" ] || [ ! -f "$source_bin" ]; then
                warn "Built binary not found for ${platform}"
                return 1
            fi
            log "Using binary: ${source_bin}"
            cp "$source_bin" "$expected_path"
        fi
        if ! (cd "$out_dir" && zip -j "${output_base}.zip" "${output_base}" >/dev/null); then
            warn "Zip failed for ${platform}"
            return 1
        fi
    fi

    echo "$target_os|$target_arch|$output_base.zip" >> "${out_dir}/.release-files"
}

write_manifest() {
    local version="$1"
    local out_dir="$2"
    local release_root="$3"
    local base_url="${NEXUS_RELEASE_BASE_URL:-}"

    local published_at
    published_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    while IFS='|' read -r os arch filename; do
        [ -z "$os" ] && continue
        local manifest_path="${release_root}/manifest-${os}-${arch}.json"
        local version_path="${release_root}/version-${os}-${arch}.txt"
        local file_path="${out_dir}/${filename}"
        local sha
        sha="$(shasum -a 256 "$file_path" | awk '{print $1}')"
        local size
        size="$(wc -c < "$file_path" | tr -d ' ')"
        local url=""
        if [ -n "$base_url" ]; then
            url="${base_url}/${filename}"
        fi

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
            printf "    {\"platform\":\"%s\",\"arch\":\"%s\",\"filename\":\"%s\",\"url\":\"%s\",\"sha256\":\"%s\",\"size\":%s}" "$os" "$arch" "$filename" "$url" "$sha" "$size"
            echo
            echo "  ]"
            echo "}"
        } > "$manifest_path"
    done < "${out_dir}/.release-files"

    rm -f "${out_dir}/.release-files"
}

main() {
    check_deps
    install_deps

    local version
    version="$(build_version)"

    if [ -z "${NEXUS_UPDATE_URL:-}" ]; then
        NEXUS_UPDATE_URL="${DEFAULT_UPDATE_URL}"
    fi
    if [ -z "${NEXUS_RELEASE_BASE_URL:-}" ]; then
        NEXUS_RELEASE_BASE_URL="${DEFAULT_RELEASE_BASE_URL}"
    fi

    local release_root="build/releases"
    local out_dir="${release_root}/${version}"
    mkdir -p "$out_dir"

    log "Building release ${version}..."
    : > "${out_dir}/.release-files"

    local targets
    mapfile -t targets < <(release_targets)
    if [ "${#targets[@]}" -eq 0 ]; then
        err "No release targets selected."
        exit 1
    fi

    local errors=0
    local target
    for target in "${targets[@]}"; do
        local target_os="${target%%/*}"
        local target_arch="${target##*/}"
        if [ "$target_os" = "$target_arch" ] || [ -z "$target_os" ] || [ -z "$target_arch" ]; then
            err "Invalid release target: ${target}"
            errors=1
            continue
        fi
        if ! build_target "$target_os" "$target_arch" "$version" "$out_dir"; then
            errors=1
        fi
    done

    write_manifest "$version" "$out_dir" "$release_root"

    log "Release artefacts written to ${out_dir}"
    log "Manifests: ${release_root}/manifest-<platform>-<arch>.json"
    log "Versions: ${release_root}/version-<platform>-<arch>.txt"

    if [ $errors -ne 0 ]; then
        err "One or more targets failed. Manifest and version were still generated."
        exit 12
    fi
}

main "$@"
