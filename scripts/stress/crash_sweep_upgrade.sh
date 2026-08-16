#!/bin/bash
# L3-1b: 升级路径崩溃断点扫射（真实网络，ripgrep 资产）
# 相位 pre-activate（版本已入库、活动文件未替换→应回滚到旧版）
# 相位 post-commit（COMMIT 已落盘、未清理→应前滚到新版）
set -u
. "$(dirname "$0")/lib.sh"

if tok=$(gh auth token 2>/dev/null); then export GH_TOKEN="$tok"; fi

run_one() { # run_one <phase> <iter>
  local phase=$1 iter=$2
  local sb="upcrash-$phase-$iter"
  new_sandbox "$sb"
  local env
  env="$(sb_env "$sb")"

  env $env "$HUKOU" adopt tool BurntSushi/ripgrep --tag 15.1.0 --exe rg > /dev/null 2>&1 || {
    expect "upcrash.$phase#$iter" 1 "adopt failed"
    return
  }
  local marker="$LOG/$sb.marker"
  rm -f "$marker"
  env $env HUKOU_STRESS_PAUSE="$phase" HUKOU_STRESS_PAUSE_FILE="$marker" \
    "$HUKOU" upgrade tool > "$LOG/$sb.out" 2> "$LOG/$sb.err" &
  local pid=$!
  # 等进程抵达目标相位（标记文件出现）；网络慢时最多等 150 秒。
  local i
  for i in $(seq 1 150); do
    [ -f "$marker" ] && break
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid"; expect "upcrash.$phase#$iter" 1 "upgrade exited before phase (rc=$?)"
      return
    fi
    sleep 1
  done
  if [ ! -f "$marker" ]; then
    kill -9 "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
    expect "upcrash.$phase#$iter" 1 "phase not reached in 150s (network too slow?)"
    return
  fi
  kill -9 "$pid"; wait "$pid" 2>/dev/null

  if ! env $env "$HUKOU" doctor > "$LOG/$sb.doctor" 2>&1; then :; fi
  cp /bin/ls "$STRESS/sb/$sb/bin/probe"
  if ! env $env "$HUKOU" adopt probe --local >> "$LOG/$sb.recover" 2>&1; then
    expect "upcrash.$phase#$iter" 1 "post-kill write failed: $(tail -1 "$LOG/$sb.recover")"
    return
  fi
  if ! env $env "$HUKOU" doctor > "$LOG/$sb.final" 2>&1; then
    expect "upcrash.$phase#$iter" 1 "final doctor unhealthy: $(grep -m1 ERROR "$LOG/$sb.final" | head -c 100)"
    return
  fi
  local tag
  tag=$(env $env "$HUKOU" list --help >/dev/null 2>&1; env $env python3 -c "
import json,sys
m=json.load(open('$STRESS/sb/$sb/data/manifest.json'))
print([e['tag'] for e in m['entries'] if e['name']=='tool'][0])
" 2>/dev/null)
  case "$phase" in
    pre-activate) [ "$tag" = "15.1.0" ] || { expect "upcrash.$phase#$iter" 1 "tag=$tag, want 15.1.0 (rollback)"; return; } ;;
    post-commit)  [ "$tag" = "15.2.0" ] || { expect "upcrash.$phase#$iter" 1 "tag=$tag, want 15.2.0 (rollforward)"; return; } ;;
  esac
  expect "upcrash.$phase#$iter" 0 "converged (tag=$tag)"
}

for phase in pre-activate post-commit; do
  say "== upgrade sweep phase: $phase =="
  for i in $(seq 1 5); do run_one "$phase" "$i"; done
done
say "upgrade sweep done"
