# ADR-0006：事务残留隔离与自愈

- Status: Accepted（2026-07-15 依独立核验意见修订决策边界）
- Date: 2026-07-15
- Extends: ADR-0003（持久化事务恢复与只读 doctor）；ADR-0005（窄版 repair）

## 背景

ADR-0003 规定 `Recover` 在取得 `state.lock` 后收敛唯一的持久化事务，并清理
building/completed 残留。早期实现对 `transactions/` 目录里出现的“未知条目”
（既非 `building-`、`pending-` 也非 `completed-` 前缀）采取零覆盖失败：在清理任何
东西之前直接返回错误。

这在实践中把一次性异常升级成硬故障：

- `cmd/helpers.go` 的 `acquireMutationLock` 依赖 `Recover`，因此一个来路不明的文件
  会让 adopt/upgrade/rollback 等全部写命令无法启动。
- `internal/repair` 的 `recover-transaction` 动作同样在预检阶段拒绝未知条目，连显式
  修复路径都被堵死。
- 唯一的出路是让用户手动 `rm`，既有误删风险，也丢失了诊断证据。

同时评审发现 live 目录可能残留孤儿临时文件 `.hukou-txn-*` / `.hukou-txn-link-*`
（进程在 rename 前崩溃）。ADR-0003 的恢复例程不清理它们，`doctor --deep` 会报
`LIVE_TRANSACTION_TEMP_PRESENT`，却没有任何动作可以处置。

## 决策

### 1. 只隔离非目录未知条目；未知目录保持楔死

`Recover` 对未知条目按拓扑分流：

- **非目录条目（普通文件、符号链接等明显垃圾）**：移入隔离容器（见 §2），记入
  `Recover` 返回的恢复摘要（`RecoverSummary`），然后继续正常恢复已知目录。数据
  完整保留，绝不删除。
- **未知真实目录**：保持 fail-closed。未知目录可能是**更新版本 hukou 的 journal
  布局**，自动降级隔离等于销毁未来版本的权威恢复证据。`Recover` 返回错误并提示
  用 `hukou doctor` 检查、手动移出或升级 hukou——宁可楔死也不销毁证据。
  非目录垃圾的隔离先于该判定完成（rename-only，可逆），但任何 journal 清理与
  收敛都不会发生。

`Inspect` 新增 `Quarantined` 分类；`quarantined-*` 不计入 `NeedsRecovery`
（已隔离 = 不再阻断 mutation 或 dry-run 检查）。幂等：`quarantined-*` 归入
`Quarantined` 而非 `Unknown`，恢复中途再崩溃、重跑时不会二次包裹。

`recover-transaction` repair 动作与此完全一致：未知非目录条目不再让 plan 失败
（apply 经由 `Recover` 隔离并写入结果），未知目录使 plan 返回 `ErrNotRepairable`。

**摘要必须被消费**：`acquireMutationLock` 把隔离记录写到 stderr warning 通道，
`repair apply` 把隔离/清除/删除/跳过明细写入命令输出——恢复的副作用永不静默。

### 2. 隔离容器命名：长度受控、碰撞安全

隔离容器名为 `quarantined-<16 hex 随机>`，**不含原名**——原名（任意字节，含反斜杠、
超长名）以 `%q` 编码写入容器内的 `META` 文件；被隔离条目本体保存为容器内固定名
`payload`。这保证：

- 容器名长度恒定（28 字节），不会因原名超长触碰 NAME_MAX；
- 分配用 `mkdir`（天然 O_EXCL 语义）——命中已存在容器时**换新随机名重试**（上限
  64 次），绝不覆盖已有隔离证据；
- Unix 上反斜杠是合法文件名字符：不再把 `\` 当路径分隔符拒绝，只拒绝空名、`.`、
  `..` 与含 `/` 的名字（目录项中本不可能出现）。

### 3. 隔离区的显式清理

隔离刻意保留证据，删除必须是显式、指纹绑定的动作，沿用 ADR-0005 的 plan/apply
两步模型：

- `doctor` 以 Warning 级别上报每个 `quarantined-*` 条目，附路径与建议动作。
- `repair` 新增 `purge-quarantine`：plan 观测隔离区（含子树内容）并绑定
  fingerprint，apply 在 state lock 内复核后删除全部 `quarantined-*` 容器，绝不
  触碰 building/pending/completed。

### 4. 孤儿 live 临时文件：身份绑定 + 无活跃写入者门禁

`repair` 新增 `clean-live-temps`，安全论证由三层构成（mtime 阈值只是辅助条件，
不是唯一依据——大文件慢拷贝的活跃事务临时文件完全可能超过任何阈值）：

1. **无活跃写入者门禁**：plan 与 apply（持 mutation lock）都要求事务系统当前
   **无任何 building/pending journal**。存在即拒绝（plan 返回 `ErrNotRepairable`，
   apply 返回 `ErrStateChanged`），因为无法证明某个临时文件不属于进行中的事务。
2. **manifest 精确 live 路径永久排除**：删除候选永不包含任何 manifest 条目的
   live 路径本身（plan 与 apply 各自对照当时的 manifest 复核一次），即使该路径
   的 basename 恰好带 `.hukou-txn-` 前缀。
3. **逐项身份绑定**：plan 把每个候选的 (路径、类型、权限位、大小、mtime、
   dev/inode、SHA-256 前 16 位 hex 或 symlink 目标) 记入 plan 的 `targets` 字段；
   apply 逐项重新观测，**任一字段不符即跳过该项**（宁可留下不删）。删除集合在
   plan 时刻固化：plan 之后新出现的孤儿不会被删。

fingerprint 语义相应调整：`clean-live-temps` 的 `state_fingerprint` 由 plan 自身的
targets 与参考时钟（`generated_at`）重算校验——它拒绝被篡改或内部不一致的 plan
文档；磁盘漂移则由逐项身份复核降级为跳过，而非整单失败。候选年龄下限（1 小时，
以 `generated_at` 为参考时刻）仍然保留为额外过滤。

`doctor` 的 `LIVE_TRANSACTION_TEMP_PRESENT` 提示语指向该动作。

## 结果

### 正面

- 一个来路不明的垃圾文件不再让所有写命令与 recover-transaction 卡死；系统自愈、
  保留证据并把隔离行为报告给用户。
- 未来版本的 journal 布局不会被当前版本销毁——升级路径的恢复证据受保护。
- 隔离与两条新 repair 动作都遵守“只读 plan + 显式 apply”边界；clean-live-temps
  的每一次删除都被完整身份观测约束。

### 代价

- 隔离会累积 `quarantined-*` 容器，需要运维用 `purge-quarantine` 显式回收。
- 未知目录仍会楔死写命令——这是有意保留的安全边界，代价由手动处置承担。
- 与 ADR-0003 一样，最后一次身份复核与 remove 之间仍存在窄 TOCTOU 窗口；hukou
  的 mutation lock 只能排除 hukou 自身的并发写者。

## 非目标

- 不自动删除隔离区或任何 live 临时文件——删除永远是显式 plan/apply。
- 不解释隔离条目的来源，也不尝试把它重新分类为合法 journal。
- 不为未知目录提供自动降级通道。
- 不改变 ADR-0003 关于唯一持久化事务与单一 COMMIT 决策的模型。
