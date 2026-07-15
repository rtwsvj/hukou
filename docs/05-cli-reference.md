# CLI Reference

本页描述用户可见接口。最终参数约束以当前 Cobra 命令为准；修改命令时必须同步本页和根 README。

## `hukou version`

输出发布版本、commit 与构建时间。本地未注入构建显示 `devel`/`unknown`，release archive 必须显示 tag 与固定提交。

副作用：无。

## `hukou scan`

```text
hukou scan [--json] [--unknown-only] [--source <name>] [--dir <dir>...]
```

- `--json`：完整 JSON report。
- `--unknown-only`：只显示 `source=unknown`。
- `--source`：按来源过滤，不区分大小写。
- `--dir`：追加扫描目录，可重复。

表格输出在汇总行后先逐行渲染 warning（前缀 `warning:`，探测器降级），再逐行渲染 note（前缀 `note:`，非致命提示）。hukou 事务残留的读路径语义：仅**已验证的 `completed-*`**（名字精确为 `completed-<32位小写hex>`、真实目录非软链、COMMIT 标记与 ID 一致）不降级，只产出 note `stale journal residue; run a mutating command or repair to clean`；`pending-*`、`building-*`（可能是另一进程活跃 Begin 的窗口，单点检查覆盖不了读取周期）、unknown 及畸形名字一律使 hukou 探测器降级——摘出链、已收编二进制回落 `system`/`unknown` 并写 warning。`--json` 中 `warnings` 与 `notes` 为独立字段。

副作用：无；不联网、不写用户目录。读路径事务检查为 `transaction.CheckReadable`（仅放行已验证 completed 残留）；写路径（`Begin`）仍对全部残留失败关闭。adopt 的安全闸门只消费 warnings，不消费 notes——普通探测器提示不会阻断 adopt。

## `hukou explain`（V0.3 分支，未发布）

```text
hukou explain <name|path> [--json]
```

- 裸名字解释 PATH 中所有同名候选，active match 与 shadowed match 都保留。
- 显式路径只解释该 regular executable。
- 报告 path/real path/kind/source/package/version/confidence/evidence/shadowed；
  detector 加载问题进入 warnings，不伪造成 exact attribution。
- `--json` 输出独立 `schema_version=1` report。

副作用：无；只读本地文件系统，不联网。

## `hukou adopt`

```text
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force]
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force] --dry-run [--json]
```

- 裸名字经 PATH 查找，路径参数直接定位。
- Go 二进制可从 build info 推导 `owner/repo`。
- 其他二进制必须给 repo 或使用 `--local`。
- `--force` 允许越过其他管理器所有权闸门，不越过文件与路径安全检查。
- 带 setuid/setgid/sticky 等特权或特殊权限位的源文件会被拒绝。
- `--tag` 必须是单一路径组件；`original`（含大小写别名）是不可变备份保留名，不能作为 adopt tag。
- V0.3 分支的 `--dry-run` 检查文件、来源、repo/tag、冲突和 SHA，并输出将要写入的计划；不创建 data root、lock、store、manifest、backup 或 transaction，也不 recovery/GC。
- `--json` 仅允许和 `--dry-run` 同时使用，输出 `schema_version=1`。

真实收编副作用：获取 `state.lock`、恢复旧 transaction、创建 data root、持久化 transaction、备份 original、创建 root activation、保存 schema v2 manifest。真实路径不会信任旧 dry-run，而是在锁内重检。H1 后同名条目不得静默覆盖。activation 会再次验证 safe tag，并拒绝让 history 中同一个 tag 绑定不同 SHA。original 备份只保留字节与 rwx 权限位，不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。

## `hukou outdated`（V0.3 分支，未发布）

```text
hukou outdated [name ...] [--json] [--asset <substring>]
```

- 不给 name 时检查全部已收编条目；重复 name 去重。
- local 条目标记 `local`，不联网；live SHA drift 在 GitHub 请求前失败关闭。
- 其余条目按 policy 查询 metadata、选择候选和平台资产；不下载、不写本地状态。
- `--asset` 与 upgrade 相同，`^` 前缀表示反选。
- current/outdated/local 是正常结果；unavailable/unsupported/drift 等失败使整体非零，同时保留 report。
- `--json` 输出 `schema_version=1`。

## `hukou upgrade`

```text
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substring>]
```

- 名称列表与 `--all` 二选一。
- `--all` 只表示 manifest 中全部 hukou-adopted entries，不运行 Homebrew/npm/Cargo/mise/系统更新。
- `--asset` 用子串过滤资产，以 `^` 开头表示反选。
- local 条目跳过。
- `--dry-run` 与 outdated 复用 policy-aware checker，查询必要 release metadata 和资产选择，但不下载、不创建目录、不持锁、不 GC。

真实升级会在锁内重检后下载并校验资产、写 store、记录 child activation、持久化 before/after transaction、切换活跃路径、更新 manifest；transaction clean 时再按 current/original/pin/lineage 保护集两阶段清理旧版本。metadata 选择遵循 effective policy，默认 SemVer 不降级；exact pin 可显式前进或回退。`--dry-run` 发现 pending transaction 时失败提示，但不会自动恢复或写状态。

## `hukou rollback`

```text
hukou rollback <name> [--to <tag|original>]
```

V0.3 分支不带 `--to` 时选择当前 activation 的逻辑 parent；连续 rollback 按 lineage 向前走，不读取 store mtime。`--to <tag>` 必须是安全单路径组件、且是当前 lineage 中最近的同 tag ancestor；目标 store artifact 的 tag/SHA 必须同时与 history 绑定一致。`--to original` 恢复不可变收编原件并生成无 parent 的新 event，后续默认 rollback 不猜测去向。操作前后都更新 active binary SHA。

副作用：获取 `state.lock`、先恢复旧 transaction、持久化新 transaction、原子替换活跃常规文件、把 history/current 放进同一 after-manifest；失败必须补偿或保留可重入恢复证据。

激活复制只保留字节与 rwx 权限位；不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。文件、rename 与父目录持久化后才进入下一事务阶段。

## `hukou list`

```text
hukou list
```

显示 `NAME / TAG / REPO / PATH / VERSIONS`。`VERSIONS` 只统计下载/保留的普通 tag，
不把 immutable `original` 算作版本；但每个条目输出前都必须证明 original namespace
恰好包含预期 regular backup，因此 original 缺失、重复或拓扑异常会使 list 失败关闭。
副作用：无。

pending transaction 或无效 store 拓扑不会被吞成正常版本数；list 会失败关闭并提示先诊断/恢复。
脚本消费者须注意：事务状态未清（存在未决 transaction）或 store 拓扑异常时 `list` 返回非零退出码而非打印部分清单，因此不能把 `list` 的成功退出之外的情况当作空清单处理。

## `hukou doctor`

```text
hukou doctor [--json] [--deep]
```

- 默认检查 manifest/backup、entry、live SHA/类型/权限、original/current tag、store 拓扑与 transaction inventory。
- `--deep` 额外 hash retained versions，并检查已登记 live parent 的 hukou 临时文件前缀。
- manifest 无效时，manifest 外 store tool 标为 `UNCLASSIFIABLE`，不会猜成可删除 orphan。
- warning/error 会输出完整报告并返回非零；JSON stdout 始终是同一 Report 模型。

副作用：无。当前正式版本 v0.2.0 没有 repair；V0.3 分支也没有 repair-all。

V0.3 分支仍保持 doctor 本身无 repair 参数；所有修复都经过下面独立命令。

## `hukou policy`（V0.3 分支，未发布）

```text
hukou policy show [name] [--json]
hukou policy set <name> [--mode semver|github-latest]
                         [--channel stable|prerelease]
                         [--pin <tag>|--unpin]
                         [--rollback-depth <N>]
```

- `show` 输出 effective policy；entry 没有 retention override 时显示 manifest 来源。
- `set` 至少需要一个变更 flag；`--pin` 与 `--unpin` 互斥，depth 必须非负。
- 显式 `--mode semver` 会拒绝 local entry，以及当前 tag 不是 Go update policy 所需的
  严格可排序 SemVer `X.Y.Z`（可带小写 `v` 和合法 prerelease/build）的 entry。
  发布/安装脚本使用更窄的 v-prefix、无 build metadata 契约；不要混用两套用途。
- `set` 在 state lock 内重新加载并原子保存 manifest，只改 policy，不触碰 live/store。
- pending transaction 使 show/set 失败关闭；set 不隐式 recovery。

## `hukou repair`（V0.3 分支，未发布）

```text
hukou repair plan --action recover-transaction --output <plan.json>
hukou repair plan --action restore-manifest-backup --output <plan.json>
hukou repair apply --plan <plan.json>
```

- `plan` 只读 hukou data root，并只写用户明确指定的 `0600` plan 文件；父目录必须已存在。
- `apply` 持 state lock，重算 data-root identity、state fingerprint 和前置条件；plan stale 时零业务状态写入失败。apply 可能创建/使用 lock 文件，所以这不是“绝对零文件写入”。
- transaction recovery 只接受可完整分类的未决 journal；backup restore 只接受主文件缺失/无效、backup 语义有效、transaction clean 且所有 live SHA 匹配的状态。
- 没有 repair-all、orphan 删除、quarantine 或 manifest merge。

建议把 plan 写在 hukou data root 之外；把 plan 放入被 fingerprint 覆盖的状态树可能会让它在 apply 前自行变 stale。

## `hukou support bundle`（V0.3 分支，未发布）

```text
hukou support bundle --format json
hukou support bundle --output <report.json>
```

两种输出方式必须且只能选一个。stdout 模式零写；文件模式只写一个 `0600` JSON。
报告离线生成且不上传，只包含安全 build/platform 值、脱敏 doctor finding、匿名
policy/history 摘要和 transaction/store 计数，不包含原始 path/repo/tag/name、用户、
环境变量、二进制或 WAL payload。

## 退出状态

- 成功为 0。
- 参数、完整性、网络、checksum、锁、文件系统或部分升级失败返回非零。
- `upgrade --all` 即使其他条目成功，只要有一项失败，整体仍返回非零并打印失败清单。
