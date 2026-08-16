#!/bin/bash
# L3-2: 锁风暴 — 16 个进程同时抢同一数据目录收编不同工具
# 期望：严格互斥；抢到的成功，抢不到的立刻清晰报"状态被锁定"；绝不挂起。
set -u
. "$(dirname "$0")/lib.sh"

new_sandbox lockstorm
env="$(sb_env lockstorm)"
for i in $(seq 1 16); do
  cp /bin/ls "$STRESS/sb/lockstorm/bin/tool$i"
done

start=$(date +%s)
pids=()
for i in $(seq 1 16); do
  env $env "$HUKOU" adopt "tool$i" --local > "$LOG/lockstorm-$i.out" 2> "$LOG/lockstorm-$i.err" &
  pids+=($!)
done
ok=0; locked=0; other=0; hangs=0
for i in $(seq 1 16); do
  pid=${pids[$((i-1))]}
  if wait "$pid" 2>/dev/null; then ok=$((ok+1));
  else
    if grep -q "locked" "$LOG/lockstorm-$i.err"; then locked=$((locked+1));
    else other=$((other+1)); fi
  fi
done
elapsed=$(( $(date +%s) - start ))

# 终态必须健康，且收编数量与成功数一致
env $env "$HUKOU" doctor > "$LOG/lockstorm.doctor" 2>&1
doc_ok=$?
count=$(env $env python3 -c "
import json
m=json.load(open('$STRESS/sb/lockstorm/data/manifest.json'))
print(len(m['entries']))" 2>/dev/null)

say "ok=$ok locked=$locked other=$other elapsed=${elapsed}s adopted=$count doctor_rc=$doc_ok"
[ "$other" = "0" ] || expect "lockstorm.no-other-errors" 1 "unexpected errors: $other"
[ "$doc_ok" = "0" ] || expect "lockstorm.doctor-healthy" 1 "doctor rc=$doc_ok"
[ "$count" = "$ok" ] || expect "lockstorm.manifest-count" 1 "adopted=$count but ok=$ok"
[ "$elapsed" -lt 60 ] || expect "lockstorm.no-hang" 1 "took ${elapsed}s"
[ "$other" = "0" ] && [ "$doc_ok" = "0" ] && [ "$count" = "$ok" ] && [ "$elapsed" -lt 60 ] \
  && expect "lockstorm.overall" 0 "serialized, fail-fast, healthy"
