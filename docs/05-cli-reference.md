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

副作用：无；不联网、不写用户目录。

## `hukou adopt`

```text
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force]
```

- 裸名字经 PATH 查找，路径参数直接定位。
- Go 二进制可从 build info 推导 `owner/repo`。
- 其他二进制必须给 repo 或使用 `--local`。
- `--force` 允许越过其他管理器所有权闸门，不越过文件与路径安全检查。
- 带 setuid/setgid/sticky 等特权或特殊权限位的源文件会被拒绝。
- `--tag` 必须是单一路径组件；`original`（含大小写别名）是不可变备份保留名，不能作为 adopt tag。

副作用：获取 `state.lock`、恢复旧 transaction、创建 data root、持久化 transaction、备份 original、保存 manifest。H1 后同名条目不得静默覆盖。original 备份只保留字节与 rwx 权限位，不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。

## `hukou upgrade`

```text
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substring>]
```

- 名称列表与 `--all` 二选一。
- `--asset` 用子串过滤资产，以 `^` 开头表示反选。
- local 条目跳过。
- `--dry-run` 查询 release metadata 和资产选择，但不下载、不创建目录、不持锁、不 GC。

真实升级会下载并校验资产、写 store、持久化 before/after transaction、切换活跃路径、更新 manifest、提交事务后清理旧版本。`--dry-run` 发现 pending transaction 时失败提示，但不会自动恢复或写状态。

## `hukou rollback`

```text
hukou rollback <name> [--to <tag|original>]
```

不带 `--to` 时，按 store 目录修改时间选择最近的其他版本或 original。这是目录 mtime 启发式，不是历史栈；连续不带 `--to` 回滚可能在两个最近 tag 之间来回切换。需要确定结果时应显式传入 `--to <tag|original>`。操作前后都更新 active binary SHA。

副作用：获取 `state.lock`、先恢复旧 transaction、持久化新 transaction、原子替换活跃常规文件、保存 manifest；失败必须补偿或保留可重入恢复证据。

激活复制只保留字节与 rwx 权限位；不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。文件、rename 与父目录持久化后才进入下一事务阶段。

## `hukou list`

```text
hukou list
```

显示 `NAME / TAG / REPO / PATH / VERSIONS`。副作用：无。

pending transaction 或无效 store 拓扑不会被吞成正常版本数；list 会失败关闭并提示先诊断/恢复。

## `hukou doctor`

```text
hukou doctor [--json] [--deep]
```

- 默认检查 manifest/backup、entry、live SHA/类型/权限、original/current tag、store 拓扑与 transaction inventory。
- `--deep` 额外 hash retained versions，并检查已登记 live parent 的 hukou 临时文件前缀。
- manifest 无效时，manifest 外 store tool 标为 `UNCLASSIFIABLE`，不会猜成可删除 orphan。
- warning/error 会输出完整报告并返回非零；JSON stdout 始终是同一 Report 模型。

副作用：无。当前版本没有 repair 或 repair-all。

## 退出状态

- 成功为 0。
- 参数、完整性、网络、checksum、锁、文件系统或部分升级失败返回非零。
- `upgrade --all` 即使其他条目成功，只要有一项失败，整体仍返回非零并打印失败清单。
