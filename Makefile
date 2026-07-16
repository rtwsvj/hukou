GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=
VERSION  ?=

.PHONY: all build build-release test race coverage vet mod-verify fmt-check license-check install-test release-test shellcheck vulncheck verify verify-network release-verify release demo clean

all: fmt-check vet test build

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./

# Fast single-platform build that injects version/commit/date metadata through
# scripts/build_flags.sh — the same single source scripts/release.sh uses — for
# local self-install ahead of a tagged release. It builds only the host
# platform and produces no archives or checksums, so it is not a substitute
# for `make release`.
#
# Version semantics (intentional difference):
#   - `make release` (via scripts/release.sh) requires strict SemVer 2.0.0.
#   - `make build-release` accepts any VERSION, and with no VERSION falls back
#     to `git describe --tags --always --dirty` — a -dirty suffix is allowed
#     for local development builds.
build-release:
	@mkdir -p $(dir $(BINARY))
	@ldflags="$$(bash scripts/build_flags.sh "$(VERSION)")"; \
	echo "building $(BINARY) with ldflags: $$ldflags"; \
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags "$$ldflags" -o $(BINARY) ./

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
# tag and additionally self-skip unless HUKOU_NETWORK_E2E=1. Because invoking
# this target is an explicit request to run the gate, a missing token is a
# hard error here — a silent skip would look like a green gate that never ran.
verify-network:
	@if [ -z "$$GITHUB_TOKEN" ] && [ -z "$$GH_TOKEN" ]; then \
		echo "verify-network: GITHUB_TOKEN or GH_TOKEN is required; refusing to report a skipped run as success (try: GH_TOKEN=\$$(gh auth token) make verify-network)" >&2; \
		exit 1; \
	fi
	HUKOU_NETWORK_E2E=1 $(GO) test -tags network_e2e -run Network -count=1 ./cmd/...

release-verify: verify shellcheck vulncheck

release: release-verify
	bash scripts/release.sh

demo: build
	bash scripts/demo.sh

clean:
	rm -rf bin/ dist/ $(COVERAGE)
