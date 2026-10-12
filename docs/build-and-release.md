# Build and Release

Nexus Terminal is a Rust workspace in `rust/`:

```
rust/
├── core/   nexus-core: storage, secrets, protocol clients, session manager, Service. No GUI.
└── app/    nexus: the egui desktop app (binary `nexus-terminal`)
```

The toolchain is pinned in `rust/rust-toolchain.toml`; `rustup` installs it on first build.

## Prerequisites

- **All platforms:** [rustup](https://rustup.rs).
- **Linux:** the X11/Wayland/GL headers eframe links against, and GTK for native file dialogs:

  ```bash
  sudo apt-get install -y build-essential pkg-config libxkbcommon-dev libgl1-mesa-dev \
    libx11-dev libxcursor-dev libxrandr-dev libxi-dev libwayland-dev libgtk-3-dev zip
  ```

- **macOS:** Xcode command-line tools (`xcode-select --install`).
- **Windows:** the MSVC build tools (Visual Studio Build Tools, "Desktop development with C++").

There are no runtime C libraries to install: SQLite is compiled in and TLS uses rustls.

## Build and run

```bash
cd rust
cargo run --release                 # the app
cargo test --workspace              # unit and integration tests
cargo clippy --workspace -- -D warnings
```

Useful flags (also handy for testing): `nexus-terminal [--theme pro|midnight|light]
[--data-dir DIR] [--screenshot out.png] [--size WxH] [--run-local CMD]`. `--data-dir` points at
another data directory; `--screenshot` renders the window, saves a PNG and exits (works under
`xvfb-run`); `--run-local` opens a local terminal and types `CMD` into it.

The SSH integration tests start a throwaway `sshd` as the current user on a random port; they
skip themselves when OpenSSH isn't installed.

## Release packages

```bash
cd rust
./release.sh                                            # this machine's platform
NEXUS_RELEASE_TARGETS=linux/amd64,windows/amd64 ./release.sh
```

Artefacts land in `build/releases/<version>/` at the repo root:

- `NexusTerminal-<os>-<arch>.zip` — on macOS a universal-capable `.app` bundle
  (`rust/packaging/macos/build-app.sh`), on Windows the `.exe` (icon and version info embedded,
  GUI subsystem), on Linux the binary plus `install.sh` and a `.desktop` entry
  (`rust/packaging/linux/install.sh` installs into `~/.local`).
- `manifest-<os>-<arch>.json` and `version-<os>-<arch>.txt` for the update server
  (`updates/index.php`), in the same format as before.

The version is `<latest tag>-<short commit>` unless `NEXUS_VERSION` is set. The update manifest
URL is compiled in from `NEXUS_UPDATE_URL` (default in `release.sh`); `{platform}` and `{arch}` use
the old Go names (`darwin`/`linux`/`windows`, `amd64`/`arm64`).

Cross-building `windows/amd64` from Linux works with `x86_64-pc-windows-gnu` and mingw-w64.
macOS bundles must be built on a Mac. CI (`.github/workflows/release-builds.yml`) builds every
platform natively on GitHub runners and attaches the zips to tagged releases;
`.github/workflows/ci.yml` runs tests and clippy on Linux, macOS and Windows.

## Signing

macOS bundles are ad-hoc signed. For distribution, sign with a Developer ID and notarize (see the
comments in `build-app.sh`). Windows binaries are unsigned.
