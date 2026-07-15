# Verification Report: V0.3 External Audit Handoff

## 元信息

- Verification ID: `VERIFY-20260715-external-audit-handoff`
- Date: 2026-07-15
- Handoff Content Subject: `b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e`
- Parent: `2cd0467098700b899b8b87ee627eb2b75412f397`
- Code Subject Referenced by Handoff: `1fa45a0d8473446e3208490f037aef924abea181`
- Branch: `codex/hukou-v0.3-private-rc`
- Related Change: `CHANGE-20260715-external-audit-handoff`
- Scope: documentation and audit-handoff integrity only
- Verdict: **pass for handoff content；external V0.3 audit pending**

本报告不重新验证 Go 行为，不改变原 V0.3 code verdict，也不声称第三方安全审计、
GitHub-hosted gate、公开发布或 attestation 已通过。

## Claims vs Evidence

| Claim | Evidence at handoff subject | Verdict |
|---|---|---|
| H1 根入口可发现 | `AUDIT.md` 链接 handoff/claims/checklist/artifacts/security | pass |
| H2 审计对象无混淆 | base、code subject、original evidence docs、handoff content 与 moving head 分列；checkout 捕获 `FETCH_HEAD` | pass |
| H3 复现命令安全可读 | Tier A-E、两套 Go toolchain、GNU tar/Syft 依赖、network/write 边界；两个 `mktemp` block fail-fast/subshell | pass |
| H4 C1-C10 可追踪 | source/test/reproduction/caveat map | pass |
| H5 风险未隐藏 | 高优先级 hypotheses 进入 checklist、risk register、release Go/No-Go | pass |
| H6 artifact/platform 不过度声明 | ignored local artifacts、archive/binary hashes、SBOM semantic rule、cross-build/native matrix | pass |
| H7 文档漂移与 internal-review 过度声明已纠正 | ADR/roadmap/Topgrade/brief 统一；`pinhaoma-review` 标为内部、无 raw report | pass |
| H8 代码与发布状态未改变 | 28-file subject 全为 Markdown；无 Go/shell/workflow/module change | pass |

## 命令与结果

| Check | Exit | Result |
|---|---:|---|
| `git status --short --branch` after content commit | 0 | clean；branch ahead of remote by one content commit |
| `git diff --check b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e^ b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e` | 0 | no whitespace errors |
| `git diff-tree --no-commit-id --name-only -r b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e` | 0 | 28 paths；all Markdown |
| DCO commit message inspection | 0 | `Signed-off-by: rtwsvj <rsxedeg@gmail.com>` present |
| Markdown relative-link scan | 0 | 77 Markdown / 115 relative targets / 0 missing |
| Markdown relative-link scan after adding this report | 0 | 78 Markdown / 115 relative targets / 0 missing |
| `gitleaks git --log-opts='b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e^..b60e890a6a5b9bbb9ad8f18bd96c8fbdf7b6139e'` | 0 | 1 commit、69.62 KB、no leaks |
| `gitleaks detect --no-git --source docs/audit` | 0 | 41.06 KB、no leaks |
| Tier D fenced Bash extraction + `bash -n` | 0 | syntax valid；build/SBOM commands not executed |
| Isolated-smoke fenced Bash extraction + `bash -n` | 0 | correct lines 268-281 syntax valid；smoke not executed |
| Four Codex read-only handoff reviews | resolved | gap/reproduction/attack-surface findings incorporated；final QC pass；unresolved documentation blocker 0 |

一次初始 smoke-block 语法检查错误截取了文档 265-278 行，包含 Markdown backticks，
因此 `bash -n` exit 2。使用真正 code block 268-281 复核后 exit 0；这是验证 harness
的行范围错误，不是交接示例语法失败，故在此保留而不抹去。

## 已核实的 Git/GitHub 边界

在 handoff content commit 前的 hosted refresh 中：

- repository 为 PRIVATE；
- PR #6 为 open/draft/unmerged；
- latest official Release 为 `v0.2.0`；
- remote 无 `v0.3*` tag；
- original evidence-docs head `2cd0467` 的 CI run `29353234544` 五个 jobs
  `steps=[]`，billing/spending limit 阻断；
- CodeQL run `29353234543` 因 private 条件 skipped。

这些 hosted facts 会变化。审计员必须在报告时重新查询；本 verification 不把它们
写成远程绿色。

## 未验证与保留边界

- 没有在本轮重跑 Go tests、race、release-verify、四目标 build、Linux container 或
  SBOM generation；handoff 只提供第三方复现方法。
- 原 V0.3 执行未保存完整 container command、全部 raw test/static logs 或 standalone
  `pinhaoma-review` report，交接文档已明确披露。
- Download/global cap、archive/member work budget、manifest/API body cap、intent
  trust anchor、repair replay、no-checksum policy、private→public attestation 与
  Go toolchain hypotheses 均未被本报告关闭。
- External auditor 尚未开始；不得把此 `pass` 当作产品安全 clean bill。

## 复核结论

交接包已经达到“第三方可以确定对象、独立重跑、知道证据缺口并按模板提交 finding”
的文档交付标准。下一步是推送 verification-only commit，并由第三方在自己的固定
documentation head 与 code subject 上执行审计。
