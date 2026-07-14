# Codex 执行报告：H2 崩溃恢复与 doctor 基础切片

## 元信息

- Date: 2026-07-14
- Branch: `codex/hukou-h2-recovery-doctor`
- Base: `main@7fc8ffd8a9bc7f7115919be019a1444a8cefa716`
- Status: implemented and locally verified; PR #3 remote gate infrastructure-blocked; publish in progress
- Approval: Eric 明确要求“还有什么没做好的，继续”

## 用户请求

在 `v0.1.0` 已发布的基础上，继续识别并关闭仍未做好的工程缺口，不因先前发布而停止。

## 只读审计结论

1. 当前最高风险缺口是 Activate 与 manifest Save 之间遭遇 `SIGKILL`/断电后可能出现 live 与 manifest 不一致。
2. 单独增加目录 `fsync` 或 manifest 备份不能决定崩溃后应前滚还是回滚，必须有持久化事务状态。
3. 当前缺少默认只读的健康检查入口；manifest 损坏、live SHA 漂移、store 拓扑异常和临时文件残留只能由人工排查。
4. 非当前 tag 的版本通常是合法 rollback retention，不能被 doctor 当作 orphan 自动删除；manifest 外的 tool 目录也只报告，不自动修复。
5. GitHub-hosted Actions 仍被账户 payment/spending limit 在 runner 调度前阻断，属于仓库外条件。

## 本轮目标

1. 增加窄版单事务 undo-WAL，覆盖 adopt、upgrade、rollback 的跨 live/manifest 崩溃恢复。
2. WAL `PREPARED` 时确定性回滚 before，持久化 `COMMITTED` 后确定性前滚 after；外部漂移时失败关闭并保留现场。
3. 为 manifest、live 激活、事务快照、store 提交和 WAL 增加必要的文件与父目录持久化。
4. 增加默认零写入的 `hukou doctor` 文本/JSON 审计，报告 manifest、live、store、临时文件和 pending transaction 状态。
5. 保持旧 manifest schema 与现有 CLI 行为兼容。

## 计划修改范围

- `internal/manifest/`：durable save、上一份可解析且 schema 受支持的 manifest 备份与恢复读取原语。
- `internal/store/`：目录持久化、可序列化 live snapshot/recovery payload、检查接口。
- 新增内部事务/WAL 模块：durable PREPARED/COMMITTED 记录和确定性恢复状态机。
- `cmd/adopt.go`、`cmd/upgrade.go`、`cmd/rollback.go`、事务辅助：接入 WAL 与启动恢复。
- 新增 `cmd/doctor.go` 及测试：默认只读审计，暂不提供危险的 repair-all。
- README、CLI reference、architecture、data/API、testing、risk/roadmap、ADR 与 Codex 记录。

## 明确不做

- 不自动删除 orphan store、异常 live 文件、未知 rollback snapshot 或 manifest 外 tool 目录。
- 不引入无差别 `--repair-all`。
- 不改动真实用户的 hukou data root、manifest、store 或 PATH 二进制。
- 不宣称 Windows 支持，不增加未经治理的 self-hosted runner。
- 不移动或重签 `v0.1.0` tag；后续发布使用新版本。

## 计划验证

- WAL 崩溃阶段矩阵：PREPARED 的 live/manifest before/after 组合、COMMITTED 前滚、外部漂移失败关闭、重复恢复幂等。
- manifest temp/file/parent sync、backup 与损坏主文件场景。
- doctor 对健康、损坏、重复、SHA 漂移、store orphan、临时残留、pending WAL 的文本/JSON结果及零写入证明。
- `make verify`、uncached test、race、coverage、四目标交叉构建。
- 临时 `HUKOU_DATA_DIR` 的 adopt/upgrade/rollback/doctor 隔离 smoke。
- Markdown 相对链接、workflow/script 静态检查与独立只读回顾。

## 风险与回滚

- WAL 是安全关键状态机；任何无法严格绑定 before/after hash 的恢复都必须失败关闭。
- durable rename 已成功但目录 sync 返回错误时，调用方必须保留 WAL，不能假定提交或回滚完成。
- 旧 symlink live path 仍作为兼容恢复输入；新激活继续使用 regular file。
- 所有改动位于独立分支；验证失败可按主题提交 revert，不影响已发布 `v0.1.0`。
