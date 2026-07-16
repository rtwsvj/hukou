# Verification Report: L6 Real-Network Smoke

## 元信息

- Verification ID: `VERIFY-20260716-l6-network-smoke`
- Date: 2026-07-16
- Parent commit: `f79951e953832ca99fcd09095c7898b2348f7dab`
- Branch: `p3-public-20260716`
- Code subject: `cmd/network_e2e_test.go` as committed in the P3 public-release
  finalization change (`chore(p3): close L6 gate ...`)
- OS: macOS 27.0 arm64
- Go toolchain: go1.26.5 darwin/arm64
- Scope: L6 opt-in real-network smoke only (GitHub release API + asset selection)
- Verdict: **pass**

本报告只核验 L6 真实网络 smoke 的一次真跑，不改变 V0.3 code verdict，不声称第三方
安全审计、GitHub-hosted gate、公开发布或 attestation 已通过。

## Claims vs Evidence

| Claim | Evidence | Verdict |
|---|---|---|
| 默认门禁零网络 | `//go:build network_e2e` 排除默认编译；`go vet ./...`/`make verify` 不触及该文件 | pass |
| 双重关闸生效 | 无 `HUKOU_NETWORK_E2E=1` 时 `go test -tags network_e2e -run Network ./cmd/` 输出 `ok`，测试 `t.Skip` | pass |
| 真实 GitHub release API 集成 | `ghrelease.Client.Latest("cli","cli")` 返回 `tag=v2.96.0`、`assets=22` | pass |
| 真实资产选择集成 | `assetpick.Pick(names,"linux","amd64","")` -> `gh_2.96.0_linux_amd64.tar.gz`，且属于返回资产集 | pass |
| 不下载资产 | 测试仅调用 `Latest` + `Pick`，无 `Download` 调用；运行 ~1.7s | pass |
| 宿主平台可解析（信息） | `assetpick.Pick(names,"darwin","arm64","")` -> `gh_2.96.0_macOS_arm64.zip` | pass |

## 命令与结果

| Check | Exit | Result |
|---|---:|---|
| `go vet -tags network_e2e ./cmd/` | 0 | 编译通过 |
| `go test -tags network_e2e -run Network ./cmd/ -count=1`（无 env） | 0 | `ok`，L6 测试 skip |
| `HUKOU_NETWORK_E2E=1 GH_TOKEN=$(gh auth token) go test -tags network_e2e -run Network ./cmd/ -v -count=1` | 0 | `ok github.com/rtwsvj/hukou/cmd` |

关键输出：

```
=== RUN   TestNetworkE2E_LatestRelease
    network_e2e_test.go:71: latest cli/cli release: tag=v2.96.0 assets=22
    network_e2e_test.go:88: assetpick.Pick(linux/amd64) -> gh_2.96.0_linux_amd64.tar.gz (tiebreak note "")
    network_e2e_test.go:96: assetpick.Pick(host darwin/arm64) -> gh_2.96.0_macOS_arm64.zip (tiebreak note "")
--- PASS: TestNetworkE2E_LatestRelease (1.66s)
PASS
ok  	github.com/rtwsvj/hukou/cmd	2.175s
```

`-run Network` 也匹配既有的 `TestExplainNameIsZeroWriteAndZeroNetwork` 与
`TestUpgradeAllStopsBeforeNextNetworkRequestWhenTransactionRemainsPending`，两者同批
PASS，与 L6 无耦合。

## 跳过项、限制与遗留风险

- 结果绑定 `cli/cli` 在 2026-07-16 的 latest release（`v2.96.0`）。上游发布新版本后
  tag/资产名会变化；断言只钉死"能解析出唯一 linux/amd64 归档"，不钉死具体版本号，
  故对上游滚动稳定。
- 未覆盖真实资产下载、解压、checksum/attestation 验证的联网路径（属更重的可选扩展，
  且会拉取多 MB 二进制）；这些仍由隔离 `httptest` 单测覆盖。
- 该 smoke 使用真实只读 `GH_TOKEN`，仅走 `api.github.com`；token 不写入任何产物。
- L6 仍是 opt-in，不进入 `make verify`/`make release-verify`/CI 默认门禁。
