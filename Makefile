.PHONY: all build build-operator build-cli build-image test clean zarf-package lula-validate

VERSION ?= v0.5.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'

all: test build

build: build-operator build-cli

build-image: build-operator
	docker build -t ghcr.io/celttechie/uds-vm-operator:$(VERSION) -t ghcr.io/celttechie/uds-vm-operator:latest .

build-operator:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -ldflags="$(LDFLAGS)" -o bin/uds-vm-operator ./cmd/uds-vm-operator

build-cli:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/uds-vmctl ./cmd/uds-vmctl

test:
	go test -v -race ./...

KUBECONFIG ?= $(shell [ -f $(HOME)/Projects/kubevirt-dev-platform/kubeconfig ] && echo $(HOME)/Projects/kubevirt-dev-platform/kubeconfig || echo $(HOME)/.kube/config)

lula-validate:
	KUBECONFIG=$(KUBECONFIG) lula validate -f compliance/oscal-component.yaml -o compliance/assessment-results.yaml --non-interactive

zarf-package:
	zarf package create . --confirm --skip-sbom

zarf-build: build-image zarf-package

clean:
	rm -rf bin/ *.tar.zst /tmp/zarf-* compliance/assessment-results.yaml
