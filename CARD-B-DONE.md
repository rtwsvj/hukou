# 卡 B：事务残留自愈 — 完成报告

分支：`card-b-20260715`　worktree：`/Users/eric/hukou-cardb`　日期：2026-07-15

## 目标

修复评审发现的两处硬故障：
1. `transactions/` 出现未知条目时 `Recover` 在清理前直接返回错误 → `acquireMutationLock` 与
   `repair recover-transaction` 全部卡死，唯一出路是手动删文件。
2. live 目录残留孤儿临时文件 `.hukou-txn-*` / `.hukou-txn-link-*`，doctor 报
   `LIVE_TRANSACTION_TEMP_PRESENT` 却无动作可处置。

## 改动清单

### 生产代码

- `internal/transaction/journal.go`
  - 新增 `quarantinedPrefix = "quarantined-"` 常量。
  - `Status` 新增 `Quarantined []string` 分类字段；`Inspect` 把 `quarantined-*` 归入该字段并排序。
  - `NeedsRecovery` 明确**不**计入 `Quarantined`（已隔离 = 不阻断 mutation / dry-run）。
- `internal/transaction/recover.go`
  - `Recover` 签名由 `error` 改为 `(RecoverSummary, error)`。遇未知条目不再楔死：调用
    `quarantineEntry` 原子 rename 到 `transactions/quarantined-<原名>-<随机后缀>`，记入返回摘要，
    再继续正常恢复已知目录。数据只改名不删除。
  - 新增类型 `RecoverSummary` / `QuarantineRecord`，helper `quarantineEntry`（带路径逃逸守卫），
    以及导出函数 `PurgeQuarantined(dataRoot) ([]string, error)`（幂等，只删 `quarantined-*`）。
- `internal/repair/repair.go`
  - 新增动作 `ActionPurgeQuarantine` 与 `ActionCleanLiveTemps`；`validatePlan` 经
    `isSupportedAction` 放行。
  - `evaluate` 增加 `now time.Time` 形参；`Apply` 复用 `plan.GeneratedAt` 作为确定性参考时钟，
    保证 plan 与 apply 指纹一致。
  - `evaluateRecoverTransaction` 放宽：不再拒绝未知条目（改由 apply→`Recover` 隔离），整棵事务树
    仍进 fingerprint，前后变化失败关闭。
  - 新增 `evaluatePurgeQuarantine`（观测隔离区子树→指纹）、`evaluateCleanLiveTemps`
    （扫描 manifest 各 live 目录 `.hukou-txn-` 前缀、mtime 早于一小时的常规文件/symlink→指纹）、
    `applyCleanLiveTemps`（apply 时删除，带前缀守卫）。
  - `Apply` 新增两个动作分支；`recover-transaction`/`purge-quarantine` 分别经
    `statejournal.Recover` / `statejournal.PurgeQuarantined`。
- `internal/doctor/scanner.go`
  - `scanTransactions` 新增 `quarantined-*` 分支 → Warning `TRANSACTION_QUARANTINED_PRESENT`
    （附路径与 purge-quarantine 建议），置于 isDir 判定之前，避免隔离文件被误判为
    `TRANSACTION_ENTRY_INVALID`。
  - `LIVE_TRANSACTION_TEMP_PRESENT` 提示语指向 `repair --action clean-live-temps`。
- `internal/supportbundle/supportbundle.go`：`TransactionTopology` 新增 `Quarantined` 计数。
- `cmd/helpers.go`、`cmd/state_transaction.go`：适配 `Recover` 新签名（丢弃摘要）。
- `cmd/repair.go`：`--action` flag 帮助文案加入两个新动作。

### 测试

- `internal/transaction/transaction_test.go`
  - `TestRecoverQuarantinesUnknownEntriesAndRecoversKnown`：未知条目（文件+目录）隔离 +
    pending 回滚 + building/completed 清理同场景组合；数据保留、`NeedsRecovery=false`。
  - `TestQuarantineUnblocksBeginNewTransaction`：隔离前 Begin 被挡，隔离后 `NeedsRecovery=false`
    且 Begin/Apply/Commit/Finalize 正常。
  - `TestRecoverQuarantineIsIdempotent`：恢复中再崩重跑为 no-op，不二次包裹。
  - 同步适配 8 处既有 `Recover` 调用点。
- `internal/repair/repair_test.go`
  - `TestRecoverTransactionRepairQuarantinesUnknownEntry`：recover-transaction 动作不再卡死。
  - `TestPurgeQuarantinePlanAndApply` / `TestPurgeQuarantineRejectsStalePlan`。
  - `TestCleanLiveTempsPlanAndApply`（含 symlink 与新鲜临时文件保留）/ `TestCleanLiveTempsRejectsStalePlan`。
  - `TestNewRepairActionsRequirePresentState`。
- `internal/doctor/doctor_test.go`：`TestQuarantinedTransactionEntryIsWarned`。

### 文档

- 新增 `docs/adr/ADR-0006-transaction-residue-self-heal.md`（隔离策略）。
- `docs/09-decision-log.md`：ADR 索引 + 历史摘要各加一条。
- `docs/05-cli-reference.md`：doctor / repair 段落补充隔离上报与两个新动作。

## 约束遵守

- 仅在本 worktree 写文件；生产代码与注释英文；无新第三方依赖；未触碰 vendor
  `gobin.go`/`detect.go`。

## 验收输出

`make verify` 全绿（EXIT=0）：`fmt-check mod-verify vet test race coverage build license-check
install-test release-test` 全部通过，total coverage 73.1%。

```
go build -trimpath -ldflags "" -o bin/hukou ./
bash scripts/license_check.sh   -> license and provenance checks passed
bash scripts/install_test.sh    -> install script tests passed
bash scripts/release_test.sh    -> release version validation tests passed
```

真实二进制端到端验证：
- 未知文件 `mystery-file` 放入 `transactions/` 后，`repair plan --action recover-transaction`
  不再卡死；apply 后隔离为 `quarantined-mystery-file-<suffix>`，原始内容 "stray junk" 保留。
- `doctor` 对 `quarantined-*` 报 `[WARNING] TRANSACTION_QUARANTINED_PRESENT` 并给出处置建议。
- `repair plan/apply --action purge-quarantine` 后 `transactions/` 清空。
