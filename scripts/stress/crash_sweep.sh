#!/bin/bash
# L3-1: SIGKILL 崩溃断点扫射（本地 adopt 相位，每相位 20 次）
# 攻击：在每个事务断点上 SIGKILL，然后验证恢复收敛。
# 相位: post-prepared / pre-commit / post-commit / pre-finalize
set -u
. "$(dirname "$0")/lib.sh"

run_one() { # run_one <phase> <iter>
  local phase=$1 iter=$2
  local sb="crash-$phase-$iter"
  new_sandbox "$sb"
  local env
  env="$(sb_env "$sb")"
  local tool="$STRESS/sb/$sb/bin/tool"
  local data="$STRESS/sb/$sb/data"

  # 启动被攻击的收编（后台，命中暂停点后停在原地）
  env $env HUKOU_STRESS_PAUSE="$phase" "$HUKOU" adopt tool owner/repo --tag v1.0.0 \
    > "$LOG/$sb.out" 2> "$LOG/$sb.err" &
  local pid=$!
  sleep 1.2
  if ! kill -0 "$pid" 2>/dev/null; then
    # 进程提前退出了：要么暂停点没命中（不可接受），要么本来就失败
    wait "$pid"; local pre=$?
    expect "crash.$phase#$iter" 1 "process exited before kill (rc=$pre) -> phase hook missed"
    return
  fi
  kill -9 "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null

  # 1) 只读体检不崩溃
  if ! env $env "$HUKOU" doctor > "$LOG/$sb.doctor" 2>&1; then
    : # doctor 非零是允许的（有待恢复事务时会报状态），但不能崩
  fi
  # 2) 下一个写命令必须自动恢复并成功
  cp /bin/ls "$STRESS/sb/$sb/bin/probe"
  if ! env $env "$HUKOU" adopt probe --local >> "$LOG/$sb.recover" 2>&1; then
    expect "crash.$phase#$iter" 1 "post-kill write failed to recover: $(tail -1 "$LOG/$sb.recover")"
    return
  fi
  # 3) 终态体检必须健康
  if ! env $env "$HUKOU" doctor > "$LOG/$sb.final" 2>&1; then
    expect "crash.$phase#$iter" 1 "final doctor not healthy: $(grep -m1 ERROR "$LOG/$sb.final" | head -c 120)"
    return
  fi
  # 4) 账本必须是合法 JSON 且一致性检查可跑
  if ! python3 -c "import json; json.load(open('$data/manifest.json'))" 2>/dev/null; then
    expect "crash.$phase#$iter" 1 "manifest corrupted"
    return
  fi
  expect "crash.$phase#$iter" 0 "recovered cleanly"
}

for phase in post-prepared pre-commit post-commit pre-finalize; do
  say "== sweep phase: $phase =="
  for i in $(seq 1 20); do
    run_one "$phase" "$i"
  done
done
say "sweep done"
