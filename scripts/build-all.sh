#!/usr/bin/env bash
# SPDX-License-Identifier: LicenseRef-Proprietary
# Copyright (C) 2026 Andy Dixon t/a Uncomplex
# Build Nexus Terminal for every platform and package each into dist/<platform>/.
#
#   scripts/build-all.sh [--platforms p1,p2,...] [--debug] [--keep-going]
#
# Platforms: linux-x86_64 linux-aarch64 windows-x86_64 windows-aarch64 macos-aarch64 macos-x86_64
# (default: all; those this host cannot build are SKIPPED with the reason).
# Output: dist/<platform>/nexus-terminal-<version>-<platform>.zip
#   macOS:   NexusTerminal.app (icon, Info.plist, ad-hoc signed; SIGN_IDENTITY for a Developer ID)
#   Windows: nexus-terminal.exe (icon and version info embedded by rust/app/build.rs)
#   Linux:   nexus-terminal, .desktop entry, icons and install.sh (installs into ~/.local)
# each with LICENSE, and next to it manifest-<os>-<arch>.json + version-<os>-<arch>.txt for the
# update server (updates/index.php; Go-style names darwin/linux/windows, amd64/arm64).
# The version is <latest tag>-<short commit> unless NEXUS_VERSION is set; NEXUS_UPDATE_URL and
# NEXUS_RELEASE_BASE_URL override the update locations. Windows on Linux needs MinGW-w64.
# .github/workflows/release.yml runs this once per platform on native runners.
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root" || exit 1
PRODUCT=nexus-terminal
VERSION="${NEXUS_VERSION:-$(git describe --tags --abbrev=0 2>/dev/null || echo dev)-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
export NEXUS_VERSION="$VERSION"
export NEXUS_UPDATE_URL="${NEXUS_UPDATE_URL:-https://dixon.cx/downloads/nexus/manifest-{platform}-{arch}.json}"
BASE_URL="${NEXUS_RELEASE_BASE_URL:-https://dixon.cx/downloads/nexus}"
target_dir="$root/rust/target"

# rustup target add runs in rust/ so it reaches the toolchain rust-toolchain.toml pins.
cargo_build() { t=$1; shift; (cd rust && { rustup target add "$t" >/dev/null 2>&1 || true; } && cargo build "$@" -p nexus --target "$t"); }

package() {
    p=$1 bin=$3 out=$4
    name="$PRODUCT-$VERSION-$p" stage="$4/$PRODUCT-$VERSION-$p"
    mkdir -p "$stage"
    case "$p" in
        macos-*)
            NEXUS_BIN="$bin/nexus-terminal" SIGN_IDENTITY="${SIGN_IDENTITY:-}" rust/packaging/macos/build-app.sh
            cp -R rust/target/macos/NexusTerminal.app "$stage/" ;;
        windows-*) cp "$bin/nexus-terminal.exe" "$stage/" ;;
        linux-*)
            cp "$bin/nexus-terminal" "$stage/"
            strip "$stage/nexus-terminal" 2>/dev/null || true
            cp rust/packaging/linux/nexus-terminal.desktop rust/packaging/linux/install.sh "$stage/"
            mkdir -p "$stage/app/assets/icon" && cp rust/app/assets/icon/icon-256.png rust/app/assets/icon/icon-512.png "$stage/app/assets/icon/"
            sed -i 's|\$HERE/../../app|$HERE/app|; s|\$HERE/../../target/release/nexus-terminal|$HERE/nexus-terminal|' "$stage/install.sh" ;;
    esac
    cp LICENSE "$stage/"
    zip_dir "$stage" "$stage.zip"
    rm -rf "$stage"

    os=${p%%-*}; [ "$os" = macos ] && os=darwin
    arch=amd64; [ "${p#*-}" = aarch64 ] && arch=arm64
    sha="$(shasum -a 256 "$stage.zip" 2>/dev/null || sha256sum "$stage.zip")"; sha="${sha%% *}"
    size="$(wc -c < "$stage.zip" | tr -d ' ')"
    echo "$VERSION" > "$out/version-$os-$arch.txt"
    cat > "$out/manifest-$os-$arch.json" <<JSON
{
  "latestVersion": "${VERSION}",
  "publishedAt": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "releaseURL": "${BASE_URL}/",
  "files": [
    {"platform":"${os}","arch":"${arch}","filename":"${name}.zip","url":"${BASE_URL}/${name}.zip","sha256":"${sha}","size":${size}}
  ]
}
JSON
    echo "$stage.zip"
}

# ---- shared by every Uncomplex repo's scripts/build-all.sh (keep in step) ------------------
# Each repo's script defines, above this block: PRODUCT, VERSION, cargo_build <triple> <cargo flags>
# and package <platform> <triple> <bin dir> <out dir>. Bash 3.2 compatible (macOS).
LLVM_MINGW=${LLVM_MINGW:-$HOME/.local/llvm-mingw}
target_dir=${target_dir:-${CARGO_TARGET_DIR:-$root/target}}
ALL="linux-x86_64 linux-aarch64 windows-x86_64 windows-aarch64 macos-aarch64 macos-x86_64"

platforms=$ALL profile=release keep_going=0
while [ $# -gt 0 ]; do
    case "$1" in
        --platforms) platforms=$(echo "${2:-}" | tr ',' ' '); shift ;;
        --platforms=*) platforms=$(echo "${1#*=}" | tr ',' ' ') ;;
        --debug) profile=debug ;;
        --keep-going) keep_going=1 ;;
        -h|--help) sed -n '2,/^set /p' "$0" | sed -e '/^set /d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
    esac
    shift
done
for p in $platforms; do
    case " $ALL " in *" $p "*) ;; *) echo "unknown platform: $p (one of: $ALL)" >&2; exit 2 ;; esac
done

have() { command -v "$1" >/dev/null 2>&1; }
case "$(uname -s)" in Darwin) host_os=macos ;; Linux) host_os=linux ;; MINGW*|MSYS*|CYGWIN*) host_os=windows ;; *) host_os=unknown ;; esac
case "$(uname -m)" in x86_64|amd64) host_arch=x86_64 ;; arm64|aarch64) host_arch=aarch64 ;; *) host_arch=unknown ;; esac

triple() {
    case "$1" in
        linux-*) echo "${1#linux-}-unknown-linux-gnu" ;;
        macos-*) echo "${1#macos-}-apple-darwin" ;;
        windows-*)
            if [ "$host_os" = windows ]; then echo "${1#windows-}-pc-windows-msvc"
            elif [ "$1" = windows-aarch64 ]; then echo aarch64-pc-windows-gnullvm
            else echo x86_64-pc-windows-gnu; fi ;;
    esac
}

# Prints why platform $1 cannot be built on this host; prints nothing if it can.
missing() {
    case "$1" in
        linux-*)
            [ "$host_os" = linux ] || { echo "build the Linux targets on a Linux host"; return; }
            [ "${1#linux-}" = "$host_arch" ] || echo "build on a ${1#linux-} Linux host (no cross toolchain configured)" ;;
        windows-*)
            case "$host_os-$1" in
                windows-*) ;;
                linux-windows-x86_64) have x86_64-w64-mingw32-gcc || echo "needs MinGW-w64 (apt install gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64), or build on Windows" ;;
                linux-windows-aarch64) [ -x "$LLVM_MINGW/bin/aarch64-w64-mingw32-clang" ] || echo "needs llvm-mingw in $LLVM_MINGW (or set LLVM_MINGW), or build on Windows" ;;
                *) echo "build on a Windows host" ;;
            esac ;;
        macos-*)
            if [ "$host_os" != macos ]; then
                echo "needs a Mac: Apple's SDK may not be used on $host_os; use the Release GitHub workflow"
            else
                have xcrun || echo "needs the Xcode command line tools (xcode-select --install)"
            fi ;;
    esac
}

# zip_dir <dir> <out.zip>: the zip holds <dir>'s basename as its top folder, keeping modes and symlinks.
zip_dir() {
    (cd "$(dirname "$1")" && rm -f "$2" &&
        if [ "$host_os" = macos ]; then ditto -c -k --sequesterRsrc --keepParent "$(basename "$1")" "$2"
        elif have zip; then zip -qry "$2" "$(basename "$1")"
        elif have 7z; then 7z a -tzip -bso0 -bsp0 "$2" "$(basename "$1")"
        else powershell -NoProfile -NonInteractive -Command "Compress-Archive -Path '$(basename "$1")' -DestinationPath '$2' -Force"
        fi)
}

# Ad-hoc signature so Apple silicon will run the bundle; set SIGN_IDENTITY for a Developer ID.
sign_app() {
    have codesign || return 0
    if [ -n "${SIGN_IDENTITY:-}" ]; then codesign --force --deep --options runtime --timestamp --sign "$SIGN_IDENTITY" "$1"
    else codesign --force --deep --sign - "$1"; fi
}

build() {
    p=$1 t=$(triple "$1")
    rel=; [ "$profile" = release ] && rel=--release
    rustup target add "$t" >/dev/null 2>&1 || true
    # llvm-mingw provides the aarch64 Windows linker, windres and dlltool (this subshell only).
    if [ "$t" = aarch64-pc-windows-gnullvm ]; then
        export PATH="$LLVM_MINGW/bin:$PATH" WINDRES=aarch64-w64-mingw32-windres
        export CARGO_TARGET_AARCH64_PC_WINDOWS_GNULLVM_LINKER=aarch64-w64-mingw32-clang
        # Static libunwind: otherwise the exe needs llvm-mingw's libunwind.dll, which Windows lacks.
        export CARGO_TARGET_AARCH64_PC_WINDOWS_GNULLVM_RUSTFLAGS="-C target-feature=+crt-static"
    fi
    cargo_build "$t" $rel --locked || return 1
    out="$root/dist/$p"
    rm -rf "$out" && mkdir -p "$out" || return 1
    package "$p" "$t" "$target_dir/$t/$profile" "$out"
}

summary=""
failed=0
for p in $ALL; do
    case " $platforms " in *" $p "*) ;; *) continue ;; esac
    why=$(missing "$p")
    if [ -n "$why" ]; then
        summary="$summary$p|SKIPPED|$why
"
        echo "=== $p: SKIPPED: $why"
        continue
    fi
    echo "=== $p: building $PRODUCT $VERSION ($(triple "$p"), $profile)"
    # A subshell so set -e applies to the product's package step (it is ignored inside an if).
    (set -e; build "$p"); status=$?
    if [ $status = 0 ]; then
        summary="$summary$p|OK|dist/$p/
"
    else
        summary="$summary$p|FAILED|see the log above
"
        failed=1
        [ $keep_going = 1 ] || { echo "=== $p failed; stopping (pass --keep-going to continue)"; break; }
    fi
done

echo
printf '%-16s %-8s %s\n' PLATFORM RESULT 'OUTPUT / REASON'
printf '%s' "$summary" | while IFS='|' read -r p r o; do printf '%-16s %-8s %s\n' "$p" "$r" "$o"; done
exit $failed
