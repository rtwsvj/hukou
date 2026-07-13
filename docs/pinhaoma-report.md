# 拼好码初始执行报告（历史文档 / Superseded）

> 本文件记录 2026-07-11 动工前的提案，已被 Phase 1、Phase 2 实施和 2026-07-13 H1 执行报告取代。不得把其中“等待批准”“无代码”或阶段范围当作当前状态。当前入口见 `docs/README.md`；当前执行见 `docs/codex/execution-reports/2026-07-13-hukou-hardening-release.md`。

日期：2026-07-11 · 项目：hukou（CLI 工具户口本）· 历史状态：当时等待批准

## 1. 我对目标的理解

做一个自研 CLI 工具 `hukou`，整合三块市场空白：

1. **存量收编**：扫描 `$PATH` 所有二进制 → 反查安装来源 → 无主二进制匹配上游 repo 接管升级（全市场无人做）。
2. **散装二进制管理**：GitHub release 下载/升级（eget/stew/soar 的地盘，无赢家）。
3. **安全层**：版本清单快照、多版本共存、一键回滚、changelog/风险提示。

MVP 范围（Phase 1）：`hukou scan` —— PATH 遍历 + 溯源归属表格输出（含 `--json`）。

暂不纳入本轮：adopt/upgrade/rollback（Phase 2-3）、changelog 风险层（Phase 4）、Windows 支持、GUI。

## 2. 当前项目判断

- `/Users/eric/hukou` 已建，私有仓库 rtwsvj/hukou 已推送（README + docs/pinhaoma-sources.md）。
- 无代码，绿地开发，无需迁就已有结构。

## 3. 调研结论

五个参考项目已全部浅克隆并完成静态源码评估（commit 号见 pinhaoma-sources.md）：

| 排名 | 项目 | 技术栈 | 可复用部分 | 适配难度 | 推荐用法 |
|---|---|---|---|---|---|
| 1 | eget (MIT) | Go | detect.go 资产检测（277 行零依赖）、archive.go tar/zip 抽象、BinaryChooser 二进制定位、verify.go SHA-256 | 低 | **整文件照搬后改**（修 `../` 穿越、补自动决胜） |
| 2 | gup (Apache-2.0) | Go | `debug/buildinfo` 溯源核（精简后 ~150-200 行）、`go list -m` 版本查询、泛型并发池（122 行）、gup.json 清单 schema | 低 | **模块照搬**（溯源核零第三方依赖） |
| 3 | stew (MIT) | Go | lockfile+binaryHash 设计、github.go 资产匹配骨架、api.github.com 下载姿势（octet-stream + token）、darwin-arm64→amd64 回退 | 低 | **模块照搬** |
| 4 | ubi (MIT/Apache-2.0 双许可) | Rust | 资产决胜规则：musl/gnu 决胜、版本号伪扩展名识别、macOS ARM 回退 Rosetta、64 位剔 32 位 | 中 | **规则移植**（Rust 代码不搬，把规则翻译进 eget 的 detect 框架） |
| 5 | soar (MIT) | Rust | core.db 状态模型：checksum 寻址安装目录 + unlinked/pinned 标志 + 事务性 `use` 切换 + `update --keep` = 完整回滚机制；完整性闸门（无校验拒装）| 中 | **设计照抄**（整机 Linux 绑死不可移植；若选 Rust 则 soar-dl 可直接依赖） |

放弃项目：topgrade / pacaptr / meta-package-manager（GPL 传染，只作外部集成不抄代码）、huber（半停更，无独特价值）、upt/pacapt（定位不符）。

评估中发现的关键坑（动工必修）：

- eget：目录提取无 `../` 路径穿越防御；多候选降级为交互式选择（无人值守场景不可用，需移植 ubi 决胜规则）；`--upgrade-only` 用 mtime 判新旧（不可抄，用显式 tag 比较）。
- stew：全 prerelease 仓库会数组越界 panic；升级路径跳过 hash 校验；依赖已归档的 mholt/archiver v3.1.1（zip-slip 前科）→ 换标准库+显式防护。
- 所有参考项目都没有 macOS 特有处理：Mach-O magic 判断、quarantine xattr、codesign——hukou 必须自己做。
- GitHub API 限流：五家全都没有 retry/backoff，批量扫描场景必须自建（token + 指数退避 + 并发上限）。

## 4. 推荐方案

**主语言：Go**（待你确认，见 §13）。理由：

1. 五个参考项目中三个是 Go，可复用代码可以直接编译级照搬；Rust 侧只能移植规则。
2. 溯源的最大杀器 `debug/buildinfo` 是 **Go 标准库**——用 Go 写，读任意 Go 二进制的来源是原生能力；用 Rust 写只能 shell 出去调 `go version -m`。
3. 单静态二进制分发、交叉编译便宜，与工具定位一致。

整体策略：

- 骨架：自建（cobra 命令层），目录结构参考 gup 的 cmd/ + internal/ 划分。
- 资产选择：eget detect.go 为基底 + ubi 决胜规则移植。
- 溯源：gup buildinfo 核 + 自建 brew/cargo/npm/pipx/uv/mise 只读探测器。
- 清单：stew lockfile（binaryHash）+ gup gup.json 字段合并设计。
- 多版本/回滚：soar 状态模型（版本寻址目录 + 软链切换），JSON 清单先行，sqlite 后置。
- 不采用：eget 的 mtime 升级判定、stew 的 archiver 依赖、soar 的 landlock 沙箱（Linux-only）。

## 5. 具体执行步骤（获批后）

1. 派发前体检：`bash ~/.claude/layer-health.sh`。
2. 我写 Phase 1 规格（scan 命令的探测器清单、输出格式、验收标准）。
3. 在隔离 worktree 派发 MiniMax-M3 实现骨架 + scan 命令；vendor eget/gup 代码由我先行手工摘入（带许可头），执行者在其上接线。
4. 质量三闸：`go test ./...` 通过 → worktree 内 `ocr review` → 我终审整合。
5. 合入 main，在你机器上实测 `hukou scan`，出归属准确率报告。

## 6. 预计创建/修改的文件（Phase 1）

| 文件路径 | 操作 | 内容概要 |
|---|---|---|
| `go.mod` / `main.go` / `cmd/root.go` / `cmd/scan.go` | 创建 | 模块初始化、cobra 骨架、scan 命令 |
| `internal/scan/scan.go` | 创建 | PATH 遍历、去重、可执行判定（含 Mach-O magic） |
| `internal/provenance/gobin.go` | 创建 | gup pkginfo.go 摘编（buildinfo 提取 + 过滤器） |
| `internal/provenance/{brew,cargo,npm,pipx,uv,mise}.go` | 创建 | 各家清单文件/软链目标的只读探测器 |
| `internal/provenance/provenance.go` | 创建 | 探测器接口 + 责任链调度 |
| `internal/manifest/manifest.go` | 创建 | 户口清单 schema（stew+gup 合并设计），scan 结果可导出 |
| `internal/output/table.go` | 创建 | 表格 + `--json` 输出 |
| `LICENSES/` + 各摘编文件头 | 创建 | eget MIT、gup Apache-2.0 原文与版权声明 |
| `docs/codex/change-records/…` | 创建 | 按 pinhaoma-docs 规程记录每次执行者改动 |

Phase 2-4 的文件规划（internal/assetpick、internal/ghrelease、internal/store 等）在各阶段开工前出补充报告。

## 7. 预计运行的命令

| 命令 | 作用 | 写入/下载 |
|---|---|---|
| `go mod init github.com/rtwsvj/hukou && go mod tidy` | 初始化模块 | 是（go.sum、模块缓存） |
| `~/.opencode/bin/opencode run -m minimax-cn-coding-plan/MiniMax-M3 "<自包含任务>"`（隔离 worktree） | 主力执行 | 是（worktree 内） |
| `go build ./... && go test ./...` | 构建与测试闸 | 是（构建缓存） |
| `ocr review` | 机器预审 | 否（只读分析） |
| `git commit` / `git push` | 阶段性提交 | 是 |

## 8. 新增依赖（Phase 1，刻意最少）

| 依赖 | 用途 | 可替代方案 |
|---|---|---|
| spf13/cobra (Apache-2.0) | 命令行框架 | 标准库 flag（后续命令多，不划算） |
| （其余全部标准库） | buildinfo/JSON/表格输出 | — |

Phase 2 起再引入：hashicorp/go-version（semver 比较）。不引 archiver 系。

## 9. 数据、配置和环境变量

- `GITHUB_TOKEN`：Phase 2 起用于 release 查询提额，只读环境变量名，不索取值。
- 户口清单：`~/.local/share/hukou/manifest.json`（XDG 规约，跟随 stew/gup 实践）。
- 本轮无数据库、无外部服务。

## 10. 来源记录方式

已建立 docs/pinhaoma-sources.md（repo URL + 评估 commit + 复用方式 + 本地对应 + 已知坑），每次摘编代码时同步更新"本地对应"列并在文件头保留原版权声明。

## 11. 风险和回滚方式

- **溯源准确率不达预期**（生僻安装方式）：责任链末端归"unknown"不误报；准确率验收看 §12。
- **执行者产出质量**：三闸把关；vendor 部分我手工摘入不经执行者。
- **GPL 污染**：禁抄名单已写入 README，终审时 diff 对照。
- 回滚：全程 git 管理,Phase 边界打 tag;任何阶段可 `git revert` 或回退 tag。

## 12. 验证方式

- 单测：探测器用 fixture 目录（伪 Cellar/伪 .crates2.json/真 Go 二进制样本）。
- 实机验收：在你的 macOS 上跑 `hukou scan`，与 `brew list`、`cargo install --list`、`gup list`、mise 清单交叉核对，归属准确率 ≥90%，全 PATH 扫描耗时 <5s。
- `ocr review` 零高危 + 我终审通过。

## 13. 需要你确认的问题

1. **主语言 Go**（我的推荐）还是 Rust？选 Rust 则复用策略反转（vendor ubi 四件套 + 依赖 soar-dl，溯源 shell 出去调 go）。
2. Phase 1 溯源器覆盖清单：brew / cargo / go / npm / pnpm / pipx / uv / mise / asdf / rustup + 无主，够吗？还要加谁（比如 gem、composer）？
3. 执行按 CLAUDE.md 标准分层（MiniMax 主力 + 三闸）默认走起，对吗？

请明确回复「批准执行」或「按方案执行」（可附上述问题的答案），我才开始动工。
