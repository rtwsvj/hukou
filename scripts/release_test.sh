#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/hukou-release-test.XXXXXX")"
cleanup() {
  rm -rf "$TMP"
}
trap cleanup EXIT HUP INT TERM

WRAPPER_DIR="$TMP/wrappers"
mkdir -p "$WRAPPER_DIR"

cat >"$WRAPPER_DIR/go" <<'EOF'
#!/bin/sh
output=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then
    shift
    output=$1
  fi
  shift
done
[ -n "$output" ] || exit 1
printf '#!/bin/sh\nexit 0\n' >"$output"
EOF

cat >"$WRAPPER_DIR/tar" <<'EOF'
#!/bin/sh
if [ "${1:-}" = --version ]; then
  printf 'tar (GNU tar) 1.35\n'
  exit 0
fi
printf 'deterministic archive fixture\n'
EOF
chmod 0755 "$WRAPPER_DIR/go" "$WRAPPER_DIR/tar"

assert_version_accepted() {
  local version=$1
  local dist="$TMP/dist-${version//[^0-9A-Za-z.-]/_}"

  if ! PATH="$WRAPPER_DIR:$PATH" ALLOW_DIRTY=1 VERSION="$version" DIST_DIR="$dist" \
    bash "$ROOT/scripts/release.sh" >/dev/null 2>&1; then
    printf 'release script rejected valid version: %s\n' "$version" >&2
    exit 1
  fi
  if [[ "$(find "$dist" -maxdepth 1 -type f -name '*.tar.gz' | wc -l | tr -d ' ')" != 4 ]]; then
    printf 'release script did not create four archives for: %s\n' "$version" >&2
    exit 1
  fi
}

assert_version_rejected() {
  local version=$1

  if PATH="$WRAPPER_DIR:$PATH" ALLOW_DIRTY=1 VERSION="$version" DIST_DIR="$TMP/invalid-dist" \
    bash "$ROOT/scripts/release.sh" >/dev/null 2>&1; then
    printf 'release script accepted invalid version: %s\n' "$version" >&2
    exit 1
  fi
}

valid_versions=(
  v0.0.0
  v1.2.3
  v10.20.30-alpha
  v1.0.0-0
  v1.0.0-alpha.1
  v1.0.0-0.3.7
  v1.0.0-x.7.z.92
  v1.0.0-x-y-z.--
)

invalid_versions=(
  1.2.3
  v1.2
  v01.2.3
  v1.02.3
  v1.2.03
  v1.2.3-
  v1.2.3-rc.
  v1.2.3-rc..1
  v1.2.3-.rc
  v1.2.3-01
  v1.2.3-alpha.01
  v1.2.3-alpha_beta
  v1.2.3-ré
  v1.2.3-alpha/1
  v1.2.3+build.1
  v1.2.3-rc.1+build.1
)

for version in "${valid_versions[@]}"; do
  assert_version_accepted "$version"
done
for version in "${invalid_versions[@]}"; do
  assert_version_rejected "$version"
done

# VERSION='' intentionally asks release.sh to derive an exact tag, so exercise
# the helper directly for the empty-string boundary instead of overriding that
# public fallback behavior.
# shellcheck source=scripts/semver.sh
. "$ROOT/scripts/semver.sh"
if hukou_is_strict_semver ''; then
  printf 'strict SemVer helper accepted an empty version\n' >&2
  exit 1
fi
assert_version_rejected $'v1.2.3\njunk'

printf 'release version validation tests passed\n'
