.PHONY: all build dev clean test test-cover lint install deps frontend-deps run package help

# Default target
all: build

# Install all dependencies
deps: frontend-deps
	go mod download

# Install frontend dependencies
frontend-deps:
	cd frontend && npm install

# Development mode with hot reload
dev: deps
	wails dev

# Production build
build: deps
	wails build

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

# Help
help:
	@echo "Nexus Terminal — Build Targets"
	@echo ""
	@echo "  make deps          — Install all dependencies"
	@echo "  make dev           — Run in development mode with hot reload"
	@echo "  make build         — Production build"
	@echo "  make test          — Run tests"
	@echo "  make test-cover    — Run tests with coverage report"
	@echo "  make clean         — Clean build artefacts"
	@echo "  make lint          — Run linter (requires golangci-lint)"
	@echo "  make package       — Build and package for distribution"
	@echo "  make help          — Show this help"
