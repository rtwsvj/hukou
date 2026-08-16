#!/usr/bin/env bash
# Assemble the npm platform packages from the release archives in dist/.
# The 10 MB binaries are intentionally NOT committed to git: this script
# unpacks them (with checksum verification) right before `npm publish`.
#
# Usage: bash npm/build.sh   (requires a completed `make release` in dist/)
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

platforms=(darwin-amd64 darwin-arm64 linux-amd64 linux-arm64)

if [ ! -f dist/checksums.txt ]; then
  echo "npm/build.sh: dist/checksums.txt not found; run make release first" >&2
  exit 1
fi

cd dist
sha256sum -c checksums.txt >/dev/null
cd "$root"

# Extract a single archive member to outdir/hukou. Both GNU tar and bsdtar
# apply --strip-components before member-name matching, so the direct form
# silently extracts nothing; a two-step extract-then-move works everywhere.
extract_bin() {
  local archive=$1 outdir=$2 member=$3
  mkdir -p "$outdir"
  local tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  if command -v gtar >/dev/null 2>&1; then
    gtar -xzf "$archive" -C "$tmp" "$member"
  else
    tar -xzf "$archive" -C "$tmp" "$member"
  fi
  mv "$tmp/$member" "$outdir/hukou"
}

for p in "${platforms[@]}"; do
  archive="hukou_0.3.0_${p/-/_}.tar.gz"
  [ -f "dist/$archive" ] || { echo "npm/build.sh: missing dist/$archive" >&2; exit 1; }
  pkg="npm/platforms/hukou-$p"
  rm -rf "$pkg/bin" "$pkg/LICENSE"
  extract_bin "dist/$archive" "$pkg/bin" "hukou_0.3.0_${p/-/_}/hukou"
  cp LICENSE "$pkg/LICENSE"
  chmod 0755 "$pkg/bin/hukou"
  echo "assembled $pkg"
done
