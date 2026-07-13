# Codex Session Log

## 2026-07-13 — H1 安全硬化与首个发布

- Branch: `codex/hukou-hardening-release`
- Base: `main@afed279`
- Execution Report: `execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `verification-reports/2026-07-13-hukou-hardening-local.md`
- Status: in progress
- Scope: 安全事务、状态锁、审计字段、文档事实源、CI、四平台发布与 `v0.1.0`
- Boundary: 所有危险验证只在临时 HOME/PATH/data root；仓库 private，不创建原创代码根 LICENSE
- Verification: local L1-L4 pass；remote snapshot / Release pending

### Docs / CI / release foundation

- Change Record: `change-records/2026-07-13-docs-ci-release-foundation.md`
- Status: implemented, static checks passed, full verification pending
- Notes: 未修改既有 H1 execution report；未运行 Go 构建或测试

完成条件：change record 与 verification report 均落盘，H1 roadmap 状态与实际证据一致。

### 本地集成门禁

- `make verify`：pass。
- uncached tests：41 个具名测试 pass。
- race：pass。
- coverage：78.9%。
- Darwin/Linux × amd64/arm64 交叉构建：pass。
- 隔离 CLI smoke：pass；未触碰真实 manifest/store。

### PR CI 第一次运行

- PR: `https://github.com/rtwsvj/hukou/pull/1`
- Run: `29265826948`
- Ubuntu、quality、coverage：pass。
- macOS：并发替换 symlink inode 时 `ReadFile` 收到 `EINVAL`；改成单次 `Readlink` 后进入第二次远端运行。

### PR CI 第二次运行与 regular-file 迁移

- Run: `29266208797`
- Ubuntu、quality、coverage：pass。
- macOS：唯一一次 `Readlink` 仍返回 `EINVAL`，确认是 macOS/APFS symlink 交换语义，不是测试的双 lookup 假阳性。
- Decision: `ADR-0002`；live 改为同目录完整 regular-file copy + `fsync` + rename。
- Commit: `513d8bb87f1931c152c7e336faf7bc4a5e4d02b3`。
- Additional hardening: original/version write-once、regular 独立 snapshot、post-snapshot SHA、pre-activation 错误只丢弃 snapshot、精确 tag+SHA Prune、case alias/内部 symlink escape/非法 adopt tag fail closed。
- Verification: `make verify`、13 packages / 325 tests、race、79.0% coverage、四目标交叉构建、store/补偿压力通过；等待第三次远端 CI。
