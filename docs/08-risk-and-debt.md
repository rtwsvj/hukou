# 风险与技术债

## H1 发布关闭条件

以下缺陷已有实现与本地测试，仍须由最终 verification report 和远端 release workflow 核验后才能视为关闭，不应理解为当前代码仍明确保留这些旧行为：

- checksum 缺条目必须失败关闭。
- upgrade/rollback 激活后保存失败必须补偿。
- 写命令必须持有进程互斥锁。
- dry-run 必须保持零本地写入。
- release artifact 必须验证版本注入、四资产数量、双构建一致性与 checksums。

## 已知但不阻断 H1 的债务

| 风险 | 当前边界 | 后续方向 |
|---|---|---|
| 断电/SIGKILL | 单全局 WAL、file/dir sync 与真实进程 kill 测试已覆盖 hukou 协作事务；普通 CI 不能完全模拟硬件掉电缓存重排 | 在 APFS/ext4 等目标文件系统做周期性 crash harness；不扩大到未验证平台 |
| 非协作外部原地写 | 下载后和 snapshot 后均复核 SHA，regular snapshot 为独立副本；但最终复核到 activate 仍有窄窗口 | 文件描述符绑定与 OS 级协调策略 |
| 文件元数据 | 复制只保证字节与 rwx 权限位；owner/group、ACL、xattr、mtime、特殊权限位与 hardlink topology 不保留，`adopt` 拒绝特权/特殊权限位 | 如需扩展保留范围，先定义跨平台契约与失败语义 |
| 默认 rollback 选择 | 按 store 目录 mtime 选最近的其他 tag/original，不是历史栈；连续默认回滚可在两个 tag 之间来回切换 | 显式历史栈或可审计的激活序列 |
| manifest 损坏 | Save 保留上一份可解析且 schema 受支持的 `.bak`，doctor 继续验证其语义恢复候选资格；仍不自动覆盖损坏主文件 | 显式 backup hash + state fingerprint 绑定的 restore action |
| store 孤儿版本 | doctor 区分 manifest 外 tool 与合法 retained version，只报告不删除 | 显式逐项 quarantine + undo，不做 repair-all |
| tar.xz | 明确不支持 | 决定是否引入 xz 依赖 |
| 版本比较 | tag 字符串不等即更新 | 是否引入语义版本策略 |
| 固定保留 3 版 | 当前不可配置 | manifest/global config |
| 真实网络覆盖 | PR 仅 httptest | 独立 fixture smoke |
| 平台 | Windows 未支持 | 单独设计和 CI |

## H2 恢复边界

- WAL 只覆盖 journal 中精确绑定的 before/after。恢复会先分类全部参与者，并在写入前复核；此时发现 live/manifest/original 已变成第三种状态会停止并保留 pending evidence。非协作外部写仍可命中最后一次复核与 rename/remove 系统调用之间的窄 TOCTOU 窗口。
- scan、list、doctor 与纯 `--dry-run` 不持写锁；它们会报告检查瞬间已知的 pending transaction，但不会对并发的非协作文件系统写入提供一致快照保证。
- durability 返回成功表示操作系统接受了 file/dir sync；不等于磁盘介质、控制器或文件系统绝不损坏。
- 旧版本遗留的 `.hukou-rollback-*` 没有 transaction ID，doctor 只能报告，不能猜测恢复。
- doctor 当前没有自动 repair；这是刻意的安全边界，不是遗漏一个通用删除按钮。

## Phase 1 已知限制

- scan 的 Stat/Open 间存在 TOCTOU。
- npm wrapper、nvm、自定义 prefix 的归属不总能精确还原。
- PATH 空段被刻意跳过，不按 POSIX 解释为 CWD。
- 探测结果是证据驱动的最佳判断，不是软件供应链证明。

## 发布与工程风险

- 仓库 private，当前不创建原创代码根 LICENSE；若分发策略变化必须先做决策。
- release archive 仍包含 `LICENSES/` 中的第三方来源文本。
- GitHub hosted runner 与 Action 主版本会演进，workflow 升级应单独验证。
- 手动 workflow 只生成 snapshot，防止误发布；只有 tag 触发 release。

## 风险关闭规则

不能仅删除本文件中的条目。关闭风险时必须同时提供：

1. 对应代码或文档变更。
2. 失败场景测试。
3. change record。
4. 指向具体 commit 的 verification report。
