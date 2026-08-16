GO       ?= go
BINARY   ?= bin/hukou
COVERAGE ?= coverage.out
LDFLAGS  ?=
VERSION  ?=

.PHONY: all build build-release test race coverage vet mod-verify fmt-check license-check install-test release-test shellcheck vulncheck verify verify-network release-verify release stress demo clean

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
# 六层极限压力测试（可选、耗时约 20 分钟、部分层需真实网络）：
#   make stress                   全部六层
#   make stress SKIP_NETWORK=1    跳过需网络的三组（约 5 分钟）
# 结果与复现说明见 docs/audit/stress-*.md；沙盒开在临时目录，可整体删除。
SKIP_NETWORK ?= 0
stress: build
	@env SKIP_NETWORK=$(SKIP_NETWORK) bash scripts/stress/run_all.sh

release-verify: verify shellcheck vulncheck

release: release-verify
	bash scripts/release.sh

demo: build
	bash scripts/demo.sh

clean:
	rm -rf bin/ dist/ $(COVERAGE)
