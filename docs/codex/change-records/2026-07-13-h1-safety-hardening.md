# Codex 改动记录：H1 安全硬化

## 元信息

- Change ID: `CHANGE-20260713-h1-safety-hardening`
- Execution Report: `docs/codex/execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `docs/codex/verification-reports/2026-07-13-hukou-hardening-local.md`
- Status: implemented
- Verification Status: partial（本地 L1-L4 通过，远端 L5/Release 待执行）
- Branch: `codex/hukou-hardening-release`

## 用户请求

在全局审阅和详细方案之后，不再设置审批停点，直接完成安全硬化、验证、GitHub 合入和首个可用版本发布。

## Claims vs expected evidence

| ID | 声称完成项 | 预期证据 |
|---|---|---|
| H1-C1 | checksum asset 存在时严格解析；缺目标条目、非法摘要、冲突或不匹配均失败关闭；精确 sidecar 支持单摘要 | `internal/verify`, `cmd/upgrade.go` 及 checksum E2E |
| H1-C2 | 已知不支持的归档容器不会被当作裸二进制；ZIP 只提取最终普通文件；结果可执行 | `internal/archive`, `internal/assetpick` 单测 |
| H1-C3 | GitHub API 与下载客户端超时分离，下载继续受 host、redirect、大小约束 | `internal/ghrelease` 单测 |
| H1-C4 | upgrade/rollback 在可观测错误和 manifest 保存失败时恢复原 regular/symlink 拓扑与旧 manifest | `internal/store/transaction.go`, `cmd/*`, 失败注入 E2E |
| H1-C5 | adopt、真实 upgrade、rollback 使用同一 data-root 进程锁；Unix 拒绝锁路径软链 | `internal/state`, 同进程/子进程锁测试 |
| H1-C6 | dry-run 不获取锁、不 GC、不下载、不创建 data root；adopt local 不绕过归属闸门且不覆盖同名登记 | `cmd/e2e_test.go` |
| H1-C7 | upgrade 激活前二次检查 live SHA，避免下载窗口内外部更新被覆盖 | 外部变更 E2E |
| H1-C8 | manifest 分开记录下载资产与 active binary 的 hash/checksum 证据；hukou detector 重新核对 live SHA | manifest/provenance round-trip 与漂移测试 |
| H1-C9 | CLI 提供可注入的 release buildinfo 与 `version` 命令 | `internal/buildinfo`, `cmd/version.go` |

## 实际修改范围

- 命令层：`cmd/adopt.go`, `cmd/upgrade.go`, `cmd/rollback.go`, `cmd/helpers.go`, `cmd/state_transaction.go`, `cmd/version.go` 与 E2E。
- 核心：`internal/archive`, `assetpick`, `ghrelease`, `manifest`, `provenance`, `state`, `store`, `verify`, `buildinfo`。
- 机械格式修正：四个既有 `internal/provenance/*_test.go`，用于满足全仓 `gofmt -l .` 门禁，不改变测试语义。
- 关联工程与文档改动由 `CHANGE-20260713-docs-ci-release-foundation` 记录。

## 已运行的定向验证

| 命令/范围 | 结果 |
|---|---|
| archive / assetpick / ghrelease 初始子任务测试 | pass |
| manifest / verify 初始子任务测试 | pass |
| provenance 定向测试 | pass |
| cmd / archive / assetpick / verify / state 修复后定向测试 | pass |
| Unix state 包 Linux/FreeBSD/NetBSD/OpenBSD 交叉编译 | pass |
| `make verify COVERAGE=/tmp/hukou-final-precommit.out` | pass：module verify、vet、test、race、coverage、build |
| `go test -json -count=1 ./...` | pass：41 个具名测试，无缓存复跑 |
| `go tool cover -func=/tmp/hukou-final-precommit.out` | pass：总语句覆盖率 78.9% |
| Darwin/Linux × amd64/arm64 交叉构建及格式检查 | pass：2 个 Mach-O、2 个静态 ELF |
| 隔离 CLI smoke（临时 HOME/PATH/data root） | pass：version、scan、adopt local、list、hukou 来源复核、upgrade local dry-run |
| workflow YAML、release shell、`gofmt -l .`、`git diff --check` | pass |

远端 Linux/macOS release gate、snapshot workflow 与 GitHub Release 证据将在提交后写入 verification report；本记录不提前声称这些远端门禁已通过。

## 已知边界

- H1 处理正常进程内可观测错误，不声称断电或 `SIGKILL` crash-safe；WAL/doctor 留 H2。
- release 完全没有 checksum asset 时继续安装，但记录 `asset_sha256` 并保持 `checksum_verified=false`；这是明确产品策略，不称为发布方校验。
- Windows 未列入首发目标；非 Unix 锁只保留编译 fallback，不声明 crash recovery。
- 失败可能留下不可达 store 版本；不会切换 live path，后续由 doctor/GC 审计处理。

## 下一步

1. 推送分支，等待 Linux/macOS CI 与手动 snapshot workflow。
2. 合入 main、创建 `v0.1.0` tag，核对 Release assets/checksums。
3. 创建最终 verification report，并把本记录的 Verification Status 更新为 pass/partial/fail。
