# Copyright 2026 Pasqal and its contributors
# SPDX-License-Identifier: Apache-2.0
#
# Build automation for the qpu-resource Go binaries.

# ----- Configuration -----

# QRMI tag or branch to build the Go hooks against. Override on the
# command line: make build-go-hooks QRMI_REF=main
QRMI_REF ?= v0.13.3

# Output directories for produced binaries.
ADAPTER_OUT ?= bin/adapter
HOOKS_OUT   ?= bin/go-hooks

# Go build image used for the gridware-adapter (no cgo).
ADAPTER_IMAGE ?= golang:1.24

# ----- Targets -----

.PHONY: help
help:
	@echo "Targets:"
	@echo "  test               Run all Go tests (stub builds, no QRMI required)"
	@echo "  vet                Run go vet over the module"
	@echo "  build-adapter      Build gridware-adapter binary via Docker"
	@echo "  build-load-sensor  Build optional OCS Load Sensor binary via Docker"
	@echo "  build-go-hooks     Build Go OCS prolog/epilog hooks via Docker"
	@echo "                     (clones QRMI from upstream and links libqrmi.so)"
	@echo ""
	@echo "Override QRMI version: make build-go-hooks QRMI_REF=v0.13.3"

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: build-adapter
build-adapter:
	mkdir -p $(ADAPTER_OUT)
	docker run --rm --user $(shell id -u):$(shell id -g) \
	  -e GOCACHE=/tmp/go-build \
	  -e GOPATH=/tmp/go \
	  -v "$(CURDIR)":/work \
	  -w /work \
	  $(ADAPTER_IMAGE) /bin/sh -lc \
	  'export PATH=/usr/local/go/bin:$$PATH && go build -buildvcs=false -o $(ADAPTER_OUT)/adapter ./src/cmd/gridware-adapter'

.PHONY: build-load-sensor
build-load-sensor:
	mkdir -p $(ADAPTER_OUT)
	docker run --rm --user $(shell id -u):$(shell id -g) \
	  -e GOCACHE=/tmp/go-build \
	  -e GOPATH=/tmp/go \
	  -v "$(CURDIR)":/work \
	  -w /work \
	  $(ADAPTER_IMAGE) /bin/sh -lc \
	  'export PATH=/usr/local/go/bin:$$PATH && go build -buildvcs=false -o $(ADAPTER_OUT)/qrmi-ocs-load-sensor ./src/cmd/qrmi-ocs-load-sensor'

.PHONY: build-go-hooks
build-go-hooks:
	mkdir -p $(HOOKS_OUT)
	DOCKER_BUILDKIT=1 docker build \
	  --target export \
	  --output type=local,dest=$(HOOKS_OUT) \
	  -f scripts/Dockerfile.hooks \
	  --build-arg QRMI_REF=$(QRMI_REF) \
	  .
	@echo "Built into $(HOOKS_OUT):"
	@ls -l $(HOOKS_OUT)
