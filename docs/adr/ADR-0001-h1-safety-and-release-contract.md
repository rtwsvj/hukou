# ADR-0001：H1 安全与发布契约

- Status: Accepted
- Date: 2026-07-13
- Scope: H1 hardening and `v0.1.0`

## Context

Phase 2 已具备 upgrade/rollback 主流程，但静态审阅发现 checksum 缺条目可放行、激活后 manifest 保存失败无法恢复、写命令缺少跨进程互斥，以及发布/验证事实源缺失。

## Decision

1. checksum asset 一旦存在，所选 asset 缺条目、条目无效或 hash 不匹配均失败关闭。
2. manifest 增加 `asset_name`、`asset_sha256`、`checksum_asset`、`checksum_verified` 可选审计字段；`sha256` 继续表示 active binary。
3. 写操作使用 `<dataRoot>/state.lock` 串行化；`HUKOU_DATA_DIR` 优先决定 data root。
4. upgrade/rollback 对可观测错误采用补偿恢复；崩溃一致性延后到 H2。
5. dry-run 不创建目录、不获取写锁、不 GC、不下载。
6. CI 必须覆盖 Linux/macOS 的 test、race、build，并保存 coverage artifact。
7. 发布使用固定 commit/time 的四平台 tar.gz、checksums 和 buildinfo 注入。
8. 仓库当前 private，不在 H1 创建原创代码根 LICENSE；archive 继续携带第三方 `LICENSES/`。

## Consequences

- 命令层需要保存旧路径拓扑与旧 manifest 快照，错误分支变复杂。
- manifest v1 读取保持兼容，但后续代码必须区分两个 hash。
- 写命令在锁竞争时立即失败；调用方需要重试时由调用方显式决定，不在锁内无限等待。
- release 依赖 GNU tar 以获得稳定 archive metadata。
- H1 通过不代表断电安全；文档与验证报告必须保留该限制。

## Verification

以 H1 change record、失败注入测试、全仓门禁、隔离 CLI smoke 和发布 artifact 验证报告为准。
