# 开发环境

## 前置条件

- Go：最低工具链版本以根 `go.mod` 为准；本地可使用兼容的更新 patch，CI 由 `go-version-file` 解析该声明。
- Git。
- GNU tar：只有运行可重复发布脚本时需要；macOS 可使用 `gtar`。
- GitHub CLI：只由 release workflow 发布资产时使用。

本机协作约定：所有 shell 命令经 `rtk` 前缀执行。GitHub Actions runner 不安装 RTK，workflow 直接运行标准命令。

## 构建与验证

```bash
make build
make test
make race
make coverage
make vet
make fmt-check
make license-check
make install-test
make release-test
make shellcheck
make vulncheck
make verify
make release-verify
```

| Target | 含义 |
|---|---|
| `build` | `-trimpath` 构建到 `bin/hukou` |
| `test` | 全仓库单测与隔离 mock e2e |
| `race` | 全仓 race detector |
| `coverage` | 生成被忽略的 `coverage.out` 并打印函数摘要 |
| `vet` | `go vet ./...` |
| `fmt-check` | 只检查 gofmt，不改文件 |
| `license-check` | 检查根许可证、notices、来源/依赖许可证与 release 打包接线 |
| `install-test` | 在临时 fixture 上验证安装、dry-run、force、严格 SemVer 2.0.0（不接受 build metadata）、checksum、URL 协议边界 |
| `release-test` | 以合法/非法矩阵验证发布脚本的严格 SemVer 2.0.0 边界，使用本地假构建器，不创建真实发布物 |
| `shellcheck` | `shellcheck scripts/*.sh`；需要本机安装 shellcheck |
| `vulncheck` | 固定运行 `govulncheck@v1.5.0 ./...`；需要可访问 Go module/vulnerability 数据源 |
| `verify` | fmt、module verify、vet、test、race、coverage、build、license、installer 与 release-version matrix 的常规门禁 |
| `release-verify` | `verify` 加 shellcheck 与 govulncheck 的发布专用门禁 |
| `release` | 先运行 `release-verify`，再调用 `scripts/release.sh` |
| `demo` | build 后在临时目录运行只读/隔离 demo，不应触碰真实 hukou 状态 |

## 安全的本地 CLI 试验

不得对真实用户数据做开发 smoke。始终建立临时根：

```bash
tmp="$(mktemp -d)"
HOME="$tmp/home" \
XDG_DATA_HOME="$tmp/data" \
HUKOU_DATA_DIR="$tmp/hukou" \
PATH="$tmp/bin" \
./bin/hukou scan
```

需要 upgrade/rollback 时，测试二进制也必须位于该临时 PATH。结束后删除整个临时目录。

## V0.3 安装器开发契约

`scripts/install.sh` 支持 Darwin/Linux × amd64/arm64，默认写入
`$HOME/.local/bin/hukou`，不使用 sudo、不修改 shell rc。它先下载
`checksums.txt` 与目标 archive，要求唯一精确 SHA-256 条目，再检查 archive root，
只解出 regular executable 并通过同目录临时文件替换目标。

- 生产/mirror URL 必须是 HTTPS；HTTP 永远拒绝。
- `file://` 只供隔离测试，并且必须显式设置 `HUKOU_ALLOW_FILE_URL=1`。
- 已有目标默认拒绝，只有 `--force` 会替换。
- non-force 同时把 dangling symlink 视为“目标已存在”；下载后用目标目录内 hard
  link 做原子 no-replace commit，若竞争者在预检后创建任意节点则失败且不覆盖。
- `--force` 是用户显式授权替换，使用同目录临时文件后 `mv -f` 激活。
- `--dry-run` 不联网、不创建 prefix，只打印 version/platform/checksum URL/destination。
- 安装器已进入私有 V0.3 分支，但没有 v0.3 发布端点，不能把脚本存在写成公开安装渠道已上线。

## 生成物

- `bin/`：本机构建
- `dist/`：发布归档
- `coverage.out`、`*.coverprofile`：覆盖率

这些文件全部被忽略，不应提交。`coverage.out` 的历史跟踪副本属于 Phase 1 旧产物，应从 Git 索引移除。

## 修改文档的同步规则

- CLI 变化：根 README、CLI reference、测试文档。
- manifest/store 变化：data/API、requirements、风险文档。
- 安全语义变化：规格、ADR、失败注入测试。
- 发布变化：Makefile、release script、workflow、release 文档。
