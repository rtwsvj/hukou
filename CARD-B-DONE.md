# 卡 B：事务残留自愈 — 完成报告（返工版）

分支：`card-b-20260715`　worktree：`/Users/eric/hukou-cardb`　日期：2026-07-15

- 首轮实现：`c32b762`（被独立核验拒绝：3 个 P1 + 配套条件）
- 返工提交：`585c17b` `feat(card-b): safe residue quarantine + guarded repair actions`
- `make verify` 在 `585c17b` 上重跑：**EXIT=0 全绿**（见文末）

## 核验意见逐条处置

### P1-1【破坏性缺陷】clean-live-temps 可能删除合法 live 文件 → 已修复

- ① **manifest 精确 live 路径永久排除**：`registeredLivePaths`
  （`internal/repair/repair.go:582`）在 plan（`repair.go:593` 起的
  `evaluateCleanLiveTemps`，候选枚举时排除）与 apply
  （`applyCleanLiveTemps`，`repair.go:700`，对照 apply 时刻重新加载的 manifest
  再排除一次）双重生效，即使 live 路径 basename 恰带 `.hukou-txn-` 前缀。
- ② **逐项强身份绑定**：新类型 `LiveTempTarget`（`repair.go:106`）把每个候选的
  (路径, kind, 权限位, size, mtime[RFC3339Nano], dev/inode[unix 实现见
  `internal/repair/fileid_unix.go`], SHA-256 前 16 位 hex 或 symlink 目标) 写入
  Plan 新字段 `targets`；apply 用 `liveTempTargetMatches`（`repair.go:738`）
  逐项复核，**任一字段不符即跳过该项**（记入 `Result.SkippedLiveTemps`），
  绝不删除 plan 未精确描述的资源。删除集合在 plan 时刻固化，plan 后新出现的
  文件不在集合内。plan 文档完整性由 `cleanLiveTempsFingerprint`
  （`repair.go:564`）从 plan 自身 targets + `generated_at` 重算校验
  （`Apply` 的专用分支，`repair.go:253-272`）。
- ③ **放弃 1 小时 mtime 作为唯一依据**：`evaluateCleanLiveTemps` 开头
  （`repair.go:597-604`）与 `applyCleanLiveTemps` 开头（`repair.go:701-707`，
  持 mutation lock 下）都要求**无任何 building/pending journal**（无活跃写入者），
  违反分别报 `ErrNotRepairable` / `ErrStateChanged`；1 小时 mtime 下限降级为
  辅助过滤（`liveTempMinAge` 注释，`repair.go:52-58`）。

### P1-2【恢复语义】不把可能权威的未知目录自动降级 → 已修复

- `Recover`（`internal/transaction/recover.go:78`）按拓扑分流：非目录条目
  （普通文件、符号链接等垃圾）隔离并记入摘要；**未知真实目录保持 fail-closed**
  （`recover.go:92-112`），报错并提示 `hukou doctor` 检查、手动移出或升级
  hukou，任何 journal 清理与收敛都不会发生。
- `evaluateRecoverTransaction` 同步（`repair.go:334-342`）：未知目录使 plan 返回
  `ErrNotRepairable`。
- ADR-0006 决策边界已改写（`docs/adr/ADR-0006-transaction-residue-self-heal.md`
  §1）。

### P1-3【命名与提交】隔离命名碰撞安全 → 已修复

- 容器名 `quarantined-<16hex>`（不含原名，长度恒定 28 字节）；原名以 `%q` 编码
  写入容器内 `META` 文件，条目本体保存为固定名 `payload`
  （`quarantineEntry`，`recover.go:158-206`；常量 `recover.go:44-52`）。
- 分配用 `os.Mkdir`（天然 exclusive-create 语义），EEXIST 时换新随机名重试
  （上限 64 次），绝不覆盖既有隔离证据；随机源经 `quarantineNameSuffix` seam
  （`recover.go:56`）可测。
- 已去掉把 `\` 当路径分隔符拒绝的检查：只拒绝空名、`.`、`..`、含 `/` 的名字
  （`recover.go:159-161`）；名为 `stray\entry` 的条目现可正常隔离。

### 第 4 条：RecoverSummary 在生产调用点消费 → 已完成

- `acquireMutationLock(stderr)`（`cmd/helpers.go:40`）经
  `reportRecoverSummary`（`cmd/helpers.go:58`）把每条隔离记录写入 stderr
  warning；三个调用点（`cmd/adopt.go:89`、`cmd/upgrade.go:61`、
  `cmd/rollback.go:46`）已传入 stderr。
- `commitStateTransaction(tx, stderr)`（`cmd/state_transaction.go:77`）的
  commit 失败恢复路径同样上报。
- `repair.Apply` 返回 `Result`（`repair.go:121`：Quarantined/PurgedQuarantine/
  RemovedLiveTemps/SkippedLiveTemps）；`doRepairApply`（`cmd/repair.go:80-97`）
  把明细逐条打印。

### 第 5 条：测试补齐 → 已完成

- **真实 crash 注入**：`TestSubprocessSIGKILLRecoveryWithStrayEntry`
  （`internal/transaction/transaction_test.go:738`）——真实 Begin（committed 分支
  含真实 Commit）后子进程 SIGKILL，掉入垃圾后 Recover 隔离 + 按 stage 正确
  回滚/前滚。
- **名称边界**：`TestQuarantineNameBoundaries`（`transaction_test.go:652`，
  200 字符长名 + 反斜杠名）；`TestQuarantineCollisionRetriesWithoutOverwriting`
  （`transaction_test.go:688`，已存在同名隔离容器 → 重试新名、绝不覆盖）。
- **未知目录 fail-closed**：`TestRecoverFailsClosedOnUnknownDirectory`
  （`transaction_test.go:518`）、`TestRecoverTransactionPlanFailsClosedOnUnknownDirectory`
  （`internal/repair/repair_test.go:389`）。
- **plan-apply 间 TOCTOU**：`TestCleanLiveTempsSkipsMutatedTargetWithoutDeleting`
  （`repair_test.go:559`，apply 前替换候选 → 断言跳过且文件保留，其余照常删除）；
  `TestCleanLiveTempsLeavesUnplannedOrphanAlone`（`repair_test.go:604`）。
- **live 精确路径永不被删**：`TestCleanLiveTempsNeverRemovesRegisteredLivePath`
  （`repair_test.go:643`，live 文件本身就叫 `.hukou-txn-livetool` 且已老化）。
- **无活跃写入者门禁**：`TestCleanLiveTempsRequiresNoActiveJournals`
  （`repair_test.go:684`，plan 门禁 + apply 门禁双向）。
- **跨 data-root**：`TestNewActionsRejectCrossDataRoot`（`repair_test.go:759`，
  purge 与 clean 两动作均拒绝）。
- **plan 完整性**：`TestCleanLiveTempsPlanRoundTripAndTamperRejection`
  （`repair_test.go:721`，round-trip + 篡改 targets 拒绝）。
- **CLI 级 e2e 全链**：`TestRepairCLIQuarantineChain`
  （`cmd/repair_support_test.go:105`，doctor 报告→recover plan/apply→doctor 报
  隔离→purge plan/apply→doctor 清净）与 `TestRepairCLICleanLiveTempsChain`
  （`cmd/repair_support_test.go:163`，doctor --deep 指引→plan/apply→孤儿删除、
  live 完好→doctor 清净）。

## 第三轮精确修复（2026-07-16）

### P1-1【clean-live-temps 删除锚定】→ 已修复

- **物理目录身份（dev+inode）去重与 live 排除**：新增
  `collectLiveIdentities`（`internal/repair/repair.go:609`）把 manifest live
  目录与 live 文件本身都解析为 `(dev,inode)` 集合；plan 只扫描物理上可信的
  live 父目录，候选若命中 live 文件身份即被排除，不再依赖路径字符串。
- **fd-anchored 删除**：`applyCleanLiveTemps`
  （`internal/repair/repair.go:776`）对每个通过重证明的候选调用
  `os.OpenRoot(parent)` 获得目录 fd，再经 `root.Remove(basename)` 删除，消除
  路径解析层的替换竞态；`testBeforeCleanLiveTempRemove`
  （`repair.go:673`）为测试提供确定性注入点。
- **测试**：`TestCleanLiveTempsRejectsSymlinkLiveDirectory`
  （`internal/repair/repair_test.go:733`，经 symlink 到达的 live 目录不被信任）
  与 `TestCleanLiveTempsFdAnchoredRemovalRejectsSymlinkSwap`
  （`repair_test.go:754`，重证明后把目标换为指向 sentinel 的 symlink，fd
  锚定删除不跟随 symlink）。

### P1-2【apply 重证明】→ 已修复

- `applyCleanLiveTemps` 不再把 plan 文档自证当作授权依据：每个 target 都经
  `revalidateLiveTempTarget`（`internal/repair/repair.go:833`）从当前 manifest
  与目录现实重新证明——① 父目录仍在 manifest 的 live 目录身份集合内；② 路径
  不是任何 manifest live 路径本体；③ 仍带 `.hukou-txn-*` 前缀；④ kind/权限/
  size/mtime/dev/inode/SHA-256 前缀（或 symlink 目标）七元组与 plan 一致；⑤
  mtime 仍低于 `generated_at` 回溯的 1 小时 cutoff；⑥ building/pending 门禁在
  apply 开头重新检查。任一条件不满足即跳过并记入 `Result.SkippedLiveTemps`。
- plan 的 `state_fingerprint` 保留，但注释明确其仅为防意外篡改提示
  （`internal/repair/repair.go:253-259`），不替代逐项目录现实重证明。
- **测试**：`TestCleanLiveTempsApplySkipsWhenParentNotLiveDir`
  （`repair_test.go:803`）、`TestCleanLiveTempsApplySkipsWhenTargetBecomesLivePath`
  （`repair_test.go:843`）、`TestCleanLiveTempsApplyRevalidatesIdentity`
  （`repair_test.go:898`，覆盖 mode/size/mtime/sha/age/symlink-target 各分支）。

### P1-3【隔离容器识别收紧】→ 已修复

- 新增 `IsValidQuarantineContainer`（`internal/transaction/journal.go:176`）：
  名字必须精确为 `quarantined-` + 16 位小写 hex；`Lstat` 必须是真实目录而非
  symlink；内部必须只含 `META`（常规文件）与 `payload` 两个条目。新增
  `quarantinedHexLen` 常量（`journal.go:30`）与导出的 `QuarantinedPrefix`
  （`journal.go:168`）。
- `Inspect`（`journal.go:225`）把验证失败的 `quarantined-*` 归入 `Unknown`；
  `PurgeQuarantined`（`internal/transaction/recover.go:214`）在删除前再次用同一
  函数防御性校验，只删通过验证的容器。
- `doctor` 扫描器（`internal/doctor/scanner.go:640-643`）对有效容器保持
  `TRANSACTION_QUARANTINED_PRESENT` Warning，对无效容器报
  `TRANSACTION_QUARANTINED_INVALID` Error，实现 fail-closed。
- **测试**：`TestInspectRejectsInvalidQuarantineContainers`
  （`internal/transaction/transaction_test.go:789`）、
  `TestRecoverQuarantinesInvalidQuarantineFileButFailsClosedOnInvalidDirectory`
  （`transaction_test.go:855`）、`TestPurgeQuarantineRemovesOnlyValidContainers`
  （`transaction_test.go:890`）、`TestValidQuarantineContainerIsWarned`
  （`internal/doctor/doctor_test.go:304`）、
  `TestInvalidQuarantineContainerIsRejected`
  （`doctor_test.go:334`）。

## 其余关键位置

- `internal/transaction/journal.go:28`（`quarantinedPrefix`）、`:134`
  （`Status.Quarantined`）、`:140`（`NeedsRecovery` 不计隔离）、`:193`
  （`Inspect` 分类）
- `internal/transaction/recover.go:214`（`PurgeQuarantined`）
- `internal/repair/repair.go:42-43`（动作常量）、`:217`（`Apply` 返回 Result）、
  `:514`（`evaluatePurgeQuarantine`）、`:850`（`validatePlanTargets`）
- `internal/repair/fileid_unix.go` / `fileid_other.go`（dev/inode 观测，
  build-tag 隔离）
- `internal/doctor/scanner.go:639`（`TRANSACTION_QUARANTINED_PRESENT` Warning）、
  `:697`（`LIVE_TRANSACTION_TEMP_PRESENT` 指向 clean-live-temps）
- `internal/supportbundle/supportbundle.go`（`TransactionTopology.Quarantined`
  计数）
- 文档：`docs/adr/ADR-0006-transaction-residue-self-heal.md`（决策边界重写）、
  `docs/05-cli-reference.md`（doctor/repair 段落）、`docs/09-decision-log.md`

## 约束遵守

仅在本 worktree 写文件；生产代码与注释英文；无新第三方依赖；未触碰 vendor
`gobin.go`/`detect.go`。

## 验收输出（在提交 585c17b 上）

`make verify`：**EXIT=0**，全目标通过：

```
fmt-check   PASS（gofmt -l 无输出）
mod-verify  PASS
vet         PASS
test        PASS（go test ./... 全 ok）
race        PASS（go test -race ./... 全 ok）
coverage    total: (statements) 73.1%
build       go build -trimpath -o bin/hukou ./
license     license and provenance checks passed
install     install script tests passed
release     release version validation tests passed
```

真实二进制端到端复核：

- `transactions/` 同时存在垃圾文件与未知目录时：`repair plan --action
  recover-transaction` 报
  `transaction root contains unknown directory "future-journal"; it may be a
  journal from a newer hukou and must be inspected manually`（fail-closed）；
  移走目录后 plan/apply 输出
  `Quarantined unknown transaction entry "stray-file" as
  transactions/quarantined-2ca7ea809d8e2924`，容器内 `META` 记录
  `original_name="stray-file"`，`payload` 内容原样保留。
- `doctor` 对隔离容器报 `[WARNING] TRANSACTION_QUARANTINED_PRESENT`；
  `purge-quarantine` plan/apply 后 `transactions/` 清空。
