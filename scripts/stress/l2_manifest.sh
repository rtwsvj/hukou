#!/bin/bash
# L2-A: 畸形账本毒杀 doctor/list/export/import
# 每种毒账本：命令不崩溃、--json 永远合法、原始毒文件一个字节都不被动、备份不被破坏。
set -u
. "$(dirname "$0")/lib.sh"

SB="$STRESS/sb/l2"; rm -rf "$SB"; mkdir -p "$SB/data"
export HUKOU_DATA_DIR="$SB/data" PATH="/usr/bin:/bin"
python3 - "$SB/data" <<'PY'
import json, os, sys, struct
data = sys.argv[1]
def put(name, content):
    p = os.path.join(data, name)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    mode = 'wb' if isinstance(content, bytes) else 'w'
    with open(p, mode) as f:
        f.write(content)
valid = {
  "schema_version": 2,
  "retention": {"rollback_depth": 2},
  "entries": [{
    "name": "tool", "path": "/tmp/x/tool", "repo": "o/r", "tag": "v1.0.0",
    "sha256": "ab"*32, "upstream": "", "adopted_at": "2026-08-01T00:00:00Z",
    "updated_at": "2026-08-01T00:00:00Z", "checksum_verified": False,
    "active_activation_id": "a1", "activations": [
      {"id":"a1","operation":"adopt","tag":"v1.0.0","sha256":"ab"*32,"activated_at":"2026-08-01T00:00:00Z"}],
    "update_policy": {"mode":"semver","channel":"stable"}
  }]
}
cases = {}
# 语法/编码层
cases["dup-key"]        = b'{"schema_version":2,"schema_version":1,"retention":{"rollback_depth":2},"entries":[]}'
cases["nan"]            = b'{"schema_version": NaN, "retention":{"rollback_depth":2},"entries":[]}'
cases["infinity"]       = b'{"schema_version": Infinity, "retention":{"rollback_depth":2},"entries":[]}'
cases["deep-nest"]      = b'[' * 1000 + b']' * 1000
cases["huge"]           = b'{"schema_version":2,"retention":{"rollback_depth":2},"entries":[]}' + b' ' * (5*1024*1024)
cases["non-utf8"]       = b'{"schema_version":2,\x00\xff\xfe,"retention":{"rollback_depth":2}}'
cases["bom"]            = b'\xef\xbb\xbf' + json.dumps(valid).encode()
cases["truncated"]      = json.dumps(valid).encode()[:100]
cases["trailing"]       = json.dumps(valid).encode() + b' {}'
cases["not-json"]       = b'just text'
# schema 层
for sv in [0,1,3,99,-1,"x",None]:
    d = dict(valid); d["schema_version"] = sv
    cases[f"schema-{sv}"] = json.dumps(d).encode()
# 字段类型/值层
def mutated(**kw):
    d = json.loads(json.dumps(valid))
    for path, val in kw.items():
        cur = d
        parts = path.split('.')
        for p in parts[:-1]: cur = cur[p]
        cur[parts[-1]] = val
    return json.dumps(d).encode()
cases["retention-neg"]   = mutated(**{"retention.rollback_depth": -1})
cases["retention-huge"]  = mutated(**{"retention.rollback_depth": 10**9})
cases["time-junk"]       = mutated(**{"entries":[dict(valid["entries"][0], adopted_at="not-a-time")]})
cases["time-negative"]   = mutated(**{"entries":[dict(valid["entries"][0], adopted_at="-1000000000")]})
cases["sha-short"]       = mutated(**{"entries":[dict(valid["entries"][0], sha256="abc")]})
cases["sha-nonhex"]      = mutated(**{"entries":[dict(valid["entries"][0], sha256="z"*64)]})
cases["sha-upper"]       = mutated(**{"entries":[dict(valid["entries"][0], sha256="AB"*32)]})
cases["tag-dot"]         = mutated(**{"entries":[dict(valid["entries"][0], tag=".")]})
cases["tag-traversal"]   = mutated(**{"entries":[dict(valid["entries"][0], tag="../x")]})
cases["tag-original"]    = mutated(**{"entries":[dict(valid["entries"][0], tag="original")]})
cases["dup-entry-name"]  = mutated(**{"entries":[valid["entries"][0], valid["entries"][0]]})
cases["name-path-mismatch"] = mutated(**{"entries":[dict(valid["entries"][0], path="/tmp/x/other")]})
cases["parent-forward"]  = mutated(**{"entries":[dict(valid["entries"][0], activations=[{"id":"a1","operation":"adopt","tag":"v1.0.0","sha256":"ab"*32,"activated_at":"2026-08-01T00:00:00Z","parent_id":"a2"}])]})
cases["cycle"]           = mutated(**{"entries":[dict(valid["entries"][0], activations=[{"id":"a1","operation":"adopt","tag":"v1.0.0","sha256":"ab"*32,"activated_at":"2026-08-01T00:00:00Z","parent_id":"a1"}])]})
cases["checksum-no-asset"] = mutated(**{"entries":[dict(valid["entries"][0], checksum_verified=True)]})
cases["unknown-top"]     = json.dumps(dict(valid, evil=1)).encode()
cases["unknown-entry"]   = mutated(**{"entries":[dict(valid["entries"][0], evil=1)]})
cases["smuggle-v2-field"] = b'{"schema_version":1,"entries":[{"name":"tool","path":"/tmp/x/tool","repo":"o/r","tag":"v1.0.0","sha256":"' + b'ab'*32 + b'","archive_exe":"x"}]}'

for name, content in cases.items():
    put(f"case-{name}/manifest.json", content)
    # 放一个合法备份，验证修复路径可用
    put(f"case-{name}/manifest.json.bak", json.dumps(valid))
print(len(cases), "cases written")
PY

run_case() { # run_case <name>
  local name dir before_manifest before_bak rc doc
  name=$1
  dir="$SB/data/case-$name"
  before_manifest=$(shasum -a 256 "$dir/manifest.json" | cut -d' ' -f1)
  before_bak=$(shasum -a 256 "$dir/manifest.json.bak" | cut -d' ' -f1)
  HUKOU_DATA_DIR="$dir" "$HUKOU" doctor > "$LOG/l2-$name.doctor" 2>&1; rc=$?
  HUKOU_DATA_DIR="$dir" "$HUKOU" doctor --json > "$LOG/l2-$name.json" 2>/dev/null
  python3 -c "import json;json.load(open('$LOG/l2-$name.json'))" 2>/dev/null && doc=0 || doc=1
  HUKOU_DATA_DIR="$dir" "$HUKOU" list > /dev/null 2>&1
  HUKOU_DATA_DIR="$dir" "$HUKOU" export > /dev/null 2>&1
  local after_manifest after_bak
  after_manifest=$(shasum -a 256 "$dir/manifest.json" | cut -d' ' -f1)
  after_bak=$(shasum -a 256 "$dir/manifest.json.bak" | cut -d' ' -f1)
  local first
  first=$(grep -m1 -oE '\[(ERROR|WARNING)\][^:]*' "$LOG/l2-$name.doctor" | head -c 80)
  if [ "$rc" -le 1 ] && [ "$doc" = "0" ] && [ "$before_manifest" = "$after_manifest" ] && [ "$before_bak" = "$after_bak" ]; then
    expect "l2.manifest.$name" 0 "no crash, json ok, files untouched ($first)"
  else
    expect "l2.manifest.$name" 1 "rc=$rc json=$doc manifestChanged=$([ "$before_manifest" != "$after_manifest" ] && echo yes || echo no) bakChanged=$([ "$before_bak" != "$after_bak" ] && echo yes || echo no)"
  fi
}

for dir in "$SB/data"/case-*; do
  run_case "$(basename "$dir" | sed 's/^case-//')"
done
