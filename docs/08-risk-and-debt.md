# 风险与技术债

## H1 发布关闭条件

以下缺陷已有实现与本地测试，仍须由最终 verification report 和远端 release workflow 核验后才能视为关闭，不应理解为当前代码仍明确保留这些旧行为：

- checksum 缺条目必须失败关闭。
- upgrade/rollback 激活后保存失败必须补偿。
- 写命令必须持有进程互斥锁。
- dry-run 必须保持零本地写入。
- release artifact 必须验证版本注入、四资产数量、双构建一致性与 checksums。

## 已知债务与 V0.3 状态

| 风险 | 当前边界 | 后续方向 |
|---|---|---|
| 断电/SIGKILL | 单全局 WAL、file/dir sync 与真实进程 kill 测试已覆盖 hukou 协作事务；普通 CI 不能完全模拟硬件掉电缓存重排 | 在 APFS/ext4 等目标文件系统做周期性 crash harness；不扩大到未验证平台 |
| 非协作外部原地写 | 下载后和 snapshot 后均复核 SHA，regular snapshot 为独立副本；但最终复核到 activate 仍有窄窗口 | 文件描述符绑定与 OS 级协调策略 |
| 文件元数据 | 复制只保证字节与 rwx 权限位；owner/group、ACL、xattr、mtime、特殊权限位与 hardlink topology 不保留，`adopt` 拒绝特权/特殊权限位 | 如需扩展保留范围，先定义跨平台契约与失败语义 |
| 默认 rollback 选择 | V0.3 subject 已改用 manifest v2 activation parent，不再读取 mtime；lineage fault matrix 与固定提交回归已通过，v0.2 仍是旧行为 | 公开发布前继续保留真实用户迁移观察 |
| manifest 损坏 | V0.3 分支已有 fingerprint-bound backup restore，但只接受 main 缺失/无效、backup 语义有效、transaction clean、全部 live SHA 匹配 | 保持窄 action，不扩成自动 merge/猜测恢复 |
| store 孤儿版本 | doctor 区分 manifest 外 tool 与合法 retained version，只报告不删除 | 显式逐项 quarantine + undo，不做 repair-all |
| tar.xz | 明确不支持 | 决定是否引入 xz 依赖 |
| 版本比较 | V0.3 subject 已有 SemVer/GitHub-latest、channel、pin 与降级保护；本地/隔离 Linux 门禁通过，未做公共 fixture 网络 smoke | 公共 fixture smoke 后再宣称公开稳定 |
| 固定保留 3 版 | V0.3 subject 改为 global/entry rollback depth 与 lineage/pin/pending 保护集，固定提交故障矩阵通过 | 观察历史增长，后续设计压缩/归档而非猜删 |
| 真实网络覆盖 | PR 仅 httptest | 独立 fixture smoke |
| 平台 | Windows 未支持 | 单独设计和 CI |

## V0.3 新边界

| 风险 | 当前边界 | 后续方向 |
|---|---|---|
| schema v2 向后兼容 | V0.2 必须拒绝 v2，避免旧 writer 丢 history/policy；迁移只从当前状态造 synthetic root，无法恢复历史事实 | 发布前备份与 downgrade 文档；不提供 v2→v1 静默转换 |
| schema 字段边界 | V0.3 decoder 已按声明 schema 要求 v2 policy/retention/history，并拒绝 v0/v1 携带 v2-only 字段；但 Go JSON 仍接受重复 object key | 后续增加 duplicate-key-aware tokenizer/decoder 与回归；当前 unknown-field 检查不冒充已覆盖 duplicate key |
| history 增长 | activation event 追加，retention 只删 artifact、不删事件 | 另立 ADR 设计可审计压缩，不在 V0.3 猜测 |
| explicit original | legacy migration 无法证明 original 的 parent，恢复后 lineage 刻意终止 | 用户需要继续升级时从该状态建立新的 forward event；文档提示默认 rollback 无 ancestor |
| repair plan 时效 | plan 绑定 data-root identity/fingerprint；任何相关变化都会 stale。plan 写进被观察树可能使自身失效 | 建议把 plan 放在 data root 外；不放宽 stale 检查 |
| repair apply 的 lock 痕迹 | apply 会确认 existing root durability 并创建/使用 `state.lock`，但 fingerprint 失败不得改 live/store/manifest/journal | verification report 明确区分锁文件与业务状态零写 |
| support 隐私 | 当前只输出匿名序号/计数/枚举；仍需持续用 secret fixture 回归，用户也应在公开提交前人工查看 | 未来字段默认 deny-list 不够，应保持显式 allow-list |
| 安装器信任根 | checksum 与 archive 来自同一 GitHub Release；能发现传输/意外损坏，不等于独立签名身份 | public 后启用并验证 provenance/attestation；评估签名 |
| 安装目标竞争 | V0.3 subject 已把 dangling symlink 计为 existing；有 Perl时最终目录项使用 `link(2)` atomic no-replace / force `rename(2)`，Linux 无 Perl时回退 `ln -T`/`mv -T`；directory、symlink-to-directory、预检后竞争与 duplicate member tests 已在固定提交通过 | 保持目标目录同文件系统前提；force 仍是显式允许覆盖的独立路径 |
| 安装包膨胀 | 当前检查 archive root、目标 member 唯一性和目标文件类型；尚未给总展开字节数与 member 数设置独立预算 | 为压缩炸弹/超多 member 增加 streaming budget，超限时在写目标前失败关闭 |
| release list 上限 | 最多 10×100 releases，填满上限时失败关闭 | 如确有超大仓库，再引入有边界的完整性证明 |
| GitHub API body | request 有总超时和有界分页，但 JSON response 尚无独立 body byte cap | 使用有界 reader 并测试超限 response，避免异常服务端造成内存放大 |
| 路径遍历 TOCTOU | 当前按组件拒绝 symlink/非目录、在写前重检并验证 tag/SHA；非协作 writer 仍可能命中 check 与 syscall 间窗口 | 评估 `openat`/目录 fd anchored traversal；先定义 Darwin/Linux 一致失败语义 |
| explain 只读证据 | V0.3 subject 已增加 name/path 独立目录快照与 `http.DefaultTransport` spy；5 项定向测试与固定提交全仓回归通过 | 保持测试，避免未来 detector 漂移破坏零写/零网络契约 |

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

- 仓库仍 private，但 V0.3 分支已加入 Apache-2.0 根 LICENSE、THIRD_PARTY_NOTICES
  和依赖/改编来源许可证；这属于公开准备，不代表仓库已经公开或 V0.3 已发布。
- release archive 的双语 README、根 LICENSE、notices 与 `LICENSES/` 已在四目标
  双构建 snapshot 验收；4/4 checksums、root/mode/buildinfo 与安装器 smoke 通过。
- 固定 subject 已在 non-root Linux/arm64 容器完成 ordinary/race，并在 GNU tar 1.34
  下完成 installer/release tests；source/module cache read-only，`GOPROXY=off`。
- GitHub hosted runner 与 Action 主版本会演进，workflow 升级应单独验证。
- Draft PR #6 的 CI run `29352308455` 已确认五个 job 均因账户 billing/spending limit
  在 0 steps 前失败；不得以本地结果冒充远端绿色。
- CodeQL job 当前只对 public repository 运行；private 中 skipped 不等于通过。
- workflow 会先从四个 archive 提取四个平台二进制，再用固定 Syft 1.46.0 生成并
  断言 SPDX 内容。验收曾发现扫描压缩包目录只得到 1 package/0 files 的空壳 SBOM，
  修复后为 21 packages/4 files；artifact attestation 仍仅 public repository 执行。
- 手动 workflow 只生成 snapshot，防止误发布；只有已推送 tag 触发 Release。
- 本轮授权禁止创建 `v0.3.0` tag、正式 Release 或改变 repository visibility。

## 风险关闭规则

不能仅删除本文件中的条目。关闭风险时必须同时提供：

1. 对应代码或文档变更。
2. 失败场景测试。
3. change record。
4. 指向具体 commit 的 verification report。
