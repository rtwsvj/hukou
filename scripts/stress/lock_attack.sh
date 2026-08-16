#!/bin/bash
# L3-3: 锁文件攻击 — state.lock 被换成符号链接/目录/权限 000
# 期望：明确拒绝启动，绝不跟随链接、绝不覆盖、绝不清除不属于自己的东西。
set -u
. "$(dirname "$0")/lib.sh"

attack() { # attack <label> <setup-fn>
  local label=$1
  shift
  new_sandbox "lockattack"
  local data="$STRESS/sb/lockattack/data"
  mkdir -p "$data"
  "$@"  # 在 data 里布置攻击
  env $(sb_env lockattack) "$HUKOU" adopt tool --local > "$LOG/lockattack.out" 2> "$LOG/lockattack.err"
  local rc=$?
  local linked=""
  if [ -L "$data/state.lock" ]; then
    linked="$(readlink "$data/state.lock")"
  fi
  echo "rc=$rc locktype=$(if [ -L "$data/state.lock" ]; then echo symlink; elif [ -d "$data/state.lock" ]; then echo dir; else echo file; fi) target='$linked'" >> "$LOG/lockattack.meta"
  if [ "$rc" != "0" ]; then
    expect "lockattack.$label" 0 "refused (rc=$rc)"
  else
    expect "lockattack.$label" 1 "adopt succeeded despite hostile lock"
  fi
}

attack symlink-to-outside sh -c 'ln -s $STRESS/evil-target "$0/data/state.lock"' "$STRESS/sb/lockattack"
attack symlink-to-own-manifest sh -c 'echo x > "$0/data/evil"; ln -s "$0/data/evil" "$0/data/state.lock"' "$STRESS/sb/lockattack"
attack lock-is-directory sh -c 'mkdir "$0/data/state.lock"' "$STRESS/sb/lockattack"
attack lock-mode-000 sh -c 'touch "$0/data/state.lock"; chmod 000 "$0/data/state.lock"' "$STRESS/sb/lockattack"

# 证据：外面的 evil-target 绝不能被动过
if [ -s $STRESS/evil-target ] || [ -e $STRESS/evil-target ]; then
  expect "lockattack.target-untouched" 1 "outside target was created/modified"
else
  expect "lockattack.target-untouched" 0 "outside target untouched"
fi
