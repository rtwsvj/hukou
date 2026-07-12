# Phase 2 规格:adopt / upgrade / rollback

状态:已批准(Eric 2026-07-11「剩下的你都直接做吧」)。派发 prompt 从此摘录,自包含。

## 目标

把 scan 找出的无主二进制**收编**进 hukou 管理,提供 GitHub release **升级**与**回滚**。

## 命令

```
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--force]
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substr>]
hukou rollback <name> [--to <tag>]
hukou list
```

- **adopt**:登记一个已存在的二进制。repo 推导:Go 二进制经 buildinfo 的 ModulePath(github.com/owner/repo 前缀直接取);其余必须显式给 owner/repo。二进制已被其他管理器认领(scan 归属非 unknown/curl-installer)时拒绝,`--force` 才放行。登记时记录当前 sha256 并把原始二进制**备份**进 store(original/)。
- **adopt --local <name|path>**:无上游登记(Eric 自有脚本等):manifest 条目 repo 留空、tag="local",照常 sha256+备份;upgrade 对 local 条目自动跳过并在输出中注明。
- **upgrade**:仅对已收编工具。查最新 release → 比较 tag(字符串不等即视为可升级,不做 semver 猜测)→ 选资产 → 下载到 store → 校验 → 原子替换。`--dry-run` 只报告不动手。
- **rollback**:软链切回 store 中上一个(或 --to 指定)版本。
- **list**:收编清单(名称/版本/repo/路径/store 版本数)。

## 数据布局(XDG)

```
~/.local/share/hukou/manifest.json          # 户口清单,schema_version=1
~/.local/share/hukou/store/<name>/<tag>/<bin>   # 各版本
~/.local/share/hukou/store/<name>/original/<bin> # 收编时的原件备份
```

manifest 条目:name, path(PATH 中位置), repo(owner/repo), tag, sha256, adopted_at, updated_at, upstream(如 go module path)。写入必须原子(临时文件+rename)。

**替换模型**:首次 upgrade 时,PATH 位置的实体文件移入 store,原位置换成指向 store 当前版本的软链;此后升级/回滚只动软链(原子:新建临时链+rename)。

## 网络层(internal/ghrelease)

- 仅 net/http;GITHUB_TOKEN/GH_TOKEN 自动携带(Authorization: Bearer)
- GET /repos/{owner}/{repo}/releases/latest;--tag 时走 /releases/tags/{tag}
- 指数退避重试 3 次(429/5xx/网络错误);403+RateLimit-Remaining:0 时报清晰错误(含 reset 时间)
- 下载资产用 browser_download_url,流式写临时文件,不整读内存

## 资产选择(internal/assetpick)

- 基底:vendor 自 eget detect.go(MIT,已在 LICENSES/)——OS/Arch 正则表 + 四级优先级瀑布
- 增补决胜规则(移植 ubi 思想,不搬 Rust 代码):
  1. 预过滤扩展名黑名单:.sha256/.sha256sum/.sig/.asc/.pem/.sbom/.txt/.md/.deb/.rpm/.apk/.msi/.exe(darwin 上)
  2. 版本号伪扩展名识别(foo-1.3.5.tar.gz 的 .5 不是扩展名)
  3. darwin/arm64:优先 arm64/universal,无则回退 amd64(Rosetta)
  4. 64 位平台剔除 32 位资产
  5. 归档格式偏好:.tar.gz > .tar.xz > .zip > 裸二进制
  6. 仍多候选:按名字典序取第一(确定性),同时把候选列表写进错误提示供 --asset 过滤
- 无交互模式:多候选无法决出且无 --asset 时报错并列出全部资产名(不做 stdin 交互)

## 解压与校验(internal/archive 复用/扩展 + internal/verify)

- Phase 2 支持:tar.gz/tgz、tar.xz、zip、单文件 gz、裸二进制;解压防 `../` 路径穿越
- 从归档定位二进制:精确名 → 可执行位启发式(参考 eget BinaryChooser 思路)
- 校验:release 带 `<asset>.sha256`/`checksums.txt`(逐行 "hash  filename" 格式)时强制校验,不带则记录下载文件自身 sha256 进 manifest(供事后审计);校验失败中止且不动现有安装

## 安全红线

- 永不触碰未收编的二进制;upgrade/rollback 前验证 manifest 中 sha256 与磁盘现状一致,不一致(被外部改过)时中止并提示
- 所有文件替换原子化;任何失败路径不得留下半损状态(临时文件统一放 store/.tmp/,启动时清理)
- scan 保持纯只读,不受 Phase 2 影响

## 验收

1. `go build ./... && go vet ./... && go test ./...` 全绿(测试 `&&` 串联)
2. 网络层/升级流程用 httptest 假 GitHub API 全覆盖:latest/指定 tag/429 退避/资产 404/校验失败中止
3. assetpick 表驱动测试:用 fzf、gh、lazygit、ripgrep、uv 真实 release 的资产名清单做用例,darwin/arm64 下全部选中正确资产
4. e2e(真网络,GITHUB_TOKEN 可用时):在临时目录放一个旧版真实小工具二进制 → adopt → upgrade --dry-run 报告出新 tag 与选中资产 → 真实 upgrade 到临时目录 → rollback 复原,全程不触碰 PATH 真实文件
5. 无新第三方依赖(仍仅 cobra);gobin.go/detect.go vendor 文件不改核心逻辑

## 禁止事项

- 禁抄 GPL 项目代码(topgrade/pacaptr/mpm)
- 探测器与 scan 路径保持无网络;网络只存在于 ghrelease
- 不做 self-update、不做 Homebrew/npm 等他人地盘的升级代理
