APP := ssh-host-proxy
STATIC_APP := $(APP)-static
BUILD_DIR := $(CURDIR)/build
BIN_DIR := $(BUILD_DIR)/bin
HOST_BIN_DIR := $(BIN_DIR)/host
RELEASE_DIR := $(BIN_DIR)/release
GOCACHE := $(BUILD_DIR)/gocache
GOMODCACHE := $(BUILD_DIR)/gomodcache
GOPATH := $(BUILD_DIR)/gopath
GOTMPDIR := $(BUILD_DIR)/tmp
GOTELEMETRYDIR := $(BUILD_DIR)/telemetry
GOENV := off
GOFLAGS := -modcacherw
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

export GOCACHE
export GOMODCACHE
export GOPATH
export GOTMPDIR
export GOTELEMETRYDIR
export GOENV
export GOFLAGS
export GOTELEMETRY=off

.PHONY: all build static release run test install install-mutable clean

all: build

build: $(HOST_BIN_DIR)/$(APP)

static: $(HOST_BIN_DIR)/$(STATIC_APP)

release: \
	$(RELEASE_DIR)/$(APP)-static-darwin-aarch64 \
	$(RELEASE_DIR)/$(APP)-static-linux-aarch64 \
	$(RELEASE_DIR)/$(APP)-static-linux-x86_64

$(HOST_BIN_DIR)/$(APP): go.mod main.go
	mkdir -p "$(HOST_BIN_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	rm -f "$(BIN_DIR)/$(APP)"
	go build -o "$(HOST_BIN_DIR)/$(APP)" .

$(HOST_BIN_DIR)/$(STATIC_APP): go.mod main.go
	mkdir -p "$(HOST_BIN_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	rm -f "$(BIN_DIR)/$(STATIC_APP)"
	CGO_ENABLED=0 GOOS="$(GOOS)" GOARCH="$(GOARCH)" go build -trimpath -ldflags='-s -w' -o "$(HOST_BIN_DIR)/$(STATIC_APP)" .

$(RELEASE_DIR)/$(APP)-static-darwin-aarch64: go.mod main.go
	mkdir -p "$(RELEASE_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "$@" .

$(RELEASE_DIR)/$(APP)-static-linux-aarch64: go.mod main.go
	mkdir -p "$(RELEASE_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "$@" .

$(RELEASE_DIR)/$(APP)-static-linux-x86_64: go.mod main.go
	mkdir -p "$(RELEASE_DIR)" "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$@" .

run: build
	"$(HOST_BIN_DIR)/$(APP)"

test:
	mkdir -p "$(GOCACHE)" "$(GOMODCACHE)" "$(GOPATH)" "$(GOTMPDIR)" "$(GOTELEMETRYDIR)"
	go test ./...

install: build
	if [ "$$(id -u)" -eq 0 ]; then \
		mkdir -p /usr/local/bin; \
		cp "$(HOST_BIN_DIR)/$(APP)" "/usr/local/bin/$(APP)"; \
	else \
		mkdir -p "$$HOME/.local/bin"; \
		cp "$(HOST_BIN_DIR)/$(APP)" "$$HOME/.local/bin/$(APP)"; \
	fi

install-mutable: build
	if [ "$$(id -u)" -eq 0 ]; then \
		mkdir -p /usr/local/bin; \
		ln -sfn "$(HOST_BIN_DIR)/$(APP)" "/usr/local/bin/$(APP)"; \
	else \
		mkdir -p "$$HOME/.local/bin"; \
		ln -sfn "$(HOST_BIN_DIR)/$(APP)" "$$HOME/.local/bin/$(APP)"; \
	fi

clean:
	chmod -R u+w "$(BUILD_DIR)" 2>/dev/null || true
	rm -rf "$(BUILD_DIR)"
