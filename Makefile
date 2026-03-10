# Makefile for ErrorTransformer
#
# All binaries are built fully static (CGO_ENABLED=0, -tags netgo).
# This is safe because the tool uses only the Go standard library with no
# cgo code, so there is no libc dependency on any platform.
#
# Usage:
#   make                      # build for the host OS/arch
#   make build_all            # build every supported platform
#   make build_darwin_amd64
#   make build_darwin_arm64
#   make build_amd64          # linux/amd64 — static
#   make build_arm64          # linux/arm64 — static
#   make build_power8         # linux/ppc64le — static
#   make build_windows_amd64
#   make build_windows_arm64
#   make test                 # run all tests
#   make test_verbose         # run tests with -v
#   make test_cover           # run tests and generate HTML coverage report
#   make clean                # remove bin/ directory

# ─── Variables ───────────────────────────────────────────────────────────────

# Binary name
BINARY    := errortransformer

# Source package for the CLI
CMD_PKG   := ./cmd/errortransformer

# Output directory
BIN_DIR   := bin

# Embed version from git tag if available, otherwise "dev"
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Build timestamp (RFC-3339)
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go tool
GO        := go

# Static build flags applied to every target:
#   CGO_ENABLED=0   — disable cgo entirely; no libc linkage
#   -tags netgo     — use the pure-Go net package (no OS resolver)
#   -s -w           — strip symbol table and DWARF debug info (smaller binary)
CGO_ENABLED := 0
BUILD_TAGS  := netgo
LDFLAGS     := -ldflags="-s -w \
  -X main.version=$(VERSION) \
  -X main.buildTime=$(BUILD_TIME)"

# Shared env prefix prepended to every go build invocation
STATIC_ENV  := CGO_ENABLED=$(CGO_ENABLED)

# ─── Default target ──────────────────────────────────────────────────────────

.PHONY: all
all: build

.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY) $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)"

# ─── Per-platform targets ─────────────────────────────────────────────────────

.PHONY: build_all
build_all: \
	build_darwin_amd64 \
	build_darwin_arm64 \
	build_amd64 \
	build_arm64 \
	build_power8 \
	build_windows_amd64 \
	build_windows_arm64

# macOS — Intel
.PHONY: build_darwin_amd64
build_darwin_amd64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=darwin GOARCH=amd64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_darwin_amd64 $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_darwin_amd64"

# macOS — Apple Silicon
.PHONY: build_darwin_arm64
build_darwin_arm64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=darwin GOARCH=arm64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_darwin_arm64 $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_darwin_arm64"

# Linux — x86-64
.PHONY: build_amd64
build_amd64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=linux GOARCH=amd64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_linux_amd64 $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_linux_amd64"

# Linux — ARM 64-bit (e.g. Raspberry Pi 4, AWS Graviton)
.PHONY: build_arm64
build_arm64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=linux GOARCH=arm64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_linux_arm64 $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_linux_arm64"

# Linux — POWER8 / ppc64le (IBM Power)
.PHONY: build_power8
build_power8:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=linux GOARCH=ppc64le \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_linux_ppc64le $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_linux_ppc64le"

# Windows — x86-64
.PHONY: build_windows_amd64
build_windows_amd64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=windows GOARCH=amd64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_windows_amd64.exe $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_windows_amd64.exe"

# Windows — ARM 64-bit (e.g. Surface Pro X, Windows on ARM)
.PHONY: build_windows_arm64
build_windows_arm64:
	@mkdir -p $(BIN_DIR)
	$(STATIC_ENV) GOOS=windows GOARCH=arm64 \
	$(GO) build $(LDFLAGS) -tags $(BUILD_TAGS) \
	  -o $(BIN_DIR)/$(BINARY)_windows_arm64.exe $(CMD_PKG)
	@echo "built: $(BIN_DIR)/$(BINARY)_windows_arm64.exe"

# ─── Test targets ─────────────────────────────────────────────────────────────

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test_verbose
test_verbose:
	$(GO) test -v ./...

.PHONY: test_cover
test_cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

# ─── Utility targets ──────────────────────────────────────────────────────────

.PHONY: vet
vet:
	$(GO) vet ./...

# gofmt accepts a directory and recurses automatically — no ./... needed.
# goimports is a plain file tool and requires explicit paths via find.
.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: imports
imports:
	find . -name "*.go" -not -path "./vendor/*" | xargs goimports -w

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

.PHONY: help
help:
	@echo "ErrorTransformer build targets"
	@echo ""
	@echo "All binaries are fully static (CGO_ENABLED=0, -tags netgo)."
	@echo ""
	@echo "  make                    Build for host OS/arch → bin/errortransformer"
	@echo "  make build_all          Build every platform below"
	@echo ""
	@echo "  make build_darwin_amd64        macOS   / Intel"
	@echo "  make build_darwin_arm64        macOS   / Apple Silicon"
	@echo "  make build_amd64               Linux   / x86-64"
	@echo "  make build_arm64               Linux   / ARM 64-bit"
	@echo "  make build_power8              Linux   / POWER8 (ppc64le)"
	@echo "  make build_windows_amd64       Windows / x86-64"
	@echo "  make build_windows_arm64       Windows / ARM 64-bit"
	@echo ""
	@echo "  make test               Run all tests"
	@echo "  make test_verbose       Run tests with -v"
	@echo "  make test_cover         Run tests and generate HTML coverage report"
	@echo "  make vet                Run go vet"
	@echo "  make fmt                Run gofmt -w ."
	@echo "  make imports            Run goimports -w on all non-vendor .go files"
	@echo "  make clean              Remove bin/ and coverage files"
