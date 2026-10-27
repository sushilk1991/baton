GO ?= go
GOLANGCI_LINT ?= golangci-lint
BUILD_DIR ?= bin
CROSS_DIR ?= .build
TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build check vet lint test cross-build

build:
	CGO_ENABLED=0 "$(GO)" build -trimpath -o "$(BUILD_DIR)/baton" ./cmd/baton

check: vet lint test cross-build

vet:
	"$(GO)" vet ./...

# Lint is optional locally; an installed linter's failure still fails the gate.
lint:
	@if command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1; then \
		"$(GOLANGCI_LINT)" run ./...; \
	else \
		echo "golangci-lint not installed; skipping lint"; \
	fi

# The race detector requires cgo; shipped binaries below never use it.
test:
	CGO_ENABLED=1 "$(GO)" test -race ./...

# Build each release target serially and stop on the first compiler failure.
cross-build:
	@set -eu; for target in $(TARGETS); do \
		target_os=$${target%/*}; target_arch=$${target#*/}; \
		suffix=; if [ "$$target_os" = windows ]; then suffix=.exe; fi; \
		echo "cross-build $$target"; \
		CGO_ENABLED=0 GOOS="$$target_os" GOARCH="$$target_arch" \
			"$(GO)" build -trimpath \
			-o "$(CROSS_DIR)/baton-$$target_os-$$target_arch$$suffix" ./cmd/baton; \
	done
