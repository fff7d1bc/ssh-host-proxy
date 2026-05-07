APP := ssh-host-proxy
BUILD_DIR := $(CURDIR)/build
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PLATFORM := $(GOOS)-$(GOARCH)
PLATFORM_BUILD_DIR := $(BUILD_DIR)/$(PLATFORM)
BIN_DIR := $(PLATFORM_BUILD_DIR)/bin
BIN := $(BIN_DIR)/$(APP)
STATIC_BIN := $(BIN_DIR)/$(APP)-static
GO_SOURCES := $(wildcard *.go)
GOCACHE := $(PLATFORM_BUILD_DIR)/gocache
GOMODCACHE := $(PLATFORM_BUILD_DIR)/gomodcache
GOPATH := $(PLATFORM_BUILD_DIR)/gopath
GOTMPDIR := $(PLATFORM_BUILD_DIR)/tmp
GOTELEMETRYDIR := $(PLATFORM_BUILD_DIR)/telemetry
GOENV := off
GOFLAGS := -modcacherw

DARWIN_ARM64_DIR := $(BUILD_DIR)/darwin-arm64
LINUX_ARM64_DIR := $(BUILD_DIR)/linux-arm64
LINUX_AMD64_DIR := $(BUILD_DIR)/linux-amd64
DARWIN_ARM64_BIN := $(DARWIN_ARM64_DIR)/bin/$(APP)-static
LINUX_ARM64_BIN := $(LINUX_ARM64_DIR)/bin/$(APP)-static
LINUX_AMD64_BIN := $(LINUX_AMD64_DIR)/bin/$(APP)-static
RELEASE_ASSETS_DIR := $(BUILD_DIR)/release-assets
DARWIN_ARM64_ASSET := $(RELEASE_ASSETS_DIR)/$(APP)-static-darwin-arm64
LINUX_ARM64_ASSET := $(RELEASE_ASSETS_DIR)/$(APP)-static-linux-arm64
LINUX_AMD64_ASSET := $(RELEASE_ASSETS_DIR)/$(APP)-static-linux-amd64

export GOCACHE
export GOMODCACHE
export GOPATH
export GOTMPDIR
export GOTELEMETRYDIR
export GOENV
export GOFLAGS
export GOTELEMETRY=off

.PHONY: all build static release release-assets run test install clean

all: build

build: $(BIN)

static: $(STATIC_BIN)

release: \
	$(DARWIN_ARM64_BIN) \
	$(LINUX_ARM64_BIN) \
	$(LINUX_AMD64_BIN)

release-assets: \
	$(DARWIN_ARM64_ASSET) \
	$(LINUX_ARM64_ASSET) \
	$(LINUX_AMD64_ASSET)

$(BIN): go.mod $(GO_SOURCES)
	mkdir -p "$(BIN_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	GOOS="$(GOOS)" GOARCH="$(GOARCH)" go build -trimpath -o "$(BIN)" .

$(BUILD_DIR)/%/bin/$(APP)-static: go.mod $(GO_SOURCES)
	platform="$*"; \
	platform_dir="$(BUILD_DIR)/$*"; \
	goos="$${platform%-*}"; \
	goarch="$${platform##*-}"; \
	mkdir -p "$(@D)" "$$platform_dir/gocache" "$$platform_dir/gomodcache" "$$platform_dir/gopath" "$$platform_dir/tmp" "$$platform_dir/telemetry"; \
	GOCACHE="$$platform_dir/gocache" GOMODCACHE="$$platform_dir/gomodcache" GOPATH="$$platform_dir/gopath" GOTMPDIR="$$platform_dir/tmp" GOTELEMETRYDIR="$$platform_dir/telemetry" GOENV=off GOFLAGS=-modcacherw GOTELEMETRY=off CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" go build -trimpath -tags "netgo osusergo" -ldflags "-s -w -buildid=" -o "$@" .

$(DARWIN_ARM64_ASSET): $(DARWIN_ARM64_BIN)
	mkdir -p "$(RELEASE_ASSETS_DIR)"
	cp "$(DARWIN_ARM64_BIN)" "$@"

$(LINUX_ARM64_ASSET): $(LINUX_ARM64_BIN)
	mkdir -p "$(RELEASE_ASSETS_DIR)"
	cp "$(LINUX_ARM64_BIN)" "$@"

$(LINUX_AMD64_ASSET): $(LINUX_AMD64_BIN)
	mkdir -p "$(RELEASE_ASSETS_DIR)"
	cp "$(LINUX_AMD64_BIN)" "$@"

run: build
	"$(BIN)" $(ARGS)

test:
	mkdir -p "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	go test ./...

install: build
	if [ "$$(id -u)" -eq 0 ]; then \
		mkdir -p /usr/local/bin; \
		install -m 0755 "$(BIN)" "/usr/local/bin/$(APP)"; \
	else \
		mkdir -p "$$HOME/.local/bin"; \
		install -m 0755 "$(BIN)" "$$HOME/.local/bin/$(APP)"; \
	fi

clean:
	chmod -R u+w "$(BUILD_DIR)" 2>/dev/null || true
	rm -rf "$(BUILD_DIR)"
