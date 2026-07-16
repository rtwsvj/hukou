GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=
VERSION  ?=
MODULE   := github.com/rtwsvj/hukou/internal/buildinfo

.PHONY: all build build-release test race coverage vet mod-verify fmt-check license-check install-test release-test shellcheck vulncheck verify verify-network release-verify release demo clean

all: fmt-check vet test build

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./

# Fast single-platform build that injects the same version/commit/date metadata
# as scripts/release.sh, for local self-install ahead of a tagged release. It
# builds only the host platform and does not produce archives or checksums, so
# it is not a substitute for `make release`. Override the version with
# `make build-release VERSION=v0.3.0`; otherwise it is derived from git.
build-release:
	@mkdir -p $(dir $(BINARY))
	@v="$(VERSION)"; \
	if [ -z "$$v" ]; then v="$$(git describe --tags --always --dirty 2>/dev/null || echo devel)"; fi; \
	c="$$(git rev-parse HEAD 2>/dev/null || echo unknown)"; \
	e="$$(git show -s --format=%ct HEAD 2>/dev/null || echo 0)"; \
	d="$$(date -u -d "@$$e" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$$e" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)"; \
	echo "building $(BINARY) version=$$v commit=$$c date=$$d"; \
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false \
	  -ldflags "-s -w -X $(MODULE).Version=$$v -X $(MODULE).Commit=$$c -X $(MODULE).Date=$$d" \
	  -o $(BINARY) ./

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

coverage:
	$(GO) test -covermode=atomic -coverprofile=$(COVERAGE) ./...
	$(GO) tool cover -func=$(COVERAGE)

vet:
	$(GO) vet ./...

mod-verify:
	$(GO) mod verify

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt required for:"; \
		echo "$$files"; \
		exit 1; \
	fi

license-check:
	bash scripts/license_check.sh

install-test:
	bash scripts/install_test.sh

release-test:
	bash scripts/release_test.sh

shellcheck:
	shellcheck scripts/*.sh

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...

verify: fmt-check mod-verify vet test race coverage build license-check install-test release-test

# Opt-in L6 real-network end-to-end gate. It is deliberately excluded from
# verify/release-verify and CI: the tests live behind the `network_e2e` build
# tag and additionally self-skip unless HUKOU_NETWORK_E2E=1, so this target only
# does anything with network access and a GITHUB_TOKEN/GH_TOKEN in the
# environment.
verify-network:
	HUKOU_NETWORK_E2E=1 $(GO) test -tags network_e2e -run Network -count=1 ./cmd/...

release-verify: verify shellcheck vulncheck

release: release-verify
	bash scripts/release.sh

demo: build
	bash scripts/demo.sh

clean:
	rm -rf bin/ dist/ $(COVERAGE)
