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

.PHONY: all fetch-zstd build-zstd build test vet check clean

all: check build

fetch-zstd:
	./scripts/fetch-zstd.sh

build-zstd: fetch-zstd
	./scripts/build-zstd.sh

build: build-zstd
	mkdir -p dist
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd -ldflags '$(LDFLAGS)' -o dist/creator ./cmd/creator
	CGO_ENABLED=1 $(GO) build -trimpath -tags vipr_static_zstd -ldflags '$(LDFLAGS)' -o dist/patcher ./cmd/patcher

test: build-zstd
	CGO_ENABLED=1 $(GO) test -race -tags vipr_static_zstd ./...

vet: build-zstd
	CGO_ENABLED=1 $(GO) vet -tags vipr_static_zstd ./...

check: test vet
	@test -z "$$(gofmt -l .)" || (echo 'Go files are not formatted; run gofmt -w .' >&2; gofmt -l . >&2; exit 1)

clean:
	rm -rf build dist
