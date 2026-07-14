# hukou（户口）

> 安全管理那些不归现有包管理器负责的 CLI 二进制。

[English](README.md) ·
[项目文档](docs/README.md) ·
[当前版本](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) ·
[安全报告](SECURITY.md)

hukou 扫描 `PATH` 中的可执行文件，解释它们从哪里来，收编没有包管理器
负责的二进制，通过 GitHub Releases 校验升级，并保留本地回滚路径。

当前正式版本是面向 macOS 和 Linux 的 **v0.2.0**。私有的 **v0.3 RC
分支**已经包含 trust-first 命令、manifest v2、repair/support 和分发准备；固定提交
`1fa45a0` 已通过 local/private RC readiness 门禁。GitHub-hosted job 仍因账户计费
限制在执行前被阻断；v0.3 尚未打 tag、发布或合并，仓库也没有因此公开。

## 为什么需要 hukou？

Homebrew、npm、Cargo、pipx、mise 等包管理器已经能管好自己的软件。真正麻烦的
是从 GitHub Release 下载、从别处复制、通过 `go install` 构建，或直接扔进
私人 `bin` 目录的工具。这些文件很容易被遗忘，手工覆盖也存在风险。

hukou 刻意保持一条很窄的工作流：

```text
安全扫描
    → 找到无人管理的二进制
    → 明确收编其中一个
    → 预览升级
    → 校验并激活
    → 必要时回滚
```

hukou **不会**替代 Homebrew、MacPorts、npm、Cargo、pipx、mise 或其他包管理器。
默认情况下，它会拒绝收编已经被其他管理器认领的二进制。

## 当前正式版本已经能做什么

- **`scan`**：遍历 `PATH` 与额外目录，识别已知包管理器、版本管理器、
  系统工具和 hukou 自己管理的工具。扫描纯本地、只读、不联网。
- **`adopt`**：登记现有可执行文件，保存原始字节并创建 manifest 条目。
- **`upgrade`**：检查最新 GitHub Release，选择平台资产，验证发布方提供的
  checksum，保存版本并原子替换活跃常规文件。
- **`rollback`**：激活保留版本或最初保存的原件。
- **`list`**：显示已收编工具与本地保留版本数。
- **`doctor`**：只读检查 manifest、活跃文件、store、事务日志和临时残留。

## 尚未发布的 v0.3 分支已经实现什么

以下接口已经存在于私有开发树，但不是 Latest Release 的一部分，也还不能当成
已经稳定的公共契约：

- **`explain`**：解释 PATH 实际选中的二进制、同名 shadowed 项，以及每条归属
  判断的证据。
- **`adopt --dry-run`**：在不创建 hukou 状态的情况下检查收编并输出计划；
  `--json` 只允许和 dry-run 一起使用。
- **`outdated`**：只查询 Release metadata 并完成资产选择，不下载、不修改本地状态。
- **`policy show/set`**：查看或原子修改 SemVer/GitHub-latest、stable/prerelease、
  精确 pin 和 rollback depth。
- **确定性回滚与保留**：manifest v2 记录 activation lineage；回滚按逻辑 parent
  前进，不再读取目录时间；清理会保护 current、original、pin、历史目标和未完成事务。
- **`repair plan/apply`**：只开放未完成事务恢复和严格校验的 manifest backup
  恢复；plan 与现场 fingerprint 绑定。
- **`support bundle`**：生成离线、脱敏、不自动上传的 JSON 诊断摘要。

该分支还包含强制 checksum、最终目录项采用原子 no-replace/replace 且拒绝重复目标
archive member 的安装器，许可证与社区文件、SBOM 打包、仅公开仓库启用的
attest/CodeQL，以及 Topgrade custom command
集成说明。当前证据和待办见
[私有 RC 执行记录](docs/codex/execution-reports/2026-07-14-v0.3-private-rc.md)。

## 安全模型

hukou 宁愿明确停止，也不在看不懂的现场中猜测：

- 在尚未发布的 v0.3 分支中，`scan`、`explain`、`list`、`doctor`、`outdated`、
  `policy show`、纯 `upgrade --dry-run` 和 `adopt --dry-run` 都不修改 hukou
  状态。metadata 检查可能联网；scan/explain/doctor/adopt dry-run 不联网。
- 预期存在 checksum 却找不到对应条目时失败关闭。
- 下载资产 hash 与激活后二进制 hash 分开记录。
- adopt、upgrade、rollback 在替换之前复核活跃文件。
- 写操作通过进程锁串行化。
- 活跃文件使用同目录临时常规文件加原子 rename 替换。
- 持久化 WAL 记录 before/after 状态：PREPARED 回滚、durable COMMIT 前滚，
  遇到未知外部漂移就停止并保留证据。
- `doctor` 默认零写、零网络，只报告问题，不暗中删除或修复。
- 尚未发布的 v0.3 把 repair 单独设计为 plan/apply。plan 不改变 hukou 状态，
  只写用户明确指定的 plan 文件；apply 持锁后重新验证 fingerprint。
- 尚未发布的 support report 不包含原始路径、私有仓库名、用户名、环境变量、
  二进制或 WAL payload，也不会自动上传。

在收编重要工具前，请阅读[数据与接口契约](docs/04-data-and-api.md)和
[已知风险](docs/08-risk-and-debt.md)。

## 当前发布目标

v0.2.0 提供：

| 操作系统 | 架构 |
|---|---|
| macOS | amd64、arm64 |
| Linux | amd64、arm64 |

Windows 当前不受支持。项目不会把“能交叉编译”直接写成“平台已受支持”；实际验证
证据记录在 [docs/codex/verification-reports](docs/codex/verification-reports/)。

## 安装 v0.2

需要已发布版本时，请继续使用 v0.2 Release 归档；也可以从源码构建私有 RC。
v0.3 分支的安装器和 SBOM 内容检查已经通过 local/private readiness 门禁，但没有
发布任何 v0.3 安装端点或 SBOM。Homebrew 和 Windows 包不存在；公开 artifact
attestation 刻意以仓库可见性为启用条件。

### Release 归档

1. 打开 [v0.2.0 Release](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0)。
2. 下载与你的系统、架构匹配的归档和 `checksums.txt`。
3. 校验归档：

```bash
# Linux
sha256sum -c checksums.txt --ignore-missing

# macOS：把输出摘要与 checksums.txt 对比
shasum -a 256 hukou_0.2.0_darwin_arm64.tar.gz
```

4. 解压后，把 `hukou` 放进已有的 `PATH` 目录。使用提权权限前先确认目标路径。

### 从源码构建

使用 [go.mod](go.mod) 声明的 Go 版本：

```bash
git clone https://github.com/rtwsvj/hukou.git
cd hukou
make build
./bin/hukou version
```

完整工程门禁：

```bash
make fmt-check
make vet
make test
make race
make coverage
make license-check
make install-test
make release-test
make verify
make release-verify  # 另含 shellcheck 与 govulncheck
```

## 五分钟开始

先运行绝不会修改工具的命令：

```bash
hukou scan --unknown-only
```

其他常用只读命令：

```bash
hukou scan
hukou scan --json
hukou scan --source hukou
hukou scan --dir /path/to/extra/bin
hukou list
hukou doctor
hukou doctor --deep --json
```

测试尚未发布的 v0.3 分支构建时，可以从以下 trust-first 入口开始：

```bash
hukou explain tool
hukou adopt /path/to/tool owner/repo --tag v1.0.0 --dry-run --json
hukou outdated tool
hukou policy show tool
hukou support bundle --format json
```

`outdated` 会为符合条件的非 local 条目查询 GitHub Release metadata；其余预览
命令只读本地。`repair plan` 会按设计写入 plan 文件，因此没有放进这组零状态变更
的入门命令。

收编纯本地工具：

```bash
hukou adopt /path/to/my-tool --local
```

或把无人管理的工具关联到 GitHub 仓库：

```bash
hukou adopt /path/to/tool owner/repo --tag v1.0.0
```

第一次升级必须先预览：

```bash
hukou upgrade tool --dry-run
hukou upgrade tool
hukou rollback tool
hukou rollback tool --to v1.0.0
```

`upgrade` 与 `rollback` 会替换登记路径上的工具。把任何重要工具交给新流程前，
请先使用一次性测试二进制完成演练。

## 数据位置

默认数据目录是 `$XDG_DATA_HOME/hukou`；未设置 `XDG_DATA_HOME` 时使用
`$HOME/.local/share/hukou`。

```text
hukou/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/
└── store/
    ├── .tmp/
    └── <name>/
        ├── original/<binary>
        └── <tag>/<binary>
```

| 环境变量 | 作用 |
|---|---|
| `HUKOU_DATA_DIR` | 覆盖完整 hukou 数据根目录 |
| `XDG_DATA_HOME` | 决定默认数据根目录 |
| `GITHUB_TOKEN`、`GH_TOKEN` | 提高 GitHub API 限额；token 不会转发给不受信任的下载主机 |

## 项目状态

v0.2 已交付持久化事务恢复与只读诊断。私有 v0.3 分支已经实现 trust-first
检查、manifest v2 activation history、策略化更新与保留、窄版 repair、脱敏
support、安装器，以及供应链和社区准备。

这是私有候选版验收状态，不是发布声明。固定提交 `1fa45a0` 已通过 321 tests / 6
packages 的安全关键路径 audit、21 packages 共 641 tests 的 direct uncached
ordinary/race、coverage 72.9% 的完整本地 `release-verify`，以及 non-root
Linux/arm64 全仓 ordinary/race 与 installer/release-script tests。四目标双构建逐字节
一致，checksums、归档内容、buildinfo、安装 smoke 和 21 packages / 4 files 的 SPDX
SBOM 均通过；独立 review 未发现 P0/P1/P2，[draft PR #6](https://github.com/rtwsvj/hukou/pull/6)
已创建。GitHub-hosted Actions 仍受账户 billing/spending 限制影响，因此这里不声称
远端绿色。

本 RC 仍明确不做：

- 公开仓库或发布 `v0.3.0`；
- 公共 fixture repo 与定时真实网络 smoke；
- Homebrew 等公共安装渠道；
- 跨管理器升级或统一回滚（Topgrade 仅负责外层编排）；
- Windows、GUI、自更新和默认遥测。

当前范围见[路线图](docs/02-roadmap.md)和[变更日志](CHANGELOG.md)。

## 获得帮助和参与

以下社区入口为公开测试版准备；仓库处于私有开发阶段时可能暂不可用。

- 使用问答：[GitHub Discussions](https://github.com/rtwsvj/hukou/discussions)
- 可复现问题：[GitHub Issues](https://github.com/rtwsvj/hukou/issues)
- 安全漏洞：[SECURITY.md](SECURITY.md)
- 支持边界：[SUPPORT.md](SUPPORT.md)
- 参与贡献：[CONTRIBUTING.md](CONTRIBUTING.md)
- 项目治理：[GOVERNANCE.md](GOVERNANCE.md)
- 社区行为：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)

公开粘贴诊断信息前，请移除用户名、文件路径、仓库名、token 和私有工具信息。

## 许可证与来源

hukou 原创内容使用 [Apache License 2.0](LICENSE)，
Copyright 2026 Eric (rtwsvj)。

改编代码和依赖继续遵循各自许可证。详情见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、
[LICENSES/](LICENSES/) 和源码文件内的来源头。
