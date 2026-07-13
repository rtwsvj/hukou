# Phase 2 规格:adopt / upgrade / rollback

状态:主体已实现，2026-07-13 H1 安全契约修订中。最终通过以当前 commit 的 verification report 为准。

## 目标

把 scan 找出的无主二进制**收编**进 hukou 管理,提供 GitHub release **升级**与**回滚**。

## 命令

```
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force]
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substr>]
hukou rollback <name> [--to <tag|original>]
hukou list
```

- **adopt**:登记一个已存在的二进制。repo 推导:Go 二进制经 buildinfo 的 ModulePath(github.com/owner/repo 前缀直接取);其余必须显式给 owner/repo。二进制已被其他管理器认领(scan 归属非 unknown/curl-installer/local-project)时拒绝,`--force` 才放行。登记时记录当前 sha256 并把原始二进制**备份**进 store(original/)。
- **adopt --local <name|path>**:无上游登记(Eric 自有脚本等):manifest 条目 repo 留空、tag="local",照常 sha256+备份;upgrade 对 local 条目自动跳过并在输出中注明。
- **upgrade**:仅对已收编工具。查最新 release → 比较 tag(字符串不等即视为可升级,不做 semver 猜测)→ 选资产 → 下载到 store → 校验 → 原子替换。`--dry-run` 只报告不动手。
- **rollback**:把 store 中上一个(或 --to 指定)版本原子复制到活跃常规文件。
- **list**:收编清单(名称/版本/repo/路径/store 版本数)。

## 数据布局(XDG)

```
~/.local/share/hukou/manifest.json          # 户口清单,schema_version=1
~/.local/share/hukou/state.lock             # 写命令进程互斥
~/.local/share/hukou/store/<name>/<tag>/<bin>   # 各版本
~/.local/share/hukou/store/<name>/original/<bin> # 收编时的原件备份
```

manifest 条目:name, path(PATH 中位置), repo(owner/repo), tag, sha256(active binary), adopted_at, updated_at, upstream(如 go module path), asset_name, asset_sha256(下载归档), checksum_asset, checksum_verified。H1 新增审计字段可选，保持 schema v1 向后兼容。写入必须原子(临时文件+rename)。

数据根优先使用 `HUKOU_DATA_DIR`，否则 `${XDG_DATA_HOME}/hukou`，默认 `~/.local/share/hukou`。adopt、真实 upgrade、rollback 非阻塞获取 `<dataRoot>/state.lock`；锁已占用时立即报错。scan 和纯 dry-run 不持写锁。

**替换模型**:original 与各版本在 store 中保持不可变副本。升级/回滚把目标版本复制为 PATH 位置同目录的完整临时常规文件，设置 mode、`fsync`、close 后 rename 覆盖活跃文件。新激活不交换 symlink inode；旧版遗留 symlink 作为兼容输入，首次成功激活后迁移为常规文件。

## 网络层(internal/ghrelease)

- 仅 net/http;GITHUB_TOKEN/GH_TOKEN 自动携带(Authorization: Bearer)
- CLI upgrade 使用 GET /repos/{owner}/{repo}/releases/latest；库级 `ghrelease.ByTag` 支持 `/releases/tags/{tag}`，当前 CLI 不暴露 `--tag`
- 指数退避重试 3 次(429/5xx/网络错误);403+RateLimit-Remaining:0 时报清晰错误(含 reset 时间)
- 下载资产用 browser_download_url,流式写临时文件,不整读内存

## 资产选择(internal/assetpick)

- 基底:vendor 自 eget detect.go(MIT,已在 LICENSES/)——OS/Arch 正则表 + 四级优先级瀑布
- 增补决胜规则(移植 ubi 思想,不搬 Rust 代码):
  1. 预过滤扩展名黑名单:.sha256/.sha256sum/.sig/.asc/.pem/.sbom/.txt/.md/.deb/.rpm/.apk/.msi/.exe(darwin 上)
  2. 版本号伪扩展名识别(foo-1.3.5.tar.gz 的 .5 不是扩展名)
  3. darwin/arm64:优先 arm64/universal,无则回退 amd64(Rosetta)
  4. 64 位平台剔除 32 位资产
  5. 归档格式偏好:.tar.gz/.tgz > .zip > .gz > 裸二进制;tar.xz/txz 与已知不支持容器格式当前不可选
  6. 仍多候选:按名字典序稳定列出候选并失败，要求用户使用 `--asset` 缩小范围
- 无交互模式:多候选无法决出且无 --asset 时报错并列出全部资产名(不做 stdin 交互)

## 解压与校验(internal/archive 复用/扩展 + internal/verify)

- Phase 2 支持:tar.gz/tgz、zip、单文件 gz、裸二进制;tar.xz 及其他已知识别但不支持的容器格式明确拒绝,不得退化为裸二进制;未知后缀若走裸文件路径,仍必须识别为当前支持的 ELF/Mach-O/shebang 可执行文件;解压防 `../` 路径穿越
- 从归档定位二进制:精确名 → 可执行位启发式(参考 eget BinaryChooser 思路)
- 校验:release 带 `<asset>.sha256`/`checksums.txt` 时强制校验。通用清单接受 GNU/BSD 命名格式；精确 sidecar 可只含一个 64 位摘要。checksum 文件缺少所选资产、条目格式无效或 hash 不匹配都必须失败关闭。不带 checksum 仍计算 `asset_sha256`,但不得设置 `checksum_verified=true`;任何校验失败中止且不动现有安装。

## 安全红线

- 永不触碰未收编的二进制;upgrade/rollback 前验证 manifest 中 sha256 与磁盘现状一致,upgrade 在下载/解压后、激活前再次验证,不一致(被外部改过)时中止并提示
- 所有文件替换原子化;下载/解压临时文件统一放 store/.tmp/、启动时清理;激活临时常规文件放在 live path 同目录的隐藏名 `.hukou-tmp-*`，完成写入、mode、`fsync`、close 后再 rename。upgrade/rollback 在激活前捕获旧路径拓扑与旧 manifest；激活后任一可观测错误必须补偿恢复。断电/SIGKILL 一致性留待 WAL/doctor。
- scan 保持纯只读,不受 Phase 2 影响
- `upgrade --dry-run` 只读取 manifest 与 GitHub release metadata:不得创建 data root、不得获取写锁、不得 GC、不得下载资产。

## 验收

1. `go build ./... && go vet ./... && go test ./...` 全绿(测试 `&&` 串联)
2. 网络层/升级流程用 httptest 假 GitHub API 全覆盖:latest/指定 tag/429 退避/资产 404/校验失败中止
3. assetpick 表驱动测试:用 fzf、gh、lazygit、ripgrep、uv 真实 release 的资产名清单做用例,darwin/arm64 下全部选中正确资产
4. e2e(真网络,GITHUB_TOKEN 可用时):在临时目录放一个旧版真实小工具二进制 → adopt → upgrade --dry-run 报告出新 tag 与选中资产 → 真实 upgrade 到临时目录 → rollback 复原,全程不触碰 PATH 真实文件
5. 无新第三方依赖(仍仅 cobra);gobin.go/detect.go vendor 文件不改核心逻辑
6. 失败注入覆盖 checksum 缺条目、manifest 保存失败补偿、锁竞争、adopt 同名冲突与纯 dry-run 无写入
7. 发布前在临时 HOME/PATH/HUKOU_DATA_DIR 完成 CLI smoke;不得触碰真实用户二进制与真实 manifest

## 禁止事项

- 禁抄 GPL 项目代码(topgrade/pacaptr/mpm)
- 探测器与 scan 路径保持无网络;网络只存在于 ghrelease
- 不做 self-update、不做 Homebrew/npm 等他人地盘的升级代理

## 已知限制(实现期追加)

- **tar.xz 暂不支持**:Go 标准库无 xz 解码器,与"零第三方依赖"约束冲突;主流 release 均有 tar.gz/zip 资产。遇到 xz-only 的上游时再评估引入依赖。
