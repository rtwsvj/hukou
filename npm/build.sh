#!/usr/bin/env bash
# Assemble the npm platform packages from the release archives in dist/.
# The 10 MB binaries are intentionally NOT committed to git: this script
# unpacks them (with checksum verification) right before `npm publish`.
#
# The version is single-sourced from npm/hukou/package.json (the wrapper).
# Every platform package.json and every optionalDependencies pin must agree
# with it, or the build refuses to run — a wrapper pinned at one version
# publishing platform binaries of another is a broken install.
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

# Read a string field from a package.json. Prefers node (a real JSON parse;
# npm publish needs node anyway), falling back to a single-line scan of the
# plain JSON text for environments without node at build time.
json_str() {
  local file=$1 key=$2 dep=${3-} val=""
  if command -v node >/dev/null 2>&1; then
    val=$(node -p 'const p=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")),k=process.argv[2],d=process.argv[3];const v=d?(p.optionalDependencies||{})[k]:p[k];typeof v==="string"?v:""' "$file" "$key" "$dep")
  else
    val=$(sed -n "s/^[[:space:]]*\"$key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$file" | head -n 1)
  fi
  printf '%s' "$val"
}

version="$(json_str npm/hukou/package.json version)"
if [ -z "$version" ]; then
  echo "npm/build.sh: could not read version from npm/hukou/package.json" >&2
  exit 1
fi
if ! printf '%s' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
  echo "npm/build.sh: npm/hukou/package.json version \"$version\" is not semver" >&2
  exit 1
fi

# Version alignment gate: all five package.json files must state the same
# version before anything is unpacked or published.
align=0
for p in "${platforms[@]}"; do
  pf="npm/platforms/hukou-$p/package.json"
  if [ ! -f "$pf" ]; then
    echo "npm/build.sh: missing $pf" >&2
    exit 1
  fi
  pv="$(json_str "$pf" version)"
  if [ "$pv" != "$version" ]; then
    echo "npm/build.sh: version mismatch: $pf has \"$pv\", wrapper has \"$version\"" >&2
    align=1
  fi
  pin="$(json_str npm/hukou/package.json "hukou-$p" pin)"
  if [ "$pin" != "$version" ]; then
    echo "npm/build.sh: version mismatch: npm/hukou/package.json optionalDependencies.\"hukou-$p\" is \"$pin\", wrapper version is \"$version\"" >&2
    align=1
  fi
done
if [ "$align" -ne 0 ]; then
  echo "npm/build.sh: package.json versions disagree; set them all to $version before building" >&2
  exit 1
fi

# Verify the archives we are about to unpack. Prefer sha256sum; fall back to
# macOS shasum (same input format); refuse to build unverified otherwise.
cd dist
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -c checksums.txt >/dev/null
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 -c checksums.txt >/dev/null
else
  echo "npm/build.sh: neither sha256sum nor shasum found; cannot verify dist/checksums.txt" >&2
  exit 1
fi
for p in "${platforms[@]}"; do
  slug="${p//-/_}"
  wanted="hukou_${version}_${slug}.tar.gz"
  count=$(awk -v name="$wanted" '$2 == name || $2 == "*" name { n++ } END { print n + 0 }' checksums.txt)
  if [ "$count" -ne 1 ]; then
    echo "npm/build.sh: dist/checksums.txt must contain exactly one entry for $wanted, found $count" >&2
    exit 1
  fi
done
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
  slug="${p//-/_}"
  archive="dist/hukou_${version}_${slug}.tar.gz"
  member="hukou_${version}_${slug}/hukou"
  [ -f "$archive" ] || { echo "npm/build.sh: missing $archive" >&2; exit 1; }
  pkg="npm/platforms/hukou-$p"
  rm -rf "$pkg/bin" "$pkg/LICENSE"
  extract_bin "$archive" "$pkg/bin" "$member"
  cp LICENSE "$pkg/LICENSE"
  chmod 0755 "$pkg/bin/hukou"
  echo "assembled $pkg"
done
