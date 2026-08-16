#!/bin/sh

set -eu

# Keep the installer single-file and pipe-safe: it must not source a sibling
# script that a curl-to-sh user does not have. scripts/semver.sh is the
# repository-side canonical copy used by release.sh; the shared acceptance
# matrix in install_test.sh/release_test.sh protects both copies from drift.
hukou_is_strict_semver() {
  hukou_semver=$1

  case "$hukou_semver" in
    *'
'*) return 1 ;;
  esac

  printf '%s\n' "$hukou_semver" | LC_ALL=C grep -Eq \
    '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || return 1

  case "$hukou_semver" in
    *-*) hukou_prerelease=${hukou_semver#*-} ;;
    *) return 0 ;;
  esac

  hukou_remaining=$hukou_prerelease
  while [ -n "$hukou_remaining" ]; do
    case "$hukou_remaining" in
      *.*)
        hukou_identifier=${hukou_remaining%%.*}
        hukou_remaining=${hukou_remaining#*.}
        ;;
      *)
        hukou_identifier=$hukou_remaining
        hukou_remaining=
        ;;
    esac
    case "$hukou_identifier" in
      *[!0-9]*) ;;
      0) ;;
      0*) return 1 ;;
      *) ;;
    esac
  done
}

REPO=${HUKOU_REPO:-rtwsvj/hukou}
RELEASE_BASE_URL=${HUKOU_RELEASE_BASE_URL:-https://github.com/${REPO}/releases}
VERSION=latest
PREFIX=${HUKOU_PREFIX:-${HOME}/.local}
DRY_RUN=0
FORCE=0

usage() {
  cat <<'EOF'
Install a verified hukou release archive without modifying shell configuration.

Usage:
  install.sh [--version vX.Y.Z] [--prefix DIR] [--dry-run] [--force]

Options:
  --version VERSION  Strict SemVer release tag without build metadata (default: latest)
  --prefix DIR       Installation prefix (default: $HOME/.local)
  --dry-run          Print the intended download and destination; write nothing
  --force            Replace an existing file/symlink entry; reject directories
  -h, --help         Show this help

Environment overrides used by tests and mirrors:
  HUKOU_REPO
  HUKOU_RELEASE_BASE_URL (must use HTTPS; file:// requires HUKOU_ALLOW_FILE_URL=1)
  HUKOU_PREFIX
  HUKOU_REQUIRE_ATTESTATION  1/true/yes: require gh attestation verification of
                             the downloaded release archive and fail when gh is
                             unavailable or unauthenticated. Empty/0/false/no:
                             allow the transport-trust fallback (default). Any
                             other value is rejected.
EOF
}

die() {
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

# Verify GitHub's Sigstore build-provenance attestation for the downloaded
# release archive before its checksum is compared and before anything is
# unpacked. The release workflow attests the artifacts listed in checksums.txt
# (the per-platform archives), so the archive itself -- not checksums.txt --
# is the attested subject. This is an out-of-band trust anchor beyond
# transport TLS: it binds the archive to this repository's release workflow
# rather than to whatever the transport served. The signer identity is
# enforced with an anchored --cert-identity-regex over the certificate
# SubjectAlternativeName: --repo alone would accept an attestation produced by
# any workflow in the repository, and gh's --signer-workflow only applies an
# unescaped, unanchored prefix regex to the SAN, so neither pins the identity
# precisely.
#
# When the gh CLI is missing or unauthenticated the installer falls back to
# transport trust only (HTTPS plus the SHA-256 comparison against a
# checksums.txt served from the same origin) with a warning, so pipe-to-sh
# bootstrap keeps working on minimal hosts. Setting HUKOU_REQUIRE_ATTESTATION
# to 1/true/yes makes the attestation mandatory: an unavailable or
# unauthenticated gh then fails closed. A gh that is present and authenticated
# always fails closed on a bad attestation, regardless of the flag.
verify_attestation() {
  attestation_subject=$1

  if ! command -v gh >/dev/null 2>&1; then
    if [ "$REQUIRE_ATTESTATION" -eq 1 ]; then
      die "HUKOU_REQUIRE_ATTESTATION is set but the gh CLI is not installed"
    fi
    printf 'install.sh: gh CLI not found; skipping attestation verification and relying on transport trust only\n' >&2
    return 0
  fi

  if ! gh auth status >/dev/null 2>&1; then
    if [ "$REQUIRE_ATTESTATION" -eq 1 ]; then
      die "HUKOU_REQUIRE_ATTESTATION is set but the gh CLI is not authenticated"
    fi
    printf 'install.sh: gh CLI not authenticated; skipping attestation verification and relying on transport trust only\n' >&2
    return 0
  fi

  # Anchor the certificate identity to this repository's release workflow
  # running for a release tag ref. Regex metacharacters in a mirror-supplied
  # HUKOU_REPO are escaped so the identity stays a literal match.
  attestation_repo_pattern=$(printf '%s' "$REPO" | sed 's/[][\.*^$+?{}|()]/\\&/g')
  attestation_identity_regex='^https://github\.com/'"${attestation_repo_pattern}"'/\.github/workflows/release\.yml@refs/tags/v[0-9][^ ]*$'
  gh attestation verify "$attestation_subject" \
    --repo "$REPO" \
    --cert-identity-regex "$attestation_identity_regex" >/dev/null 2>&1 \
    || die "attestation verification failed for ${attestation_subject##*/} (repo $REPO)"
  printf 'Verified build-provenance attestation for %s (%s)\n' "${attestation_subject##*/}" "$REPO"
}

# Commit a prepared file by operating on the destination directory entry
# itself. POSIX ln/mv may treat an existing directory (or a symlink to one) as
# a container, so their default forms are not safe for this final step.
#
# When available, Perl directly exposes link(2) and rename(2). GNU/BusyBox -T
# is a Linux fallback with the same no-target-directory behavior. If neither
# primitive exists, fail closed with an explicit dependency error.
supports_no_target_directory() {
  "$1" --help 2>&1 | grep -Eq -- '(^|[[:space:],])-T([,[:space:]]|$)|--no-target-directory'
}

activate_no_replace() {
  ACTIVATE_SOURCE=$1
  ACTIVATE_DEST=$2

  if command -v perl >/dev/null 2>&1; then
    perl -e '
      use strict;
      use warnings;
      link($ARGV[0], $ARGV[1])
        or die "cannot create destination entry $ARGV[1]: $!\n";
    ' "$ACTIVATE_SOURCE" "$ACTIVATE_DEST"
  elif supports_no_target_directory ln; then
    ln -T -- "$ACTIVATE_SOURCE" "$ACTIVATE_DEST"
  else
    die "atomic no-replace activation requires Perl or ln with -T support"
  fi
}

activate_replace() {
  ACTIVATE_SOURCE=$1
  ACTIVATE_DEST=$2

  if command -v perl >/dev/null 2>&1; then
    perl -MFcntl=:mode -MErrno=ENOENT -e '
      use strict;
      use warnings;

      my ($source, $dest) = @ARGV;
      my @dest_stat = lstat($dest);
      if (@dest_stat) {
        die "destination is a directory: $dest\n" if S_ISDIR($dest_stat[2]);
        if (S_ISLNK($dest_stat[2]) && -d $dest) {
          die "destination is a symlink to a directory: $dest\n";
        }
      } elsif ($! != ENOENT) {
        die "cannot inspect destination entry $dest: $!\n";
      }

      rename($source, $dest)
        or die "cannot replace destination entry $dest: $!\n";
    ' "$ACTIVATE_SOURCE" "$ACTIVATE_DEST"
  elif supports_no_target_directory mv; then
    # -d follows symlinks here so both a real directory and a symlink to one
    # are rejected. mv -T then replaces only the destination entry and never
    # treats it as a directory, even if it changes after this check.
    [ ! -d "$ACTIVATE_DEST" ] || {
      printf 'install.sh: destination is a directory or a symlink to one: %s\n' "$ACTIVATE_DEST" >&2
      return 1
    }
    mv -fT -- "$ACTIVATE_SOURCE" "$ACTIVATE_DEST"
  else
    die "atomic replacement requires Perl or mv with -T support"
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || die "--version requires a value"
      VERSION=$2
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix requires a value"
      PREFIX=$2
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

[ -n "$PREFIX" ] || die "installation prefix must not be empty"

# Parse HUKOU_REQUIRE_ATTESTATION strictly: a typo must fail loudly instead of
# silently degrading a hardened install to the transport-trust fallback.
case "$(printf '%s' "${HUKOU_REQUIRE_ATTESTATION:-}" | tr '[:upper:]' '[:lower:]')" in
  ''|0|false|no) REQUIRE_ATTESTATION=0 ;;
  1|true|yes) REQUIRE_ATTESTATION=1 ;;
  *) die "invalid HUKOU_REQUIRE_ATTESTATION value '${HUKOU_REQUIRE_ATTESTATION}'; use 1/true/yes to require attestation or 0/false/no (or unset) to allow the transport-trust fallback" ;;
esac

case "$RELEASE_BASE_URL" in
  https://*) CURL_PROTOCOLS='=https' ;;
  file://*)
    [ "${HUKOU_ALLOW_FILE_URL:-0}" = 1 ] || die "file:// release URLs require HUKOU_ALLOW_FILE_URL=1"
    CURL_PROTOCOLS='=file'
    ;;
  *) die "release base URL must use HTTPS" ;;
esac

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" != latest ] && ! hukou_is_strict_semver "$VERSION"; then
  die "release version must be strict SemVer 2.0.0 without build metadata, such as v0.3.0 or v0.3.0-rc.1: $VERSION"
fi

DEST_DIR=${PREFIX}/bin
DEST=${DEST_DIR}/hukou

if [ "$DRY_RUN" -eq 1 ]; then
  printf 'Would resolve hukou release: %s\n' "$VERSION"
  printf 'Would select platform: %s/%s\n' "$OS" "$ARCH"
  printf 'Would verify checksums from: %s/download/<tag>/checksums.txt\n' "$RELEASE_BASE_URL"
  printf 'Would verify the archive build-provenance attestation when gh is available\n'
  printf 'Would install atomically to: %s\n' "$DEST"
  exit 0
fi

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

if [ "$VERSION" = latest ]; then
  EFFECTIVE_URL=$(curl -fsSL --proto "$CURL_PROTOCOLS" --proto-redir "$CURL_PROTOCOLS" \
    -o /dev/null -w '%{url_effective}' "${RELEASE_BASE_URL}/latest") || die "cannot resolve latest release"
  VERSION=${EFFECTIVE_URL##*/}
fi

if ! hukou_is_strict_semver "$VERSION"; then
  die "release version must be strict SemVer 2.0.0 without build metadata, such as v0.3.0 or v0.3.0-rc.1: $VERSION"
fi

VERSION_NUMBER=${VERSION#v}
ARCHIVE=hukou_${VERSION_NUMBER}_${OS}_${ARCH}.tar.gz
ARCHIVE_ROOT=hukou_${VERSION_NUMBER}_${OS}_${ARCH}
DOWNLOAD_BASE=${RELEASE_BASE_URL}/download/${VERSION}

if { [ -e "$DEST" ] || [ -L "$DEST" ]; } && [ "$FORCE" -ne 1 ]; then
  die "$DEST already exists; use its package manager or pass --force explicitly"
fi

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hukou-install.XXXXXX") || die "cannot create temporary directory"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT HUP INT TERM

CHECKSUMS=${TMP_DIR}/checksums.txt
ARCHIVE_PATH=${TMP_DIR}/${ARCHIVE}

curl -fsSL --proto "$CURL_PROTOCOLS" --proto-redir "$CURL_PROTOCOLS" --tlsv1.2 --retry 2 --retry-delay 1 \
  -o "$CHECKSUMS" "${DOWNLOAD_BASE}/checksums.txt" || die "cannot download checksums.txt"
curl -fsSL --proto "$CURL_PROTOCOLS" --proto-redir "$CURL_PROTOCOLS" --tlsv1.2 --retry 2 --retry-delay 1 \
  -o "$ARCHIVE_PATH" "${DOWNLOAD_BASE}/${ARCHIVE}" || die "cannot download $ARCHIVE"

# The archive is the attested subject; verify it before trusting the SHA-256
# comparison below and before any tar inspection or extraction runs.
verify_attestation "$ARCHIVE_PATH"

EXPECTED=$(awk -v name="$ARCHIVE" '$2 == name || $2 == "*" name { print $1 }' "$CHECKSUMS")
[ -n "$EXPECTED" ] || die "checksums.txt has no exact entry for $ARCHIVE"
[ "$(printf '%s\n' "$EXPECTED" | wc -l | tr -d ' ')" = 1 ] || die "checksums.txt has duplicate entries for $ARCHIVE"
case "$EXPECTED" in
  *[!0-9A-Fa-f]*|'') die "invalid SHA-256 for $ARCHIVE" ;;
esac
[ "${#EXPECTED}" -eq 64 ] || die "invalid SHA-256 length for $ARCHIVE"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$ARCHIVE_PATH" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL=$(shasum -a 256 "$ARCHIVE_PATH" | awk '{print $1}')
else
  die "sha256sum or shasum is required"
fi
[ "$ACTUAL" = "$EXPECTED" ] || die "SHA-256 mismatch for $ARCHIVE"

MEMBERS=${TMP_DIR}/members.txt
tar -tzf "$ARCHIVE_PATH" >"$MEMBERS" || die "cannot inspect $ARCHIVE"
[ -s "$MEMBERS" ] || die "$ARCHIVE is empty"
while IFS= read -r MEMBER; do
  case "$MEMBER" in
    /*|..|../*|*/..|*/../*) die "unsafe archive member: $MEMBER" ;;
    "${ARCHIVE_ROOT}"|"${ARCHIVE_ROOT}/"*) ;;
    *) die "unexpected archive root: $MEMBER" ;;
  esac
done <"$MEMBERS"
TARGET_MEMBER=${ARCHIVE_ROOT}/hukou
TARGET_MEMBER_COUNT=$(awk -v target="$TARGET_MEMBER" '$0 == target { count++ } END { print count + 0 }' "$MEMBERS")
[ "$TARGET_MEMBER_COUNT" -eq 1 ] || die "$ARCHIVE must contain exactly one $TARGET_MEMBER"

UNPACK=${TMP_DIR}/unpack
mkdir "$UNPACK"
tar -xzf "$ARCHIVE_PATH" -C "$UNPACK" "$TARGET_MEMBER" || die "cannot extract hukou from $ARCHIVE"
SOURCE=${UNPACK}/${TARGET_MEMBER}
if [ ! -f "$SOURCE" ] || [ -L "$SOURCE" ]; then
  die "archive does not contain a regular hukou binary"
fi
[ -x "$SOURCE" ] || die "archive hukou binary is not executable"

mkdir -p "$DEST_DIR"
INSTALL_TMP=$(mktemp "${DEST_DIR}/.hukou-install.XXXXXX") || die "cannot create destination temporary file"
trap 'rm -f "$INSTALL_TMP"; cleanup' EXIT HUP INT TERM
cp "$SOURCE" "$INSTALL_TMP" || die "cannot copy hukou into destination"
chmod 0755 "$INSTALL_TMP" || die "cannot set executable mode"
if [ "$FORCE" -eq 1 ]; then
  # rename(2) atomically replaces regular files and symlink entries without
  # following them. Directories and symlinks to directories are rejected.
  activate_replace "$INSTALL_TMP" "$DEST" || die "cannot activate $DEST"
else
  # link(2) is an atomic no-replace commit. It fails if any file, directory,
  # or symlink appeared after the initial check, without treating a directory
  # destination as a container.
  activate_no_replace "$INSTALL_TMP" "$DEST" || die "$DEST appeared during installation; refusing to replace it without --force"
  rm -f "$INSTALL_TMP"
fi
trap cleanup EXIT HUP INT TERM

printf 'Installed hukou %s to %s\n' "$VERSION" "$DEST"
case ":${PATH}:" in
  *":${DEST_DIR}:"*) ;;
  *) printf 'Add %s to PATH to run hukou.\n' "$DEST_DIR" ;;
esac
