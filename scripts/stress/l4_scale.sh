#!/bin/bash
# L4: 规模与性能极限（黑盒部分）
set -u
. "$(dirname "$0")/lib.sh"

say "== A. 10 万文件巨型盘点 =="
BIG="$STRESS/sb/l4big"; rm -rf "$BIG"; mkdir -p "$BIG/bin"
python3 - <<PY
import os
d = '$STRESS/sb/l4big/bin'
for i in range(100000):
    p = os.path.join(d, f'tool{i}')
    open(p, 'w').write(f'#!/bin/sh\necho {i}\n')
    os.chmod(p, 0o755)
print('100000 files created')
PY
/usr/bin/time -l env PATH="$BIG/bin" "$HUKOU" scan --json > "$LOG/l4big.json" 2> "$LOG/l4big.time"
rc=$?
total=$(python3 -c "import json;print(json.load(open('$LOG/l4big.json'))['summary']['total'])" 2>/dev/null)
wall=$(grep -E "real" "$LOG/l4big.time" | awk '{print $1}')
rss=$(grep -E "maximum resident" "$LOG/l4big.time" | awk '{print $1}')
[ "$rc" = "0" ] && [ "$total" -ge 100000 ] \
  && expect "l4.scan-100k" 0 "total=$total wall=${wall}s maxrss=${rss}KB" \
  || expect "l4.scan-100k" 1 "rc=$rc total=$total wall=${wall}s"

say "== B. 千工具账本 =="
SB="$STRESS/sb/l4k"; rm -rf "$SB"; mkdir -p "$SB/bin" "$SB/data"
python3 - <<PY
import os
d = '$STRESS/sb/l4k/bin'
for i in range(1000):
    p = os.path.join(d, f'tool{i}')
    open(p, 'w').write(f'#!/bin/sh\necho {i}\n')
    os.chmod(p, 0o755)
PY
export PATH="$SB/bin:/usr/bin:/bin" HUKOU_DATA_DIR="$SB/data"
t0=$(date +%s)
for i in $(seq 0 999); do
  "$HUKOU" adopt "tool$i" --local > /dev/null 2>&1 || { expect "l4.k-adopt" 1 "adopt tool$i failed"; exit 1; }
done
t1=$(date +%s)
adopt_sec=$((t1-t0))
"$HUKOU" list > "$LOG/l4k.list" 2>&1 && \
"$HUKOU" doctor > "$LOG/l4k.doctor" 2>&1 && \
"$HUKOU" export > "$LOG/l4k.export" 2>&1 && \
"$HUKOU" support bundle --format json > "$LOG/l4k.support" 2>&1
rc=$?
export_count=$(python3 -c "import json;print(len(json.load(open('$LOG/l4k.export'))['tools']))" 2>/dev/null)
[ "$rc" = "0" ] && [ "$export_count" = "1000" ] \
  && expect "l4.k-tools" 0 "1000 adopts in ${adopt_sec}s; list/doctor/export/support all ok" \
  || expect "l4.k-tools" 1 "rc=$rc export=$export_count"
