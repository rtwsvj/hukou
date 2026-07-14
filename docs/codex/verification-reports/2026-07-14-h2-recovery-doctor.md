# Verification Report：H2 崩溃恢复与只读 doctor

## 元信息

- Verification ID: `VERIFY-20260714-h2-recovery-doctor`
- Date: 2026-07-14
- Subject Commit: `fa00ac1f4c3b2073828b7479248ab020b3c24495`
- Branch: `codex/hukou-h2-recovery-doctor`
- Environment: macOS 27.0 Darwin 27.0.0 arm64；原生 Linux/arm64 容器
- Go: `go1.26.5 darwin/arm64`；`golang:1.26.5-bookworm`
- Verdict: **pass**
- Remote Gate: PR 尚未推送；不把 GitHub Actions 结果计入本报告的本地 verdict
- Related Change: `CHANGE-20260714-h2-recovery-doctor`

该 commit 的 macOS 普通、race、压力、乱序、覆盖率、四目标构建、隔离 CLI smoke，以及非 root Linux/arm64 普通与 race 全量验证均通过。验证没有读写真实 hukou 状态或替换真实 PATH 二进制。

## Claims vs Evidence

| Claim | 核验方式 | 结果 |
|---|---|---|
| PREPARED 恢复 before、durable COMMIT 恢复 after | 四种 live/manifest crash-visible 组合、真实子进程 `Process.Kill`、重复恢复 | pass |
| unknown drift 在预检/写入前复核时失败关闭并保留 WAL | 预检第三态、existing→regular/delete/symlink hook 竞争测试 | pass |
| absent 安装不覆盖竞争者 | hard-link/symlink no-replace 提交点测试 | pass |
| 可见但未确认 durable 的 fast path 可在重试时补同步 | matched regular/symlink/absent、Sync 失败保留 pending、MkdirAll/RemoveAll 重试测试 | pass |
| Store.Put 可恢复空 tag 目录与 link-visible 未同步状态 | 空目录续写、link 后故障、file/dir 再同步与重试测试 | pass |
| manifest 主文件/backup 拒绝 symlink/非 regular，并保存上一份可解码受支持 schema | 首次/后续 Save、恶意 backup、损坏主文件、atomicity/durability tests | pass |
| 写命令在锁内、业务读取/GC/network 前恢复；dry-run 保持零写 | command transaction E2E、missing-root 与 pending tests | pass |
| `upgrade --all` 留下 pending 后不继续下一项网络/store | 两工具批次与请求计数测试，50 次定向复跑 | pass |
| doctor 零写、零网络、稳定且抗文本控制字符注入 | missing-root、损坏拓扑、双 case-alias 50 次稳定 JSON、DataRoot/finding 转义测试 | pass |
| list/provenance 不吞 pending 或异常 store | list 与 provenance 单测；Versions 只读不调用 durability primitive | pass |
| Linux 目录同步运行时路径可用 | 非 root 原生 Linux/arm64 容器普通与 race 全仓运行 | pass |

## 已运行命令

| 命令/检查 | Exit | 关键结果 |
|---|---:|---|
| `make verify COVERAGE=/tmp/hukou-fa00ac1.out` | 0 | mod verify、vet、test、race、coverage、build 全绿 |
| `go test -json -count=1 ./...` | 0 | 401 passed / 14 个含测试包；全仓 16 packages |
| `go test -race -count=1 ./...` | 0 | 401 passed / 16 packages |
| transaction 关键路径 `-count=50` | 0 | 950 passed |
| store durability `-count=50` | 0 | 300 passed |
| batch-stop `-count=50` | 0 | 50 passed |
| doctor 稳定性/注入 `-count=100` | 0 | 200 passed |
| `go test -shuffle=on -count=3 ./...` | 0 | 1203 passed / 16 packages |
| `go tool cover -func=/tmp/hukou-fa00ac1.out` | 0 | total statements 73.8% |
| Linux/arm64 非 root 容器 `go test -mod=readonly -count=1 ./...` | 0 | 16 packages；source 与 module cache 只读挂载 |
| 同容器 `go test -mod=readonly -race -count=1 ./...` | 0 | 16 packages |
| `CGO_ENABLED=0 GOOS={darwin,linux} GOARCH={amd64,arm64} go build` | 0 | Mach-O x86_64/arm64；静态 ELF x86-64/aarch64 |
| workflow Ruby YAML parse | 0 | `ci.yml`、`release.yml` 可解析 |
| `bash -n scripts/release.sh` | 0 | shell 语法通过 |
| Markdown 相对链接检查 | 0 | 48 个 Markdown 文件无缺失相对目标 |
| `gofmt -l .` / `git diff --check` | 0 | 无输出 |
| 隔离 CLI smoke | 0 | version、help、doctor JSON；不存在的 `HUKOU_DATA_DIR` 保持零写 |

## 独立只读回顾

三路并行回顾分别审查 WAL/命令接入、doctor/read-only 输出、durablefs/manifest/store。回顾最初发现并已在 subject commit 前关闭：

1. 已匹配恢复状态和既有/已缺失目录 fast path 未补 file/parent sync。
2. Store.Put 在 mkdir→link 与 link→directory-sync 窗口不可安全重试。
3. `upgrade --all` 在某项留下 pending 后仍可能继续网络与 store 活动。
4. 首次 manifest Save 未验证预置 `.bak` 拓扑。
5. existing→regular/delete/symlink 在初次分类后缺少写入前二次复核。
6. doctor case-alias map first-match 不稳定，DataRoot 文本可含控制字符伪造行。
7. `PendingError` 对损坏/unknown journal 过度承诺必然恢复。

最终复核与新增定向测试未发现残留 P0/P1。保留的非协作 writer TOCTOU 已在 ADR、架构和风险文档中明确，不把它包装成原子 CAS 保证。

## 未运行与限制

- 未模拟存储控制器、磁盘介质或文件系统掉电缓存重排；真实子进程 kill 只证明进程级中断恢复。
- 未做公共网络 fixture repo smoke；GitHub API/下载仍由 `httptest` 覆盖。
- 未验证 Windows crash/directory-sync 运行时语义，也不声明 Windows 支持。
- doctor 不自动 repair、删除 orphan、恢复旧无 transaction ID 的 snapshot，属于刻意安全边界。
- 一次 Linux/amd64 QEMU 容器尝试在依赖下载/模块索引阶段由 Go 工具链自身 SIGSEGV，项目测试尚未开始；该模拟器故障不计作代码失败或通过。amd64 由静态交叉构建覆盖，Linux 运行时由原生 arm64 容器覆盖。
- PR CI、合并 commit、tag workflow 与 release assets 将在推送后由单独发布/收尾证据补充。
