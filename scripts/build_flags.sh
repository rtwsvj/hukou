#!/usr/bin/env bash
# Single source of truth for the buildinfo version-injection ldflags.
# Both scripts/release.sh and the Makefile build-release target call this,
# so the -X assembly cannot drift between the two build paths.
#
# Usage: build_flags.sh [version]
#
# Version semantics differ by caller and are intentional:
#   - scripts/release.sh validates strict SemVer 2.0.0 BEFORE calling this
#     script and always passes the version explicitly.
#   - `make build-release` may call this with no argument for local dev
#     builds, in which case the version falls back to
#     `git describe --tags --always --dirty` (a `-dirty` suffix is allowed
#     and expected for uncommitted trees).
#
# Output: the complete ldflags string on stdout.
set -euo pipefail

module="github.com/rtwsvj/hukou/internal/buildinfo"

version="${1:-}"
if [[ -z "$version" ]]; then
  version="$(git describe --tags --always --dirty 2>/dev/null || echo devel)"
fi

commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"

epoch="$(git show -s --format=%ct HEAD 2>/dev/null || echo 0)"
# GNU date uses -d @epoch; BSD/macOS date uses -r epoch.
if build_date="$(date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then
  :
else
  build_date="$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ)"
fi

printf '%s\n' "-s -w -X ${module}.Version=${version} -X ${module}.Commit=${commit} -X ${module}.Date=${build_date}"
