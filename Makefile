# hukou — sandbox-friendly Go build defaults (overridable via environment).
GOPATH    ?= /tmp/gopath
GOCACHE   ?= /tmp/go-cache
GOMODCACHE ?= /tmp/gomod

export GOPATH
export GOCACHE
export GOMODCACHE

GO ?= go

.PHONY: build test vet all clean

all: vet test build

build:
	$(GO) build -o bin/hukou ./

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/
