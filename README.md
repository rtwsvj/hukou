# hukou（户口）

给机器上的 CLI 工具上户口：扫描、溯源、收编、校验升级和回滚。

> brew 管不着的那些工具，归我管。

## 当前状态

项目已经交付 Phase 1 `scan` 与 Phase 2 `adopt / upgrade / rollback / list`。当前正在完成 H1 安全硬化与首个 SemVer 发布；最终通过情况以 [`docs/codex/verification-reports/`](docs/codex/verification-reports/) 中对应提交的验证报告为准。

- 目标平台：macOS、Linux
- 当前模块工具链：以 [`go.mod`](go.mod) 的 `go` 指令为准
- 分发状态：private repository，尚未声明原创代码的公开分发许可证
- Windows、topgrade 集成、mise/Brewfile 导出、changelog 风险层：未实现

## 能做什么

1. **scan**：遍历 `PATH` 与额外目录，识别 Homebrew、MacPorts、语言包管理器、版本管理器、系统工具以及 hukou 自己收编的工具。
2. **adopt**：登记一个现有可执行文件；Go 二进制可从 build info 推导 GitHub repo，其他工具显式指定 `owner/repo`，本地工具使用 `--local`。
3. **upgrade**：查询已收编工具的最新 GitHub Release，选择平台资产、下载、校验、入库并切换软链。
4. **rollback**：切换回指定版本或最近的旧版本，并同步 manifest。
5. **list**：查看当前户口清单与本地保留版本数。

hukou 不代理 Homebrew、npm、Cargo 等其他管理器的升级。默认拒绝收编已被其他管理器认领的二进制；`--force` 是显式逃生口。

## 构建

```bash
make build
./bin/hukou version
```

常用工程命令：

```bash
make fmt-check
make vet
make test
make race
make coverage
make verify
```

## 快速开始

扫描当前 `PATH`：

```bash
hukou scan
hukou scan --unknown-only
hukou scan --json
hukou scan --source hukou
hukou scan --dir /path/to/extra/bin
```

收编本地工具或带 GitHub 上游的工具：

```bash
hukou adopt /path/to/my-tool --local
hukou adopt /path/to/tool owner/repo --tag v1.0.0
```

升级前先预览：

```bash
hukou upgrade tool --dry-run
hukou upgrade tool
hukou rollback tool
hukou rollback tool --to v1.0.0
hukou list
```

`upgrade` 与 `rollback` 会修改被收编工具所在路径。首次在真实工具上操作前，请先阅读 [`docs/04-data-and-api.md`](docs/04-data-and-api.md) 和 [`docs/08-risk-and-debt.md`](docs/08-risk-and-debt.md)。

## 数据与环境变量

默认数据目录遵循 XDG：

```text
${XDG_DATA_HOME:-$HOME/.local/share}/hukou/
├── state.lock
├── manifest.json
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

| 环境变量 | 作用 |
|---|---|
| `HUKOU_DATA_DIR` | 覆盖 manifest 与 store 根目录；测试必须使用临时目录 |
| `XDG_DATA_HOME` | 未设置 `HUKOU_DATA_DIR` 时决定默认数据位置 |
| `GITHUB_TOKEN` / `GH_TOKEN` | 提高 GitHub API 限额；下载请求不会向非授权宿主泄露 token |

`state.lock` 只串行化 adopt、真实 upgrade 和 rollback；scan 与纯 `--dry-run` 不持写锁。

安全契约：

- `scan` 纯本地、只读、不联网。
- upgrade/rollback 前核对当前二进制与 manifest 的 SHA-256。
- 上游提供 checksum 时必须成功找到并验证所选资产，否则失败关闭。
- 下载资产 hash 与激活后二进制 hash 分开记录，便于审计和完整性检查。
- 文件与软链切换采用同目录临时文件；可观测错误路径必须补偿恢复。
- H1 只承诺处理正常错误返回；断电、`SIGKILL` 等崩溃一致性仍是已知债务。

## 发布

`scripts/release.sh` 在固定提交上交叉构建以下 tar.gz，并生成 `checksums.txt`：

- darwin/amd64
- darwin/arm64
- linux/amd64
- linux/arm64

GitHub Actions 的手动运行只生成快照 artifact；推送 `v*` tag 才会创建 GitHub Release。详见 [`docs/09-release.md`](docs/09-release.md)。

## 文档入口

- [项目当前事实与阅读顺序](docs/README.md)
- [需求与安全不变量](docs/01-requirements.md)
- [路线图](docs/02-roadmap.md)
- [架构](docs/03-architecture.md)
- [开发环境](docs/06-dev-setup.md)
- [测试与验收](docs/07-testing-and-verification.md)
- [风险与技术债](docs/08-risk-and-debt.md)
- [Codex 执行、改动与验证记录](docs/codex/README.md)

## 第三方来源记录

来源与复用关系见 [`docs/pinhaoma-sources.md`](docs/pinhaoma-sources.md)。仓库保留：

- `LICENSES/eget-MIT.txt`
- `LICENSES/gup-APACHE-2.0.txt`
- `LICENSES/stew-MIT.txt`

禁止复制 topgrade、pacaptr、meta-package-manager 的 GPL 代码；只允许外部集成或独立实现相同思想。
