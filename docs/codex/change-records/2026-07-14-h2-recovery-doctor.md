# Change Record：H2 崩溃恢复与只读 doctor 基础

## 元信息

- Change ID: `CHANGE-20260714-h2-recovery-doctor`
- Date: 2026-07-14
- Execution Report: `../execution-reports/2026-07-14-h2-recovery-doctor.md`
- Branch: `codex/hukou-h2-recovery-doctor`
- Subject Commit: `fa00ac1f4c3b2073828b7479248ab020b3c24495`
- Status: implemented
- Verification Status: pass (`../verification-reports/2026-07-14-h2-recovery-doctor.md`)

## 用户请求

在 `v0.1.0` 已发布后继续关闭尚未做好的部分，自主推进到可用发布并推送 GitHub。

## Claims 与预期证据

| ID | Claim | 主要证据 |
|---|---|---|
| H2-C1 | adopt、upgrade、rollback 在改变 original/live/manifest 前发布单全局 before/after WAL | `internal/transaction/`、`cmd/{adopt,upgrade,rollback}.go`、命令事务 E2E |
| H2-C2 | PREPARED 确定回滚、durable COMMIT 确定前滚；恢复可重入，已匹配 fast path 也重新确认 file/parent durability | crash matrix、真实子进程 kill、二次恢复与 durability fault tests |
| H2-C3 | 预检和写入前复核发现第三态时失败关闭并保留 journal；absent 安装使用原子 no-replace | unknown drift、existing/absent 竞争测试；风险文档记录最终 syscall 前 TOCTOU 边界 |
| H2-C4 | manifest、store、live、journal namespace mutation 在成功前同步文件及相应目录；不确定 fast path 可重试收敛 | `internal/durablefs/`、manifest/store durability tests |
| H2-C5 | manifest Save 拒绝 symlink/非 regular 主文件与 backup，并保留上一份可解析且 schema 受支持的 `.bak` | manifest 保存、备份、首次保存恶意拓扑测试 |
| H2-C6 | `hukou doctor [--json] [--deep]` 默认零写、零网络，稳定报告 manifest/live/store/transaction/orphan 状态 | `internal/doctor/`、`cmd/doctor.go`、missing-root 与 deterministic tests |
| H2-C7 | list、scan provenance 与 dry-run 不在未决事务上静默读取；`upgrade --all` 留下 pending 后立即停止后续网络/store 工作 | list/provenance/pending tests、upgrade batch stop test |
| H2-C8 | 文档明确区分协作事务保证、硬件掉电限制、只读时间点诊断与非协作 writer TOCTOU | ADR-0003、architecture、risk、CLI/testing/roadmap |

## 实际修改

- 新增 `internal/transaction/`：durable intent/payload/COMMIT、PREPARED/COMMITTED 恢复、只读 inventory、可重入清理。
- 新增 `internal/durablefs/`：file/dir sync、原子写、同目录 rename、hard-link、持久化 mkdir/remove 原语。
- 新增 `internal/doctor/` 与 `cmd/doctor.go`：文本/JSON 只读诊断、深度 hash、稳定排序与控制字符转义。
- adopt、upgrade、rollback 接入事务；写锁取得后在 manifest/GC/network 前恢复；dry-run 只读拒绝 pending。
- store 的 Put/Activate/Prune/GC 与 original/live snapshot 使用持久化 namespace 操作；Put 可恢复空 tag 目录和已链接 fast path。
- manifest Save 增加严格拓扑、上一版 backup、schema 负值拒绝、临时文件与目录同步。
- list 与 hukou provenance detector 在读取 manifest 前检查未决事务，异常 store 不再被吞成零版本。
- 更新 README、需求、路线图、架构、CLI、数据、测试、风险、术语、规格和 ADR 索引。

## 当前验证证据

以下结果已在 subject commit 上复跑；完整证据见对应 verification report：

| 检查 | 结果 |
|---|---|
| `make verify COVERAGE=/tmp/hukou-h2-postaudit.out` | pass：mod verify、vet、test、race、coverage、build |
| `go test -json -count=1 ./...` | 401 pass / 14 个含测试包；全仓 16 packages |
| WAL/durability 定向压力 | transaction 950、store 300、batch-stop 50、doctor 200 次 pass |
| `go test -shuffle=on -count=3 ./...` | 1203 pass / 16 packages |
| 覆盖率 | total statements 73.8% |
| Darwin/Linux × amd64/arm64 独立构建 | pass；Mach-O x86_64/arm64、静态 ELF x86-64/aarch64 |
| 原生 Linux/arm64 非 root 容器 `go test` + `-race` | pass / 16 packages；源码与模块缓存只读挂载 |
| YAML、shell、Markdown 相对链接、gofmt、diff | pass |
| 隔离 CLI smoke | version/help/doctor JSON pass；不存在的 data root 保持零写 |

## 未改与边界

- 没有触碰真实用户的 hukou data root、manifest、store 或 PATH 二进制。
- 没有自动 repair、orphan 删除、历史栈、公共网络 fixture 或 Windows crash 支持。
- 不把普通 CI 的真实 `SIGKILL` 测试表述为硬件控制器/文件系统掉电缓存重排证明。
- 非协作 writer 在最终状态复核与 rename/remove 系统调用之间仍有窄 TOCTOU；这是已记录边界，不宣称原子 CAS。
- GitHub-hosted runner 若仍在 0 step 前被 payment/spending limit 拒绝，作为外部基础设施例外单独记录，不改写为代码测试通过。

## 下一步

1. PR #3 已推送；Run `29297898605` 的四个 jobs 均在 0 steps 前被 billing/spending-limit 阻断。
2. 依据本地、原生 Linux 容器与独立回顾证据合并 `main`，随后发布新的 SemVer，不移动 `v0.1.0`。

一次 Linux/amd64 QEMU 容器尝试在下载依赖阶段由 Go 工具链自身触发 SIGSEGV，尚未执行项目测试；随后原生 Linux/arm64 非 root 容器在只读模块缓存下完成普通与 race 全量验证。该模拟器故障保留为环境证据，不计作项目测试失败或通过。
