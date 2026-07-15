# ADR-0006：事务残留隔离与自愈

- Status: Accepted
- Date: 2026-07-15
- Extends: ADR-0003（持久化事务恢复与只读 doctor）；ADR-0005（窄版 repair）

## 背景

ADR-0003 规定 `Recover` 在取得 `state.lock` 后收敛唯一的持久化事务，并清理
building/completed 残留。早期实现对 `transactions/` 目录里出现的“未知条目”
（既非 `building-`、`pending-` 也非 `completed-` 前缀）采取零覆盖失败：在清理任何
东西之前直接返回错误。

这在实践中把一次性异常升级成硬故障：

- `cmd/helpers.go` 的 `acquireMutationLock` 依赖 `Recover`，因此一个来路不明的文件
  会让 adopt/upgrade/rollback/policy set 全部无法启动。
- `internal/repair` 的 `recover-transaction` 动作同样在预检阶段拒绝未知条目，连显式
  修复路径都被堵死。
- 唯一的出路是让用户手动 `rm`，既有误删风险，也丢失了诊断证据。

同时评审发现 live 目录可能残留孤儿临时文件 `.hukou-txn-*` / `.hukou-txn-link-*`
（进程在 rename 前崩溃）。ADR-0003 的恢复例程不清理它们，`doctor --deep` 会报
`LIVE_TRANSACTION_TEMP_PRESENT`，却没有任何动作可以处置。

## 决策

### 1. 未知条目隔离，而非楔死

`Recover` 遇到未知条目时，不再失败关闭。它把每个未知条目**原子 rename** 到
`transactions/quarantined-<原名>-<随机后缀>`，然后继续正常恢复已知目录。

- 隔离只改名，不改内容：原始数据完整保留，供诊断，**绝不删除**。
- 隔离结果记入 `Recover` 返回的恢复摘要（`RecoverSummary`）。
- `Inspect` 新增 `Quarantined` 分类；`quarantined-*` 不计入 `NeedsRecovery`
  （已隔离 = 已隔离，不再阻断 mutation 或 dry-run 检查）。
- 幂等：`quarantined-*` 被归入 `Quarantined` 而非 `Unknown`，因此恢复中途再崩溃、
  重跑时不会二次包裹，收敛到同一结果。

`recover-transaction` repair 动作随之放宽：不再拒绝未知条目，改为在 apply 阶段经由
`Recover` 隔离它们；完整事务树仍进入 state fingerprint，隔离前后任何变化都失败关闭。

### 2. 隔离区的显式清理

隔离刻意保留证据，因此删除必须是显式、指纹绑定的动作，沿用 ADR-0005 的
plan/apply 两步模型：

- `doctor` 以 Warning 级别上报每个 `quarantined-*` 条目，附路径与建议动作。
- `repair` 新增 `purge-quarantine`：plan 观测隔离区并绑定 fingerprint，apply 在
  state lock 内复核后删除全部 `quarantined-*` 条目，绝不触碰 building/pending/completed。

### 3. 孤儿 live 临时文件的清理

`repair` 新增 `clean-live-temps`：

- plan 遍历 manifest 每个条目的 live 目录，选出 `.hukou-txn-` 前缀、且 mtime 早于
  “参考时刻减一小时”的常规文件与 symlink，绑定 fingerprint（含各文件身份与 mtime）。
- 一小时下限避免误删正在进行的恢复所暂存的临时文件。
- 参考时刻取自 plan 的 `generated_at`，apply 复用同一时刻重算，保证指纹一致；隔离/清理
  集合在 plan 与 apply 之间的任何变化都失败关闭。
- `doctor` 的 `LIVE_TRANSACTION_TEMP_PRESENT` 提示语指向该动作。

## 结果

### 正面

- 一个来路不明的文件不再让所有写命令与 recover-transaction 卡死；系统自愈并保留证据。
- 隔离与两条新 repair 动作都遵守既有的“只读 plan + 指纹绑定 apply”边界，不新增隐式删除。
- doctor 对隔离条目与孤儿临时文件都给出明确、可机器读取的处置指引。

### 代价

- 隔离会累积 `quarantined-*` 条目，需要运维用 `purge-quarantine` 显式回收。
- `clean-live-temps` 的选择依赖文件系统 mtime 的可信度；一小时窗口是启发式，不是精确证明。
- 与 ADR-0003 一样，最后一次复核与 rename/remove 之间仍存在窄 TOCTOU 窗口；hukou 的
  mutation lock 只能排除 hukou 自身的并发写者。

## 非目标

- 不自动删除隔离区或任何 live 临时文件——删除永远是显式 plan/apply。
- 不解释隔离条目的来源，也不尝试把它重新分类为合法 journal。
- 不改变 ADR-0003 关于唯一持久化事务与单一 COMMIT 决策的模型。
