# Phase 2: assetpick 决胜扩展层实现完成记录

## 范围

在 vendor 自 eget 的 `internal/assetpick/detect.go`(**一字未改**)之上，新建
两个文件叠加决胜层：

- `internal/assetpick/pick.go` —— 入口 `Pick` + 黑名单预过滤 + assetFilter + 决胜瀑布
- `internal/assetpick/pick_test.go` —— 表驱动真实资产名测试

仅用标准库(`fmt` `path` `sort` `strings`)。

## 入口签名

```go
func Pick(assets []string, goos, goarch, assetFilter string) (choice string, note string, err error)
```

`note` 记录决胜路径(`asset-filter` / `rosetta-fallback` / `drop-32bit` /
`archive-preference` / `lexical-tiebreak`，逗号连接)；过滤后为空或彻底无候选时
返回 `err`，文案列出**全部原始资产名**。

## 流程

1. **黑名单预过滤**：后缀 `.sha256/.sha256sum/.sig/.asc/.pem/.sbom/.txt/.md/.deb/.rpm/.apk/.msi`；
   `goos=darwin` 时再排除 `.exe/.appimage`。
   - 版本号伪扩展名处理：判后缀前先剥离复合归档后缀
     `.tar.gz/.tar.xz/.tgz/.txz/.zip/.gz`，再把纯数字尾巴(如 `foo-1.3.5` 的 `.5`)
     视为无扩展名(`effectiveExt`)。
2. **assetFilter**：非空时按子串过滤，`^` 前缀反选，复用 `detect.go` 的 `SingleAssetDetector`。
3. **主匹配**：`NewSystemDetector(goos, goarch).Detect` 跑 eget 四级瀑布；直接命中即返回。
4. **决胜瀑布**(仍多候选)：
   - (a) darwin/arm64：优先 `arm64/aarch64/universal`(复用 `ArchArm64`)；一个没有则
     回退 `amd64/x86_64`(复用 `ArchAMD64`) → `rosetta-fallback`。
   - (b) 64 位平台：剔除命中 `386/x32/armv6/arm32` 的资产(复用 `ArchI386`/`ArchArm`) → `drop-32bit`。
   - (c) 归档偏好 `.tar.gz(.tgz) > .tar.xz(.txz) > .zip > 裸名` → `archive-preference`。
5. **仍多个**：字典序取第一(确定性) → `lexical-tiebreak`。

## 验收结果

| 检查项 | 命令 | 结果 |
|---|---|---|
| build | `go build ./...` | ✅ PASS |
| vet | `go vet ./...` | ✅ PASS |
| test | `go test ./internal/assetpick/` | ✅ PASS (15/15) |

命令以 `&&` 串联执行，全绿。

## 测试覆盖

| # | 测试用例 | 覆盖场景 |
|---|---|---|
| T01 | TestPickDarwinArm64/fzf | fzf-0.60.3 全平台清单 → `fzf-0.60.3-darwin_arm64.tar.gz` |
| T02 | TestPickDarwinArm64/gh | gh 2.63.2(`macOS`/`.msi`/`checksums.txt`) → `gh_2.63.2_macOS_arm64.zip` |
| T03 | TestPickDarwinArm64/lazygit | `Darwin`+`x86_64`/`arm64` → `lazygit_0.44.1_Darwin_arm64.tar.gz` |
| T04 | TestPickDarwinArm64/ripgrep | `aarch64-apple-darwin` 三段式命名 → 选中 aarch64 |
| T05 | TestPickDarwinArm64/uv | `uv-aarch64-apple-darwin.tar.gz` |
| T06 | TestPickLinuxAmd64 | fzf 清单 linux/amd64 视角选中正确资产 |
| T07 | TestPickWindowsAmd64PrefersZipOverMsi | `.msi` 被黑名单剔除，选 `.zip` |
| T08 | TestPickRosettaFallback | 仅 amd64 darwin 资产 → Rosetta 回退 + 归档偏好(note 双标) |
| T09 | TestPickIgnoresChecksumAndSignatureNoise | `.txt/.sig/.sha256/.asc/.sbom/.pem/.md` 干扰项全过滤 |
| T10 | TestPickAssetFilter | `--asset` 子串强制选 zip 变体，note=asset-filter |
| T11 | TestPickAssetFilterAnti | `^` 反选排除 arm 后按 linux/amd64 选中 |
| T12 | TestPickAssetFilterNoMatch | 过滤无命中 → err |
| T13 | TestPickNoCandidatesListsAll | 全黑名单 → err 且列出全部资产名 |
| T14 | TestPickUnsupportedOS | 非法 goos → err |
| T15 | TestEffectiveExt / TestBlacklistDarwinExe | 伪扩展名剥离、大小写、darwin 特有黑名单单元断言 |

## 约束遵守

- `detect.go` 未改动(`git status` 仅新增 pick.go / pick_test.go)。
- 仅标准库，仅落在 `internal/assetpick/` 下。
