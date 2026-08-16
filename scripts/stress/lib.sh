#!/bin/bash
# hukou 压力测试台 - 共用库
# 攻击原则：只打沙盒(/tmp/hukou-stress)，绝不触碰真实系统与真实账本。
set -u

# 可配置：HUKOU_BIN 指向被测二进制，STRESS_ROOT 指向攻击沙盒根。
# 默认在临时目录开沙盒，跑完可整目录删除；结果留 results.tsv 与各层日志。
HUKOU="${HUKOU_BIN:-$(cd "$(dirname "$0")/../.." && pwd)/bin/hukou}"
STRESS="${STRESS_ROOT:-${STRESS:-${TMPDIR:-/tmp}/hukou-stress-$$}}"
LOG="$STRESS/log"
export STRESS LOG
mkdir -p "$LOG"

now() { date "+%H:%M:%S"; }
say()  { echo "[$(now)] $*"; }

# new_sandbox <name> — 干净沙盒：一个假 tool + 空账本
new_sandbox() {
  local name=$1
  rm -rf "$STRESS/sb/$name"
  mkdir -p "$STRESS/sb/$name/bin" "$STRESS/sb/$name/data"
  cp /bin/echo "$STRESS/sb/$name/bin/tool"
  chmod 755 "$STRESS/sb/$name/bin/tool"
}

sb_env() { # sb_env <name> — 输出该沙盒的环境前缀
  echo "PATH=$STRESS/sb/$1/bin:/usr/bin:/bin HUKOU_DATA_DIR=$STRESS/sb/$1/data"
}

# expect <label> <ok:0/1> <detail> — 记录一行结果
expect() {
  local label=$1 ok=$2 detail=$3
  if [ "$ok" = "0" ]; then
    say "PASS  $label  | $detail"
    echo "PASS	$label	$detail" >> "$LOG/results.tsv"
  else
    say "FAIL  $label  | $detail"
    echo "FAIL	$label	$detail" >> "$LOG/results.tsv"
  fi
}
