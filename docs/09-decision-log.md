# 决策日志

重要、长期或难以回滚的决策写入 `docs/adr/`；本文件只做索引。

| ADR | 状态 | 决策 |
|---|---|---|
| [`ADR-0001`](adr/ADR-0001-h1-safety-and-release-contract.md) | Accepted | H1 安全边界、数据审计字段和发布契约 |
| [`ADR-0002`](adr/ADR-0002-regular-file-activation.md) | Accepted | 活跃路径使用 regular-file copy + rename，不交换 symlink inode |

## 历史决策摘要

- 2026-07-11：主语言选择 Go，CLI 使用 Cobra，其余尽量标准库。
- 2026-07-11：scan 必须纯本地只读；其他管理器已有所有权时不默认抢占。
- 2026-07-12：版本 store + 软链切换；manifest schema v1。
- 2026-07-13：仓库保持 private，本轮不创建原创代码 LICENSE。
- 2026-07-13：首发版本目标 `v0.1.0`，四平台 tar.gz + checksums。
- 2026-07-13：两次 macOS CI 证明并发替换 symlink inode 会产生瞬时 `EINVAL`；激活模型改为同目录 regular-file copy + rename。

决策发生变化时，应新增 ADR 或把旧 ADR 标成 Superseded，不静默改写历史理由。
