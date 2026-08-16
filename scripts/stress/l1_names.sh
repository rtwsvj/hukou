#!/bin/bash
# L1: 命名与文件系统攻击
# 原则：攻击沙盒内所有"不可能"的名字与文件类型，验证 scan/adopt/doctor 的
# 消毒、拒绝与零崩溃。全部证据落 LOG。
set -u
. "$(dirname "$0")/lib.sh"

SB="$STRESS/sb/l1"
rm -rf "$SB"; mkdir -p "$SB/bin" "$SB/data"
cd "$SB/bin" || exit 1

# --- 1. 名称地狱（可执行脚本） ---
mkscript() { printf '#!/bin/sh\necho x\n' > "$1"; chmod 755 "$1"; }
mkscript $'tab\tname'
mkscript $'nl\nname'
mkscript $'esc\033name'
mkscript 'trailing '
mkscript '   '
python3 -c "import os;open('rtl\u202ename','w').write('#!/bin/sh\necho x\n');os.chmod('rtl\u202ename',0o755)"
mkscript '🦀crab'
mkscript '我的工具'
mkscript '-rf'
mkscript '~'
mkscript '*.txt'
mkscript 'a`cmd`b'
mkscript 'a$(x)b'
mkscript 'a;rm;b'
LONG=$(python3 -c "print('L'*250+'x')")   # 251 字节
mkscript "$LONG"

# --- 2. 文件类型全集 ---
mkfifo pipefile
python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.bind('sockfile')"
dd if=/dev/zero of=sparse bs=1 count=0 seek=10G 2>/dev/null
cp /bin/echo setuidbin; python3 -c "import os;os.chmod('setuidbin',0o4755)"; stat -f '%Sp' setuidbin >> "$LOG/l1-setuid.mode"
printf 'x' > noperm; chmod 000 noperm
printf 'x' > noexec; chmod 644 noexec
mkdir subdir
# --- 3. 链接地狱 ---
ln -s /nonexistent dangling
ln -s loop loop
ln -s loopA loopB; ln -s loopB loopA
ln -s /dev/null devnullink
ln -s subdir dirlink
python3 - <<PY
import os
p = 'deep'
for i in range(1000):
    os.symlink(p, f'tmp{i}')
    p = f'tmp{i}'
os.rename('tmp999', 'deep1000')
for i in range(999):
    os.remove(f'tmp{i}')
PY

env="PATH=$SB/bin:/usr/bin:/bin HUKOU_DATA_DIR=$SB/data"
export HUKOU_DATA_DIR="$SB/data" PATH="$SB/bin:/usr/bin:/bin"

say "== A. scan 表格式 =="
"$HUKOU" scan > "$LOG/l1-scan.out" 2> "$LOG/l1-scan.err"
rc=$?
esc=$(python3 -c "
data=open('$LOG/l1-scan.out','rb').read()
print(1 if b'\\x1b' in data or b'\\r' in data else 0)")
rows=$(grep -c . "$LOG/l1-scan.out")
expect "l1.scan.no-crash" "$rc" "rc=$rc rows=$rows"
[ "$esc" = "0" ] || expect "l1.scan.no-escape" 1 "raw ESC/CR leaked into table"
[ "$esc" = "0" ] && expect "l1.scan.no-escape" 0 "table fully sanitized"

say "== B. scan --json =="
"$HUKOU" scan --json > "$LOG/l1-scan.json" 2>/dev/null
python3 - <<PY
import json
d = json.load(open('$STRESS/log/l1-scan.json'))
print("json-ok", d['summary'])
for fe in d.get('file_errors', [])[:6]:
    print("  skipped:", fe)
PY

say "== C. adopt 攻击矩阵 =="
adopt_try() { # adopt_try <label> <name> <want-refuse:0/1>
  local label=$1 name=$2 refuse=$3
  if "$HUKOU" adopt "$name" --local > /dev/null 2>&1; then
    [ "$refuse" = "1" ] && expect "l1.adopt.$label" 1 "accepted but should refuse" \
      || expect "l1.adopt.$label" 0 "accepted (documented behavior)"
  else
    [ "$refuse" = "1" ] && expect "l1.adopt.$label" 0 "refused cleanly" \
      || expect "l1.adopt.$label" 1 "refused unexpectedly: $(tail -1 /dev/null)"
  fi
}
adopt_try pipe pipefile 1
adopt_try sock sockfile 1
adopt_try dir subdir 1
adopt_try dangling dangling 1
adopt_try selfloop loop 1
adopt_try devnull devnullink 1
adopt_try dirlink dirlink 1
# 沙盒剥除 setuid 位（见 l1-setuid.mode），无法活体触发；逻辑由
# internal/store 单测（AdoptOriginal special mode rejection）覆盖
say "NOTE  setuid: sandbox strips the bit; logic covered by unit tests"
adopt_try noexec noexec 1
adopt_try noperm noperm 1
adopt_try space 'trailing ' 0
adopt_try pure-space '   ' 1
adopt_try unicode '我的工具' 0
adopt_try emoji '🦀crab' 0
adopt_try tilde '~' 0
adopt_try glob '*.txt' 0
adopt_try backtick 'a`cmd`b' 0
adopt_try dollar 'a$(x)b' 0
adopt_try semi 'a;rm;b' 0
RTL=$(python3 -c "print('rtl\u202ename')")
adopt_try rtl "$RTL" 0
adopt_try long "$LONG" 0
adopt_try tab $'tab\tname' 1
adopt_try nl $'nl\nname' 1
adopt_try esc $'esc\033name' 1

say "== D. 收编后终态体检 =="
if "$HUKOU" doctor > "$LOG/l1-doctor.out" 2>&1; then
  expect "l1.doctor-final" 0 "healthy after name-hell adopts"
else
  expect "l1.doctor-final" 1 "unhealthy: $(grep -m1 ERROR "$LOG/l1-doctor.out" | head -c 100)"
fi
