#!/usr/bin/env bash

set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

required=(
  LICENSE
  THIRD_PARTY_NOTICES.md
  LICENSES/eget-MIT.txt
  LICENSES/gup-APACHE-2.0.txt
  LICENSES/golang-x-mod-BSD-3-Clause.txt
  LICENSES/golang-x-mod-PATENTS.txt
  LICENSES/pflag-BSD-3-Clause.txt
  LICENSES/stew-MIT.txt
  docs/pinhaoma-sources.md
)

for path in "${required[@]}"; do
  if [[ ! -s "$path" ]]; then
    echo "required license/provenance file is missing or empty: $path" >&2
    exit 1
  fi
done

grep -q 'Apache License' LICENSE
grep -q 'LICENSES/eget-MIT.txt' internal/assetpick/detect.go
grep -q 'LICENSES/gup-APACHE-2.0.txt' internal/provenance/gobin.go
grep -q 'internal/assetpick/detect.go' THIRD_PARTY_NOTICES.md
grep -q 'internal/provenance/gobin.go' THIRD_PARTY_NOTICES.md
grep -q 'golang.org/x/mod' THIRD_PARTY_NOTICES.md

for packaged in LICENSE THIRD_PARTY_NOTICES.md README.zh-CN.md; do
  grep -q "cp $packaged" scripts/release.sh || {
    echo "release script does not package $packaged" >&2
    exit 1
  }
done

echo "license and provenance checks passed"
