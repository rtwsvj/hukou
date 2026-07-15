# Execution Report: V0.3 External Audit Handoff

## 元信息

- Execution ID: `EXEC-20260715-external-audit-handoff`
- Date: 2026-07-15
- Branch: `codex/hukou-v0.3-private-rc`
- Code Subject: `1fa45a0d8473446e3208490f037aef924abea181`
- Documentation Baseline: `2cd0467098700b899b8b87ee627eb2b75412f397`
- Approval: 用户已要求准备外部审计交接材料；沿用已批准的持续文档维护范围
- Mutation Boundary: 仅文档与审计索引；不改业务代码、测试、workflow 或发布对象

## 用户请求

用户准备让其他人审计项目，要求把相关内容准备完整，以便第三方直接接手。

## 目标理解

交付一个不依赖 Codex 会话历史的审计入口。审计员应能独立确认：

1. 应审哪个 commit，以及为什么 branch HEAD 与 code subject 不同。
2. V0.3 声称完成了什么，每条 claim 对应哪些代码、测试和复现命令。
3. 哪些证据是可重新生成的，哪些只是原执行者记录、不能替代复现。
4. 当前已知风险、未完成门禁和禁止声明是什么。
5. 如何在不触碰真实 HOME、PATH 二进制或 hukou data root 的情况下开始核验。

## 计划修改文件

| 文件 | 动作 | 目的 |
|---|---|---|
| `AUDIT.md` | 新建 | 仓库根部的第三方审计入口 |
| `docs/audit/README.md` | 新建 | 审计包地图、对象与事实源优先级 |
| `docs/audit/v0.3-private-rc-handoff.md` | 新建 | checkout、环境、分层复现和结果提交方式 |
| `docs/audit/v0.3-claims-evidence.md` | 新建 | C1-C10 claims-to-evidence 映射 |
| `docs/audit/v0.3-review-checklist.md` | 新建 | 高风险攻击面、已知缺口和审计问题 |
| `docs/audit/v0.3-artifact-and-platform-evidence.md` | 新建 | artifact 哈希、可移交性、native/build 与 toolchain 矩阵 |
| `docs/audit/external-finding-template.md` | 新建 | 第三方 finding 与最终报告模板 |
| `README.md`、`docs/README.md`、`docs/codex/README.md` | 更新 | 把审计入口加入当前文档地图 |
| project brief、roadmap、testing、risk、release、ADR/spec/integration | 更新 | 消除旧状态漂移并登记新审计 hypotheses |
| `docs/codex/session-log.md` | 更新 | 记录本轮交接任务 |
| `docs/codex/change-records/2026-07-15-external-audit-handoff.md` | 新建 | 可核验的实际改动记录 |
| `docs/codex/verification-reports/2026-07-15-external-audit-handoff.md` | 新建 | 只验证交接包本身，不重复声称代码测试 |

## 计划运行命令

| 命令类别 | 目的 | 副作用 |
|---|---|---|
| `git status/log/diff/show` | 固定代码与文档 SHA、检查范围 | 只读 |
| `gh repo/pr/run/release view` | 捕获当前 GitHub visibility、PR 和 hosted gate 状态 | 只读网络 |
| Markdown relative-link checker | 检查交接包没有断链 | 只读 |
| `git diff --check` | 检查文档 whitespace | 只读 |
| `gitleaks detect --no-git`（若可用） | 防止交接材料误含 secret | 只读扫描 |

本轮不重新运行全仓 Go、race、release build 或 Linux 容器测试。那些行为结果继续绑定
到 code subject 的既有 verification report；交接包只提供第三方可重跑的命令，不把旧
结果冒充本轮新结果。

## 预期交付物

- 一个从根目录可发现的外部审计入口。
- 明确区分 base、code subject、documentation head、official release 和 local-only artifacts。
- 可复制的快速、完整和发布复现命令。
- 每条 V0.3 claim 的源码、测试、文档和 caveat 索引。
- 不隐藏已知缺口的优先审计清单。

## 风险与回滚

- 风险：复制旧报告中的结果时形成二次过度声明。
  - 控制：所有历史结果标记为 recorded evidence；第三方复跑结果单独记录。
- 风险：把本地 ignored release snapshot 写成 GitHub 可下载资产。
  - 控制：明确标注 local-only/unavailable to reviewer，要求重建。
- 风险：branch HEAD 文档提交被误当作已完整测试的代码 subject。
  - 控制：每个入口同时写出两个完整 SHA。
- 回滚：本轮仅新增/更新 Markdown，可通过回退本轮文档 commit 完整撤销。

## 验证方式

1. 所有新相对链接存在。
2. 所有 SHA、PR、visibility、release 与 hosted run 状态和 Git/GitHub 当前事实一致。
3. Claims 表中的文件和测试符号在 code subject 中存在。
4. 文档明确保留 remote billing block、private/no-tag/no-release 边界。
5. 独立只读复核不发现把未运行门禁写成通过的表述。
