# hukou（户口）

**给你机器上"黑户"二进制的一本户口本——找出、收编、升级、回滚那些没有任何包管理器认领的 CLI 工具。**

[![CI](https://github.com/rtwsvj/hukou/actions/workflows/ci.yml/badge.svg)](https://github.com/rtwsvj/hukou/actions/workflows/ci.yml)
[![CodeQL](https://github.com/rtwsvj/hukou/actions/workflows/codeql.yml/badge.svg)](https://github.com/rtwsvj/hukou/actions/workflows/codeql.yml)
[![Release](https://img.shields.io/github/v/release/rtwsvj/hukou?sort=semver)](https://github.com/rtwsvj/hukou/releases)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

[English](README.md) · [项目文档](docs/README.md) · [安全策略](SECURITY.md) · [架构决策记录](docs/adr/)

---

每台开发机都会堆积一批没有包管理器负责的二进制：从 GitHub Release 抓来的、
`go install` 构建的、从另一台机器拷过来的、某个 `curl … | sh` 安装脚本丢进
`~/.local/bin` 的。`brew`、`mise`、`npm` 这些工具管好它们自己装的东西——其余一切
不在它们的职责范围内。而恰恰是这些文件，你最容易忘记、从不升级、又不敢手工覆盖。

hukou（户口，*household registry*）就是为这块空白而生。它遍历你的 `PATH`，把每个
可执行文件溯源到一个已知来源——或者诚实地告诉你：来源不明——再给那些黑户
（strays）一条它们从来没有过的、可升级可回滚的注册路径。

```
topgrade  负责编排你的各个管理器
mise      负责装新工具
hukou     负责收编无人认领的黑户
```

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/rtwsvj/hukou/main/scripts/install.sh | sh
```

安装器会下载最新 Release 归档，对同源 `checksums.txt` 校验，并在检测到已认证的
[`gh`](https://cli.github.com/) CLI 时，先按锚定的签名者身份验证归档的 GitHub
build-provenance（Sigstore）attestation，再解包。设置
`HUKOU_REQUIRE_ATTESTATION=1` 可把该校验从"缺失则告警并回退到传输层信任"的默认
行为改为强制。想先看清楚？加 `--dry-run`，或直接从
[Releases 页面](https://github.com/rtwsvj/hukou/releases)自行下载归档与
`checksums.txt`。

## 30 秒上手

hukou 刻意保持 trust-first：*读*命令排在*写*命令之前，重量级的写操作都能先预
览——`adopt --dry-run`、`upgrade --dry-run`、`repair plan`。（`rollback` 和
`policy set` 没有 dry-run，执行即生效。）

下面这段是在一个演示沙箱里真实跑出来、未经修饰的会话，`PATH` 上有一个黑户 `rg`。

**1. 看清 `PATH` 上谁归谁**（纯本地、只读、不联网）：

```console
$ hukou scan --unknown-only
NAME  PATH                    KIND    SOURCE   PACKAGE  VERSION  SHADOWED  EVIDENCE
rg    /tmp/quickstart/bin/rg  Script  unknown  rg                          no prior detector matched; realpath=/private/tmp/quicksta...

summary: total=1 sources=1 unknown=1 shadowed=0 skipped=1
by source: unknown=1
```

**2. 收编一个黑户。** 关联到 GitHub 仓库（无上游的用 `--local`）。hukou 会把原始
字节作为不可变备份保存下来：

```console
$ hukou adopt rg BurntSushi/ripgrep --tag 15.1.0
Adopted rg (15.1.0) at /tmp/quickstart/bin/rg
repo: BurntSushi/ripgrep
```

**3. 升级它。** 先预览——`--dry-run` 绝不碰文件——再应用。hukou 会选择平台资产、
在 Release 提供 checksum 时校验发布方 checksum（见[安全模型](#安全模型)），并用
原子 rename 替换活跃文件：

```console
$ hukou upgrade rg --dry-run
Would upgrade rg: 15.1.0 -> 15.2.0 using asset ripgrep-15.2.0-aarch64-apple-darwin.tar.gz (higher eligible semantic version is available)

$ hukou upgrade rg
Upgraded rg: 15.1.0 -> 15.2.0
```

**4. 回滚**——新版本出问题就退回去。hukou 保留历史版本和收编时的原件：

```console
$ hukou rollback rg
Rolled back rg to 15.1.0
```

整条闭环就是：`scan → adopt → upgrade → rollback`。任何时候都能用
`hukou doctor` 只读审计当前状态，一个字节都不写。

![hukou demo](assets/demo.gif)

## 为什么不直接用 topgrade / mise / eget？

它们都是好工具。hukou 不替代它们——它补的是它们留下的空位。这些工具围绕**从它们
安装那一刻起往后**的软件构建；hukou 为**规模化存量收编**而设计——扫描整条
`PATH`、带证据地溯源归属，再给收编的黑户提供谱系化的升级与回滚。

| 工具 | 它管什么 | hukou 补的空位 |
|---|---|---|
| **Homebrew / mise / aqua** | *它们自己*装的工具，只管往后 | 存量二进制。`mise link` 可以手动登记单个外部工具，但没有全 `PATH` 扫描、没有溯源归属、没有谱系化回滚 |
| **topgrade** | 一条命令编排其他升级器 | 没东西可交给黑户；hukou 作为一个 custom command 接进来 |
| **eget / stew** | 把 GitHub Release 拉到位，带 checksum 校验（stew 还有 lockfile） | 不探测、不收编磁盘上*已存在*的二进制，也没有针对被替换文件的激活谱系回滚 |
| **hukou** | **存量**无主二进制：溯源 → 收编 → 升级 → 回滚 | — |

hukou 与 topgrade 是组合关系而非竞争关系。把它注册成 custom command，你那条
"升级一切"就会带上你的黑户：

```toml
# topgrade.toml
[commands]
"hukou adopted CLI tools" = "hukou upgrade --all"
```

`upgrade --all` 指的是*被 hukou 收编的条目*——绝不越界到别的管理器地盘。所有权边界
见 [docs/integrations/topgrade.md](docs/integrations/topgrade.md)。

## 特性

- **靠证据溯源，不靠猜。** 一条 24 个来源探测器组成的链（Homebrew、MacPorts、
  mise、asdf、rustup、cargo、npm、pnpm、yarn、bun、pipx、uv、pip、gem、nix、
  volta、deno、dotnet、composer、krew、`curl … | sh` 安装器、macOS app bundle、
  本地构建、Go build info），末端由 hukou/system/unknown 三个终端归属收口，为
  每个二进制归属并记录*理由*。Go 二进制直接从内嵌 build info 溯源——这也意味着
  go 探测器会先认领 `go install` 的二进制，收编它需要显式 `--force`；之后
  `adopt` 无需额外旗标即可从 build info 推断出 `owner/repo`。见
  [`hukou scan`](docs/05-cli-reference.md)。
- **崩溃安全的事务化替换。** 活跃二进制通过同目录临时文件加原子 rename 切换，一份
  持久化写前日志（WAL）记录 before/after 状态。升级中途被杀的进程可确定性恢复——
  PREPARED 事务回滚、COMMITTED 事务前滚，遇到未知外部漂移则失败关闭。见
  [ADR-0002](docs/adr/ADR-0002-regular-file-activation.md) 与
  [ADR-0003](docs/adr/ADR-0003-crash-recovery-and-doctor.md)。
- **多版本 store + 真回滚。** 每个收编工具都保留收编时的不可变原件加历史版本。
  `rollback` 沿记录下来的激活谱系（逻辑 parent，而非目录时间戳）前进。见
  [ADR-0005](docs/adr/ADR-0005-manifest-v2-history-policy-and-repair.md)。
- **供应链感知的安装。** Release 提供 checksum 资产时，所选资产的条目缺失、
  无效或不匹配一律失败关闭；Release 完全没有 checksum 资产时，hukou 大声告警
  并记录下载资产自身的 SHA-256，而不是假装校验过。下载资产与激活二进制的 hash
  分开记录；安装器按锚定的签名者身份验证 GitHub build-provenance attestation。见
  [SECURITY.md](SECURITY.md)。
- **可信任的只读诊断。** `doctor` 默认不取锁、不写入、不联网。它只报告问题，绝不
  偷偷删除或"修复"你的数据。repair 是单独的、显式的 `plan → apply` 流程，且与状态
  fingerprint 绑定。
- **策略化更新。** 逐工具的 SemVer / GitHub-latest、stable / prerelease、精确
  pin，以及回滚保留深度——用 `policy show` 查看，用 `policy set` 原子修改。
- **脱敏的 support bundle。** `support bundle` 生成离线、脱敏的 JSON 诊断——不含
  路径、仓库名、用户名、环境变量或 WAL payload——且绝不自动上传。

## 命令一览

| 命令 | 写？ | 联网？ | 用途 |
|---|---|---|---|
| `scan` | 否 | 否 | 盘点 `PATH` 并为每个可执行文件归属 |
| `explain <name\|path>` | 否 | 否 | 显示 `PATH` 上哪个二进制胜出、每个同名项归谁 |
| `adopt <name\|path> [owner/repo]` | 是 | 否 | 登记二进制；`--dry-run` 预览，`--local` 跳过仓库 |
| `list` | 否 | 否 | 列出已收编工具与保留版本数 |
| `outdated [name…]` | 否 | 是 | 检查是否有更新版本，不下载 |
| `upgrade [name…]` | 是 | 是 | 校验并替换；`--dry-run` 预览，`--all` 全部条目 |
| `rollback <name>` | 是 | 否 | 激活某个保留版本；`--to <tag>` 或 `--to original` |
| `up` | 否（本版本仅 dry-run） | 否 | 规划全机升级（覆盖已知包管理器）；`--dry-run` 打印将执行的命令与库存概要，真实执行在后续版本落地（占位退出码 2） |
| `policy show/set` | show: 否；set: manifest | 否 | 查看或原子修改更新/回滚策略 |
| `doctor` | 否 | 否 | 审计 manifest、store、journal 与活跃文件；`--deep`、`--json` |
| `repair plan/apply` | plan: 仅写 plan 文件；apply: manifest、活跃二进制、事务状态（可能隔离 journal 残留） | 否 | fingerprint 绑定的未决事务或 manifest 备份恢复 |
| `support bundle` | 仅写 bundle | 否 | 脱敏、离线诊断 |

完整旗标与副作用：[docs/05-cli-reference.md](docs/05-cli-reference.md)。多数只读
命令支持 `--json` 供脚本使用。

## 平台支持

| 系统 | 架构 | 状态 |
|---|---|---|
| macOS | arm64 | 每日实际使用的主目标；在真实机器上运行 |
| macOS | amd64 | 交叉编译的发布构建；尚无运行时测试 |
| Linux | amd64 | 交叉编译的发布构建；有发布归档冒烟检查 |
| Linux | arm64 | 交叉编译的发布构建；尚无运行时测试 |
| Windows | — | 不支持 |

hukou 不会把"能交叉编译"当成"已受支持"。macOS arm64 是它每天真正运行的地方；其余
目标是交叉编译的发布构建，其中 Linux amd64 另有发布归档冒烟检查。欢迎在真实硬件上
做独立验证——把你看到的结果开个 issue 告诉我们。

## 安全模型

hukou 会重写可执行文件并保存回滚材料，因此完整性、路径处理、归档解压、凭据处理和
崩溃恢复都属于它的安全边界。设计取向很简单：**宁可明确拒绝，也不聪明地猜。**

- Release 存在 checksum 资产时，所选资产的条目缺失、无效或不匹配一律失败关闭——
  hukou 不会"应该没事吧"地绕过校验缺口。Release 完全没有 checksum 资产时会带着
  醒目告警继续，且两种情况下都会记录下载资产的 SHA-256。
- 下载资产 hash 与激活二进制 hash 分开记录，被掉包的产物无法冒充已校验的产物。
- `adopt`、`upgrade`、`rollback` 在替换前持进程锁复核活跃文件；dry-run 计划本身
  永远不是写授权。
- 恢复对 PREPARED 事务回滚、对 durable COMMIT 前滚，遇到未知外部漂移则停下并保留
  证据。它绝不为了"修好"一个它看不懂的状态而删除用户数据。
- adopt 会拒绝带 setuid/setgid/sticky 等特殊权限位的源文件，而非静默丢弃。

阅读 [SECURITY.md](SECURITY.md) 了解威胁模型与私密漏洞上报，阅读
[ADR-0004](docs/adr/ADR-0004-trust-first-and-manager-boundaries.md) 了解
trust-first 命令阶梯与管理器所有权边界。收编重要二进制前，先扫一眼
[数据与接口契约](docs/04-data-and-api.md)和[已知风险](docs/08-risk-and-debt.md)。

## 数据位置

hukou 遵循 XDG，所有状态存放在 `$XDG_DATA_HOME/hukou`（或
`$HOME/.local/share/hukou`）。它绝不改动你的 shell 配置。

```text
hukou/
├── state.lock
├── manifest.json
├── manifest.json.bak
├── transactions/            # 写前日志
└── store/
    └── <name>/
        ├── original/<binary>   # 收编时的不可变备份
        └── <tag>/<binary>      # 保留版本
```

| 环境变量 | 作用 |
|---|---|
| `HUKOU_DATA_DIR` | 覆盖完整 hukou 数据根目录 |
| `XDG_DATA_HOME` | 决定默认数据根目录 |
| `GITHUB_TOKEN`、`GH_TOKEN` | 提高 GitHub API 限额；绝不转发给下载主机 |
| `HUKOU_REQUIRE_ATTESTATION` | 强制安装器 attestation 校验（`1`/`true`/`yes`） |

## 从源码构建

需要 [go.mod](go.mod) 声明的 Go 工具链版本。

```sh
git clone https://github.com/rtwsvj/hukou.git
cd hukou
make build
./bin/hukou version
```

完整本地门禁是 `make verify`（fmt、vet、单测、race、coverage、build、license、
安装器、release 脚本检查）；`make release-verify` 另加 `shellcheck` 与
`govulncheck`。见 [docs/06-dev-setup.md](docs/06-dev-setup.md)。

## 常见问题

**为什么叫"hukou"？**
户口是中国的户籍制度——谁住在哪的官方记录。这正是本工具在做的事的精确隐喻：你的
`PATH` 里住满了"居民"，大多数在某个包管理器名下登记造册，还有一小撮无证黑户。hukou
给这些黑户一个户口条目——一个已知的来源、一段版本历史，以及升级出岔子时的回家路。

**它会和 Homebrew / mise / npm 打架吗？**
不会。默认情况下 hukou *拒绝*收编已被其他管理器认领的二进制（除非你清楚为什么，用
`--force` 覆盖）。它管的是空白地带，不碰别人的地盘。

**指向重要二进制安全吗？**
安全机制正是为此而造，但信任要一步步来：先用只读命令（`scan`、`explain`、`list`、
`doctor`），第一次 `adopt` 和 `upgrade` 都用 `--dry-run` 预览，并在收编你真正在乎
的东西之前，先拿一个一次性二进制把整条闭环演练一遍。

**能一条命令升级我机器上的一切吗？**
它自己不能——而且是刻意的。跨管理器编排属于
[topgrade](https://github.com/topgrade-rs/topgrade)；hukou 作为一个 custom command
接进来，让它强悍的事务与回滚保证始终限定在它真正拥有的东西上。

**它把状态放哪？会动我的 shell 配置吗？**
放在 `$XDG_DATA_HOME/hukou`（见[数据位置](#数据位置)）。它绝不修改 `.bashrc`、
`.zshrc` 或任何 shell 启动文件。

## 参与贡献

欢迎 issue 和 PR——尤其是真实 Linux 硬件上的验证，以及尚未进入探测链的包管理器的
探测覆盖。见 [CONTRIBUTING.md](CONTRIBUTING.md)、
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) 和 [GOVERNANCE.md](GOVERNANCE.md)。使用
问题走 [GitHub Discussions](https://github.com/rtwsvj/hukou/discussions)；疑似漏洞
按 [SECURITY.md](SECURITY.md) 而非公开 tracker 上报。

## 许可证

hukou 原创内容使用 [Apache License 2.0](LICENSE)，Copyright 2026 rtwsvj。
改编代码与依赖保留各自许可证——见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、
[docs/VENDORED.md](docs/VENDORED.md) 和 [LICENSES/](LICENSES/)。
