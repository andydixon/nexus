#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Andy Dixon
#
# Publish a Nexus Terminal release as the `nexusterminal` package (.deb, .rpm and Arch) in the
# signed apt, dnf and pacman repositories served at https://repo.dixon.cx.
#
#   packaging/repo.sh          # the newest tag
#   packaging/repo.sh v3.0.2   # a given tag
#
# Unlike the command-line tools' copies of this script, nothing is built here: Nexus is a GUI app
# linked against the platform's windowing stack, so the packages wrap the official binaries that
# the Release Builds workflow made for the tag (native glibc builds on amd64 and arm64). They're
# downloaded from the GitHub release and checked against its SHA256SUMS first.
#
# The publish half is the same as in ../../../mapsize/packaging/repo.sh and friends: every index
# is rebuilt over all packages in the repository, so other projects' packages are kept.
#
# Needs: curl, unzip, curl (for the pinned nfpm), docker, gpg, apt-ftparchive, flock.
#   REPO_DIR        where the repository lives (default below)
#   REPO_GNUPGHOME  GnuPG home holding the signing key (default below)
#   ARCHS           package architectures (default: amd64 arm64)
set -euo pipefail

# ---- project ---------------------------------------------------------------
PACKAGE=nexusterminal
SUMMARY="Native desktop client for SSH, SFTP, Telnet, Serial, VNC, RDP, IRC and GitHub"
HOMEPAGE=https://dixon.cx/nexus
GITHUB=andydixon/nexus
LICENSE=AGPL-3.0-only
ARCHS=${ARCHS:-"amd64 arm64"}
# The binaries come from ubuntu-22.04 runners: glibc 2.35. Windowing, GL and audio libraries are
# loaded at run time, so they're recommended rather than hard dependencies (any desktop has them).
# ALSA is linked directly (RDP audio), so it's a hard dependency. libasound2t64 comes first: on
# Ubuntu 24.04 and later plain libasound2 is only a virtual name, which apt may satisfy with the
# OSS shim liboss4-salsa-asound2 (missing real ALSA symbols); older Debian falls through to it.
DEB_DEPENDS='"libc6 (>= 2.35)", "libgcc-s1", "zlib1g", "libasound2t64 | libasound2"'
DEB_RECOMMENDS='"libxkbcommon0", "libwayland-client0", "libx11-6", "libxcursor1", "libxrandr2", "libxi6", "libgl1", "libegl1", "xdg-desktop-portal"'
RPM_DEPENDS='"glibc >= 2.35", "libgcc", "zlib", "alsa-lib"'
RPM_RECOMMENDS='"libxkbcommon", "libwayland-client", "libX11", "libXcursor", "libXrandr", "libXi", "mesa-libGL", "mesa-libEGL", "xdg-desktop-portal"'
ARCH_DEPENDS='"glibc", "gcc-libs", "zlib", "alsa-lib", "libxkbcommon", "libglvnd"'
ARCH_SUGGESTS='"wayland", "libx11", "libxcursor", "libxrandr", "libxi", "xdg-desktop-portal"'
# Package revision: bump to republish the same Nexus version with packaging fixes.
PKGREL=${PKGREL:-1}
# ----------------------------------------------------------------------------

REPO_DIR=${REPO_DIR:-/home/andy/domains/repo.dixon.cx/public_html}
REPO_GNUPGHOME=${REPO_GNUPGHOME:-$HOME/.config/repo.dixon.cx/gnupg}
SUITE=stable
COMPONENT=main
ARCH_REPO=dixon # the pacman repository name: [dixon]
HERE=$(cd "$(dirname "$0")" && pwd)

say() { echo "repo: $*" >&2; }
die() { say "$*"; exit 1; }

for tool in unzip curl docker gpg apt-ftparchive flock sha256sum; do
	command -v $tool >/dev/null || die "needs $tool"
done
[[ -d $REPO_DIR ]] || die "$REPO_DIR does not exist"
export GNUPGHOME=$REPO_GNUPGHOME
KEY=$(gpg --list-secret-keys --with-colons 2>/dev/null | awk -F: '/^fpr/{print $10; exit}')
[[ -n $KEY ]] || die "no signing key in $GNUPGHOME"

cd "$(git -C "$HERE" rev-parse --show-toplevel)"
TAG=${1:-$(git describe --tags --abbrev=0)}
VERSION=${TAG#v}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
gpg --batch --armor --export-secret-keys "$KEY" >"$WORK/signing.asc"

# The pinned nfpm, as in the other projects (checksummed download, cached under target/tools).
NFPM_VERSION=2.41.1
case $(uname -m) in
x86_64) nfpm_arch=x86_64 nfpm_sum=b3cf95aa6dabed836d09ad7f0c190a13c74c5b1304db60846f0f702ee407f430 ;;
aarch64) nfpm_arch=arm64 nfpm_sum=17350a838c8e2c422c6e573ed379b18424565d2de8a2b1cb1b20211976124eb5 ;;
*) die "unsupported host $(uname -m)" ;;
esac
NFPM_DIR=$PWD/rust/target/tools/nfpm-$NFPM_VERSION
if [[ ! -x $NFPM_DIR/nfpm ]]; then
	mkdir -p "$NFPM_DIR"
	curl -fsSL -o "$NFPM_DIR/nfpm.tar.gz" "https://github.com/goreleaser/nfpm/releases/download/v$NFPM_VERSION/nfpm_${NFPM_VERSION}_Linux_$nfpm_arch.tar.gz"
	echo "$nfpm_sum  $NFPM_DIR/nfpm.tar.gz" | sha256sum -c --quiet
	tar -xzf "$NFPM_DIR/nfpm.tar.gz" -C "$NFPM_DIR" nfpm
	rm -f "$NFPM_DIR/nfpm.tar.gz"
fi
NFPM=$NFPM_DIR/nfpm

# ---- fetch and verify the release binaries ---------------------------------
say "fetching $TAG from github.com/$GITHUB"
mkdir -p "$WORK/dl"
base=https://github.com/$GITHUB/releases/download/$TAG
for f in SHA256SUMS $(for arch in $ARCHS; do echo "NexusTerminal-linux-$arch.zip"; done); do
	curl -fsSL -o "$WORK/dl/$f" "$base/$f" || die "release $TAG has no $f yet"
done
(cd "$WORK/dl" && grep -E "NexusTerminal-linux-($(tr ' ' '|' <<<"$ARCHS"))\.zip" SHA256SUMS | sha256sum -c --quiet) || die "checksum mismatch"

# ---- package -----------------------------------------------------------------
say "packaging $PACKAGE $VERSION for $ARCHS"
mkdir -p "$WORK/out"
for arch in $ARCHS; do
	src=$WORK/src-$arch
	mkdir -p "$src"
	unzip -q "$WORK/dl/NexusTerminal-linux-$arch.zip" -d "$src"
	[[ -x $src/nexus-terminal ]] || die "no nexus-terminal binary in the $arch zip"
	cp "$src/nexus-terminal.desktop" "$src/$PACKAGE.desktop"
	cat >"$WORK/nfpm-$arch.yaml" <<-EOF
	name: $PACKAGE
	arch: $arch
	platform: linux
	version: $VERSION
	release: $PKGREL
	section: net
	maintainer: Andy Dixon <andy@dixon.cx>
	description: |
	  $SUMMARY.
	  Tabs, split panes, SFTP, port forwards, jump hosts, session recording and an
	  encrypted credential vault, in one native app written in Rust.
	homepage: $HOMEPAGE
	license: $LICENSE
	contents:
	  - src: $src/nexus-terminal
	    dst: /usr/bin/nexus-terminal
	    file_info: {mode: 0755}
	  - src: /usr/bin/nexus-terminal
	    dst: /usr/bin/$PACKAGE
	    type: symlink
	  - src: $src/$PACKAGE.desktop
	    dst: /usr/share/applications/$PACKAGE.desktop
	    file_info: {mode: 0644}
	  - src: $src/app/assets/icon/icon-256.png
	    dst: /usr/share/icons/hicolor/256x256/apps/nexus-terminal.png
	    file_info: {mode: 0644}
	  - src: $src/app/assets/icon/icon-512.png
	    dst: /usr/share/icons/hicolor/512x512/apps/nexus-terminal.png
	    file_info: {mode: 0644}
	  - src: $src/LICENSE
	    dst: /usr/share/licenses/$PACKAGE/LICENSE
	    file_info: {mode: 0644}
	  - src: $PWD/README.md
	    dst: /usr/share/doc/$PACKAGE/README.md
	    file_info: {mode: 0644}
	overrides:
	  deb:
	    depends: [$DEB_DEPENDS]
	    recommends: [$DEB_RECOMMENDS]
	  rpm:
	    depends: [$RPM_DEPENDS]
	    recommends: [$RPM_RECOMMENDS]
	  archlinux:
	    depends: [$ARCH_DEPENDS]
	    suggests: [$ARCH_SUGGESTS]
	archlinux:
	  packager: Andy Dixon <andy@dixon.cx>
	rpm:
	  signature:
	    key_file: $WORK/signing.asc
	EOF
	for fmt in deb rpm archlinux; do
		"$NFPM" pkg -f "$WORK/nfpm-$arch.yaml" -p $fmt -t "$WORK/out/" >"$WORK/nfpm.log" 2>&1 ||
			{ cat "$WORK/nfpm.log" >&2; die "nfpm $fmt for $arch failed"; }
	done
done

# ---- publish (shared with the other projects' repo.sh) -----------------------
exec 9>"$REPO_DIR/../.publish.lock"
flock 9
say "publishing to $REPO_DIR"
gpg --batch --armor --export "$KEY" >"$REPO_DIR/dixon.asc"
gpg --batch --export "$KEY" >"$REPO_DIR/dixon.gpg"
sign() { gpg --batch --yes --local-user "$KEY" "$@"; }

pool=$REPO_DIR/apt/pool/$COMPONENT/${PACKAGE:0:1}/$PACKAGE
mkdir -p "$pool"
cp "$WORK"/out/*.deb "$pool/"
(
	cd "$REPO_DIR/apt"
	dists=dists/.$SUITE.new
	rm -rf "$dists"
	debarchs=$(find pool -name '*.deb' | sed -E 's/.*_([^_]+)\.deb$/\1/' | sort -u | tr '\n' ' ')
	for a in $debarchs; do
		d=$dists/$COMPONENT/binary-$a
		mkdir -p "$d"
		apt-ftparchive --arch "$a" packages pool >"$d/Packages"
		gzip -9kn "$d/Packages"
	done
	apt-ftparchive \
		-o APT::FTPArchive::Release::Origin=repo.dixon.cx \
		-o APT::FTPArchive::Release::Label=repo.dixon.cx \
		-o APT::FTPArchive::Release::Suite=$SUITE \
		-o APT::FTPArchive::Release::Codename=$SUITE \
		-o APT::FTPArchive::Release::Components=$COMPONENT \
		-o "APT::FTPArchive::Release::Architectures=${debarchs% }" \
		release "$dists" >"$WORK/Release"
	mv "$WORK/Release" "$dists/Release"
	sign --clearsign -o "$dists/InRelease" "$dists/Release"
	sign --armor --detach-sign -o "$dists/Release.gpg" "$dists/Release"
	rm -rf "dists/.$SUITE.old"
	[[ -d dists/$SUITE ]] && mv "dists/$SUITE" "dists/.$SUITE.old"
	mv "$dists" "dists/$SUITE"
	rm -rf "dists/.$SUITE.old"
)

mkdir -p "$REPO_DIR/rpm"
cp "$WORK"/out/*.rpm "$REPO_DIR/rpm/"
docker image inspect repo-tools:fedora >/dev/null 2>&1 ||
	docker build -q -t repo-tools:fedora - >/dev/null <<-'EOF'
	FROM fedora:42
	RUN dnf -y install createrepo_c && dnf clean all
	EOF
docker run --rm --user "$(id -u):$(id -g)" -v "$REPO_DIR/rpm:/repo" repo-tools:fedora \
	createrepo_c --quiet --update --general-compress-type=gz /repo
sign --armor --detach-sign -o "$REPO_DIR/rpm/repodata/repomd.xml.asc" "$REPO_DIR/rpm/repodata/repomd.xml"

added=
for pkg in "$WORK"/out/*.pkg.tar.zst; do
	a=$(basename "$pkg" .pkg.tar.zst)
	a=${a##*-}
	mkdir -p "$REPO_DIR/arch/$a"
	cp "$pkg" "$REPO_DIR/arch/$a/"
	sign --detach-sign -o "$REPO_DIR/arch/$a/$(basename "$pkg").sig" "$pkg"
	added+=" $a"
done
for a in $(tr ' ' '\n' <<<"$added" | sort -u); do
	dir=$REPO_DIR/arch/$a
	rm -f "$dir/$ARCH_REPO".{db,files}{,.tar.gz}{,.sig}
	docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$dir:/repo" -w /repo archlinux:latest \
		sh -c "repo-add --quiet --prevent-downgrade $ARCH_REPO.db.tar.gz *.pkg.tar.zst" 2>&1 |
		{ grep -v "newer version\|^$" >&2 || true; }
	for db in $ARCH_REPO.db $ARCH_REPO.files; do
		sign --detach-sign -o "$dir/$db.tar.gz.sig" "$dir/$db.tar.gz"
		ln -sfn "$db.tar.gz.sig" "$dir/$db.sig"
	done
	rm -f "$dir"/*.old "$dir"/*.old.sig
done

say "published $PACKAGE $VERSION: $(cd "$WORK/out" && ls | wc -l) packages"
