# Codex Change Record: V0.3 External Audit Handoff

## 元信息

- Change ID: `CHANGE-20260715-external-audit-handoff`
- Date: 2026-07-15
- Branch: `codex/hukou-v0.3-private-rc`
- Base Documentation Commit: `2cd0467098700b899b8b87ee627eb2b75412f397`
- Code Subject: `1fa45a0d8473446e3208490f037aef924abea181`
- Related Execution: `../execution-reports/2026-07-15-external-audit-handoff.md`
- Status: implemented
- Verification Status: pending immutable handoff commit and final document checks
- Mutation Boundary: documentation only

## 用户请求

为即将接手的第三方审计员准备完整材料，使其无需读取 Codex 对话即可确定审计
对象、复现门禁、核对 claims、理解证据限制并提交可行动 finding。

## 声称完成的改动清单

- [x] H1：仓库根部提供可发现的外部审计入口。
  - Evidence expected: `AUDIT.md` 存在，直接链接 handoff、claims、checklist、
    artifact/platform matrix、原 verification report 与安全报告入口。
- [x] H2：base、code subject、original evidence docs 与 moving PR head 被严格分开。
  - Evidence expected: 所有审计文档使用完整 SHA；checkout 流程先 capture fetched
    audit-package SHA，再为 `1fa45a0` 创建独立 worktree。
- [x] H3：第三方可按 tier 重跑定向、全仓、race、release、双构建和 Linux gate。
  - Evidence expected: 命令、环境、网络、GNU tar、Go 1.26.2/1.26.5、Syft、
    destructive `DIST_DIR` 语义和隔离边界均有说明。
- [x] H4：C1-C10 均有 source/test/reproduction/caveat 映射。
  - Evidence expected: `docs/audit/v0.3-claims-evidence.md` 能定位实现 seam 与现有
    test names，且不把原报告当作独立 proof。
- [x] H5：风险 checklist 不隐藏原 RC 未发现或未关闭的攻击面。
  - Evidence expected: download global cap、Go/shell archive budget、manifest/API
    body cap、WAL intent trust anchor、repair replay、store-root trust、checksum
    policy、private→public attestation 与 toolchain hypotheses 明确列出。
- [x] H6：artifact、SBOM、平台与 toolchain 证据不被过度解释。
  - Evidence expected: local ignored artifacts 标为不可从 GitHub 获取；build 与
    native run 分开；SBOM 只要求 semantic reproduction，不要求新 JSON 同 hash。
- [x] H7：原文档状态漂移和“独立 review”过度声明已更正。
  - Evidence expected: ADR/roadmap/Topgrade/project brief 不再写 RC gate 仍 pending；
    原 `pinhaoma-review` 标为 Codex 团队内部记录、无 standalone raw report。
- [x] H8：业务代码和发布状态保持不变。
  - Evidence expected: diff 仅为 Markdown；repository private、PR draft/unmerged、
    official release v0.2.0、无 `v0.3*` remote tag。

## 实际修改范围

| 范围 | 动作 | 说明 |
|---|---|---|
| `AUDIT.md` | created | 外部审计根入口与 immutable object 边界 |
| `docs/audit/` | created | handoff、claims map、checklist、artifact/platform matrix、finding template |
| `README.md`、`README.zh-CN.md`、`CHANGELOG.md` | modified | 暴露交接入口并降级 internal-review 过度声明 |
| `docs/{00-project-brief,02-roadmap,04-data-and-api,07-testing-and-verification,08-risk-and-debt,09-release}.md` | modified | 状态、工具链、风险与公开 Go/No-Go 对账 |
| `docs/README.md`、`docs/codex/README.md` | modified | 文档地图和 evidence rule 更新 |
| `docs/adr/ADR-0004*`、`docs/adr/ADR-0005*` | modified | 清理过时的 RC verification pending 文案 |
| `docs/integrations/topgrade.md`、`docs/specs/v0.3-private-rc.md` | modified | 链接现有报告并区分 internal/external review |
| V0.3 execution/change/verification/session records | modified | 对历史结论增加 2026-07-15 qualification，不删除原记录 |
| 本 execution/change record | created | 保存批准范围与可核验 claims |

## 运行命令与结果

| 命令/检查 | 结果 | 说明 |
|---|---|---|
| Git status/log/diff/show | pass | 固定 base、subject、original docs、diff 和 branch 状态 |
| GitHub repo/PR/run/release/tag read-only queries | pass | private；PR #6 open/draft/unmerged；v0.2.0 latest；无 v0.3 tag |
| Markdown relative-link scan | pass：77 Markdown / 115 relative targets / 0 missing | 包含未提交 audit package 与本 change record |
| `gitleaks detect --no-git --source docs/audit --redact --no-banner` | pass | 41.06 KB；no leaks found |
| Tier D / isolated smoke fenced Bash `bash -n` | pass | 两段示例语法有效；没有执行构建或 smoke |
| `git diff --check` | pass | 无 whitespace error |
| 三路只读审阅 | pass after corrections | gap/reproduction/attack-surface 分别复核边界、命令和风险措辞 |

本轮没有重新运行 Go test、race、build、release、SBOM 或 Linux 容器。旧行为结果仍只
绑定 `1fa45a0` 的原 verification report；本记录不把它们写成本轮新通过。

## 新披露的审计 hypotheses

以下为交接审阅发现、尚待第三方复现和定级的线索，不是本轮已修复项：

1. API asset size 为正时下载器缺少独立全局 ceiling。
2. Go tar first pass 与 shell installer 缺少完整 work/member budget。
3. manifest/API response、installer curl 仍有资源/网络预算缺口。
4. WAL/repair intent path 授权依赖 data root 可信和非提权运行假设。
5. Repair plan 无 expiry，状态精确回到原 fingerprint 时可能复用。
6. 无 publisher checksum 时真实 upgrade 继续的默认策略需公开 Go/No-Go。
7. Private Release 后再公开仓库可能暴露未 attested 资产。
8. `go.mod` 1.26.2 compatibility 尚未由 hosted gate 证明；历史验证为 Go 1.26.5。

## 未完成事项

- 创建 immutable handoff content commit。
- 对该 commit 做最终 Markdown/link/secret/diff/status 核验并保存 verification report。
- 推送分支，在 PR #6 中发布给外部审计员的入口和最终 handoff SHA。
- 外部审计本身尚未开始；任何 finding 状态均不得预填为 pass。
