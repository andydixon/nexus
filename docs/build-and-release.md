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

Build packaged releases for every supported 64-bit desktop target:

```bash
./release.sh
```

By default, `./release.sh` builds all six OS/architecture combinations:

- macOS (`darwin`): `amd64` and `arm64`
- Linux: `amd64` and `arm64`
- Windows: `amd64` and `arm64`

Go calls the x86-64 architecture `amd64` and the AArch64 architecture `arm64`.

On macOS, Linux builds run in architecture-matched Podman or Docker containers
because Wails does not cross-compile Linux applications directly from macOS.
The reusable builder images keep their `node_modules` separate from the host
checkout.

## Optional targeted builds

The release scripts disable CGO and use a pure-Go SQLite driver, so Linux and Windows cross-targets do not need C cross-compilers for the application backend. To build a smaller set, override the default targets:

```bash
LINK_RELEASE_TARGETS=linux/amd64,windows/amd64 ./release.sh
```

## Platform caveats

- To produce all six targets in one run, use macOS with Podman or Docker running.
- macOS targets cannot be cross-built from Linux or Windows with Wails.
- Cross-target support still depends on Wails and frontend packaging support for the requested platform.
- Tested on Ubuntu 24.04 `amd64`: `linux/amd64` and `windows/amd64` build successfully.
