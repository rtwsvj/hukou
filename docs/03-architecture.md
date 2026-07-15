# 架构

## 模块地图

| 模块 | 职责 | 关键边界 |
|---|---|---|
| `main.go`, `cmd/` | Cobra 命令编排 | 不把业务安全规则只留在帮助文案 |
| `internal/scan` | PATH 遍历、类型识别、shadowed | 只读文件系统，不联网 |
| `internal/provenance` | 来源责任链 | 首个匹配生效，hukou 自有登记优先 |
| `internal/output` | 表格与 JSON | 表格清理控制字符，JSON 保留完整错误 |
| `internal/manifest` | schema v2、v0/v1 migration、严格语义验证与原子保存 | future/未知字段/不完整 lineage 失败关闭；写入由命令层锁保护 |
| `internal/activation` | 不可变 activation lineage 与逻辑 rollback cursor | parent/reverts 必须指向此前 event；不读取时钟或文件系统 |
| `internal/versionpolicy` | SemVer/GitHub-latest、channel、pin 的纯选择逻辑 | 不联网、不写入，默认拒绝隐式降级 |
| `internal/updatecheck` | outdated、dry-run 与真实 upgrade 的共享检查层 | drift 先于网络；metadata only；不包含下载能力 |
| `internal/store` | original/版本目录、激活、两阶段 Prune、GC | name/tag/目标限制，同目录常规文件原子替换；保护集不使用 mtime |
| `internal/durablefs` | 文件与目录持久化原语 | file sync、same-dir rename/link/remove 后 parent sync |
| `internal/transaction` | 单全局 before/after WAL 与恢复 | PREPARED 回滚、COMMITTED 前滚、预检可见的 unknown drift 零覆盖 |
| `internal/doctor` | 只读状态审计与稳定报告 | 无写入/网络；损坏 manifest 下不猜测 orphan |
| `internal/repair` | 两类 fingerprint-bound repair plan/apply | plan 只读 hukou 状态；apply 持锁重检；无 repair-all |
| `internal/supportbundle` | 匿名 doctor/manifest/history/transaction/store 摘要 | 离线、无原始路径/repo/env/WAL payload、无自动上传 |
| `internal/ghrelease` | GitHub API 与下载 | host 白名单、token 隔离、超时、大小限制 |
| `internal/assetpick` | 平台资产选择 | 无交互、结果确定性 |
| `internal/archive` | tar.gz/zip/gz/裸文件解包 | 防路径穿越与解压炸弹；不支持容器不得退化成可激活裸文件，裸资产还需可执行格式识别 |
| `internal/verify` | checksum 解析和校验 | checksum 存在但缺条目时由调用方 fail closed |
| `internal/buildinfo` | 发布版本元数据 | 由 release ldflags 注入 |

`internal/manifest.Decode` 是 transaction command、doctor 与 repair 共用的 strict
schema boundary；任何调用者都不能先用宽松结构体解析绕过 unknown-field、policy、
checksum evidence 或 lineage 验证。

## scan 流程

```text
PATH + --dir
  -> scan.Walk
  -> provenance.DefaultRunner.Load
  -> 每个 Binary 走责任链
  -> output.Report
  -> table 或 JSON
```

责任链首位读取 hukou manifest；随后依次判断系统包管理器、版本管理器、语言包管理器、curl/local 路径、Go build info、system，最后 unknown。

## adopt 流程

```text
定位文件 -> 校验 regular/executable -> 推导或读取 repo/tag
-> 来源安全闸（探测器加载失败也拒绝）-> 冲突检查 -> SHA-256
-> dry-run: 输出 plan，零 hukou 写入
-> real: 获取写锁并从头重检 -> original + root activation + schema v2 manifest 事务提交
```

## upgrade 流程

```text
选目标 -> dry-run/outdated 使用共享 checker，或真实路径获取写锁并重检
-> 当前 SHA 闸门 -> policy-aware GitHub release metadata -> assetpick -> 有界下载
-> checksum fail-closed -> 有界解压 -> store.Put
-> 激活前再次核对当前 SHA -> 捕获旧路径/manifest 状态
-> activation.RecordUpgrade -> Activate -> history/current 同一 after-manifest
-> 失败则补偿；成功且 transaction clean 后 PlanPrune -> ApplyPrunePlan
```

网络只允许出现在 `internal/ghrelease`。真实升级前后的路径拓扑和 manifest 是一个持久化逻辑事务：取得 state lock 后先恢复旧 journal，业务资源改变前发布 PREPARED，live/manifest durable 后写 COMMIT，再进入 cleanup-only 状态。

活跃路径保持为常规文件：`Activate` 把不可变 store 版本复制到活跃目录内的完整临时文件，设置 mode、`fsync`、关闭后再 rename。这样读者始终打开旧或新 regular inode，避免 macOS/APFS 在并发替换 symlink inode 时出现瞬时 `EINVAL`。旧版本遗留 symlink 仍可被事务快照恢复，首次成功激活会迁移为常规文件。

## rollback 流程

```text
获取写锁 -> 当前 SHA 闸门 -> activation.Previous 或显式 ancestor/original
-> 捕获旧状态 -> RecordRollback/RecordRestoreOriginal
-> Activate -> 重算 active SHA -> history/current 同一 after-manifest
-> 失败补偿旧状态
```

默认 rollback 沿 active event 的 `parent_id` 前进，`A→B→C→B→A` 不读取目录
mtime。显式 `--to <tag>` 只在当前 lineage ancestors 中查找；显式 original
允许恢复不可变收编原件，但新 event 不再声明可猜测 parent。

## policy / repair / support

```text
policy show -> transaction clean check -> load/validate manifest -> effective policy report
policy set  -> read-only preflight -> transaction clean -> state lock -> recheck -> atomic manifest save

repair plan -> read-only observe + fingerprint -> write only requested 0600 plan file
repair apply -> existing root -> state lock -> identity/fingerprint/preconditions recheck -> one action

support bundle -> doctor + anonymous manifest/history/topology summaries
               -> stdout JSON, or one explicitly requested 0600 file
```

policy set 不调用自动 WAL recovery，因为 recovery 会改变 live；存在未决事务时直接
失败关闭。repair 是唯一能显式请求 transaction recovery 的新入口，但 action 数量固定
为两个。support 不读取 WAL payload、不上传，也不把 manifest name/path/repo/tag 复制到
报告中。

## 并发模型

- scan 可并发执行，因为不写数据。
- adopt/upgrade/rollback、policy set 与 repair apply 对同一 data root 使用进程级锁。
- explain/outdated/policy show/doctor/support collect/adopt dry-run 不获得写锁；repair plan 只写用户显式指定的 plan 文件，建议放在 data root 外以免改变自己的 fingerprint。
- manifest 内部数据结构不承诺跨进程并发；命令层负责串行化。
- release 构建使用固定 commit、固定 Go 版本和固定归档时间戳。

## 崩溃恢复状态机

```text
.building-* --payload + intent durable--> pending-* (PREPARED)
PREPARED --apply live/original + manifest--> COMMIT durable
COMMIT --rename--> completed-* --cleanup--> removed

Recover(PREPARED)  -> all resources converge to before
Recover(COMMITTED) -> all resources converge to after
preflight drift    -> no writes, keep pending evidence
```

`upgrade --dry-run`、list、scan 的 hukou detector 和 doctor 只执行 transaction inventory，不自动恢复；普通写命令在锁内恢复。

恢复会先分类全部参与者，并在每次替换/删除前再次复核当前状态。该机制保护 hukou 协作写入和检查时已可见的外部漂移；不合作的外部进程若恰在最后一次复核与 rename/remove 系统调用之间改写同一路径，仍存在不可消除的窄 TOCTOU 窗口。

## doctor 流程

```text
只读 lstat/read/hash
-> manifest syntax + semantic audit
-> live/store/backup/transaction cross-check
-> orphan 或 UNCLASSIFIABLE 分类
-> stable Report
-> text 或 JSON renderer
```
