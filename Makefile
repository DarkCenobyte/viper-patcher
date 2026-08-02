SHELL := /bin/sh
GO ?= go
VERSION ?= $(shell cat VERSION)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
MODULE := $(shell $(GO) list -m)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)
NATIVE_STAMP := build/native-deps/.built

.PHONY: all fetch-zstd fetch-blake3 build-zstd build-blake3 build-native build test vet check clean

all: check build

fetch-zstd:
	./scripts/fetch-zstd.sh

fetch-blake3:
	./scripts/fetch-blake3.sh

$(NATIVE_STAMP): scripts/fetch-zstd.sh scripts/build-zstd.sh scripts/fetch-blake3.sh scripts/build-blake3.sh internal/zstdversion/version.go internal/blake3version/version.go
	./scripts/fetch-zstd.sh
	./scripts/build-zstd.sh
	mkdir -p $(dir $(NATIVE_STAMP))
	touch $(NATIVE_STAMP)

build-native: $(NATIVE_STAMP)

build-zstd: build-native

build-blake3: build-native

build: $(NATIVE_STAMP)
	mkdir -p dist
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd,migrated_fynedo -ldflags '$(LDFLAGS)' -o dist/creator ./cmd/creator
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd,migrated_fynedo -ldflags '$(LDFLAGS)' -o dist/patcher ./cmd/patcher
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd -ldflags '$(LDFLAGS)' -o dist/creator-cli ./cmd/creator-cli
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd -ldflags '$(LDFLAGS)' -o dist/patcher-cli ./cmd/patcher-cli

test: $(NATIVE_STAMP)
	CGO_ENABLED=1 $(GO) test -race -tags vipr_static_zstd,migrated_fynedo ./...

vet: $(NATIVE_STAMP)
	CGO_ENABLED=1 $(GO) vet -tags vipr_static_zstd,migrated_fynedo ./...

check: $(NATIVE_STAMP)
	CGO_ENABLED=1 $(GO) test -race -tags vipr_static_zstd,migrated_fynedo ./...
	CGO_ENABLED=1 $(GO) vet -tags vipr_static_zstd,migrated_fynedo ./...
	@test -z "$$(gofmt -l .)" || (echo 'Go files are not formatted; run gofmt -w .' >&2; gofmt -l . >&2; exit 1)

clean:
	rm -rf build dist
