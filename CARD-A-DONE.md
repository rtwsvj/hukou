# Card A: scan 信任可观测 — 完成记录（含 Codex 否决返工）

分支 `card-a-20260715`。生产代码与注释英文；无新第三方依赖；vendor 文件
（`internal/provenance/gobin.go`、`internal/assetpick/detect.go`）未改动。

首版实现被独立核验（Codex）否决后按整改要求返工，本记录反映返工后的最终状态。

## 改动清单

### 1. 表格渲染 warnings / notes（评审发现①）
- `internal/output/output.go` — `WriteTable` 在汇总行 + `by source` 明细之后，
  先逐行渲染 `Report.Warnings`（前缀 `warning:`），再逐行渲染 `Report.Notes`
  （前缀 `note:`），均经 `sanitizeField` 清洗，风格与 `WriteExplainTable` 一致。
  **渲染循环不吞写错误：首个 `fmt.Fprintf` 错误向上返回**（整改③）。
  `Report` 新增 `Notes []string` 字段（JSON `notes,omitempty`）。
- `internal/output/explain.go` — `ExplainReport` 增补可选 `Notes` 字段
  （additive，schema_version 仍为 1）；`WriteExplainTable` 渲染 note 行，
  warning/note 循环同样上传写错误。

### 2. 读路径检查收窄到"已验证 completed-* 专项"（整改①核心）
- `internal/transaction/journal.go`
  - `CheckReadable(dataRoot) ([]string, error)`：**仅当全部 completed 条目通过
    三重验证**——名字精确 `completed-<32位小写hex>`（`isJournalID`）、Lstat 为
    真实目录非软链、目录内 COMMIT 标记与 ID 一致（`hasValidCommit`）——才返回
    非致命 note（`stale journal residue; run a mutating command or repair to
    clean (completed=N)`）。其余一律返回 `*PendingError` fail-closed：
    - `pending-*`：已发布未收敛，受保护路径可能在途；
    - `building-*`：**竞态理由已写入注释**——单点检查覆盖不了调用者的整个
      读取周期，`.building-*` 可能是另一进程活跃 Begin 的可见窗口，随时会
      发布并开始施加变更，与遗弃残留不可区分；
    - unknown 与一切畸形名字（错误 ID 形状、大写 hex、软链目录、COMMIT
      缺失/不匹配/软链），验证途中任何不确定（含 I/O 错误）均按未验证处理。
  - 新增 `isVerifiedCompletedJournal`、`isJournalID`；`CheckClean` 语义不变。
- `internal/provenance/hukou.go` — 探测器经 `CheckReadable`：已验证 completed
  残留下照常归属并记 note；pending/building/unknown/畸形一律 Load 失败降级
  （runner 摘除 + warning，已收编二进制回落 system/unknown），与卡 A 之前的
  行为一致。

### 3. warnings / notes 通道分离（整改②）
- `internal/provenance/runner.go` — `Runner.Load(env) (warnings, notes []string)`
  返回两个独立切片：warnings = 探测器加载失败（摘出链）；notes = 加载成功
  探测器经可选接口 `noteReporter` 上报的非致命提示（保留在链中）。注释明确
  两通道不得合并的理由。
- `cmd/inventory.go` — warnings 并入 `Report.Warnings`，notes 单独进
  `Report.Notes`。
- `cmd/explain.go` — `explainPath` 与 `buildExplainReport` 同步双通道。
- `cmd/helpers.go` — **`runSecurityGate` 只消费 warnings，不消费 notes**，
  注释说明：避免任意探测器的普通 note 阻断 adopt。

### 4. 写路径严格检查（保持不变）
- `Begin` 仍用 `status.NeedsRecovery()`，对四类残留全部失败关闭。未改逻辑。

### 5. 测试（整改④：真实生命周期注入）
- `internal/transaction/transaction_test.go`
  - `makeVerifiedCompletedResidue` 助手 — **真实 Begin→Apply→Commit 成功后，
    对 journal 目录（RemoveAll 需解链的条目之父目录）chmod 0500 逼 Finalize
    的 RemoveAll 失败**，制造与"COMMIT 后清理前崩溃"完全一致的残留。
  - `TestCheckReadableAllowsVerifiedCompletedResidue` — 真实 completed 残留
    产出唯一 note 且 live 已收敛到 after 态；同残留下 Begin 仍拒绝。
  - `TestCheckReadableFailsClosedOnUnverifiedResidue` — 畸形名字手工构造
    （对抗性输入）：unknown junk、伪造 building/pending 名、非 hex ID、短 ID、
    大写 hex、软链目录（目标内含匹配 COMMIT）、COMMIT 缺失、COMMIT ID 不匹配、
    COMMIT 为软链、"已验证 completed + junk 混合"，全部 fail-closed 且无 note。
  - `TestCheckReadableCleanRoots` — 干净/缺失 data root 均无 note 无错误且
    严格只读。
  - `TestBeginRefusesEveryResidueClass` — Begin 在四类残留下均拒绝（回归）。
- `internal/transaction/journal_readable_unix_test.go`（unix）
  - `TestCheckReadableFailsClosedDuringActiveBegin` — **真实 Begin 由 goroutine
    持有并被确定性卡在 capture 内**（参与路径为指向无写者 FIFO 的软链，
    sha256 追链打开 FIFO 阻塞），此时并发调 `CheckReadable` 断言 fail-closed；
    解除阻塞、Begin 发布 pending 后再断言仍 fail-closed。
  - `TestCheckReadableFailsClosedAfterBeginCrash` — **子进程真实 Begin 卡在
    FIFO 上被 SIGKILL**（清理 defer 不会运行），留下 `.building-*` 残留，
    断言 Inspect 见 building 且读路径 fail-closed。
- `internal/provenance/hukou_test.go`
  - `TestHukouDetectorRejectsPendingTransactionState` — 改为**真实 pending**
    （真实 Begin 后不 Commit/Finalize），探测器降级且无 note。
  - `TestHukouDetectorToleratesVerifiedCompletedResidue` — 真实生命周期
    completed 残留下照常归属 + 唯一 note。
  - `TestHukouDetectorDegradesOnUnknownResidue` — 手工 junk（对抗性）降级。
  - `TestRunnerSurfacesDetectorNotes` — note 走独立通道（warnings 必须为空）、
    探测器保留在链中、归属正常。
- `internal/provenance/hukou_unix_test.go`（unix）
  - `TestHukouDetectorFailsClosedDuringActiveBegin` — 真实活跃 Begin 窗口内
    探测器降级；发布 pending 后仍降级。
- `internal/output/output_test.go`
  - `TestWriteTable_rendersWarningsAndNotes` — 汇总行→warnings→notes 顺序、
    双前缀、note 不得漏入 warning 前缀。
  - `TestWriteTable_sanitizesWarningsAndNotes` — 双通道清洗。
  - `TestWriteTable_propagatesWriteErrors` — 容量受限 writer 逐步扩容，断言
    渲染循环首个写错误向上返回（整改③）。
  - JSON roundtrip 增补 `notes` 独立字段断言。

### 6. 文档
- `docs/specs/phase1-scan.md` — hukou 行改为"仅已验证 completed-* 放行"语义
  （含 building 竞态理由与畸形名单）；验收项 5 补 warnings/notes 双通道渲染
  与写错误上传。
- `docs/05-cli-reference.md` — scan 段同步收窄语义；注明 adopt 安全闸门只
  消费 warnings。

## 验收输出

见文末附录（提交后固定树上执行 `make verify` 的完整门禁输出摘录）。

## 附录：门禁输出摘录（提交 65dad1b 固定树，`make verify` exit=0）

```text
$ git rev-parse HEAD
65dad1b1b898b56118f29a2a31f195d4b2bbf34f
$ git status --porcelain | wc -l
0
$ make verify; echo exit=$?
gofmt (fmt-check)        -> clean
go mod verify            -> all modules verified
go vet ./...             -> clean

go test ./...            -> all packages ok, incl.
  ok  github.com/rtwsvj/hukou/cmd                     17.118s
  ok  github.com/rtwsvj/hukou/internal/output          0.290s
  ok  github.com/rtwsvj/hukou/internal/provenance      0.861s
  ok  github.com/rtwsvj/hukou/internal/transaction     6.023s

go test -race ./...      -> all packages ok, incl.
  ok  github.com/rtwsvj/hukou/cmd                     20.921s
  ok  github.com/rtwsvj/hukou/internal/output          1.578s
  ok  github.com/rtwsvj/hukou/internal/provenance      2.471s
  ok  github.com/rtwsvj/hukou/internal/transaction     6.480s

go test -covermode=atomic -coverprofile=coverage.out ./...
  ok  github.com/rtwsvj/hukou/cmd                  coverage: 73.9%
  ok  github.com/rtwsvj/hukou/internal/output      coverage: 54.3%
  ok  github.com/rtwsvj/hukou/internal/provenance  coverage: 89.6%
  ok  github.com/rtwsvj/hukou/internal/transaction coverage: 63.5%
  total: (statements) 73.2%

go build -trimpath -o bin/hukou ./   -> ok
bash scripts/license_check.sh        -> license and provenance checks passed
bash scripts/install_test.sh         -> install script tests passed
bash scripts/release_test.sh         -> release version validation tests passed
exit=0
```
