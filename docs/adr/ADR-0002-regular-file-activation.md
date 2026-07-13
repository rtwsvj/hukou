# ADR-0002：活跃路径使用常规文件原子替换

- Status: Accepted
- Date: 2026-07-13
- Scope: H1 activation model on macOS/Linux

## Context

Phase 2 通过“创建完整临时 symlink，再同目录 rename”切换活跃版本。PR #1 的前两次 macOS CI 在紧密并发读取测试中分别从 `ReadFile(linkPath)` 和单次 `Readlink(linkPath)` 收到 `EINVAL`；Ubuntu 与本机未复现。这说明即使目录项替换本身是原子的，macOS/APFS 读者仍可能在 symlink inode 交换窗口看到瞬时解引用失败。

CLI 工具路径的首要契约是调用者应持续打开完整旧版本或完整新版本。不能仅在测试中忽略 `EINVAL`。

## Decision

1. store 中的 `original/<binary>` 与 `<tag>/<binary>` 保持不可变常规文件副本。
2. `Activate` 在 live path 同目录创建临时常规文件，完整复制目标、设置权限、`fsync`、close 后 rename 覆盖 live path。
3. `AdoptOriginal` 只复制 original 备份，不改变现有 live regular file。
4. rollback original 与普通 tag 使用同一 `Activate` 路径。
5. `Prune` 由调用方显式传入受保护 tag，不再从活跃 symlink 反推当前版本。
6. 事务快照继续识别 regular file 与遗留 symlink；失败时恢复原拓扑。遗留 symlink 首次成功激活后迁移为 regular file。
7. store 内部目录解析拒绝大小写别名、symlink 与非目录组件，避免跨平台保留名碰撞及写入/删除越界。

## Consequences

- 活跃文件与 store 版本各占一份空间；换取平台一致的打开语义和更简单的 live path。
- 活跃文件不再通过链接直接证明所属版本，manifest tag + SHA-256 继续作为事实源，hukou detector 会重新哈希验证。
- 复制契约只保证文件字节与 rwx 权限位。owner/group、ACL、xattr、mtime、setuid/setgid/sticky 等特殊权限位以及 hardlink topology 均不保留；`adopt` 会拒绝带特权或特殊权限位的源文件，而不是静默降级。
- regular-file 快照使用独立副本，避免与 live file 共享 inode；普通错误返回的补偿路径与新激活模型直接兼容。
- 这仍不是断电安全事务：目录 `fsync`、WAL 与 doctor 留 H2；进程被强制终止时，live path 所在目录可能留下未进入生效路径的 `.hukou-tmp-*` 临时文件。

## Evidence

- Initial CI: `https://github.com/rtwsvj/hukou/actions/runs/29265826948`
- Single-Readlink retry CI: `https://github.com/rtwsvj/hukou/actions/runs/29266208797`
- 最终证据写入 H1 verification report 与后续 macOS CI run。
