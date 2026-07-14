#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/hukou-install-test.XXXXXX")
cleanup() {
  rm -rf "$TMP"
}
trap cleanup EXIT HUP INT TERM

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) printf 'unsupported test OS\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) printf 'unsupported test architecture\n' >&2; exit 1 ;;
esac

VERSION=v9.9.9
NUMBER=${VERSION#v}
NAME=hukou_${NUMBER}_${OS}_${ARCH}
DOWNLOAD=${TMP}/releases/download/${VERSION}
STAGE=${TMP}/stage/${NAME}
mkdir -p "$DOWNLOAD" "$STAGE"

cat >"${STAGE}/hukou" <<'EOF'
#!/bin/sh
printf 'fixture hukou 9.9.9\n'
EOF
chmod 0755 "${STAGE}/hukou"
printf 'fixture readme\n' >"${STAGE}/README.md"
tar -czf "${DOWNLOAD}/${NAME}.tar.gz" -C "${TMP}/stage" "$NAME"
if command -v sha256sum >/dev/null 2>&1; then
  HASH=$(sha256sum "${DOWNLOAD}/${NAME}.tar.gz" | awk '{print $1}')
else
  HASH=$(shasum -a 256 "${DOWNLOAD}/${NAME}.tar.gz" | awk '{print $1}')
fi
printf '%s  %s.tar.gz\n' "$HASH" "$NAME" >"${DOWNLOAD}/checksums.txt"

PREFIX=${TMP}/prefix
HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$PREFIX"
[ "$("${PREFIX}/bin/hukou")" = "fixture hukou 9.9.9" ]

if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$PREFIX" >/dev/null 2>&1; then
  printf 'installer replaced an existing binary without --force\n' >&2
  exit 1
fi

DRY_PREFIX=${TMP}/dry-prefix
HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$DRY_PREFIX" --dry-run >/dev/null
[ ! -e "$DRY_PREFIX" ]

assert_version_accepted() {
  if ! HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
    "$ROOT/scripts/install.sh" --version "$1" --prefix "$DRY_PREFIX" --dry-run >/dev/null 2>&1; then
    printf 'installer rejected valid release version: %s\n' "$1" >&2
    exit 1
  fi
}

assert_version_rejected() {
  if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
    "$ROOT/scripts/install.sh" --version "$1" --prefix "$DRY_PREFIX" --dry-run >/dev/null 2>&1; then
    printf 'installer accepted invalid release version: %s\n' "$1" >&2
    exit 1
  fi
}

for VALID_VERSION in \
  v0.0.0 \
  v1.2.3 \
  v10.20.30-alpha \
  v1.0.0-0 \
  v1.0.0-alpha.1 \
  v1.0.0-0.3.7 \
  v1.0.0-x.7.z.92 \
  v1.0.0-x-y-z.--; do
  assert_version_accepted "$VALID_VERSION"
done

for INVALID_VERSION in \
  '' \
  1.2.3 \
  v1.2 \
  v01.2.3 \
  v1.02.3 \
  v1.2.03 \
  v1.2.3- \
  v1.2.3-rc. \
  v1.2.3-rc..1 \
  v1.2.3-.rc \
  v1.2.3-01 \
  v1.2.3-alpha.01 \
  v1.2.3-alpha_beta \
  v1.2.3-ré \
  v1.2.3-alpha/1 \
  v1.2.3+build.1 \
  v1.2.3-rc.1+build.1; do
  assert_version_rejected "$INVALID_VERSION"
done
assert_version_rejected "$(printf 'v1.2.3\njunk')"

# The published installer remains self-contained when semver.sh is not next to
# it. Exercise both sides of the fallback boundary before any network access.
STANDALONE_INSTALLER=${TMP}/standalone-install.sh
cp "$ROOT/scripts/install.sh" "$STANDALONE_INSTALLER"
chmod 0755 "$STANDALONE_INSTALLER"
STANDALONE_PREFIX=${TMP}/standalone-prefix
HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$STANDALONE_INSTALLER" --version "$VERSION" --prefix "$STANDALONE_PREFIX" >/dev/null
[ "$("${STANDALONE_PREFIX}/bin/hukou")" = "fixture hukou 9.9.9" ]
if ! HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$STANDALONE_INSTALLER" --version v1.2.3-rc.1 --prefix "$DRY_PREFIX" --dry-run >/dev/null 2>&1; then
  printf 'standalone installer rejected a valid strict SemVer prerelease\n' >&2
  exit 1
fi
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$STANDALONE_INSTALLER" --version v1.2.3-rc.01 --prefix "$DRY_PREFIX" --dry-run >/dev/null 2>&1; then
  printf 'standalone installer accepted a numeric prerelease identifier with a leading zero\n' >&2
  exit 1
fi
PIPE_PREFIX=${TMP}/pipe-prefix
if ! HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  sh -s -- --version v1.2.3-rc.1 --prefix "$PIPE_PREFIX" --dry-run \
  <"$STANDALONE_INSTALLER" >/dev/null 2>&1; then
  printf 'curl-to-sh style installer execution rejected a valid strict SemVer prerelease\n' >&2
  exit 1
fi
[ ! -e "$PIPE_PREFIX" ]

cp "${DOWNLOAD}/${NAME}.tar.gz" "${DOWNLOAD}/${NAME}.tar.gz.good"
printf 'tamper' >>"${DOWNLOAD}/${NAME}.tar.gz"
BAD_PREFIX=${TMP}/bad-prefix
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$BAD_PREFIX" >/dev/null 2>&1; then
  printf 'installer accepted a bad checksum\n' >&2
  exit 1
fi
[ ! -e "${BAD_PREFIX}/bin/hukou" ]
mv "${DOWNLOAD}/${NAME}.tar.gz.good" "${DOWNLOAD}/${NAME}.tar.gz"

# A checksum-valid archive still has to expose one unambiguous executable
# member. Duplicate target entries make extraction order tar-dependent.
cp "${DOWNLOAD}/${NAME}.tar.gz" "${DOWNLOAD}/${NAME}.tar.gz.single"
cp "${DOWNLOAD}/checksums.txt" "${DOWNLOAD}/checksums.txt.single"
tar -czf "${DOWNLOAD}/${NAME}.tar.gz" -C "${TMP}/stage" "${NAME}/hukou" "${NAME}/hukou"
if command -v sha256sum >/dev/null 2>&1; then
  DUPLICATE_HASH=$(sha256sum "${DOWNLOAD}/${NAME}.tar.gz" | awk '{print $1}')
else
  DUPLICATE_HASH=$(shasum -a 256 "${DOWNLOAD}/${NAME}.tar.gz" | awk '{print $1}')
fi
printf '%s  %s.tar.gz\n' "$DUPLICATE_HASH" "$NAME" >"${DOWNLOAD}/checksums.txt"
DUPLICATE_PREFIX=${TMP}/duplicate-prefix
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$DUPLICATE_PREFIX" >/dev/null 2>&1; then
  printf 'installer accepted duplicate executable archive members\n' >&2
  exit 1
fi
[ ! -e "${DUPLICATE_PREFIX}/bin/hukou" ]
mv "${DOWNLOAD}/${NAME}.tar.gz.single" "${DOWNLOAD}/${NAME}.tar.gz"
mv "${DOWNLOAD}/checksums.txt.single" "${DOWNLOAD}/checksums.txt"

cat >"${PREFIX}/bin/hukou" <<'EOF'
#!/bin/sh
printf 'old hukou\n'
EOF
chmod 0755 "${PREFIX}/bin/hukou"
HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$PREFIX" --force >/dev/null
[ "$("${PREFIX}/bin/hukou")" = "fixture hukou 9.9.9" ]

# --force replaces a file entry, but must reject directories instead of moving
# the prepared binary into them.
DIRECTORY_PREFIX=${TMP}/directory-prefix
mkdir -p "${DIRECTORY_PREFIX}/bin/hukou"
printf 'directory sentinel\n' >"${DIRECTORY_PREFIX}/bin/hukou/sentinel"
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$DIRECTORY_PREFIX" --force >/dev/null 2>&1; then
  printf 'force install accepted a directory destination\n' >&2
  exit 1
fi
[ "$(find "${DIRECTORY_PREFIX}/bin/hukou" -mindepth 1 -maxdepth 1 -print)" = "${DIRECTORY_PREFIX}/bin/hukou/sentinel" ]
[ "$(cat "${DIRECTORY_PREFIX}/bin/hukou/sentinel")" = "directory sentinel" ]

# A symlink to a directory is also rejected and neither the link nor its target
# may be modified.
SYMLINK_DIRECTORY_PREFIX=${TMP}/symlink-directory-prefix
SYMLINK_DIRECTORY_TARGET=${TMP}/symlink-directory-target
mkdir -p "${SYMLINK_DIRECTORY_PREFIX}/bin" "$SYMLINK_DIRECTORY_TARGET"
printf 'external directory sentinel\n' >"${SYMLINK_DIRECTORY_TARGET}/sentinel"
ln -s "$SYMLINK_DIRECTORY_TARGET" "${SYMLINK_DIRECTORY_PREFIX}/bin/hukou"
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$SYMLINK_DIRECTORY_PREFIX" --force >/dev/null 2>&1; then
  printf 'force install accepted a symlink-to-directory destination\n' >&2
  exit 1
fi
[ -L "${SYMLINK_DIRECTORY_PREFIX}/bin/hukou" ]
[ "$(readlink "${SYMLINK_DIRECTORY_PREFIX}/bin/hukou")" = "$SYMLINK_DIRECTORY_TARGET" ]
[ "$(find "$SYMLINK_DIRECTORY_TARGET" -mindepth 1 -maxdepth 1 -print)" = "${SYMLINK_DIRECTORY_TARGET}/sentinel" ]
[ "$(cat "${SYMLINK_DIRECTORY_TARGET}/sentinel")" = "external directory sentinel" ]

# A symlink to a file is replaced as a directory entry. The linked file may be
# outside PREFIX and must never be followed or changed.
SYMLINK_FILE_PREFIX=${TMP}/symlink-file-prefix
SYMLINK_FILE_TARGET=${TMP}/outside-prefix-file
mkdir -p "${SYMLINK_FILE_PREFIX}/bin"
printf 'external file sentinel\n' >"$SYMLINK_FILE_TARGET"
ln -s "$SYMLINK_FILE_TARGET" "${SYMLINK_FILE_PREFIX}/bin/hukou"
HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$SYMLINK_FILE_PREFIX" --force >/dev/null
[ ! -L "${SYMLINK_FILE_PREFIX}/bin/hukou" ]
[ -f "${SYMLINK_FILE_PREFIX}/bin/hukou" ]
[ "$("${SYMLINK_FILE_PREFIX}/bin/hukou")" = "fixture hukou 9.9.9" ]
[ "$(cat "$SYMLINK_FILE_TARGET")" = "external file sentinel" ]

DANGLING_PREFIX=${TMP}/dangling-prefix
mkdir -p "${DANGLING_PREFIX}/bin"
ln -s "${DANGLING_PREFIX}/missing-target" "${DANGLING_PREFIX}/bin/hukou"
if HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$DANGLING_PREFIX" >/dev/null 2>&1; then
  printf 'installer replaced a dangling symlink without --force\n' >&2
  exit 1
fi
[ -L "${DANGLING_PREFIX}/bin/hukou" ]
[ "$(readlink "${DANGLING_PREFIX}/bin/hukou")" = "${DANGLING_PREFIX}/missing-target" ]

# Make curl create a competing destination immediately after the archive has
# downloaded. These changes occur after the installer's initial destination
# check, so the final commit primitive—not the precheck—must enforce safety.
WRAPPER_DIR=${TMP}/curl-wrapper
mkdir -p "$WRAPPER_DIR"
cat >"${WRAPPER_DIR}/curl" <<'EOF'
#!/bin/sh
"$REAL_CURL" "$@"
status=$?
case "$*" in
  *.tar.gz*)
    mkdir -p "$(dirname "$RACE_DEST")"
    case "$RACE_KIND" in
      file)
        printf 'competing installer\n' >"$RACE_DEST"
        chmod 0755 "$RACE_DEST"
        ;;
      directory)
        mkdir -p "$RACE_DEST"
        printf 'directory sentinel\n' >"$RACE_DEST/sentinel"
        ;;
      symlink-directory)
        mkdir -p "$RACE_EXTERNAL"
        printf 'external directory sentinel\n' >"$RACE_EXTERNAL/sentinel"
        ln -s "$RACE_EXTERNAL" "$RACE_DEST"
        ;;
      *)
        printf 'unknown race fixture: %s\n' "$RACE_KIND" >&2
        exit 1
        ;;
    esac
    ;;
esac
exit "$status"
EOF
chmod 0755 "${WRAPPER_DIR}/curl"

RACE_PREFIX=${TMP}/race-file-prefix
if REAL_CURL=$(command -v curl) RACE_KIND=file RACE_DEST="${RACE_PREFIX}/bin/hukou" RACE_EXTERNAL='' \
  PATH="${WRAPPER_DIR}:${PATH}" HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$RACE_PREFIX" >/dev/null 2>&1; then
  printf 'installer overwrote a destination created during installation\n' >&2
  exit 1
fi
[ "$(cat "${RACE_PREFIX}/bin/hukou")" = "competing installer" ]

for RACE_FORCE in no-force force; do
  RACE_DIRECTORY_PREFIX=${TMP}/race-directory-${RACE_FORCE}
  if [ "$RACE_FORCE" = force ]; then
    if REAL_CURL=$(command -v curl) RACE_KIND=directory RACE_DEST="${RACE_DIRECTORY_PREFIX}/bin/hukou" RACE_EXTERNAL='' \
      PATH="${WRAPPER_DIR}:${PATH}" HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
      "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$RACE_DIRECTORY_PREFIX" --force >/dev/null 2>&1; then
      printf 'force installer accepted a directory created during installation\n' >&2
      exit 1
    fi
  else
    if REAL_CURL=$(command -v curl) RACE_KIND=directory RACE_DEST="${RACE_DIRECTORY_PREFIX}/bin/hukou" RACE_EXTERNAL='' \
      PATH="${WRAPPER_DIR}:${PATH}" HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
      "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$RACE_DIRECTORY_PREFIX" >/dev/null 2>&1; then
      printf 'installer accepted a directory created during installation\n' >&2
      exit 1
    fi
  fi
  [ "$(find "${RACE_DIRECTORY_PREFIX}/bin/hukou" -mindepth 1 -maxdepth 1 -print)" = "${RACE_DIRECTORY_PREFIX}/bin/hukou/sentinel" ]
  [ "$(cat "${RACE_DIRECTORY_PREFIX}/bin/hukou/sentinel")" = "directory sentinel" ]

  RACE_SYMLINK_PREFIX=${TMP}/race-symlink-directory-${RACE_FORCE}
  RACE_SYMLINK_TARGET=${TMP}/race-external-directory-${RACE_FORCE}
  if [ "$RACE_FORCE" = force ]; then
    if REAL_CURL=$(command -v curl) RACE_KIND=symlink-directory RACE_DEST="${RACE_SYMLINK_PREFIX}/bin/hukou" RACE_EXTERNAL="$RACE_SYMLINK_TARGET" \
      PATH="${WRAPPER_DIR}:${PATH}" HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
      "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$RACE_SYMLINK_PREFIX" --force >/dev/null 2>&1; then
      printf 'force installer accepted a symlink to a directory created during installation\n' >&2
      exit 1
    fi
  else
    if REAL_CURL=$(command -v curl) RACE_KIND=symlink-directory RACE_DEST="${RACE_SYMLINK_PREFIX}/bin/hukou" RACE_EXTERNAL="$RACE_SYMLINK_TARGET" \
      PATH="${WRAPPER_DIR}:${PATH}" HUKOU_ALLOW_FILE_URL=1 HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
      "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "$RACE_SYMLINK_PREFIX" >/dev/null 2>&1; then
      printf 'installer accepted a symlink to a directory created during installation\n' >&2
      exit 1
    fi
  fi
  [ -L "${RACE_SYMLINK_PREFIX}/bin/hukou" ]
  [ "$(readlink "${RACE_SYMLINK_PREFIX}/bin/hukou")" = "$RACE_SYMLINK_TARGET" ]
  [ "$(find "$RACE_SYMLINK_TARGET" -mindepth 1 -maxdepth 1 -print)" = "${RACE_SYMLINK_TARGET}/sentinel" ]
  [ "$(cat "${RACE_SYMLINK_TARGET}/sentinel")" = "external directory sentinel" ]
done

if HUKOU_RELEASE_BASE_URL="file://${TMP}/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "${TMP}/file-without-opt-in" >/dev/null 2>&1; then
  printf 'installer accepted file:// without explicit opt-in\n' >&2
  exit 1
fi

if HUKOU_RELEASE_BASE_URL="http://127.0.0.1:9/releases" \
  "$ROOT/scripts/install.sh" --version "$VERSION" --prefix "${TMP}/http-prefix" >/dev/null 2>&1; then
  printf 'installer accepted an HTTP release URL\n' >&2
  exit 1
fi
[ ! -e "${TMP}/http-prefix" ]

printf 'install script tests passed\n'
