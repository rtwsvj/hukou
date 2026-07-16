# L6 Real-Network Smoke — Bound Evidence

## 元信息

- Verification ID: `VERIFY-20260716-l6-network-smoke`
- Date: 2026-07-16
- **Subject commit: `a668ef49d2d7be41c990664ada130df31cb7c92f`**
  （branch `p3-public-20260716`；运行时工作树在该提交上干净，仅含 gitignore 的
  本地卡片摘要，非构建输入）
- OS: macOS 27.0 arm64
- Go toolchain: go1.26.5 darwin/arm64
- Scope: L6 opt-in 真实网络 smoke——GitHub release API + 资产选择 + 资产元数据
  （URL scheme/大小）验证；**不含下载/CDN 传输**
- Verdict: **pass**

本报告只核验 L6 的一次绑定固定提交的真跑，不改变 V0.3 code verdict，不声称第三方
安全审计、GitHub-hosted gate、公开发布或 attestation 已通过。

## Claims vs Evidence

| Claim | Evidence | Verdict |
|---|---|---|
| 默认门禁零网络 | `//go:build network_e2e` 排除默认编译；`make verify` 在 subject 上 exit 0 且不触及该文件 | pass |
| 双重关闸生效 | 无 `HUKOU_NETWORK_E2E=1` 时测试 `t.Skip`；`make verify-network` 在缺 token 时硬性报错退出（exit 非 0），不静默绿灯 | pass |
| 真实 GitHub release API 集成 | `ghrelease.Client.Latest("cli","cli")` 返回 `tag=v2.96.0`、`assets=22` | pass |
| 真实资产选择集成 | `assetpick.Pick(names,"linux","amd64","")` -> `gh_2.96.0_linux_amd64.tar.gz`，且属于返回资产集 | pass |
| 选中资产元数据可用 | `BrowserDownloadURL=https://github.com/cli/cli/releases/download/v2.96.0/gh_2.96.0_linux_amd64.tar.gz`（https+host），`Size=14652560 > 0` | pass |
| 不下载资产 | 测试仅调用 `Latest` + `Pick` + 元数据字段断言，无 `Download` 调用；运行 ~1.6s | pass |
| 宿主平台可解析（信息） | `assetpick.Pick(names,"darwin","arm64","")` -> `gh_2.96.0_macOS_arm64.zip` | pass |

## 命令与结果（均在 subject `a668ef4` 固定提交上运行）

| Check | Exit | Result |
|---|---:|---|
| `HUKOU_NETWORK_E2E=1 GH_TOKEN=$(gh auth token) go test -tags network_e2e -run Network ./cmd/ -v -count=1` | 0 | `ok github.com/rtwsvj/hukou/cmd` |
| `make verify` | 0 | 全 target pass（含经 `scripts/build_flags.sh` 重构后的 release_test 矩阵） |
| `GH_TOKEN=$(gh auth token) make verify-network` | 0 | `ok github.com/rtwsvj/hukou/cmd` |
| `env -u GH_TOKEN -u GITHUB_TOKEN make verify-network` | 2 | 硬性报错 `GITHUB_TOKEN or GH_TOKEN is required`，无静默绿灯 |

关键输出：

```
=== RUN   TestNetworkE2E_LatestRelease
    network_e2e_test.go:77: latest cli/cli release: tag=v2.96.0 assets=22
    network_e2e_test.go:95: assetpick.Pick(linux/amd64) -> gh_2.96.0_linux_amd64.tar.gz (tiebreak note "")
    network_e2e_test.go:109: picked asset metadata: url=https://github.com/cli/cli/releases/download/v2.96.0/gh_2.96.0_linux_amd64.tar.gz size=14652560
    network_e2e_test.go:117: assetpick.Pick(host darwin/arm64) -> gh_2.96.0_macOS_arm64.zip (tiebreak note "")
--- PASS: TestNetworkE2E_LatestRelease (1.55s)
PASS
ok  	github.com/rtwsvj/hukou/cmd	2.676s
```

`-run Network` 也匹配既有的 `TestExplainNameIsZeroWriteAndZeroNetwork` 与
`TestUpgradeAllStopsBeforeNextNetworkRequestWhenTransactionRemainsPending`，两者同批
PASS，与 L6 无耦合。

## 跳过项、限制与遗留风险

- 结果绑定 `cli/cli` 在 2026-07-16 的 latest release（`v2.96.0`）。上游发布新版本后
  tag/资产名/大小会变化；断言只钉死"能解析出唯一 linux/amd64 归档且其 URL/大小元
  数据可用"，不钉死具体版本号，故对上游滚动稳定。
- 未覆盖真实资产下载、解压、checksum/attestation 验证的联网路径（真实 CDN 传输不在
  L6 当前声明范围内，会拉取多 MB 二进制）；这些仍由隔离 `httptest` 单测覆盖。
- 该 smoke 使用真实只读 `GH_TOKEN`，仅走 `api.github.com`；token 不写入任何产物。
- L6 仍是 opt-in，不进入 `make verify`/`make release-verify`/CI 默认门禁。
- 本证据报告落在 subject 之后的紧邻 docs 提交中（报告无法包含自身所在提交的 SHA）；
  subject 与报告提交之间只有本报告与 docs/07 的 SHA 引用两处 Markdown 变更，无任何
  构建输入变化。
