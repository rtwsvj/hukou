# Phase 2 命令层实现完成记录

## 完成项

实现了 `cmd/adopt.go`、`cmd/upgrade.go`、`cmd/rollback.go`、`cmd/list.go` 四个命令文件，并注册到 root command；新增 `cmd/helpers.go` 存放数据根目录解析、manifest/store 初始化、安全闸、文件复制/移动等共享逻辑。新增 `cmd/e2e_test.go` 覆盖全链路集成测试。

### 命令行为

- **`hukou adopt <name|path> [owner/repo]`**
  - 裸名字通过 `exec.LookPath` 在 PATH 中定位；路径参数直接使用。
  - Go 二进制通过 `provenance.ReadGoBinary` 的 `ModulePath` 推导 `github.com/owner/repo`；其余必须显式给 repo 或使用 `--local`。
  - `--local` 时 repo 留空、tag 默认 `local`。
  - 跑 provenance 责任链；归属非 `unknown`/`curl-installer`/`local-project` 时拒绝，`--force` 放行。
  - 计算 sha256，将当前二进制**拷贝**到 `store/<name>/original/<bin>`（不动原文件），写入 manifest。

- **`hukou upgrade [name ...] [--all] [--dry-run] [--asset <substr>]`**
  - `local` 条目跳过并注明。
  - 升级前校验 manifest 中 sha256 与磁盘当前文件一致，不一致则中止该条并警告。
  - token 从 `GITHUB_TOKEN`/`GH_TOKEN` 读取，调用 `ghrelease.Latest`。
  - tag 相同则提示已最新；`--dry-run` 输出形如 `将升级 X: <旧tag> → <新tag>, 选中资产 <名>`。
  - 真升级：`assetpick.Pick` → 下载资产 → 下载 `checksums` 类资产并校验 → `archive.Extract` → `store.Put`。
  - 首次升级（原位不是软链）：若 `original/` 不存在则 `store.AdoptOriginal`；若 adopt 时已备份 `original/`，则将当前实体文件移入 `store/<name>/<当前tag>/` 再激活新版本，避免丢失 adopt 时的原件备份。
  - 激活后更新 manifest tag/sha256/`UpdatedAt`，并 `store.Prune(name, 3)`。

- **`hukou rollback <name> [--to <tag>]`**
  - 校验当前 sha256。
  - `--to` 指定版本（含 `original`）；未指定则按修改时间取上一个版本。
  - `store.Activate`/`activateOriginal` 切换软链后回写 manifest。

- **`hukou list`**
  - 表格输出 `NAME/TAG/REPO/PATH/VERSIONS`；空清单友好提示。

### 数据目录

- 默认遵循 XDG：`~/.local/share/hukou`
- 可通过 `HUKOU_DATA_DIR` 环境变量覆盖（e2e 测试使用）。
- 布局：`manifest.json`、`store/<name>/<tag>/<bin>`、`store/<name>/original/<bin>`、`store/.tmp/`。

### 集成测试

`cmd/e2e_test.go` 使用 `httptest` 构造假 GitHub API 与测试内生成的 `tar.gz` 资产，走通：

1. `adopt <path> owner/repo`
2. `adopt --local <path>`（local 条目 upgrade 跳过）
3. `upgrade --dry-run`
4. 真 `upgrade`（替换 `t.TempDir` 中假二进制为软链）
5. `rollback`

全程不访问真网络、不触碰真 PATH。

## 验收结果

```bash
$ go build ./... && go vet ./... && go test ./...
?       github.com/rtwsvj/hukou [no test files]
ok      github.com/rtwsvj/hukou/cmd (cached)
ok      github.com/rtwsvj/hukou/internal/archive (cached)
ok      github.com/rtwsvj/hukou/internal/assetpick (cached)
ok      github.com/rtwsvj/hukou/internal/ghrelease (cached)
ok      github.com/rtwsvj/hukou/internal/manifest (cached)
ok      github.com/rtwsvj/hukou/internal/output (cached)
ok      github.com/rtwsvj/hukou/internal/provenance (cached)
ok      github.com/rtwsvj/hukou/internal/scan (cached)
ok      github.com/rtwsvj/hukou/internal/store (cached)
ok      github.com/rtwsvj/hukou/internal/verify (cached)
```

全部通过。

## 约束遵守

- 未修改任何 `internal/` 库文件或 vendor 文件。
- 未新增第三方依赖（仅使用现有 `cobra`）。
- `manifest`/`store` 根路径支持 XDG 与 `HUKOU_DATA_DIR` 覆盖。
