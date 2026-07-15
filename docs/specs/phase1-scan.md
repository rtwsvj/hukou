# Phase 1 规格:hukou scan

状态:已实现并进入维护(Eric 2026-07-11 批准)。本文件定义 scan 契约；当前验证结果以 `docs/codex/verification-reports/` 为准。

## 目标

`hukou scan`:遍历 PATH 中所有可执行文件,给每个二进制判定安装来源(归属),输出表格或 JSON。不做任何写操作、不联网(Phase 1 纯本地)。

## 技术基线

- Go 版本以根 `go.mod` 的精确 `go` 指令为准,module `github.com/rtwsvj/hukou`
- CLI 框架:spf13/cobra;其余仅标准库(禁止引入归档库/网络库)
- 平台:macOS 优先,代码用 build tag / runtime.GOOS 留 Linux 位
- 输出默认人类表格,`--json` 输出完整结构

## 命令与旗标

```
hukou scan [--json] [--unknown-only] [--source <name>] [--dir <extra-dir>...]
```

- `--unknown-only`:只列无主二进制
- `--source`:只列指定来源
- `--dir`:PATH 之外追加扫描目录

## 架构

```
main.go / cmd/root.go / cmd/scan.go     # cobra 层,薄
internal/scan/       # PATH 遍历、去重(同名取 PATH 序首个,其余记 shadowed)、可执行判定
internal/provenance/ # Detector 接口 + 责任链 + 各来源探测器
internal/output/     # 表格 + JSON
internal/manifest/   # 扫描结果的结构定义(Phase 2 的户口清单在此演进)
```

### 核心类型(必须遵守)

```go
type Binary struct {
    Name, Path, RealPath string // RealPath = EvalSymlinks 后
    Kind BinKind               // MachO | ELF | Script | Other
    Shadowed bool
}
type Attribution struct {
    Source     string // "brew" | "cargo" | "go" | ... | "system" | "unknown"
    Package    string // 包/formula/module 名
    Version    string // 可廉价获得时填
    Upstream   string // 可推导时填(如 go module path)
    Confidence string // "exact" | "inferred"
    Evidence   string // 人类可读依据,如 "symlink → ../Cellar/fzf/0.46.0/bin/fzf"
}
type Detector interface {
    Name() string
    Load(env Env) error            // 预载清单(如解析 .crates2.json),一次
    Match(b Binary) *Attribution   // 无法归属返回 nil
}
```

- `Env` 注入 HOME、PATH、各根目录——**探测器内禁止直接读 os.Getenv/硬编码 $HOME**,全部经 Env,便于 fixture 测试。
- 责任链顺序:hukou manifest → 路径前缀类 → 符号链接解析类 → buildinfo(仅对未归属的)→ system → unknown。首个命中即止。

## 探测器清单

Tier 1(必须实现,各配 fixture 单测):

| 来源 | 判定依据 |
|---|---|
| hukou | manifest 中登记的 Path/RealPath，作为链首权威来源；manifest 损坏只产生 warning，不中止 scan。事务残留：读路径仅放行**已验证的 `completed-*`**——名字精确为 `completed-<32位小写hex>`、Lstat 为真实目录（非软链）、目录内 COMMIT 标记与 ID 一致（即已提交且已收敛，只差删目录）；此时探测器照常归属，仅产出非致命 note（`stale journal residue; run a mutating command or repair to clean`），经 runner 的独立 notes 通道上报（不混入 warnings）。其余一律 fail-closed 降级（探测器从链中摘除→登记二进制回落 system/unknown 并写 warning）：`pending-*`（已发布未收敛，受保护路径可能在途）；`building-*`（可能是另一进程活跃 Begin 的可见窗口——单点检查覆盖不了整个读取周期，与遗弃残留不可区分，存在活跃写入者竞态）；unknown 及一切畸形名字（错误 ID 形状、大写 hex、软链目录、COMMIT 缺失或不匹配） |
| brew | RealPath 落在 `$(brew --prefix)/Cellar/<formula>/<ver>/`(prefix 经 Env 注入,默认 /opt/homebrew 与 /usr/local 双查);从路径提取 formula+版本 |
| macports | RealPath 前缀 /opt/local |
| cargo | `~/.cargo/.crates2.json` 解析(包名/版本/来源 URL);兜底 `~/.cargo/bin` 路径前缀 |
| rustup | `~/.rustup/toolchains` 或 `~/.cargo/bin` 中的 rustup proxy(rustc/cargo 等固定名单) |
| go | GOBIN/GOPATH/bin 路径 + **任意未归属二进制上 debug/buildinfo.ReadFile 直读**(用 internal/provenance/gobin.go,已 vendor 自 gup,勿重写) |
| npm / pnpm / yarn / bun | 各家全局 bin 目录(npm prefix、~/Library/pnpm、~/.yarn/bin、~/.bun/bin);npm 经全局 node_modules/.bin 软链反查包名 |
| pipx | `~/.local/pipx/venvs` 或 XDG;软链反查 venv 名 |
| uv | `~/.local/share/uv/tools`;`~/.local/bin` 中软链指向 uv tools 者 |
| pip-user | `~/Library/Python/*/bin` |
| mise | `~/.local/share/mise/{shims,installs}`;从 installs 路径提取工具名+版本 |
| asdf | `~/.asdf/{shims,installs}` |
| gem | gem bindir(`~/.gem/ruby/*/bin` 及系统 gem 路径) |
| nix | RealPath 前缀 /nix/store,从 store 路径提取 drv 名+版本 |
| volta | `~/.volta/bin` |
| deno | `~/.deno/bin` |
| dotnet | `~/.dotnet/tools` |
| composer | `~/.composer/vendor/bin` 或 XDG config/composer |
| krew | `~/.krew/bin` |
| curl-installer | 已知安装目录表:`~/.opencode/bin`→opencode、`~/.kimi-code/bin`→kimi、`~/.claude/local`→claude、`~/.codeium`、`~/.foundry/bin`、`~/.bun/install` 等,表驱动可扩展 |
| system | /bin /sbin /usr/bin /usr/sbin /usr/libexec、/System/*、Xcode CommandLineTools |
| unknown | 兜底 |

Tier 2(时间允许尽量做,同样标准):opam(~/.opam)、ghcup/stack(~/.ghcup、~/.local/bin 反查)、luarocks、cpanm(~/perl5/bin)、conda(~/miniconda*/bin、~/anaconda*/bin)、sdkman(~/.sdkman)、flutter/dart pub(~/.pub-cache/bin)、gcloud(google-cloud-sdk/bin)、aqua、proto(~/.proto/bin)、stew(~/.local/share/stew 默认布局)。

## macOS 细节(必须)

- 可执行判定:mode&0111;Kind 判定读文件头 4 字节:Mach-O `0xfeedface/0xfeedfacf/0xcafebabe/0xbebafeca`(含 fat/字节序),ELF `0x7f454c46`,`#!` 为 Script
- 大文件只读头部,不整读;不可读文件跳过并计数
- 同名去重:PATH 顺序首个生效,其余标记 shadowed 仍归属

## 验收标准(全部满足才算完成)

1. `go build ./... && go vet ./... && go test ./...` 全绿;测试命令必须 `&&` 串联执行
2. 每个 Tier 1 探测器至少一个 fixture 单测(testdata/ 下伪目录结构;go 探测器用 testdata 里预编译的最小 Go 二进制或对 hukou 自身二进制做集成测试)
3. `hukou scan` 在真实机器整 PATH 扫描 <5s;`--json` 输出可被 `python3 -m json.tool` 解析
4. 无第三方依赖新增(cobra 之外);`internal/provenance/gobin.go` 保持 vendor 原样接线,不改其核心逻辑
5. 表格输出末尾有汇总行:总数 / 来源数 / unknown 数 / shadowed 数;汇总行（及可选 by source 明细）之后先逐行渲染 `Report.Warnings`（前缀 `warning:`，探测器降级等），再逐行渲染 `Report.Notes`（前缀 `note:`，加载成功探测器的非致命提示），与 `explain` 表风格一致，渲染循环的首个写错误向上返回;两通道各有独立 JSON 字段（`warnings`/`notes`），安全闸门只消费 warnings

完成记录中的历史 green 不证明当前 HEAD；每次发布前仍须执行 `docs/07-testing-and-verification.md` 的全量门禁。

## 禁止事项

- 禁止复制 topgrade/pacaptr/meta-package-manager(GPL)的任何代码
- 禁止网络请求、禁止写用户目录(scan 是纯只读命令)
- 禁止在探测器里 shell 出去调 brew/npm 等外部命令(慢且不可测;一切靠文件系统证据)。唯一例外:Env 构造时允许一次性读取环境变量与固定候选路径

## 已知限制

1. **TOCTOU**：`Walk` 中 `Stat` 与后续 `Open`/`DetectKind` 之间存在时间窗；扫描过程中文件被替换、删除或改权限时，结果可能与最终打开时不一致（记入 `FileErrors` 或 `Kind=Other`），不做重试或锁。事务残留检查（`transaction.CheckReadable`）存在同款窗口——三重验证与调用方后续读取之间、以及验证三步（名字/Lstat/COMMIT）彼此之间均无原子性——已记录接受，不加读锁（2026-07-15 裁决，见 `docs/09-decision-log.md`）：读路径是同用户诊断视图而非安全边界，hukou 探测器对每个命中条目独立做 sha256 复核，归属结论不依赖该检查的时点正确性；写路径（`Begin`）保持全类别 fail-closed 且持 mutation lock。
2. **npm `.bin` 包装脚本无法反查包名**：全局 `node_modules/.bin` 下的 shim 若无法解析到真实包目录，只能回退为二进制名，不能可靠还原 npm 包名。
3. **nvm / 自定义 npm prefix 未覆盖**：仅识别 `npm_config_prefix` / `NPM_CONFIG_PREFIX` 与 brew 前缀下的全局布局；nvm 版本目录、用户手改 prefix 等未枚举。
4. **PATH 空段刻意不按 POSIX 当作 CWD**：POSIX 将 `PATH` 中空段视为当前目录；hukou 跳过空段并写入 `Report.Warnings`（与 shell 语义不一致，属有意选择）。
