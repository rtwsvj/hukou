# Codex 改动记录：文档、CI 与发布基础设施

## 元信息

- Change ID: `CHANGE-20260713-docs-ci-release-foundation`
- Execution Report: `docs/codex/execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `docs/codex/verification-reports/2026-07-13-hukou-hardening-local.md`
- Status: implemented
- Verification Status: pass-with-infrastructure-exception（发布链完成；GitHub-hosted runner 因计费限制未执行）
- Scope owner: docs/engineering foundation subtask

## 用户请求

在不修改 Go 文件、不提交、不推送的边界内，建立当前文档真相源，修正规格和历史报告，增加 Linux/macOS CI，以及可重复的四平台 tar.gz + checksums 发布流程。

## 声称完成的改动清单

- [x] C1: README 反映 Phase 1/2 已实现、H1 执行中，并提供当前命令、数据和安全说明。
  - Evidence expected: README 不再把 Phase 1/2 标成未开始；命令与环境变量能对应当前 CLI。
  - Files: `README.md`
- [x] C2: 建立长期文档入口、需求、路线图、架构、数据、CLI、开发、测试、风险、决策、发布和术语文档。
  - Evidence expected: `docs/README.md` 的内部链接可解析；状态区分 implemented、in progress、unverified。
  - Files: `docs/README.md`, `docs/00-project-brief.md` ... `docs/10-glossary.md`, `docs/adr/`
- [x] C3: 历史提案标记 Superseded，来源表和 Phase 1/2 规格与 H1 契约对齐。
  - Evidence expected: 初始报告有历史横幅；规格包含 hukou detector、state.lock、审计字段、fail-closed 与补偿语义。
  - Files: `docs/pinhaoma-report.md`, `docs/pinhaoma-sources.md`, `docs/specs/*.md`
- [x] C4: 建立可供 `$pinhaoma-review` 核验的 Codex 三段式记录目录。
  - Evidence expected: execution/change/verification README 与 session log 存在，且不把批准报告视为验证通过。
  - Files: `docs/codex/`, `docs/records/README.md`
- [x] C5: CI 覆盖 fmt、module verify、vet、test、race、coverage、build 和 version smoke，测试矩阵包含 Linux/macOS。
  - Evidence expected: workflow YAML 可解析，权限只读，artifact 名唯一。
  - Files: `.github/workflows/ci.yml`, `Makefile`
- [x] C6: 发布流程从固定 commit/time 注入 buildinfo，交叉构建四个平台，生成稳定 tar.gz 与 checksums；手动运行不发布，tag 运行发布。
  - Evidence expected: shell 语法通过；workflow 使用 tag/main/annotated 校验、精确版本 smoke、checksum 自检和最小权限独立 publish job；archive 文件名与文档一致。
  - Files: `scripts/release.sh`, `.github/workflows/release.yml`, `docs/09-release.md`
- [x] C7: coverage 与发布生成物不再进入版本控制。
  - Evidence expected: `.gitignore` 覆盖 profile/dist/bin；旧 `coverage.out` 被删除。
  - Files: `.gitignore`, `coverage.out`

## 实际修改文件

| 文件 | 动作 | 说明 |
|---|---|---|
| `README.md` | modified | 当前用户入口与安全/发布说明 |
| `.gitignore` | modified | 忽略 coverage profile |
| `coverage.out` | deleted | 移除 Phase 1 旧生成物 |
| `Makefile` | modified | fmt/race/coverage/verify/release targets |
| `.github/workflows/ci.yml` | created | Linux/macOS CI |
| `.github/workflows/release.yml` | created | snapshot/tag 发布 |
| `scripts/release.sh` | created | 可重复四平台打包 |
| `docs/README.md` | created | 文档真相入口 |
| `docs/00-project-brief.md` | created | 项目简报 |
| `docs/01-requirements.md` | created | 需求与不变量 |
| `docs/02-roadmap.md` | created | 阶段状态 |
| `docs/03-architecture.md` | created | 架构与流程 |
| `docs/04-data-and-api.md` | created | manifest/store/API |
| `docs/05-cli-reference.md` | created | CLI 契约 |
| `docs/06-dev-setup.md` | created | 开发与隔离 |
| `docs/07-testing-and-verification.md` | created | 验证分层 |
| `docs/08-risk-and-debt.md` | created | 风险债务 |
| `docs/09-decision-log.md` | created | 决策索引 |
| `docs/09-release.md` | created | 发布手册 |
| `docs/10-glossary.md` | created | 术语 |
| `docs/adr/*` | created | H1 ADR 与索引 |
| `docs/codex/*` | created | 记录链和 session log |
| `docs/records/README.md` | created | 历史记录解释 |
| `docs/pinhaoma-report.md` | modified | 标记 Superseded |
| `docs/pinhaoma-sources.md` | modified | 实际来源映射 |
| `docs/specs/*.md` | modified | 当前 H1 契约 |

未修改 `docs/codex/execution-reports/2026-07-13-hukou-hardening-release.md`。

## 运行命令与结果

| 命令 | 结果 | 说明 |
|---|---|---|
| `bash -n scripts/release.sh` | pass | shell 静态语法 |
| `make -n fmt-check vet test race coverage build release` | pass | 只展开命令，未执行 Go 工具链 |
| Ruby YAML parse 两个 workflow | pass | YAML 基础语法 |
| Markdown 相对链接只读检查 | pass | 当时无缺失目标 |
| `git diff --check` | pass | 当时无 whitespace error |
| Go build/test/vet/race/coverage | pass | 最终集成阶段由 H1 全仓验证执行 |
| release snapshot 等价回退 | pass | 两个全新 Go/Linux 容器运行正式脚本，结果逐字节一致 |

最终集成阶段随后运行 `make verify COVERAGE=/tmp/hukou-prepush-final.out` 并通过，包含 module verify、vet、全仓 test、race、79.0% coverage 与 build；四个目标的独立交叉构建、隔离 GNU tar 双构建和正式 Release 均已通过。GitHub-hosted CI/snapshot/tag workflow 因账户 payment/spending limit 在 0 step 前失败，不声称远端 runner 门禁通过。

## 验证结果

- 已验证：文档链接、YAML 基础语法、release shell 语法、Makefile、diff whitespace、Go 全量门禁、四平台构建、GNU tar 双构建逐字节复现、远端 Release 资产。
- 基础设施例外：GitHub Actions 服务端未分配 runner；三个最终 run 均在 0 step 前被账户计费限制拒绝。
- 需要 `$pinhaoma-review` 回顾：是。

## 未完成事项

- 计费/额度恢复后重跑 GitHub-hosted CI 与手动 snapshot，作为补充证据。
- 不移动 `v0.1.0` tag，不覆盖已发布资产；后续修复使用 patch release。

## 下一步建议

1. 恢复 GitHub Actions 计费后重跑 runner gate。
2. `$pinhaoma-review` 核对本记录 C1-C7 与最终 release report。
