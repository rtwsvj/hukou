# Phase 1 骨架完成报告

**分支**: phase1-scan（隔离 worktree）  
**范围**: 仅 CLI / scan 遍历 / provenance 责任链骨架 / output / Makefile / 单测  
**不含**: Tier 1 各来源探测器（brew/cargo/go/npm…）— 预留给另一执行者；`gobin.go` 未改动

## 验收命令与结果

```bash
GOPATH=/tmp/gopath GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/gomod \
  go build ./... && go vet ./... && go test ./...
```

| 步骤 | 结果 |
|------|------|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过 |

测试包：

```
ok  github.com/rtwsvj/hukou/internal/output
ok  github.com/rtwsvj/hukou/internal/provenance
ok  github.com/rtwsvj/hukou/internal/scan
```

Makefile 等价：`make all`（`vet` → `test` → `build`，内置 `GOPATH/GOCACHE/GOMODCACHE ?= /tmp/...`）亦全绿。

## 实现要点

### 1. CLI（`main.go` / `cmd/`）

- `hukou scan [--json] [--unknown-only] [--source <name>] [--dir <extra-dir>...]`
- 薄编排：`scan.Walk` → `provenance.DefaultRunner` → `output.WriteTable|WriteJSON`

### 2. `internal/scan`

- `Binary` / `BinKind`（MachO \| ELF \| Script \| Other）
- 可执行：`mode&0111`（`Stat` 跟随软链）
- Kind：只读文件头 4 字节  
  - Mach-O：`0xfeedface` / `0xfeedfacf` / `0xcafebabe` / `0xbebafeca`（含字节序 swap）  
  - ELF：`0x7f454c46`  
  - Script：`#!`
- 同名去重：PATH 序首个生效，后续 `Shadowed=true` 仍保留
- 不可读：跳过并 `Skipped++`

### 3. `internal/provenance`

- `Attribution`、`Detector`、`Env`（注入 HOME/PATH/各根目录；探测器禁止 `os.Getenv`）
- `Runner`：首个非 nil `Match` 即止
- 已实现：`system`（`/bin` `/sbin` `/usr/bin` `/usr/sbin` `/usr/libexec` `/System` + Xcode CLT）、`unknown` 兜底
- **预留接入位**：`DefaultRunner` 注释标明 path-prefix / symlink / buildinfo（`gobin.go`）插在 system 之前；`gobin.go` 原样保留

### 4. `internal/output`

- 表格列：NAME PATH KIND SOURCE PACKAGE VERSION SHADOWED EVIDENCE
- 汇总行：`total` / `sources`（distinct） / `unknown` / `shadowed`（+ optional `skipped`）
- `--json`：完整 `Report`（含 `summary`）

## 文件清单

### 本次新增

| 路径 | 说明 |
|------|------|
| `main.go` | 入口 |
| `cmd/root.go` | cobra root |
| `cmd/scan.go` | scan 子命令与旗标 |
| `internal/scan/binary.go` | Binary / BinKind |
| `internal/scan/kind.go` | 文件头 Kind 判定 |
| `internal/scan/walk.go` | PATH 遍历、shadowed、跳过计数 |
| `internal/scan/walk_test.go` | fixture：软链、shadowed、非可执行、Mach-O/script 头 |
| `internal/provenance/types.go` | Attribution / Detector |
| `internal/provenance/env.go` | Env + DefaultEnv |
| `internal/provenance/runner.go` | 责任链 Runner |
| `internal/provenance/system.go` | system 探测器 |
| `internal/provenance/unknown.go` | unknown 兜底 |
| `internal/provenance/system_test.go` | system/unknown/Runner 单测 |
| `internal/output/output.go` | 表格 + JSON |
| `internal/output/output_test.go` | 汇总行 + JSON roundtrip |
| `Makefile` | build / test / vet（可覆盖 GOPATH 等） |
| `SKELETON-DONE.md` | 本文件 |

### 既有未改（或仅 go mod tidy）

| 路径 | 说明 |
|------|------|
| `internal/provenance/gobin.go` | vendor 代码，**未修改** |
| `go.mod` / `go.sum` | tidy 后 cobra 升为 direct require |
| `docs/specs/phase1-scan.md` | 规格来源 |
| `README.md` / `LICENSES/*` | 未动 |

## 后续对接提示（探测器执行者）

1. 新增 `Detector` 实现，在 `DefaultRunner()` 中 **插在 `NewSystemDetector()` 之前**。
2. go 探测器直接调用已有 `ReadGoBinary` / `GoBinDir`（`gobin.go`），勿改其核心逻辑。
3. 所有路径/HOME 经 `Env` 注入；fixture 单测构造假 `Env` 即可。
