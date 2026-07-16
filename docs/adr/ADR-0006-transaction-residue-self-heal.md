# ADR-0006：安全隔离（仅移动）+ 未知目录 fail-closed + 只读诊断，不做自动删除

- Status: Accepted（2026-07-16 依产品决策缩窄范围修订）
- Date: 2026-07-16
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

因此卡 B 的原始诉求是：**`transactions/` 里的未知非目录条目不再楔死写命令**。

在后续实现中曾尝试通过 `repair purge-quarantine` 与 `repair clean-live-temps`
两个动作提供自动删除能力，但二者均涉及对用户文件的破坏性删除。经过多轮独立核验，
删除动作的安全论证始终无法稳定通过评审，反复出现“可能删除合法数据”或
“身份绑定/TOCTOU 边界不达标”的问题。产品决策决定：**彻底移除所有破坏性删除动作**，
把卡 B 拉回原始诉求，只保留安全的移动隔离与只读诊断。

## 决策

### 1. 只隔离非目录未知条目；未知目录保持楔死；不做任何自动删除

`Recover` 对未知条目按拓扑分流：

- **非目录条目（普通文件、符号链接等明显垃圾）**：移入隔离容器（见 §2），记入
  `Recover` 返回的恢复摘要（`RecoverSummary`），然后继续正常恢复已知目录。数据
  完整保留，**只 rename 不删除**。
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

**本 ADR 不再提供任何自动删除隔离区或 live 临时文件的动作。** 删除必须交给用户
在确认无价值后手动执行。

**摘要必须被消费**：`acquireMutationLock` 把隔离记录写到 stderr warning 通道，
`repair apply` 把隔离明细写入命令输出——恢复的副作用永不静默。

### 2. 隔离容器命名：长度受控、碰撞安全

隔离容器名为 `quarantined-<16 hex 随机>`，**不含原名**——原名（任意字节，含反斜杠、
超长名）以 `%q` 编码写入容器内的 `META` 文件；被隔离条目本体保存为容器内固定名
`payload`。这保证：

- 容器名长度恒定（28 字节），不会因原名超长触碰 NAME_MAX；
- 分配用 `mkdir`（天然 O_EXCL 语义）——命中已存在容器时**换新随机名重试**（上限
  64 次），绝不覆盖已有隔离证据；
- Unix 上反斜杠是合法文件名字符：不再把 `\` 当路径分隔符拒绝，只拒绝空名、`.`、
  `..` 与含 `/` 的名字（目录项中本不可能出现）。

### 3. 隔离容器识别：只用于诊断，不驱动删除

`IsValidQuarantineContainer` 对 `quarantined-*` 容器做精确布局校验：

- 名字必须精确为 `quarantined-` + 16 位小写 hex；
- 容器本身必须是真实目录（非软链）；
- 内部必须只含 `META` 常规文件与 `payload` 非目录条目，无其他名字；
- `META` 为软链、`payload` 为目录等畸形布局一律判为非法容器。

校验结果仅用于 `Inspect`/`doctor` 分类展示：合法容器归入 `Quarantined` 并以
Warning 提示用户手动检查/删除；非法容器归入 `Unknown` 并以 Error 提示手动处置。
任何删除决策都不由程序自动做出。

### 4. 孤儿 live 临时文件：只读上报，用户手动清理

`doctor --deep` 继续报告已登记 live parent 目录下的 `.hukou-txn-*` /
`.hukou-txn-link-*` 临时文件名（`LIVE_TRANSACTION_TEMP_PRESENT`），但仅作为只读
诊断：提示用户“确认无活跃事务后手动删除”。不再提供任何自动清理命令或动作。

### 5. repair 动作范围

`repair` 仅保留卡 B 之前已有的两个动作：

- `recover-transaction`：收敛未决 journal；未知非目录条目隔离后继续，未知目录
  fail-closed。
- `restore-manifest-backup`：在主 manifest 缺失/无效、backup 语义有效、transaction
  clean 且所有 live SHA 匹配时恢复备份。

`purge-quarantine` 与 `clean-live-temps` 已移除。

## 结果

### 正面

- 一个来路不明的垃圾文件不再让所有写命令与 recover-transaction 卡死；系统自愈、
  保留证据并把隔离行为报告给用户。
- 未来版本的 journal 布局不会被当前版本销毁——升级路径的恢复证据受保护。
- 隔离动作只 rename 不删除，不存在误删用户数据的风险。
- 实现范围收窄后，代码与测试更容易验证，安全边界清晰。

### 代价

- 隔离会累积 `quarantined-*` 容器，需要运维在确认无价值后手动删除。
- 未知目录仍会楔死写命令——这是有意保留的安全边界，代价由手动处置承担。
- live 目录的孤儿临时文件需要用户自行判断并手动删除。

## 非目标

- **不自动删除**隔离区、live 临时文件或任何其他用户文件。
- 不解释隔离条目的来源，也不尝试把它重新分类为合法 journal。
- 不为未知目录提供自动降级通道。
- 不改变 ADR-0003 关于唯一持久化事务与单一 COMMIT 决策的模型。

## 历史记录

- 2026-07-15：初稿接受，包含 `purge-quarantine` 与 `clean-live-temps` 两个删除动作。
- 2026-07-16：产品决策缩窄范围，移除全部破坏性删除动作，本 ADR 改写为当前版本。
