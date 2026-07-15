#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
export TZ=UTC
umask 022

root="$(git rev-parse --show-toplevel)"
cd "$root"
. ./scripts/semver.sh

if [[ "${ALLOW_DIRTY:-0}" != "1" ]] && [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  echo "refusing to package a dirty worktree (set ALLOW_DIRTY=1 only for local experiments)" >&2
  exit 1
fi

version="${VERSION:-${1:-}}"
if [[ -z "$version" ]]; then
  version="$(git describe --tags --exact-match 2>/dev/null || true)"
fi
if ! hukou_is_strict_semver "$version"; then
  echo "VERSION must be strict SemVer 2.0.0 without build metadata, such as v0.3.0 or v0.3.0-rc.1 (got: ${version:-empty})" >&2
  exit 1
fi

commit="$(git rev-parse HEAD)"
epoch="$(git show -s --format=%ct HEAD)"
export SOURCE_DATE_EPOCH="$epoch"
if build_date="$(date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then
  :
else
  build_date="$(date -u -r "$epoch" +%Y-%m-%dT%H:%M:%SZ)"
fi

if tar --version 2>/dev/null | grep -q 'GNU tar'; then
  tar_cmd="tar"
elif command -v gtar >/dev/null 2>&1; then
  tar_cmd="gtar"
else
  echo "GNU tar is required for reproducible archives (install gtar on macOS)" >&2
  exit 1
fi

dist="${DIST_DIR:-$root/dist}"
rm -rf "$dist"
mkdir -p "$dist"

module="github.com/rtwsvj/hukou/internal/buildinfo"
ldflags="-s -w -X ${module}.Version=${version} -X ${module}.Commit=${commit} -X ${module}.Date=${build_date}"
short_version="${version#v}"
targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  package="hukou_${short_version}_${goos}_${goarch}"
  stage="$dist/.stage/$package"
  mkdir -p "$stage/LICENSES"

  echo "building $package"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/hukou" ./
  cp README.md "$stage/README.md"
  cp README.zh-CN.md "$stage/README.zh-CN.md"
  cp LICENSE "$stage/LICENSE"
  cp THIRD_PARTY_NOTICES.md "$stage/THIRD_PARTY_NOTICES.md"
  cp LICENSES/*.txt "$stage/LICENSES/"
  chmod 0755 "$stage" "$stage/LICENSES" "$stage/hukou"
  chmod 0644 \
    "$stage/README.md" \
    "$stage/README.zh-CN.md" \
    "$stage/LICENSE" \
    "$stage/THIRD_PARTY_NOTICES.md" \
    "$stage"/LICENSES/*.txt

  "$tar_cmd" \
    --sort=name \
    --mtime="@$epoch" \
    --owner=0 \
    --group=0 \
    --numeric-owner \
    -cf - \
    -C "$dist/.stage" "$package" \
    | gzip -n -9 >"$dist/$package.tar.gz"
done

rm -rf "$dist/.stage"

if command -v sha256sum >/dev/null 2>&1; then
  (
    cd "$dist"
    sha256sum ./*.tar.gz | sed 's#  \./#  #' >checksums.txt
  )
else
  (
    cd "$dist"
    shasum -a 256 ./*.tar.gz | sed 's#  \./#  #' >checksums.txt
  )
fi

echo "release artifacts written to $dist"
