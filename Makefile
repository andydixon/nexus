.PHONY: all build build-compressed dev clean test test-cover lint install deps frontend-deps upx-check run package release help

# Resolve the Wails binary. Prefer wails on PATH; fall back to GOPATH/bin.
WAILS := $(or $(shell which wails 2>/dev/null),$(shell go env GOPATH)/bin/wails)
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)-$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LD_UPDATE_URL := $(if $(NEXUS_UPDATE_URL),-X github.com/andydixon/nexus/internal/api.appUpdateURL=$(NEXUS_UPDATE_URL),)
UPX_FLAGS ?=
NEXUS_FORCE_MACOS_UPX ?= 0
GOBIN_DIR := $(shell go env GOPATH)/bin
export PATH := $(GOBIN_DIR):$(PATH)

# Default target
all: build

# Install all dependencies
deps: frontend-deps
	go mod download

upx-check:
	@command -v upx >/dev/null 2>&1 || { echo "UPX is required for compressed builds. Install it first, for example: brew install upx"; exit 1; }
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$(NEXUS_FORCE_MACOS_UPX)" != "1" ]; then \
		echo "UPX compression is disabled for macOS by default. Use ./release.sh for a zipped .app, or run NEXUS_FORCE_MACOS_UPX=1 UPX_FLAGS='--force-macos --best --no-color --no-progress' make build-compressed."; \
		exit 1; \
	fi
	@if [ "$$(uname -s)" = "Darwin" ] && [ "$(NEXUS_FORCE_MACOS_UPX)" = "1" ] && ! printf '%s\n' "$(UPX_FLAGS)" | grep -q -- '--force-macos'; then \
		echo "macOS UPX builds must pass --force-macos in UPX_FLAGS."; \
		exit 1; \
	fi

# Install frontend dependencies
frontend-deps:
	cd frontend && npm install

# Development mode with hot reload
dev: deps
	$(WAILS) dev

# Production build
build: deps
	CGO_CFLAGS="$${CGO_CFLAGS:--O2 -g0}" CGO_CXXFLAGS="$${CGO_CXXFLAGS:--O2 -g0}" $(WAILS) build -trimpath -ldflags "-s -w -buildid= -X github.com/andydixon/nexus/internal/api.appVersion=$(VERSION) $(LD_UPDATE_URL)"

# Production build with UPX compression
build-compressed: deps upx-check
	CGO_CFLAGS="$${CGO_CFLAGS:--O2 -g0}" CGO_CXXFLAGS="$${CGO_CXXFLAGS:--O2 -g0}" $(WAILS) build -trimpath -upx $(if $(UPX_FLAGS),-upxflags "$(UPX_FLAGS)",) -ldflags "-s -w -buildid= -X github.com/andydixon/nexus/internal/api.appVersion=$(VERSION) $(LD_UPDATE_URL)"

# Run tests
test:
	go test ./... -v -count=1

# Run tests with coverage
test-cover:
	go test ./... -v -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Clean build artefacts
clean:
	rm -rf build/bin
	rm -rf frontend/node_modules
	rm -rf frontend/src/js/lib
	rm -rf frontend/src/css/lib
	go clean -testcache

# Lint (requires golangci-lint)
lint:
	golangci-lint run ./...

# Package for distribution
package: build
	@echo "Binary built at build/bin/"

# Build and package release artefacts for all platforms
release:
	./release.sh

# Help
help:
	@echo "Nexus Terminal — Build Targets"
	@echo ""
	@echo "  make deps          — Install all dependencies"
	@echo "  make dev           — Run in development mode with hot reload"
	@echo "  make build         — Production build"
	@echo "  make build-compressed — Production build with UPX compression"
	@echo "  make test          — Run tests"
	@echo "  make test-cover    — Run tests with coverage report"
	@echo "  make clean         — Clean build artefacts"
	@echo "  make lint          — Run linter (requires golangci-lint)"
	@echo "  make package       — Build and package for distribution"
	@echo "  make release       — Build multi-platform release artefacts"
	@echo "  make help          — Show this help"
