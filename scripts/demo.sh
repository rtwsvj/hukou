#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
HUKOU_BIN=${HUKOU_BIN:-${ROOT}/bin/hukou}
[ -x "$HUKOU_BIN" ] || {
  printf 'Build hukou first with: make build\n' >&2
  exit 1
}
HUKOU_BIN=$(CDPATH='' cd -- "$(dirname -- "$HUKOU_BIN")" && pwd)/$(basename -- "$HUKOU_BIN")

SANDBOX=$(mktemp -d "${TMPDIR:-/tmp}/hukou-demo.XXXXXX")
cleanup() {
  rm -rf "$SANDBOX"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$SANDBOX/bin"
cat >"$SANDBOX/bin/orphan-tool" <<'EOF'
#!/bin/sh
printf 'orphan-tool v1\n'
EOF
chmod 0755 "$SANDBOX/bin/orphan-tool"

run() {
  printf '\n$ %s\n' "$*"
  HUKOU_DATA_DIR="$SANDBOX/data" PATH="$SANDBOX/bin" "$HUKOU_BIN" "$@"
}

printf 'hukou isolated trust-first demo\n'
printf 'sandbox: temporary and removed automatically\n'

run scan --unknown-only
run explain orphan-tool
run adopt orphan-tool --local --dry-run
run adopt orphan-tool --local
run list
run doctor

printf '\nDemo complete. No real PATH entry or hukou data directory was changed.\n'
