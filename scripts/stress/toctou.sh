#!/bin/bash
# L3-4: TOCTOU 竞速 — 升级停在 pre-activate 暂停点时，外部进程偷换活动文件
# 期望：升级拒绝覆盖并干净中止（或补偿回滚）；终态体检健康；绝不出现半吊子。
set -u
. "$(dirname "$0")/lib.sh"
if tok=$(gh auth token 2>/dev/null); then export GH_TOKEN="$tok"; fi

new_sandbox toctou
env="$(sb_env toctou)"
live="$STRESS/sb/toctou/bin/tool"

env $env "$HUKOU" adopt tool BurntSushi/ripgrep --tag 15.1.0 --exe rg > /dev/null 2>&1 || { expect "toctou" 1 "adopt failed"; exit 1; }

marker="$LOG/toctou.marker"
rm -f "$marker"
env $env HUKOU_STRESS_PAUSE=pre-activate HUKOU_STRESS_PAUSE_FILE="$marker" \
  "$HUKOU" upgrade tool > "$LOG/toctou.out" 2> "$LOG/toctou.err" &
pid=$!
# 等目标相位标记（最多 150 秒）
for i in $(seq 1 150); do
  [ -f "$marker" ] && break
  kill -0 "$pid" 2>/dev/null || { wait "$pid"; expect "toctou" 1 "upgrade exited before phase (rc=$?)"; exit 1; }
  sleep 1
done
if [ ! -f "$marker" ]; then
  kill -9 "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
  expect "toctou" 1 "pre-activate not reached in 150s (network too slow?)"
  exit 1
fi

# 攻击窗口：把活动文件换成一个完全不同内容的新文件（外部非协作写者）
printf '#!/bin/sh\necho TAMPERED\n' > "$live"
chmod 755 "$live"
sleep 1
kill -0 "$pid" 2>/dev/null || { wait "$pid"; expect "toctou" 1 "upgrade exited before attack window (rc=$?)"; exit 1; }

# 放行（等暂停点超时自然继续）或直接 SIGKILL 都算场景：
# 场景A：让它继续 -> 必须拒绝覆盖
wait "$pid" 2>/dev/null
rc=$?
env $env "$HUKOU" doctor > "$LOG/toctou.doctor" 2>&1
doc_rc=$?
tag=$(env $env python3 -c "
import json
m=json.load(open('$STRESS/sb/toctou/data/manifest.json'))
print([e['tag'] for e in m['entries'] if e['name']=='tool'][0])" 2>/dev/null)
live_sha=$(shasum -a 256 "$live" | cut -d' ' -f1)

say "rc=$rc doctor_rc=$doc_rc tag=$tag live_sha=$live_sha"
grep -E "refusing overwrite|changed" "$LOG/toctou.err" | head -2

# 判定：要么升级被拒绝且账本仍 15.1.0、要么体检如实报漂移——但绝不能是"新版本+坏账本"
if [ "$tag" = "15.1.0" ] && [ "$doc_rc" = "0" ]; then
  expect "toctou.refused-cleanly" 0 "refused overwrite, state intact (tag=15.1.0)"
elif [ "$tag" = "15.1.0" ] && [ "$doc_rc" != "0" ]; then
  expect "toctou.drift-reported" 0 "state intact, doctor honestly reports external drift"
else
  expect "toctou" 1 "unexpected end state: tag=$tag doctor_rc=$doc_rc"
fi
