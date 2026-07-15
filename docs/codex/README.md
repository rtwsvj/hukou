# Codex 工作记录

## 用途

这里把“准备做什么”“实际改了什么”“是否真的通过”分开，供人和 `$pinhaoma-review` 核验。

## 目录

- `execution-reports/`：修改前的批准范围、计划、风险和回滚。
- `change-records/`：实际文件、可核验 claims、命令结果和未完成项。
- `verification-reports/`：独立核验当前 commit 的结果。
- `session-log.md`：按时间记录任务入口与状态变化。
- `../audit/`：第三方接手时使用的固定对象、复现命令、claims map 与 findings 模板。

## 状态规则

- execution report 的 `approved` 只代表允许执行，不代表完成。
- change record 初始 `Verification Status: unverified`。
- 只有 verification report 可以给出 pass/partial/fail/inconclusive。
- 未运行的命令必须写“未运行”，不能引用旧记录冒充本轮结果。
- 影响命令、数据、安全或发布时，还必须同步主文档。

## 标准链路

1. 创建 execution report，并获得批准。
2. 在隔离分支实施。
3. 创建 change record，列 Claims vs expected evidence。
4. 运行验证并创建 verification report。
5. 更新 session log 与 roadmap。
6. 用 `$pinhaoma-review` 对照代码、diff 和证据复核。

内部 `$pinhaoma-review` 不能自动等同于独立外部审计。没有单独 raw report 时必须明确
标注证据缺口，并要求第三方重新运行与保留自己的原始日志。
