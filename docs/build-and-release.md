# Build and Release

This project uses [Wails](https://wails.io/) for desktop packaging.

## Ubuntu 24.04 prerequisites

Install the Linux build toolchain and Wails runtime headers with:

```bash
sudo apt-get update
sudo apt-get install -y \
  build-essential \
  libgtk-3-dev \
  libwebkit2gtk-4.1-dev \
  libayatana-appindicator3-dev \
  pkg-config \
  zip
```

Notes:

- Ubuntu 24.04 does not ship `libwebkit2gtk-4.0-dev`.
- Wails `v2.12.0` supports Ubuntu 24.04 by building with the `webkit2_41` tag.
- The local build scripts detect that package layout automatically and add the tag when needed.

You will also need these tools available in `PATH`:

- `go`
- `node`
- `npm`
- `wails`

Install the Wails CLI with:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Build commands

Build the app binary for the current host:

```bash
./build.sh
```

Build a packaged release for the current host platform:

```bash
./release.sh
```

By default, `./release.sh` now builds only the current host OS/architecture. On this Ubuntu 24.04 `amd64` host that means `linux/amd64`.

## Optional cross-target builds

The release scripts disable CGO and use a pure-Go SQLite driver, so Linux and Windows cross-targets no longer need C cross-compilers for the application backend. Request explicit targets with:

```bash
NEXUS_RELEASE_TARGETS=linux/amd64,windows/amd64 ./release.sh
```

## Platform caveats

- macOS targets cannot be cross-built from Linux with Wails. Build those on macOS.
- Cross-target support still depends on Wails and frontend packaging support for the requested platform.
- Tested on Ubuntu 24.04 `amd64`: `linux/amd64` and `windows/amd64` build successfully.
