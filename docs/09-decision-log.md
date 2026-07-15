# 决策日志

重要、长期或难以回滚的决策写入 `docs/adr/`；本文件只做索引。

| ADR | 状态 | 决策 |
|---|---|---|
| [`ADR-0001`](adr/ADR-0001-h1-safety-and-release-contract.md) | Accepted | H1 安全边界、数据审计字段和发布契约 |
| [`ADR-0002`](adr/ADR-0002-regular-file-activation.md) | Accepted | 活跃路径使用 regular-file copy + rename，不交换 symlink inode |
| [`ADR-0003`](adr/ADR-0003-crash-recovery-and-doctor.md) | Accepted | H2 使用持久化事务决策恢复；doctor 默认只读且不猜测修复 |
| [`ADR-0004`](adr/ADR-0004-trust-first-and-manager-boundaries.md) | Accepted / implemented in private RC | trust-first CLI；`upgrade --all` 只管 hukou；Topgrade 仅外层编排 |
| [`ADR-0005`](adr/ADR-0005-manifest-v2-history-policy-and-repair.md) | Accepted / implemented in private RC | schema v2 lineage/policy/retention、窄版 repair 与脱敏 support |
| [`ADR-0006`](adr/ADR-0006-transaction-residue-self-heal.md) | Accepted | 未知事务条目隔离自愈；新增 purge-quarantine 与 clean-live-temps repair 动作 |

## 历史决策摘要

- 2026-07-11：主语言选择 Go，CLI 使用 Cobra，其余尽量标准库。
- 2026-07-11：scan 必须纯本地只读；其他管理器已有所有权时不默认抢占。
- 2026-07-12：版本 store + 软链切换；manifest schema v1。
- 2026-07-13：仓库保持 private，本轮不创建原创代码 LICENSE。
- 2026-07-13：首发版本目标 `v0.1.0`，四平台 tar.gz + checksums。
- 2026-07-13：两次 macOS CI 证明并发替换 symlink inode 会产生瞬时 `EINVAL`；激活模型改为同目录 regular-file copy + rename。
- 2026-07-14：跨 live/manifest 的崩溃恢复采用 PREPARED 回滚、COMMITTED 前滚的持久化事务；doctor 默认零写、零网络。
- 2026-07-14：为公开准备加入 Apache-2.0 根许可证和第三方 notices；仓库保持 private，许可证落盘不等于公开或发布。
- 2026-07-14：V0.3 先解释/预览再修改；跨管理器升级不进入 hukou，Topgrade 只负责串联各自独立的 manager。
- 2026-07-14：manifest 提升到 v2，rollback/retention 只依据显式 lineage；repair 只开放 fingerprint 绑定的两个 action。
- 2026-07-15：未知事务条目不再楔死恢复，改为原子隔离到 `quarantined-*` 并保留证据；新增 `purge-quarantine` 与 `clean-live-temps` 两个 fingerprint 绑定的 repair 动作。

决策发生变化时，应新增 ADR 或把旧 ADR 标成 Superseded，不静默改写历史理由。
