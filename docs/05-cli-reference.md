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

副作用：获取 `state.lock`、创建 data root、备份 original、保存 manifest。H1 后同名条目不得静默覆盖。original 备份只保留字节与 rwx 权限位，不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。

## `hukou upgrade`

```text
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substring>]
```

- 名称列表与 `--all` 二选一。
- `--asset` 用子串过滤资产，以 `^` 开头表示反选。
- local 条目跳过。
- `--dry-run` 查询 release metadata 和资产选择，但不下载、不创建目录、不持锁、不 GC。

真实升级会下载并校验资产、写 store、切换活跃路径、更新 manifest、清理旧版本。

## `hukou rollback`

```text
hukou rollback <name> [--to <tag|original>]
```

不带 `--to` 时，按 store 目录修改时间选择最近的其他版本或 original。这是目录 mtime 启发式，不是历史栈；连续不带 `--to` 回滚可能在两个最近 tag 之间来回切换。需要确定结果时应显式传入 `--to <tag|original>`。操作前后都更新 active binary SHA。

副作用：获取 `state.lock`、原子替换活跃常规文件、保存 manifest；失败必须补偿恢复。

激活复制只保留字节与 rwx 权限位；不保留 owner/group、ACL、xattr、mtime、特殊权限位或 hardlink topology。当前无目录 `fsync`/WAL 承诺，进程被强制终止时，live 目录可能留下 `.hukou-tmp-*` 临时文件。

## `hukou list`

```text
hukou list
```

显示 `NAME / TAG / REPO / PATH / VERSIONS`。副作用：无。

## 退出状态

- 成功为 0。
- 参数、完整性、网络、checksum、锁、文件系统或部分升级失败返回非零。
- `upgrade --all` 即使其他条目成功，只要有一项失败，整体仍返回非零并打印失败清单。
