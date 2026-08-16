#!/bin/bash
# L6: 界面与多语言极限
set -u
. "$(dirname "$0")/lib.sh"

SB="$STRESS/sb/l6"; rm -rf "$SB"; mkdir -p "$SB/bin" "$SB/data"
cp /bin/echo "$SB/bin/tool"; cp /bin/ls "$SB/bin/localtool"
export PATH="$SB/bin:/usr/bin:/bin" HUKOU_DATA_DIR="$SB/data"
"$HUKOU" adopt tool owner/repo --tag v1.0.0 > /dev/null 2>&1
"$HUKOU" adopt localtool --local > /dev/null 2>&1

CMDS=(
  "version"
  "scan"
  "scan --json"
  "explain tool"
  "explain tool --json"
  "list"
  "doctor"
  "doctor --json"
  "doctor receipt tool"
  "doctor receipt tool --json"
  "policy show"
  "policy show --json"
  "support bundle --format json"
  "up --dry-run"
  "up --dry-run --json"
  "up history"
  "up history --json"
  "export"
)
say "== A. 全命令矩阵：中英双模式 =="
matrix_ok=0; matrix_fail=0
for cmd in "${CMDS[@]}"; do
  en_out="$LOG/l6-en.out"; en_err="$LOG/l6-en.err"
  zh_out="$LOG/l6-zh.out"; zh_err="$LOG/l6-zh.err"
  HUKOU_LANG=en "$HUKOU" $cmd > "$en_out" 2> "$en_err"; en_rc=$?
  HUKOU_LANG=zh "$HUKOU" $cmd > "$zh_out" 2> "$zh_err"; zh_rc=$?
  key=$(echo "$cmd" | tr ' ' '_')
  if [ "$en_rc" != "$zh_rc" ]; then
    expect "l6.matrix.$key.rc" 1 "en=$en_rc zh=$zh_rc"
    matrix_fail=$((matrix_fail+1)); continue
  fi
  if echo "$cmd" | grep -q -- "--json"; then
    # 机器契约：JSON 必须逐字节一致（不管语言）
    if cmp -s "$en_out" "$zh_out"; then
      expect "l6.matrix.$key.json-identical" 0 "byte-identical JSON (rc=$en_rc)"
      matrix_ok=$((matrix_ok+1))
    else
      expect "l6.matrix.$key.json-identical" 1 "JSON differs between locales"
      matrix_fail=$((matrix_fail+1))
    fi
  else
    if [ "$(wc -l < "$en_out" | tr -d ' ')" = "$(wc -l < "$zh_out" | tr -d ' ')" ]; then
      expect "l6.matrix.$key.lines" 0 "same line count (rc=$en_rc)"
      matrix_ok=$((matrix_ok+1))
    else
      expect "l6.matrix.$key.lines" 1 "line counts differ: en=$(wc -l < "$en_out") zh=$(wc -l < "$zh_out")"
      matrix_fail=$((matrix_fail+1))
    fi
  fi
done
say "matrix: ok=$matrix_ok fail=$matrix_fail"

say "== B. 中文表格宽度对齐（扫描表列起点一致）=="
HUKOU_LANG=zh "$HUKOU" scan > "$LOG/l6-zh-scan.out" 2>&1
python3 - <<PY
import sys
lines = open('$STRESS/log/l6-zh-scan.out', encoding='utf-8').read().splitlines()
if len(lines) < 3:
    print('FAIL not enough lines'); sys.exit(1)
def width(s):
    return sum(2 if (0x1100 <= ord(c) <= 0x115f or 0x2e80 <= ord(c) <= 0xa4cf or 0xac00 <= ord(c) <= 0xd7a3 or 0xf900 <= ord(c) <= 0xfaff or 0xff00 <= ord(c) <= 0xff60) else 1 for c in s)
header = lines[0]
hpath = header.find('路径')
hwidth = width(header[:hpath]) if hpath >= 0 else -1
ok = hwidth >= 0
for line in lines[1:6]:
    if not line.strip():
        continue
    idx = line.find('/')
    if idx < 0 or width(line[:idx]) != hwidth:
        ok = False
        print(f'FAIL row misaligned: header={hwidth} row={width(line[:idx]) if idx>=0 else -1} line={line!r}')
print('PASS' if ok else f'FAIL header-width={hwidth}')
sys.exit(0 if ok else 1)
PY
[ $? = 0 ] && expect "l6.zh-align" 0 "column starts aligned" || expect "l6.zh-align" 1 "column starts diverge"

say "== C. 窄终端（COLUMNS=20/40）=="
for w in 20 40; do
  COLUMNS=$w HUKOU_LANG=zh "$HUKOU" scan > /dev/null 2>&1; rc=$?
  [ "$rc" = "0" ] && expect "l6.narrow-$w" 0 "no crash at COLUMNS=$w" \
    || expect "l6.narrow-$w" 1 "rc=$rc at COLUMNS=$w"
done

say "== D. 管道提前关闭（SIGPIPE）=="
set +e
HUKOU_LANG=zh "$HUKOU" scan 2> "$LOG/l6-pipe.err" | head -1 > /dev/null
rcs=("${PIPESTATUS[@]}")
set -e
if [ "${rcs[0]}" = "0" ] || [ "${rcs[0]}" = "141" ]; then
  expect "l6.pipe-close" 0 "graceful (rc=${rcs[0]}, standard SIGPIPE allowed)"
else
  expect "l6.pipe-close" 1 "rc=${rcs[0]}"
fi
grep -q "panic" "$LOG/l6-pipe.err" && expect "l6.pipe-no-panic" 1 "panic trace on pipe close" \
  || expect "l6.pipe-no-panic" 0 "no panic"

say "== E. 语言环境冲突矩阵 =="
locale_case() { # locale_case <label> <env...> <want zh:0/1>
  local label=$1; shift
  local want=$1; shift
  local first
  first=$(env "$@" "$HUKOU" scan 2>/dev/null | sed -n '1p')
  local is_zh=0
  echo "$first" | grep -q "名称" && is_zh=1
  if [ "$is_zh" = "$want" ]; then
    expect "l6.locale.$label" 0 "first-header='$first'"
  else
    expect "l6.locale.$label" 1 "first-header='$first'"
  fi
}
locale_case explicit-zh-beats-c 1 HUKOU_LANG=zh LANG=C LC_ALL=C
locale_case explicit-en-beats-zh 0 HUKOU_LANG=en LANG=zh_CN.UTF-8 LC_ALL=zh_CN.UTF-8
locale_case lcall-beats-lang 1 LANG=C LC_ALL=zh_CN.UTF-8
locale_case unknown-explicit-falls-to-en 0 HUKOU_LANG=xx LANG=zh_CN.UTF-8
locale_case system-zh 1 LANG=zh_CN.UTF-8
locale_case system-c 0 LANG=C
