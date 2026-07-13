GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=

.PHONY: all build test race coverage vet mod-verify fmt-check verify release clean

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

verify: fmt-check mod-verify vet test race coverage build

release: verify
	bash scripts/release.sh

clean:
	rm -rf bin/ dist/ $(COVERAGE)
