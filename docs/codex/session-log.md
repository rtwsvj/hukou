# Codex Session Log

## 2026-07-13 — H1 安全硬化与首个发布

- Branch: `codex/hukou-hardening-release`
- Base: `main@afed279`
- Execution Report: `execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `verification-reports/2026-07-13-hukou-hardening-local.md`
- Release Report: `verification-reports/2026-07-13-v0.1.0-release.md`
- Status: completed with GitHub Actions infrastructure exception
- Scope: 安全事务、状态锁、审计字段、文档事实源、CI、四平台发布与 `v0.1.0`
- Boundary: 所有危险验证只在临时 HOME/PATH/data root；仓库 private，不创建原创代码根 LICENSE
- Verification: local L1-L4、隔离 Linux release gate 与 Release pass；GitHub-hosted runner gate infrastructure-blocked

### Docs / CI / release foundation

- Change Record: `change-records/2026-07-13-docs-ci-release-foundation.md`
- Status: implemented and verified with the release-wide infrastructure exception
- Notes: 文档、CI 与 release foundation 已进入 `main`；最终代码/发布验证由两份 verification report 记录

完成条件：change record 与 verification report 均落盘，H1 roadmap 状态与实际证据一致。已完成。

### 本地集成门禁

- `make verify`：pass。
- uncached tests：13 packages / 325 tests pass。
- race：pass。
- coverage：79.0%。
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
- Verification: `make verify`、13 packages / 325 tests、race、79.0% coverage、四目标交叉构建、store/补偿压力通过；第三次远端 CI 被计费限制在 0 step 前拒绝。

### 合并、发布与基础设施例外

- PR #1 merge commit: `d15331dbe4d258d54253643b758c787bb63c95e1`。
- CI Run `29268485688`：四个 job 均因账户 payment/spending limit 在 runner 调度前失败，steps 为空；非代码失败。
- Snapshot Run `29268603489` 与 tag Run `29268975174`：同一计费原因在 0 step 前失败。
- Fallback: 临时启动原本停止的 Colima，用两个全新 `golang:1.26.5-bookworm` 容器分别运行正式 `scripts/release.sh`；四个归档逐字节相同，4/4 checksum 与双平台 buildinfo smoke 通过；Colima 已恢复停止。
- Release: `https://github.com/rtwsvj/hukou/releases/tag/v0.1.0`，非 draft/prerelease，四个 tar.gz + `checksums.txt`；远端重新下载与本地产物逐字节一致。
