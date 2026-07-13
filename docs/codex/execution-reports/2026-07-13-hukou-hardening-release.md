# Codex 执行报告：H1 安全硬化与首个 SemVer 发布

## 元信息

- Date: 2026-07-13
- Branch: `codex/hukou-hardening-release`
- Base: `main@afed279`
- Status: approved
- Approval: Eric 明确要求“直接推进到发布，无需授权，记得在完成后推送到GitHub”

## 用户请求

在全局理解报告和待批准方案基础上，直接完成安全硬化、工程验证、GitHub 推送和发布；方案内动作无需再次逐项授权。

## 目标理解

1. 关闭 checksum fail-open、归档格式不一致、ZIP 嵌套路径、HTTP 下载超时和 upgrade/rollback 错误恢复缺口。
2. 增加进程互斥、纯本地 dry-run、收编冲突保护与 hukou 自有归属完整性提示。
3. 建立当前项目文档入口、变更记录、验证记录、CI 和可重复发布流程。
4. 在不触碰真实用户二进制和真实 hukou 数据目录的条件下完成全量测试与隔离 smoke。
5. 提交、推送、通过 PR 合入 `main`，创建首个 SemVer GitHub Release。

## 计划修改范围

- CLI：`cmd/adopt.go`、`cmd/upgrade.go`、`cmd/rollback.go`、`cmd/root.go` 及测试。
- 核心包：`internal/archive`、`assetpick`、`ghrelease`、`manifest`、`provenance`、`store`、`verify`，并新增状态锁/事务辅助。
- 工程：`Makefile`、`.gitignore`、GitHub Actions、版本与发布脚本。
- 文档：根 README、阶段规格、`docs/` 当前事实入口、Codex 记录与 ADR。

## 计划运行命令

- 针对性 `go test`（archive、assetpick、verify、ghrelease、manifest、store、cmd、provenance）。
- `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`。
- 当前全仓覆盖率生成与汇总。
- 仅使用临时 `HOME/PATH/XDG_DATA_HOME/HUKOU_DATA_DIR` 的 CLI smoke。
- `git diff --check`、工作树范围检查、独立只读审阅。
- Git commit、push、PR、合并、tag 和 GitHub Release。

## 预期交付物

- 可核验的代码修复和失败注入测试。
- 当前项目文档入口、change record、verification report。
- GitHub CI 与可重复 release workflow。
- 合入 `main` 的提交、SemVer tag、GitHub Release 与多平台资产/checksums。

## 风险与回滚

- 事务补偿涉及 regular file 与 symlink 两种拓扑，必须以失败注入测试作为合入门。
- Manifest 只增加向后兼容的可选字段，不执行真实用户数据迁移。
- 所有危险场景只在临时目录运行；不读取或修改真实 hukou manifest/store。
- 每个主题保持独立提交；合入前可逐项 revert，发布后用 patch release 修复，不移动已发布 tag。
- SIGKILL/断电一致性需要后续 WAL/doctor；本轮只声称覆盖可观测错误返回路径。

## 明确边界

- 仓库当前为 private；本轮不替用户选择原创代码许可证，不做法律结论。
- 不引入新的运行时第三方依赖。
- 不实现 Windows、topgrade、mise/Brewfile 导出或风险提示产品层。
