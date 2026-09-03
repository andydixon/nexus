#!/usr/bin/env bash
# release.sh — Build and package release artefacts for Alchementary Link.
# Produces per-platform zips, manifests, and version files for every supported
# 64-bit desktop OS/architecture combination.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"
source "$SCRIPT_DIR/scripts/build-common.sh"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

DEFAULT_UPDATE_URL="https://alchementary.co.uk/download/link/manifest-{platform}-{arch}.json"
DEFAULT_RELEASE_BASE_URL="https://alchementary.co.uk/download/link"
WAILS_VERSION="$(link_wails_version go.mod)"
GO_TOOLCHAIN_VERSION="$(
    awk '
        $1 == "go" { go_version = $2 }
        $1 == "toolchain" {
            sub(/^go/, "", $2)
            toolchain_version = $2
        }
        END {
            if (toolchain_version != "") {
                print toolchain_version
            } else {
                print go_version
            }
        }
    ' go.mod
)"
DEFAULT_LINUX_BUILD_IMAGE="docker.io/library/golang:${GO_TOOLCHAIN_VERSION:-1.26}-bookworm"
DEFAULT_NODE_VERSION="20.19.4"

log()  { echo -e "${GREEN}[link]${NC} $1"; }
warn() { echo -e "${YELLOW}[link]${NC} $1"; }
err()  { echo -e "${RED}[link]${NC} $1" >&2; }

host_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux) echo "linux" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) echo "unknown" ;;
    esac
}

build_tags() {
    link_build_tags
}

default_targets() {
    printf '%s\n' \
        "darwin/amd64" \
        "darwin/arm64" \
        "linux/amd64" \
        "linux/arm64" \
        "windows/amd64" \
        "windows/arm64"
}

release_targets() {
    if [ -n "${LINK_RELEASE_TARGETS:-}" ]; then
        echo "$LINK_RELEASE_TARGETS" | tr ',' '\n' | sed '/^[[:space:]]*$/d'
        return
    fi

    default_targets
}

check_deps() {
    command -v go >/dev/null 2>&1 || { err "Go is not installed"; exit 1; }
    export PATH="$PATH:$(go env GOPATH)/bin"
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

    ensure_upx
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
    link_build_version
}

build_ldflags() {
    link_build_ldflags "$1"
}

container_engine() {
    local candidate
    for candidate in podman docker; do
        if command -v "$candidate" >/dev/null 2>&1 \
            && "$candidate" info >/dev/null 2>&1; then
            echo "$candidate"
            return 0
        fi
    done
    return 1
}

ensure_upx() {
    if ! link_bool_enabled "${LINK_COMPRESS:-0}"; then
        return
    fi
    local requires_upx=0
    local target
    while IFS= read -r target; do
        [ -n "$target" ] || continue
        local target_os="${target%%/*}"
        if link_use_upx_for_target "$target_os"; then
            requires_upx=1
            break
        fi
    done < <(release_targets)
    if [ "$requires_upx" -eq 0 ]; then
        return
    fi
    if command -v upx >/dev/null 2>&1; then
        return
    fi
    err "LINK_COMPRESS=1 requires UPX. Install it first, for example: brew install upx"
    exit 1
}

build_linux_target_in_container() {
    local target="$1"
    local version="$2"
    local out_dir="$3"
    local target_os="${target%%/*}"
    local target_arch="${target##*/}"

    local engine
    if ! engine="$(container_engine)"; then
        warn "Linux targets require a running Podman or Docker engine on $(host_os)."
        return 1
    fi

    local node_arch
    case "$target_arch" in
        amd64) node_arch="x64" ;;
        arm64) node_arch="arm64" ;;
        *)
            warn "No Node.js Linux archive mapping for ${target_arch}."
            return 1
            ;;
    esac

    mkdir -p "$SCRIPT_DIR/build"
    local container_release_dir
    container_release_dir="$(mktemp -d "$SCRIPT_DIR/build/.release-linux.XXXXXX")"
    local container_release_root="build/$(basename "$container_release_dir")"
    local image="${LINK_LINUX_BUILD_IMAGE:-$DEFAULT_LINUX_BUILD_IMAGE}"
    local node_version="${LINK_NODE_VERSION:-$DEFAULT_NODE_VERSION}"
    local builder_image="link-linux-release:${GO_TOOLCHAIN_VERSION:-1.26}-${node_version}-${target_arch}"

    log "Preparing the ${engine} Linux/${target_arch} release builder..."
    if ! "$engine" build \
        --platform "linux/${target_arch}" \
        --build-arg "GO_IMAGE=${image}" \
        --build-arg "NODE_ARCH=${node_arch}" \
        --build-arg "NODE_VERSION=${node_version}" \
        --build-arg "WAILS_VERSION=${WAILS_VERSION}" \
        -t "$builder_image" \
        -f "$SCRIPT_DIR/scripts/linux-release.Dockerfile" \
        "$SCRIPT_DIR/scripts"; then
        warn "Could not prepare the Linux/${target_arch} release builder."
        rm -rf -- "$container_release_dir"
        return 1
    fi

    log "Building ${target} in the ${engine} container..."
    if ! "$engine" run --rm \
        --platform "linux/${target_arch}" \
        -v "${SCRIPT_DIR}:/src" \
        --tmpfs /src/frontend/node_modules \
        -w /src \
        -e "LINK_RELEASE_TARGETS=${target}" \
        -e "LINK_RELEASE_ROOT=${container_release_root}" \
        -e "LINK_UPDATE_URL=${LINK_UPDATE_URL}" \
        -e "LINK_RELEASE_BASE_URL=${LINK_RELEASE_BASE_URL}" \
        -e "GIT_CONFIG_COUNT=1" \
        -e "GIT_CONFIG_KEY_0=safe.directory" \
        -e "GIT_CONFIG_VALUE_0=/src" \
        "$builder_image" \
        ./release.sh; then
        warn "Container build failed for ${target}."
        rm -rf -- "$container_release_dir"
        return 1
    fi

    local output_base="AlchementaryLink-${target_os}-${target_arch}"
    local container_out="${container_release_dir}/${version}"
    if [ ! -f "${container_out}/${output_base}" ] \
        || [ ! -f "${container_out}/${output_base}.zip" ]; then
        warn "Container did not produce the expected artefacts for ${target}."
        rm -rf -- "$container_release_dir"
        return 1
    fi

    cp "${container_out}/${output_base}" "${out_dir}/${output_base}"
    cp "${container_out}/${output_base}.zip" "${out_dir}/${output_base}.zip"
    echo "${target_os}|${target_arch}|${output_base}.zip" >> "${out_dir}/.release-files"
    rm -rf -- "$container_release_dir"
}

build_target() {
    local target_os="$1"
    local target_arch="$2"
    local version="$3"
    local out_dir="$4"

    local platform="${target_os}/${target_arch}"

    local output_base="AlchementaryLink-${target_os}-${target_arch}"
    local output_path
    if [ "$target_os" = "darwin" ]; then
        output_path="${out_dir}/${output_base}.app"
    elif [ "$target_os" = "windows" ]; then
        output_path="${out_dir}/${output_base}.exe"
    else
        output_path="${out_dir}/${output_base}"
    fi

    log "Building ${platform}..."
    link_configure_pure_go_build
    local ldflags
    ldflags="$(build_ldflags "$version")"
    local tags
    tags="$(build_tags)"
    local build_args=("-platform" "$platform" "-trimpath")
    if link_use_upx_for_target "$target_os"; then
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
    build_args+=("-ldflags" "$ldflags" "-o" "$output_path")
    if ! CGO_ENABLED=0 wails build "${build_args[@]}"; then
        warn "Build failed for ${platform}"
        return 1
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
    local base_url="${LINK_RELEASE_BASE_URL:-}"

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

    if [ -z "${LINK_UPDATE_URL:-}" ]; then
        LINK_UPDATE_URL="${DEFAULT_UPDATE_URL}"
    fi
    if [ -z "${LINK_RELEASE_BASE_URL:-}" ]; then
        LINK_RELEASE_BASE_URL="${DEFAULT_RELEASE_BASE_URL}"
    fi

    local release_root="${LINK_RELEASE_ROOT:-build/releases}"
    local out_dir="${release_root}/${version}"
    mkdir -p "$out_dir"

    log "Building release ${version}..."
    : > "${out_dir}/.release-files"

    local targets=()
    local selected_target
    while IFS= read -r selected_target; do
        [ -n "$selected_target" ] && targets+=("$selected_target")
    done < <(release_targets)
    if [ "${#targets[@]}" -eq 0 ]; then
        err "No release targets selected."
        exit 1
    fi

    local errors=0
    local host
    host="$(host_os)"
    local target
    local container_linux_targets=()
    if [ "$host" != "linux" ]; then
        for target in "${targets[@]}"; do
            if [[ "$target" == linux/* ]]; then
                container_linux_targets+=("$target")
            fi
        done
    fi

    if [ "${#container_linux_targets[@]}" -gt 0 ]; then
        for target in "${container_linux_targets[@]}"; do
            if ! build_linux_target_in_container "$target" "$version" "$out_dir"; then
                errors=1
            fi
        done
    fi

    for target in "${targets[@]}"; do
        local target_os="${target%%/*}"
        local target_arch="${target##*/}"
        if [ "$target_os" = "$target_arch" ] || [ -z "$target_os" ] || [ -z "$target_arch" ]; then
            err "Invalid release target: ${target}"
            errors=1
            continue
        fi
        if [ "$target_os" = "linux" ] && [ "$host" != "linux" ]; then
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
