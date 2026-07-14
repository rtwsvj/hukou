# hukou 文档入口

## 用途

本目录是项目当前事实、长期决策和 Codex 操作证据的统一入口。阅读者不应从某一份历史 `*-DONE.md` 推断当前行为；历史记录只证明当时声称完成过什么。

## 当前状态

| 领域 | 状态 | 当前证据入口 |
|---|---|---|
| Phase 1：PATH 扫描与溯源 | 已实现 | `specs/phase1-scan.md`、`records/` |
| Phase 2：收编、升级、回滚、清单 | 已实现 | `specs/phase2-adopt-upgrade.md`、`records/` |
| H1 安全硬化 | 已交付；GitHub-hosted runner 计费例外已记录 | `codex/verification-reports/2026-07-13-hukou-hardening-local.md` |
| 首个 SemVer 发布 | [`v0.1.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0) 已发布 | `codex/verification-reports/2026-07-13-v0.1.0-release.md` |
| H2 恢复与诊断基础 | 已随 [`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) 发布并完成验证；后续边界见路线图 | `codex/verification-reports/2026-07-14-h2-recovery-doctor.md`、`codex/verification-reports/2026-07-14-v0.2.0-release.md` |
| 风险提示、生态导出、Windows | 未实现 | `02-roadmap.md` |

任何“通过”结论必须同时给出提交号和 `codex/verification-reports/` 中的报告。当前执行报告不等于验证报告。

## 事实源优先级

1. 用户行为：根 `README.md`、`05-cli-reference.md`（命令真相最终以当前代码为准）。
2. 需求与安全不变量：`01-requirements.md`、已批准规格、ADR。
3. 架构与数据：`03-architecture.md`、`04-data-and-api.md`。
4. 验证与发布：`07-testing-and-verification.md`、`09-release.md`。
5. 当前进度：`02-roadmap.md`、Codex change/verification records。
6. 历史材料：`pinhaoma-report.md`、`records/*-DONE.md`。

出现矛盾时，不静默选择其中一份：在 `09-decision-log.md` 记录裁决，并同步更新受影响的规格与用户文档。

## 文档地图

- [`00-project-brief.md`](00-project-brief.md)：目标、用户和边界
- [`01-requirements.md`](01-requirements.md)：功能需求与安全不变量
- [`02-roadmap.md`](02-roadmap.md)：阶段进度和未完成项
- [`03-architecture.md`](03-architecture.md)：模块与关键流程
- [`04-data-and-api.md`](04-data-and-api.md)：manifest、store、环境变量和外部 API
- [`05-cli-reference.md`](05-cli-reference.md)：命令、旗标与副作用
- [`06-dev-setup.md`](06-dev-setup.md)：开发、构建和本地隔离
- [`07-testing-and-verification.md`](07-testing-and-verification.md)：验证分层与证据规则
- [`08-risk-and-debt.md`](08-risk-and-debt.md)：风险、限制和技术债
- [`09-decision-log.md`](09-decision-log.md)：重要决策索引
- [`09-release.md`](09-release.md)：版本与发布流程
- [`10-glossary.md`](10-glossary.md)：术语
- [`pinhaoma-sources.md`](pinhaoma-sources.md)：历史调研来源、许可证与复用边界
- [`adr/`](adr/)：不可轻易反转的技术决策
- [`codex/`](codex/)：执行、改动和验证记录
- [`records/`](records/)：历史阶段完成记录
- [`specs/`](specs/)：阶段规格

## 维护时机

- 命令、旗标、环境变量变化：更新根 README、CLI reference、dev setup。
- 数据模型或安全语义变化：更新 requirements、data/API、风险文档和 ADR。
- 阶段状态变化：更新 roadmap；只有验证报告完成后才能标记“已验证”。
- 每次 Codex 修改：先有 execution report，后有 change record，验收后补 verification report。
