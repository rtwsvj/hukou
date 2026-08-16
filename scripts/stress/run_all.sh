#!/bin/bash
# hukou six-layer stress suite entry point: make stress
# Usage and switches: docs/audit/stress-2026-08-14.md (rerun section).
#   SKIP_NETWORK=1  skip the three network-dependent groups
#                   (upgrade crash sweep / TOCTOU / L5 bad-proxy matrix)
set -u
cd "$(dirname "$0")"
. ./lib.sh
: > "$LOG/results.tsv"

FAILED=0
run() {
  local label=$1
  shift
  say "===== $label ====="
  "$@" > "$LOG/$label.log" 2>&1
  local rc=$?
  say "$label exit=$rc (log: $LOG/$label.log)"
  if [ "$rc" != "0" ]; then
    FAILED=$((FAILED+1))
  fi
}

run l3-adopt-crash   ./crash_sweep.sh
run l3-lock-storm    ./lock_storm.sh
run l3-lock-attack   ./lock_attack.sh
run l1-names         ./l1_names.sh
run l1-extra         ./l1_extra.sh
run l2-manifest      ./l2_manifest.sh
run l4-scale         ./l4_scale.sh
run l6-ui            ./l6_ui.sh

if [ "${SKIP_NETWORK:-0}" != "1" ]; then
  run l3-upgrade-crash ./crash_sweep_upgrade.sh
  run l3-toctou        ./toctou.sh
  run l5-network       ./l5_net.sh
else
  say "SKIP_NETWORK=1: l3-upgrade-crash / l3-toctou / l5-network skipped"
fi

say "======== summary ========"
total_pass=$(grep -c "^PASS" "$LOG/results.tsv" 2>/dev/null || true)
total_fail=$(grep -c "^FAIL" "$LOG/results.tsv" 2>/dev/null || true)
say "PASS=$total_pass FAIL=$total_fail layers-failed=$FAILED"
say "details: $LOG/results.tsv | sandbox root: $STRESS (safe to delete)"
if [ "$total_fail" != "0" ] || [ "$FAILED" != "0" ]; then
  say "FAILURES PRESENT"
  exit 1
fi
say "ALL LAYERS GREEN"
