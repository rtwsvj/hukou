# Codex 改动记录：H1 安全硬化

## 元信息

- Change ID: `CHANGE-20260713-h1-safety-hardening`
- Execution Report: `docs/codex/execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `docs/codex/verification-reports/2026-07-13-hukou-hardening-local.md`
- Status: implemented
- Verification Status: pass-with-infrastructure-exception（Release 通过；GitHub-hosted runner gate 因计费限制未执行）
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
| H1-C10 | live path 使用 regular-file copy + fsync + rename；original/版本 write-once，regular snapshot 独立，不共享 live inode | `internal/store`, regular 原子观察与 rollback→upgrade E2E |
| H1-C11 | store 内部目录精确拼写且拒绝 case alias/symlink escape；`.tmp`/`original` 保留名和非法 adopt tag 在持久化前失败关闭 | store 路径边界单测与 invalid-tag E2E |

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
| `make verify COVERAGE=/tmp/hukou-prepush-final.out` | pass：module verify、vet、test、race、coverage、build |
| `go test -json -count=1 ./...` | pass：13 packages / 325 passed，无缓存复跑 |
| `go tool cover -func=/tmp/hukou-prepush-final.out` | pass：总语句覆盖率 79.0% |
| store/命名空间/补偿路径定向压力 | pass：500 次 store/invalid-tag + 200 次 upgrade/rollback 补偿 |
| Darwin/Linux × amd64/arm64 交叉构建及格式检查 | pass：2 个 Mach-O、2 个静态 ELF |
| 隔离 CLI smoke（临时 HOME/PATH/data root） | pass：version、scan、adopt local、list、hukou 来源复核、upgrade local dry-run |
| workflow YAML、release shell、`gofmt -l .`、`git diff --check` | pass |

GitHub Release 与本地等价 release gate 已完成。GitHub-hosted CI/snapshot/tag workflow 因账户 payment/spending limit 在 0 step 调度前失败；本记录不把它写成代码失败，也不声称远端 runner 门禁通过。最终证据见 `2026-07-13-v0.1.0-release.md`。

## 已知边界

- H1 处理正常进程内可观测错误，不声称断电或 `SIGKILL` crash-safe；WAL/doctor 留 H2。
- release 完全没有 checksum asset 时继续安装，但记录 `asset_sha256` 并保持 `checksum_verified=false`；这是明确产品策略，不称为发布方校验。
- Windows 未列入首发目标；非 Unix 锁只保留编译 fallback，不声明 crash recovery。
- 失败可能留下不可达 store 版本；不会切换 live path，后续由 doctor/GC 审计处理。
- 文件复制只承诺字节与 rwx 位；owner/group、ACL、xattr、mtime、特殊权限位和 hardlink topology 不保留，adopt 对特权/特殊权限位失败关闭。
- 默认 rollback 依 store mtime 选择最近的其他 tag，不是历史栈；最终 SHA 到 activate 的非协作外部写窄窗口留 H2。

## 发布结果

1. PR #1 已合入 `main@d15331dbe4d258d54253643b758c787bb63c95e1`。
2. annotated `v0.1.0` tag 与非 draft/prerelease GitHub Release 已创建。
3. 四个归档与 `checksums.txt` 远端重新下载后 4/4 checksum 通过，并与验证产物逐字节一致。
4. 计费恢复后重跑 GitHub-hosted CI/release snapshot，作为补充证据，不改写本次基础设施例外。
