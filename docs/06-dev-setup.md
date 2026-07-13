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
make verify
```

| Target | 含义 |
|---|---|
| `build` | `-trimpath` 构建到 `bin/hukou` |
| `test` | 全仓库单测与隔离 mock e2e |
| `race` | 全仓 race detector |
| `coverage` | 生成被忽略的 `coverage.out` 并打印函数摘要 |
| `vet` | `go vet ./...` |
| `fmt-check` | 只检查 gofmt，不改文件 |
| `verify` | fmt、module verify、vet、test、race、coverage、build 发布前门禁 |
| `release` | 先运行 `verify`，再调用 `scripts/release.sh` |

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
