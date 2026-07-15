GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=

.PHONY: all build test race coverage vet mod-verify fmt-check license-check install-test release-test shellcheck vulncheck verify release-verify release demo clean

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

release-verify: verify shellcheck vulncheck

release: release-verify
	bash scripts/release.sh

demo: build
	bash scripts/demo.sh

clean:
	rm -rf bin/ dist/ $(COVERAGE)
