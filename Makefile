.PHONY: all build build-operator build-cli test clean zarf-package

VERSION ?= v0.3.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

all: test build

build: build-operator build-cli

build-operator:
	@mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o bin/uds-vm-operator ./cmd/uds-vm-operator

build-cli:
	@mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o bin/uds-vmctl ./cmd/uds-vmctl

test:
	go test -v -race ./...

zarf-package:
	zarf package create . --confirm --skip-sbom

clean:
	rm -rf bin/ *.tar.zst /tmp/zarf-*
