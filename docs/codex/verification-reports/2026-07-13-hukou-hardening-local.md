# Verification Report：H1 本地集成验证

## 元信息

- Verification ID: `VERIFY-20260713-h1-local`
- Date: 2026-07-13
- Subject Commits:
  - `f20c64264720dcc08ac2964aee16bc26608ef120`（H1 implementation）
  - `f3417bd82fe992f3f98596f636d5ec79240a1f5b`（CI 暴露的跨平台测试观察修正）
- Branch: `codex/hukou-hardening-release`
- Environment: macOS Darwin 27.0.0, arm64
- Go: `go1.26.5 darwin/arm64`
- Verdict: **partial**
- Related Changes:
  - `CHANGE-20260713-h1-safety-hardening`
  - `CHANGE-20260713-docs-ci-release-foundation`

`partial` 仅表示远端 GitHub Actions、GNU tar 双构建 snapshot 与 GitHub Release 尚未执行；本地 L1-L4 门禁已通过。

## Claims vs Evidence

| Claim | 核验方式 | 结果 |
|---|---|---|
| checksum 缺条目、非法/冲突摘要与不匹配失败关闭，精确 sidecar 可用 | verify 单测 + cmd mock E2E | pass |
| 不支持容器不退化为 bare，ZIP 只提取目标普通文件 | archive/assetpick 单测 + 无效 bare E2E | pass |
| API 与大文件下载使用不同 timeout，redirect/token/大小边界保留 | ghrelease 单测 | pass |
| upgrade/rollback 可观测失败恢复 live regular/symlink 与旧 manifest | store transaction 单测 + cmd 失败注入 E2E | pass |
| adopt/真实 upgrade/rollback 使用非阻塞进程锁，锁软链被拒绝 | state 同进程/子进程测试 | pass |
| dry-run 不写 data root、不持锁、不 GC、不下载 | cmd E2E | pass |
| 激活前二次 SHA 闸门拒绝下载期间的外部变更 | cmd E2E | pass |
| manifest 区分资产摘要与 active binary 摘要；hukou detector 复核 live SHA | manifest/provenance 单测 | pass |
| CLI buildinfo/version 接线可测试 | version 单测与隔离 smoke | pass |
| 当前工程门禁与四目标独立构建可用 | `make verify` + 交叉构建 | pass |
| GitHub 双平台 release gate 与可重复归档 | 远端 workflow 尚未执行 | pending |
| `v0.1.0` Release 及五个远端资产 | 尚未创建 tag | pending |

## 已运行命令

| 命令/检查 | Exit | 关键结果 |
|---|---:|---|
| `make verify COVERAGE=/tmp/hukou-final-precommit.out` | 0 | module verify、vet、test、race、coverage、build 全绿 |
| `go test -json -count=1 ./...` | 0 | 41 个具名测试无缓存复跑；RTK 汇总 13 packages / 289 passed |
| `go test -race -count=1 ./...` | 0 | 13 packages / 289 passed |
| `go test ./internal/store -run '^TestSymlinkAtomicReplace$' -count=500` | 0 | 修正后的单次 `Readlink` 原子观察连续 500 次通过 |
| `go tool cover -func=/tmp/hukou-final-precommit.out` | 0 | total statements 78.9% |
| `CGO_ENABLED=0 GOOS={darwin,linux} GOARCH={amd64,arm64} go build` | 0 | Darwin 为 x86_64/arm64 Mach-O；Linux 为 x86-64/aarch64 静态 ELF |
| Unix state 包 Linux/FreeBSD/NetBSD/OpenBSD 交叉编译 | 0 | 四个 Unix 目标通过 |
| workflow Ruby YAML parse | 0 | `ci.yml`、`release.yml` 可解析 |
| `bash -n scripts/release.sh` | 0 | shell 语法通过 |
| Markdown 相对链接检查 | 0 | 无缺失相对目标 |
| `gofmt -l .` | 0 | 无输出 |
| `git diff --check` | 0 | 无 whitespace error |
| 隔离 CLI smoke | 0 | 临时 HOME/PATH/data root 中 version、scan、adopt local、list、hukou 来源复核、upgrade local dry-run 均通过 |

## 独立回顾

并行只读回顾逐项对照代码、测试和 workflow。发现的高优先级缺口已在 subject commit 前关闭，包括：未知容器 bare 退化、长下载窗口 TOCTOU、歧义资产静默选择、`upgrade --all` 参数扩大、tag dispatch 误发布、tag/main/annotated 校验、checksum 文件映射、buildinfo smoke、macOS 发布门禁与发布 token 作用域。最终回顾未发现残留 P0/P1。

## 首次远端 CI 反馈

- PR: `https://github.com/rtwsvj/hukou/pull/1`
- Initial CI Run: `https://github.com/rtwsvj/hukou/actions/runs/29265826948`
- Ubuntu test/race/build/smoke、quality、coverage：pass。
- macOS 初次失败：既有 `TestSymlinkAtomicReplace` 先 `Lstat(linkPath)`、再 `ReadFile(linkPath)`，在并发替换期间进行了两次独立路径查找，runner 返回瞬时 `EINVAL`。
- 修正：测试用单次 `Readlink` 观察原子名字状态，只接受旧/新两个完整 target，再直接读取该不可变 store target；不降低对“link 始终为完整旧值或新值”的断言。
- 修正后本地定向 500 次、全仓 uncached test 与 race 均通过；远端重跑结果仍 pending。

## 未运行与限制

- 本机只有 BSD tar，未安装 `gtar`；没有为了验证而改变机器软件状态。归档位复现留给 Ubuntu snapshot workflow。
- 新建的 `workflow_dispatch` 需要 workflow 先进入默认分支；应在合并后、打 tag 前执行。
- 没有读写真实 hukou manifest/store，也没有替换真实用户二进制。
- 未做真实公共网络 fixture E2E；当前网络行为由 `httptest` 覆盖，公共 fixture 留 H2。
- 可观测错误补偿不等于断电或 `SIGKILL` crash safety；WAL/doctor 留 H2。

## 下一验证门

1. 推送分支并通过 PR 的 Linux/macOS CI。
2. 合入 `main` 后手动运行 `release.yml` snapshot，核对双构建、四个 archive、四条 checksum 与 buildinfo。
3. 推送 annotated `v0.1.0` tag，确认 tag release workflow 与五个远端资产。
4. 将本报告补充远端 run/release 证据并更新最终 verdict。
