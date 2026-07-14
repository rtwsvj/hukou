# 路线图

## 已交付

### Phase 1：scan

- PATH 遍历、类型识别、shadowed 处理。
- Tier 1 来源探测责任链。
- 表格/JSON 输出和错误/警告报告。
- macOS 本机探测器补丁。

### Phase 2：adopt / upgrade / rollback / list

- manifest 与版本 store。
- GitHub Releases 客户端、资产选择、归档解压与 checksum 解析。
- 命令层 mock e2e。
- 第一轮下载、路径与重定向安全加固。

“已交付”表示实现存在；当前 HEAD 是否通过以最新 verification report 为准。

## H1：安全硬化与首个 SemVer 发布

状态：**`v0.1.0` 已发布；GitHub-hosted runner 计费调度例外已记录**。

- [x] checksum 缺条目 fail closed
- [x] 下载资产 hash 与 active binary hash 分离
- [x] upgrade/rollback 可观测失败补偿
- [x] manifest/store 进程锁
- [x] 纯本地 dry-run
- [x] adopt 同名冲突保护
- [x] hukou 来源完整性提示
- [x] 当前文档事实源与 Codex 记录链
- [x] Linux/macOS CI workflow（最新调度因账户 payment/spending limit 在 0 step 前被 GitHub 拒绝；不得解读为代码通过或失败）
- [x] 四平台可重复打包与 checksums
- [x] 全量验证与发布报告
- [x] [`v0.1.0` GitHub Release](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0)

代码、本地全量/race/对抗压力、隔离 Linux 双构建、四平台产物、远端重新下载与 Release 均有证据。GitHub-hosted runner gate 因账户计费限制未执行，属于明确基础设施例外；恢复计费后应重跑，不补写为历史通过。

## H2：运维与崩溃恢复

状态：**恢复与只读诊断基础已随 [`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) 发布；repair/历史策略已进入未发布的 V0.3 分支，公共网络 smoke 尚未完成**。

- [x] adopt/upgrade/rollback 单全局 WAL：PREPARED 回滚、COMMITTED 前滚、unknown drift 失败关闭
- [x] manifest、store、live、事务 payload 的文件与父目录持久化
- [x] 上一份可解析且 schema 受支持的 `manifest.json.bak`
- [x] 默认零写、零网络的 `doctor` 文本/JSON审计与 orphan/unclassifiable 区分
- [x] macOS + 原生 Linux 全量/race、崩溃/持久化压力、四平台可重复资产与远端下载复核
- [ ] 显式枚举、state fingerprint 绑定的安全 repair 动作（V0.3 分支已实现两类窄 action；RC 全门禁待完成）
- [ ] 激活历史与可配置版本保留策略（V0.3 分支已实现 manifest v2；全仓/发布验收待完成）
- [ ] 真实公共 fixture repo 的定时 smoke

## V0.3：Trust-first 私有 Release Candidate

状态：**subject commit `1fa45a0` 已完成 local/private RC readiness 验收并进入
[draft PR #6](https://github.com/rtwsvj/hukou/pull/6)；GitHub-hosted CI 因 billing 在
0 steps 前 infrastructure-blocked。未合并、未打 tag、未发布、仓库仍 private**。

- [x] `explain`、`adopt --dry-run --json`、共享 inventory
- [x] `outdated` 与 upgrade dry-run/真实升级共享 policy-aware checker
- [x] `policy show/set`：SemVer/GitHub-latest、channel、exact pin、rollback depth
- [x] manifest schema v2、v0/v1 deterministic migration、strict validation
- [x] activation lineage、parent-based rollback、history-aware prune plan
- [x] fingerprint-bound `repair plan/apply` 两类动作
- [x] offline redacted `support bundle`
- [x] 英文默认 CLI、双语 README、Apache-2.0、notices、community health 文件
- [x] checksum 安装器、license/install/shell gates、SBOM 与 public-only attest/CodeQL 配置
- [x] Topgrade custom command 集成文档；`upgrade --all` 继续只管 hukou entries
- [x] uncached 全仓 ordinary/race/coverage/vet/build 最终绿灯并固化 commit
- [x] macOS/Linux、四目标 build、双构建可重复性与 release snapshot/SBOM 内容验收
- [x] `pinhaoma-review` claims-vs-evidence 独立复核（P0/P1/P2 = 0）
- [x] 推送私有分支并创建 draft private PR；hosted gate 的 billing 阻断已记录为 external gate

最终固定提交证据：

- 安全关键路径定向 audit：321 tests / 6 packages。
- direct uncached 全仓 ordinary/race：各 641 tests / 21 packages，零失败。
- `GOPROXY=https://goproxy.cn,direct make release-verify`：exit 0，coverage 72.9%，
  govuln `No vulnerabilities found`；默认 `proxy.golang.org` IPv6 timeout 另行保留。
- installer link(2)/rename(2)、重复 member、symlink/竞争与 strict shell SemVer；
  manifest schema-specific/legacy-smuggling、activation safe-tag/tag-SHA、list original
  完整性等定向矩阵：pass。
- actionlint/Ruby YAML、68 Markdown/89 relative targets、production 汉字 sweep、
  official Action tag pin 对账、secret scan 与 `git diff --check`：pass。
- non-root Linux/arm64（UID 65534、source/module cache read-only、`GOPROXY=off`）
  全仓 ordinary/race 与 GNU tar 1.34 installer/release tests：pass。
- 四目标双构建逐字节一致，4/4 checksums、archive 内容/mode、buildinfo、installer
  smoke：pass。Syft 1.46.0 SBOM 为 SPDX 2.3、21 packages/4 files。
- Draft PR #6 的 CI run `29352308455` 五个 job 均 `steps=[]`，billing annotation
  明确为账户基础设施阻断；CodeQL private skip 不记 pass。

这些结果只关闭 V0.3 local/private RC readiness。合并、tag、Release、public visibility
与公开配套仓库仍需独立 Go/No-Go。

## V0.3 之后

- 版本快照、changelog diff、风险提示。
- mise/Brewfile 导出。
- 非 Go 二进制自动 repo 匹配。
- tar.xz 支持决策。
- Windows 设计与测试。
- 公共 fixture repo、定时真实网络 smoke、Homebrew Tap 与公开 beta Go/No-Go。
- 若未来需要跨管理器控制平面，另立 ADR；V0.3 只让 Topgrade 负责外层编排。
- Defense-in-depth backlog：duplicate JSON key rejection、GitHub API body cap、
  installer 总解压体积/member 数预算、`openat`/目录 fd 路径锚定。
