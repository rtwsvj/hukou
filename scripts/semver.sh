#!/bin/sh

# Return success only for a v-prefixed SemVer 2.0.0 version without build
# metadata. Numeric core and prerelease identifiers must not have leading
# zeroes. Keep this helper POSIX-sh compatible because install.sh uses /bin/sh.
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
