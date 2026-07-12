# Phase 2: manifest 包实现完成记录

## 验收结果

| 检查项 | 命令 | 结果 |
|---|---|---|
| build | `go build ./...` | ✅ PASS |
| vet | `go vet ./...` | ✅ PASS |
| test | `go test -v ./internal/manifest/` | ✅ PASS (10/10) |

## 测试覆盖

| # | 测试用例 | 覆盖场景 |
|---|---|---|
| T01 | TestLoadMissingFile | 缺失文件 → 返回空清单 SchemaVersion=1，Entries 非 nil |
| T02 | TestPutGetRoundtrip | Put 后 Get 按 Name 查回所有字段 |
| T03 | TestPutOverwrite | 同名 Entry 被覆盖，数量不增长 |
| T04 | TestRemove | 删除已有 Entry 返回 true |
| T05 | TestRemoveUnknown | 删除不存在的 Entry 返回 false |
| T06 | TestSaveLoadRoundtrip | Save + Load 完整往返数据一致 |
| T07 | TestSaveAtomicity | 目标目录只读导致 Rename 失败，原文件内容不变 |
| T08 | TestUnknownSchemaVersion | schema_version > 1 返回含 "unsupported schema_version" 的错误 |
| T09 | TestJSONFormatStable | JSON indent=2，字段有 2 空格缩进，行尾有 \n |
| T10 | TestSchemaZeroNormalised | schema_version=0 在 Load 时归一为 1 |

## 实现要点

- `Load`：文件不存在 → 空清单；文件存在但 decode 失败 / schema_version > 1 → error
- `Save`：`os.CreateTemp(dir) → json.Encoder.Encode → os.Rename` 三步原子替换；临时文件失败时 `defer os.Remove` 清理
- `Get/Put/Remove`：基于 `slices.IndexFunc` O(n) 查找，Put 覆盖追加，Remove 后收缩 slice
- 无标准库之外依赖；库代码不调用 `time.Now` / `os.Getenv`

## 文件清单

- `internal/manifest/manifest.go` — 库实现
- `internal/manifest/manifest_test.go` — 完整单测
