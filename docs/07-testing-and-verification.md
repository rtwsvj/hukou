# 测试与验证

## 证据等级

| 等级 | 内容 | 能证明什么 |
|---|---|---|
| L1 静态 | diff、文件存在、配置解析 | 改动已落盘，不能证明行为 |
| L2 单测 | 包级 `go test` | 局部契约 |
| L3 全仓 | test、race、vet、build、coverage | 当前提交的工程基线 |
| L4 隔离 smoke | 临时 HOME/PATH/data root 的 CLI 流程 | 命令接线与文件系统行为 |
| L5 发布验证 | 四平台 archive、checksums、版本注入 | 发布链路 |
| L6 真实网络 smoke | 受控公共 fixture repo | GitHub API/CDN 集成；当前后置 |

历史 DONE 文档只记录当时声称的结果。新的“通过”结论必须写入 `docs/codex/verification-reports/`，并包含提交 SHA、命令、退出状态和关键输出。

## 必需门禁

```bash
make verify
make release-verify
```

`make verify` 展开为 fmt-check、module verify、vet、test、race、coverage、build、
license-check、install-test 和 release-test。release-test 以合法/非法矩阵覆盖严格
SemVer 2.0.0（含 prerelease 空段、纯数字前导零与 build metadata 拒绝）。
`make release-verify` 再增加 shellcheck 与固定版本
govulncheck；各 target 仍可单独运行。

同时执行：

- `git diff --check`
- `go mod verify`
- release 脚本静态检查与 snapshot 打包
- Linux amd64 archive 解包并执行 `hukou version`

## CI

`.github/workflows/ci.yml`：

- Ubuntu：格式、模块完整性、license/notices、installer、shellcheck、vet。
- Ubuntu：独立 `govulncheck` job。
- Ubuntu + macOS matrix：test、race、build、binary smoke。
- Ubuntu：coverage profile 与 artifact。

`.github/workflows/codeql.yml` 只在 repository visibility 为 public 时运行；当前
private RC 中 job 会跳过，不能称为 CodeQL 通过。现有 GitHub-hosted runner 还可能
在任何 step 之前被账户 billing/spending limit 阻断；这类运行只记
`infrastructure-blocked`，不等于代码 pass/fail。

CI 使用 `go.mod` 的 Go 版本，不维护第二份版本字符串。

## 关键失败注入

H1 至少覆盖：

- checksum asset 存在但缺所选文件条目。
- checksum 不匹配。
- 精确 checksum sidecar 的单摘要格式与 BSD checksum 格式。
- asset 下载 404、大小不符、超限。
- 下载期间外部修改 live binary，激活前二次 SHA 闸门拒绝覆盖。
- manifest 保存失败发生在 Activate 之后。
- rollback 保存失败发生在 Activate 之后。
- regular-file 激活始终只暴露完整旧内容或新内容；事务恢复仍覆盖 regular file 与旧版 symlink 两种输入拓扑。
- 写锁在同进程和子进程竞争时立即返回 `ErrLocked`，释放后可重新获取；锁路径软链被拒绝。
- adopt 同名冲突不覆盖 original/manifest。
- dry-run 不创建 data root、不 GC、不下载。
- PREPARED 的 live/manifest before/after 四种组合全部收敛到 before。
- durable COMMIT 后相同四种组合全部收敛到 after。
- transaction payload/COMMIT 损坏，或预检/写入前复核发现任一资源 unknown drift 时，零覆盖并保留 pending evidence。
- 子进程在 PREPARED/COMMITTED 窗口被强杀后，下一次 Recover 分别回滚/前滚。
- absent→regular/symlink 使用原子 no-replace，检查后竞争写不会被覆盖。
- doctor 在 data root 缺失、健康和损坏 fixture 上都保持零写；文本/JSON同源且稳定。

V0.3 还必须覆盖：

- explain/adopt dry-run 的目录快照、网络请求计数为零，JSON schema/排序稳定。
- outdated local/pin-current 的零网络路径，以及 drift-before-network、metadata-only/no-download。
- schema 0/1→2 deterministic migration；schema 2 缺 policy/history、未知字段、future schema、重复 path/name、forward/missing lineage 拒绝。
- schema 0/1 携带 v2-only retention/policy/history 字段必须拒绝，不能通过 migration
  smuggling；activation unsafe tag 与同 tag/different-SHA binding 必须拒绝且不修改 entry。
- `A→B→C→B→A` 默认 rollback，不读取/信任目录 mtime；explicit ancestor/original 边界。
- stable/prerelease、SemVer normalized equal、隐式 downgrade refusal、exact pin 前进/回退、bounded release pagination。
- retention 对 current/original/pin/N ancestors/pending refs 的保护；malformed protected ref 与 apply 前替换必须零删除。
- repair plan 的零 hukou 写入、plan 0600、stale fingerprint/data-root mismatch/ambiguous journal/live SHA mismatch 零业务写入。
- support stdout 零写、file 0600，且 fixture 中 path/repo/user/HOME/env/WAL/binary secret 不出现在 JSON。
- list 在统计下载版本前验证 original namespace 完整；original 不计入 `VERSIONS`。
- installer 拒绝 HTTP、未授权 file URL、坏/重复/缺失 checksum、错误 archive root、
  重复目标 member、已有目标无 force；有 Perl时最终提交覆盖 `link(2)` atomic
  no-replace 与 force `rename(2)`，Linux 无 Perl时覆盖 `ln -T`/`mv -T` fallback，
  并测试 directory、symlink-to-directory 与预检后竞争；dry-run 零写。
- release archive 包含 LICENSE、THIRD_PARTY_NOTICES、双语 README、LICENSES，SBOM 与 checksums 对应固定 commit。

## V0.3 当前工作树阶段证据（2026-07-15）

| 检查 | 当前结果 | 能证明/不能证明 |
|---|---|---|
| 安全关键路径定向 audit | 321 passed / 6 packages | schema/activation/archive/store 等最新修复的阶段证据；不等于固定提交验收 |
| `go test -count=1 ./...` | 641 passed / 21 packages | 最新 direct uncached ordinary；仍需绑定最终 commit 复跑 |
| `go test -count=1 -race ./...` | 641 passed / 21 packages | 最新 direct uncached race；仍需绑定最终 commit 复跑 |
| `GOPROXY=https://goproxy.cn,direct make release-verify` | exit 0 | 全 target pass；coverage 72.9%；govuln 无已知漏洞；默认 proxy 路径另有 IPv6 timeout |
| explain name/path 只读定向 | 5 passed | 独立目录快照与 `http.DefaultTransport` spy 证明该批次零写/零网络 |
| `scripts/install_test.sh` | pass | 含 Perl link(2)/rename(2)、Linux 无 Perl `-T` fallback、directory/symlink-dir/竞争/duplicate member |
| `scripts/release_test.sh` | pass | v-prefix、无 build metadata 的 strict shell SemVer matrix；不证明 snapshot |
| Linux/arm64 non-root container ordinary/race | pass / all packages | `golang:1.26.5-bookworm`、UID 65532、repo read-only、mirror；最终 commit 仍需复跑 |
| Linux GNU tar 1.34 installer/release tests | pass | release test 在配置 git safe.directory 后通过；root/default-proxy 首次失败不计代码失败 |
| actionlint 1.7.12 / Ruby YAML parse / Action pin 对账 | pass | workflow 静态结构与固定 SHA；不证明 hosted run |
| Markdown links / production 汉字 sweep / `git diff --check` | 68 Markdown、89 targets、0 missing；0 汉字；diff pass | 当前文档/界面阶段门禁 |

未完成：最终 commit 的全仓/coverage/build/Linux 复跑与证据固化、最终四目标与
双构建、release snapshot/SBOM、远端 Actions 和独立 `pinhaoma-review`。只有新的
verification report 绑定最终 commit 后，才能把这些项目改成通过。

Gap audit 缺口已在工作树关闭：installer 有 Perl时采用 `link(2)` atomic no-replace /
force `rename(2)`，Linux 无 Perl时采用 `ln -T`/`mv -T` fallback；覆盖 directory、
symlink-to-directory、预检后竞争并拒绝重复目标 member。Release workflow
删除历史 `v0.1.0` 手动 snapshot default。另新增 schema-specific manifest required
fields、legacy v2-only smuggling rejection、activation safe tag 与 tag/SHA binding、
list original completeness，以及 symlink adopt→upgrade→implicit rollback E2E。它们仍需
随最终 subject commit 复跑，不能仅凭实现存在改成 RC pass。

Gap audit 的后续两个 P2 也已在工作树关闭：Store.Versions 对非目录/畸形版本
失败关闭并有两组测试；explain 已补上述 5 项零写/network-spy 定向测试。

后续 defense-in-depth 尚未完成，测试计划也不得提前标绿：duplicate JSON key、
GitHub API body cap、installer 总解压体积/member 数膨胀预算，以及 `openat`/目录 fd
路径锚定。

## 覆盖率

- profile 是 CI artifact，不进入 Git。
- H1 首先建立当前真实基线；在不知道基线前不虚构百分比门槛。
- 后续不得无说明降低总体覆盖率。
- `cmd`、store、manifest、verify、archive、ghrelease 是优先提高的安全关键包。

## 隔离要求

- 测试统一使用 `t.TempDir` 或 runner 临时目录。
- 不读取真实 manifest/store。
- 不把真实 PATH 交给 adopt/upgrade/rollback e2e。
- PR 测试使用 `httptest`，默认不访问公网。
- 真实网络 smoke 独立为手动/定时 workflow，使用只读 fixture repo 和最小权限 token。

## 验证报告最小字段

- Verification ID、日期、commit、OS、Go 版本。
- 实际运行命令及退出状态。
- Claims vs Evidence。
- 未运行/跳过项及原因。
- 生成的 release artifact 名与 checksums。
- 总结：pass、partial、fail 或 inconclusive。
