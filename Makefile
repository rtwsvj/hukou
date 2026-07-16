GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=

.PHONY: all build test race coverage vet mod-verify fmt-check license-check install-test release-test shellcheck vulncheck verify verify-network release-verify release demo clean

all: fmt-check vet test build

build:
	@mkdir -p $(dir $(BINARY))
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./

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
