#!/bin/bash
# L5: 网络与供应链极限（经可编程坏代理）
# 每个模式用全新沙盒；断言干净失败/成功 + 账本与活动文件零损坏 + 凭据零泄漏。
set -u
. "$(dirname "$0")/lib.sh"

if tok=$(gh auth token 2>/dev/null); then export GH_TOKEN="$tok"; fi
PROXY=127.0.0.1:18080

start_proxy() { # start_proxy <mode>
  PROXY_MODE="$1" PROXY_PORT=18080 PROXY_LOG="$LOG/proxy.log" python3 "$(dirname "$0")/proxy.py" > /dev/null 2>&1 &
  PROXY_PID=$!
  sleep 0.6
}
stop_proxy() { kill "$PROXY_PID" 2>/dev/null; wait "$PROXY_PID" 2>/dev/null; }

new_adopted() { # new_adopted <name> — 沙盒 + 收编 ripgrep@15.1.0
  new_sandbox "$1"
  env $(sb_env "$1") "$HUKOU" adopt tool BurntSushi/ripgrep --tag 15.1.0 --exe rg > /dev/null 2>&1 \
    || { expect "l5.$1.setup" 1 "adopt failed"; return 1; }
  return 0
}

check_intact() { # check_intact <name> <label> — 失败后：体检健康(或如实报漂移)、账本合法、活动文件未变
  local sb label data
  sb=$1; label=$2; data="$STRESS/sb/$sb/data"
  env $(sb_env "$sb") "$HUKOU" doctor > "$LOG/$sb.final" 2>&1
  python3 -c "import json;json.load(open('$data/manifest.json'))" 2>/dev/null || { expect "$label" 1 "manifest corrupted"; return; }
  local tag
  tag=$(python3 -c "
import json
m=json.load(open('$data/manifest.json'))
print([e['tag'] for e in m['entries'] if e['name']=='tool'][0])" 2>/dev/null)
  echo "  state: doctor_rc=$? tag=$tag"
  [ "$tag" = "15.1.0" ] && expect "$label" 0 "intact (tag=15.1.0)" \
    || expect "$label" 1 "tag moved to $tag"
}

run_mode() { # run_mode <label> <mode> <expect-upgrade-ok:0/1>
  local label=$1 mode=$2 ok=$3
  new_adopted "l5-$label" || return
  start_proxy "$mode"
  local rc
  HTTPS_PROXY="http://$PROXY" HTTP_PROXY="http://$PROXY" \
    env $(sb_env "l5-$label") "$HUKOU" upgrade tool > "$LOG/l5-$label.out" 2> "$LOG/l5-$label.err"
  rc=$?
  stop_proxy
  if [ "$ok" = "0" ]; then
    if [ "$rc" = "0" ]; then
      expect "l5.$label" 0 "upgraded through proxy (rc=0)"
    else
      expect "l5.$label" 1 "rc=$rc: $(tail -1 "$LOG/l5-$label.err" | head -c 120)"
    fi
  else
    if [ "$rc" != "0" ]; then
      expect "l5.$label" 0 "clean refusal: $(tail -1 "$LOG/l5-$label.err" | head -c 120)"
      check_intact "l5-$label" "l5.$label.intact"
    else
      expect "l5.$label" 1 "upgrade SUCCEEDED despite hostile network"
    fi
  fi
}

say "== 基线：透明转发 =="
run_mode passthrough passthrough 0

say "== 半途掐断（多个断点）=="
for n in 1024 100000 500000 1000000; do
  run_mode "cut-$n" "cut:$n" 1
done

say "== 限速 150KB/s =="
run_mode throttle150k throttle:153600 0

say "== 代理直接拒绝（CONNECT 502）=="
run_mode refuse refuse 1

say "== 代理重定向到不允许主机 =="
run_mode redirect redirect 1

say "== 断网（代理端口关闭）=="
new_adopted l5-dead || exit 1
HTTPS_PROXY="http://127.0.0.1:19999" env $(sb_env l5-dead) "$HUKOU" upgrade tool > "$LOG/l5-dead.out" 2> "$LOG/l5-dead.err"
rc=$?
[ "$rc" != "0" ] && expect "l5.dead-proxy" 0 "clean refusal (rc=$rc)" \
  || expect "l5.dead-proxy" 1 "succeeded with dead proxy"
check_intact l5-dead "l5.dead-proxy.intact"

say "== 凭据零泄漏（代理只应看到 CONNECT 行）=="
if grep -q "AUTHORIZATION" "$LOG/proxy.log"; then
  expect "l5.no-token-leak" 1 "token header reached proxy"
else
  expect "l5.no-token-leak" 0 "proxy saw only CONNECT lines"
fi
