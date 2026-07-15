# Codex Session Log

## 2026-07-15 — V0.3 外部审计交接

- Branch: `codex/hukou-v0.3-private-rc`
- Code Subject: `1fa45a0d8473446e3208490f037aef924abea181`
- Original Evidence Docs: `2cd0467098700b899b8b87ee627eb2b75412f397`
- Draft PR: `https://github.com/rtwsvj/hukou/pull/6`
- Execution Report: `execution-reports/2026-07-15-external-audit-handoff.md`
- Change Record: `change-records/2026-07-15-external-audit-handoff.md`
- Verification Report: `verification-reports/2026-07-15-external-audit-handoff.md`
- Scope: documentation/audit handoff only；no production code, workflow, tag,
  Release, merge, or visibility change

### 交付与更正

- 新增根 `AUDIT.md` 与 `docs/audit/`，固定 base/code subject/original docs 三类
  对象，并要求审计员另行记录实际 audit-package commit。
- 提供分层复现命令、C1-C10 source/test/caveat 映射、攻击面 checklist、artifact/
  platform/toolchain matrix 和 finding template。
- 明确 ignored `dist/` 产物不是 portable evidence；SBOM 可复现语义，不保证新生成
  JSON 字节 hash 相同；原容器完整命令与部分静态检查 raw logs 没有保留。
- 修正 ADR、roadmap 与 Topgrade 文档中的旧 pending 状态，并把原
  `pinhaoma-review` 降级为“Codex 团队内部记录、无单独 raw report”，不再描述为
  外部独立 clean bill。
- 外部交接审阅新增待第三方确认/定级的高优先级 hypotheses：download global cap、
  Go/shell archive work/member budget、manifest/API body cap、transaction intent trust
  anchor、repair plan replay、publisher checksum policy、private→public attestation
  lifecycle，以及 Go 1.26.2/1.26.5 toolchain 差异。
- 当前仍是 private draft PR；正式发布仍为 v0.2.0，无 v0.3 tag/Release。原
  evidence-docs head 的 CI `29353234544` 为五个 `steps=[]` billing failure，CodeQL
  `29353234543` 为 private skip；审计员必须刷新自己审计时的 PR head。

## 2026-07-14 — V0.3 private release candidate

- Branch: `codex/hukou-v0.3-private-rc`
- Base: `main@bd4faa32d9b5b604b1b224f97fe891ed670f3742`
- Subject Commit: `1fa45a0d8473446e3208490f037aef924abea181`
- Draft PR: `https://github.com/rtwsvj/hukou/pull/6`
- Execution Report: `execution-reports/2026-07-14-v0.3-private-rc.md`
- Change Record: `change-records/2026-07-14-v0.3-private-rc.md`
- Verification Report: `verification-reports/2026-07-14-v0.3-private-rc.md`
- Status: local/private RC readiness pass; GitHub-hosted gate infrastructure-blocked
- Boundary: repository remains private; no `v0.3.0` tag or public release

### 最终结论

- V0.3 spec 与两份 ADR 已落盘；trust-first、manifest v2/history/policy/retention、
  两类 repair 与脱敏 support bundle 已进入固定 subject commit。
- 许可证、notices、双语入口、社区模板、HTTPS/checksum 安装器、SBOM 与
  public-only attestation/CodeQL 配置已进入共享工作树；这些都没有改变仓库可见性。
- 已证实固定提交结果：安全关键路径 audit 321 tests / 6 packages；direct
  uncached 全仓 ordinary/race 各 641 tests / 21 packages；命令级 GOPROXY mirror
  下 `make release-verify` exit 0、coverage 72.9%、govuln 无已知漏洞。默认
  `proxy.golang.org` IPv6 timeout 仍单独保留，不能记为默认网络路径通过。
- non-root Linux/arm64（UID/GID 65534、只读 source/module cache、固定
  `golang:1.26.5-bookworm` digest、`GOPROXY=off`）全仓 ordinary/race 通过；GNU tar
  1.34 下 installer/release tests 通过。
- manifest schema-specific required fields/legacy v2 smuggling、activation safe tag 与
  tag/SHA binding、installer link(2)/rename(2)+duplicate member、shell/Go strict SemVer、
  list original completeness 已进入定向回归。
- actionlint/Ruby YAML、68 Markdown/89 relative targets、production 汉字 sweep、
  official Action pin 对账、secret scan 与 diff check 通过。
- 四目标 release build 两次逐字节一致；checksums、archive root/mode、buildinfo、
  installer smoke 通过。本地可运行二进制为 `bin/hukou`，固定 RC snapshot 位于被忽略的
  `dist/release-builds/v0.3.0-rc.1/`。
- SBOM 验收发现旧 workflow 扫描 `dist/` 仅得到 1 package/0 files；修复后使用 Syft
  1.46.0 扫描四个平台二进制，得到 SPDX 2.3、21 packages/4 files。
- Codex 团队内部 `pinhaoma-review` 当时记录 P0/P1/P2 = 0；coverage
  73.8%→72.9% 的 -0.9pp 作为代码面扩大后的 P3 记录并接受。该 review 无单独
  raw report，不替代后续外部审计。
- 分支已推送并创建 draft PR #6。CI run `29352308455` 的五个 job 均 `steps=[]`，
  billing/spending annotation 明确为账户基础设施阻断；CodeQL run `29352310557`
  在 private repository 按设计 skipped。
- local/private RC readiness 已通过；draft PR 合并、tag、Release、仓库公开与公共
  fixture/Homebrew 仍需独立 Go/No-Go。
- 后续 defense-in-depth 明确保留：duplicate JSON key、GitHub API body cap、installer
  总解压体积/member 数预算、`openat`/目录 fd 路径锚定。

## 2026-07-14 — v0.2 文档一致性验收

- Branch: `codex/hukou-v0.2-docs-acceptance`
- Base: `main@728fc22b07f0e9d65276d1ac71a7add81af33161`
- Subject Commit: `a5d48d170f0cca23652595d9babdd98139775d49`
- PR: `https://github.com/rtwsvj/hukou/pull/5`
- Execution Report: `execution-reports/2026-07-14-v0.2-documentation-acceptance.md`
- Change Record: `change-records/2026-07-14-v0.2-documentation-acceptance.md`
- Verification Report: `verification-reports/2026-07-14-v0.2-documentation-acceptance.md`
- Status: verification pass with GitHub-hosted Actions infrastructure exception

### 结论

- 三路独立复核确认文档体系已有 project brief → requirements → roadmap → architecture/data/CLI → development/testing/risk/decision/release/glossary → ADR/specs → Codex records 的闭环。
- 修正总入口 H2 状态、brief/spec 的 doctor/WAL 范围，以及 release report 的 main 时点表述；未改代码、tag 或 Release。
- Markdown links、`make verify`、普通/race 401 tests、73.8% coverage 与 GitHub 发布对象复核通过，未发现 P0/P1。
- PR #5 hosted run `29336431803` 仍在 0 steps 前被 billing/spending limit 阻塞；不写成代码通过或失败。

## 2026-07-14 — H2 崩溃恢复、doctor 与 v0.2.0

- Implementation Branch: `codex/hukou-h2-recovery-doctor`
- Closeout Branch: `codex/hukou-v0.2.0-closeout`
- Base: `main@7fc8ffd8a9bc7f7115919be019a1444a8cefa716`
- Subject Commit: `fa00ac1f4c3b2073828b7479248ab020b3c24495`
- PR #3 Merge: `60554bf95c3299ac7daec429b11139034dfee893`
- Execution Report: `execution-reports/2026-07-14-h2-recovery-doctor.md`
- Change Record: `change-records/2026-07-14-h2-recovery-doctor.md`
- Verification Report: `verification-reports/2026-07-14-h2-recovery-doctor.md`
- Release Report: `verification-reports/2026-07-14-v0.2.0-release.md`
- Release: `https://github.com/rtwsvj/hukou/releases/tag/v0.2.0`
- Status: completed with GitHub-hosted Actions infrastructure exception

### 交付

- adopt/upgrade/rollback durable before/after WAL：PREPARED 回滚、COMMIT 后前滚、恢复可重入。
- durablefs、manifest backup、Store.Put 崩溃续写与 namespace fast-path 再同步。
- 默认零写/零网络的 `hukou doctor` 文本/JSON诊断。
- pending transaction 对 list/provenance/dry-run 失败关闭；批量升级遇未决状态停止。

### 核验与发布

- macOS：401 tests、race、73.8% coverage；transaction/store/doctor/batch-stop 定向压力和 1203 次 shuffle 通过。
- 原生 Linux/arm64 非 root 容器：普通与 race 全仓通过。
- 三路独立回顾发现的 fast-path durability、Store.Put retry、batch pending、backup topology、doctor determinism 等缺口均在 subject commit 前关闭；最终无 P0/P1。
- PR/main/tag GitHub-hosted jobs 因 billing/spending limit 在 0 steps 前被拒绝，明确记为 infrastructure-blocked。
- 两个全新 Linux 容器运行正式 release 脚本；四平台资产逐字节一致，4/4 checksum、三平台 buildinfo 与远端重新下载复核通过。
- Colima 从停止状态临时启动，发布后已恢复停止。

### 保留边界

- 不宣称硬件掉电缓存重排证明；非协作 writer 最终 syscall 前 TOCTOU 已记录。
- doctor 无自动 repair；激活历史/retention、公共 fixture smoke、Windows 与签名/attestation 留后续。

## 2026-07-13 — H1 安全硬化与首个发布

- Branch: `codex/hukou-hardening-release`
- Base: `main@afed279`
- Execution Report: `execution-reports/2026-07-13-hukou-hardening-release.md`
- Verification Report: `verification-reports/2026-07-13-hukou-hardening-local.md`
- Release Report: `verification-reports/2026-07-13-v0.1.0-release.md`
- Status: completed with GitHub Actions infrastructure exception
- Scope: 安全事务、状态锁、审计字段、文档事实源、CI、四平台发布与 `v0.1.0`
- Boundary: 所有危险验证只在临时 HOME/PATH/data root；仓库 private，不创建原创代码根 LICENSE
- Verification: local L1-L4、隔离 Linux release gate 与 Release pass；GitHub-hosted runner gate infrastructure-blocked

### Docs / CI / release foundation

- Change Record: `change-records/2026-07-13-docs-ci-release-foundation.md`
- Status: implemented and verified with the release-wide infrastructure exception
- Notes: 文档、CI 与 release foundation 已进入 `main`；最终代码/发布验证由两份 verification report 记录

完成条件：change record 与 verification report 均落盘，H1 roadmap 状态与实际证据一致。已完成。

### 本地集成门禁

- `make verify`：pass。
- uncached tests：13 packages / 325 tests pass。
- race：pass。
- coverage：79.0%。
- Darwin/Linux × amd64/arm64 交叉构建：pass。
- 隔离 CLI smoke：pass；未触碰真实 manifest/store。

### PR CI 第一次运行

- PR: `https://github.com/rtwsvj/hukou/pull/1`
- Run: `29265826948`
- Ubuntu、quality、coverage：pass。
- macOS：并发替换 symlink inode 时 `ReadFile` 收到 `EINVAL`；改成单次 `Readlink` 后进入第二次远端运行。

### PR CI 第二次运行与 regular-file 迁移

- Run: `29266208797`
- Ubuntu、quality、coverage：pass。
- macOS：唯一一次 `Readlink` 仍返回 `EINVAL`，确认是 macOS/APFS symlink 交换语义，不是测试的双 lookup 假阳性。
- Decision: `ADR-0002`；live 改为同目录完整 regular-file copy + `fsync` + rename。
- Commit: `513d8bb87f1931c152c7e336faf7bc4a5e4d02b3`。
- Additional hardening: original/version write-once、regular 独立 snapshot、post-snapshot SHA、pre-activation 错误只丢弃 snapshot、精确 tag+SHA Prune、case alias/内部 symlink escape/非法 adopt tag fail closed。
- Verification: `make verify`、13 packages / 325 tests、race、79.0% coverage、四目标交叉构建、store/补偿压力通过；第三次远端 CI 被计费限制在 0 step 前拒绝。

### 合并、发布与基础设施例外

- PR #1 merge commit: `d15331dbe4d258d54253643b758c787bb63c95e1`。
- CI Run `29268485688`：四个 job 均因账户 payment/spending limit 在 runner 调度前失败，steps 为空；非代码失败。
- Snapshot Run `29268603489` 与 tag Run `29268975174`：同一计费原因在 0 step 前失败。
- Fallback: 临时启动原本停止的 Colima，用两个全新 `golang:1.26.5-bookworm` 容器分别运行正式 `scripts/release.sh`；四个归档逐字节相同，4/4 checksum 与双平台 buildinfo smoke 通过；Colima 已恢复停止。
- Release: `https://github.com/rtwsvj/hukou/releases/tag/v0.1.0`，非 draft/prerelease，四个 tar.gz + `checksums.txt`；远端重新下载与本地产物逐字节一致。
