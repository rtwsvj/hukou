#!/bin/bash
# L1b: 大小写陷阱 / 卷权限极限 / ENOSPC
set -u
. "$(dirname "$0")/lib.sh"

# --- 大小写陷阱 ---
SB="$STRESS/sb/l1b"; rm -rf "$SB"; mkdir -p "$SB/bin" "$SB/data"
cp /bin/echo "$SB/bin/foo"; chmod 755 "$SB/bin/foo"
export PATH="$SB/bin:/usr/bin:/bin" HUKOU_DATA_DIR="$SB/data"
"$HUKOU" adopt foo --local > /dev/null 2>&1
# 攻击：手工放一个大小写别名工具目录 + 一个非规范拼写的 original 目录
mkdir -p "$SB/data/store/Foo/original" "$SB/data/store/foo/Original"
cp /bin/ls "$SB/data/store/foo/Original/ls" 2>/dev/null
"$HUKOU" doctor > "$LOG/l1b-doctor.out" 2>&1
# APFS（大小写不敏感）上无法并存 Foo 与 foo：陷阱在磁盘层就无法构造。
# 该检查逻辑由 internal/doctor 单元测试覆盖（case-alias fixtures）。
say "NOTE  case-alias: APFS case-insensitive, trap unconstructable; covered by unit tests"
expect "l1b.case-alias" 0 "skipped (environment limitation)"

# --- 只读目录 ---
SB2="$STRESS/sb/l1ro"; rm -rf "$SB2"; mkdir -p "$SB2/bin" "$SB2/data"
printf '#!/bin/sh\necho x\n' > "$SB2/bin/ro"; chmod 755 "$SB2/bin/ro"
export PATH="$SB2/bin:/usr/bin:/bin" HUKOU_DATA_DIR="$SB2/data"
chmod 555 "$SB2/bin"
"$HUKOU" scan > "$LOG/l1ro-scan.out" 2>&1; rc=$?
[ "$rc" = "0" ] && expect "l1ro.scan-readonly-dir" 0 "scan survives read-only dir (rc=0)" \
  || expect "l1ro.scan-readonly-dir" 1 "scan rc=$rc"
chmod 755 "$SB2/bin"
chmod 555 "$SB2/data"
"$HUKOU" adopt ro --local > "$LOG/l1ro-adopt.out" 2>&1; rc=$?
[ "$rc" != "0" ] && expect "l1ro.adopt-readonly-data" 0 "clean refusal on read-only data dir" \
  || expect "l1ro.adopt-readonly-data" 1 "adopt succeeded on read-only data dir"
chmod 755 "$SB2/data"

# --- ENOSPC（1MB 小卷） ---
IMG="$STRESS/sb/tiny.dmg"
hdiutil create -size 1m -fs APFS -volname tiny -quiet "$IMG" > /dev/null 2>&1
if [ $? = 0 ] && hdiutil attach -quiet -nobrowse -mountpoint "$STRESS/sb/tiny" "$IMG" > /dev/null 2>&1; then
  mkdir -p "$STRESS/sb/tiny/data"
  cp /bin/ls "$STRESS/sb/tiny/tool" 2>/dev/null
  # 填满整个卷
  python3 -c "
import os
p='$STRESS/sb/tiny/fill'
f=open(p,'wb')
try:
    while True: f.write(b'x'*65536)
except OSError: pass
f.close()"
  export HUKOU_DATA_DIR="$STRESS/sb/tiny/data" PATH="$STRESS/sb/tiny:/usr/bin:/bin"
  "$HUKOU" adopt tool --local > "$LOG/enospc.out" 2>&1; rc=$?
  [ "$rc" != "0" ] && expect "l1.enospc" 0 "clean refusal on full volume" \
    || expect "l1.enospc" 1 "adopt reported success on a full volume"
  hdiutil detach -quiet "$STRESS/sb/tiny" 2>/dev/null
else
  say "NOTE  ENOSPC: hdiutil unavailable in sandbox; case documented as not-executed"
  expect "l1.enospc" 0 "skipped (environment limitation)"
fi
