# 卡 D：供应链收尾 + 文档收账（DONE，含 Codex 两轮复核返工）

- 分支：`card-d-20260715`
- 日期：2026-07-15
- 约束遵守：生产代码/注释英文；shell 过 shellcheck 零告警；无新第三方依赖（网络 e2e 只用既有 `internal/ghrelease` 与 cobra）
- Codex 一轮 10 项发现 + 二轮 3 项收口已全部处置，见第 5 节对账表
- **本文件行号以三轮收口补提交后的最终树为准（已逐条重新核对）**

## 1. 供应链：installer 消费 attestation（传输信任残留收口）

`scripts/install.sh`
- 新增 `verify_attestation()`（:104）：**验证对象是下载的 release archive**（attestation 的真实 subject——release.yml 的 `actions/attest` 用 `subject-checksums` 证明的是 checksums.txt 清单里列出的归档文件，不是 checksums.txt 本身）。时序：下载 checksums.txt + 归档（:309-312）→ `verify_attestation "$ARCHIVE_PATH"`（:316）→ SHA-256 比对（:318 起）→ tar 检查/解压安装。验证失败发生在任何 tar 调用之前。
  - 签名者身份用**锚定的 `--cert-identity-regex`** 钉死证书 SubjectAlternativeName（:126-131）：`^https://github\.com/rtwsvj/hukou/\.github/workflows/release\.yml@refs/tags/v[0-9][^ ]*$`（repo 部分由 `$REPO` 经 sed 转义正则元字符后拼入，:126）。不用 `--signer-workflow`——gh 将其实现为未转义、未锚定的 SAN 前缀正则，不能自称精确钉定（gh 2.96.0 `--help` 核对：`--cert-identity-regex` 语义为 "Enforce that the certificate's SubjectAlternativeName matches the provided regex"）。锚定正则同时把 ref 钉在 `refs/tags/v*`，分支运行的证明不接受。失败即 `die`（:131）。
  - `gh` 不可用（:107）或未认证（:115）→ 一行警告后继续（transport-trust fallback：HTTPS + 同源 checksums.txt 的 SHA-256）。
  - `HUKOU_REQUIRE_ATTESTATION` 严格解析（:235-239）：大小写不敏感 `1/true/yes`=强制（gh 缺失/未认证即 die，:109/:117）；空或 `0/false/no`=不强制（合法）；**其余任何取值 die 并提示合法值**（:238），拼写错误不许静默降级。解析在参数校验阶段执行，dry-run 也会 fail fast。
  - usage()（:71-75）与 dry-run 输出（:273）同步描述。

`scripts/install_test.sh`
- gh mock（:36 起）严格校验实参：子命令必须是 `attestation verify`（未知子命令失败）、subject 必须是**存在的 `*.tar.gz` 归档**、`--repo` 与 `--cert-identity-regex` 值必须精确匹配（期望正则即上述锚定形式，:83；可经 `GH_MOCK_EXPECT_*` 覆盖）、未知 flag 失败；实参行写入 `GH_MOCK_LOG`。默认「present 但未认证」，既有用例全走 transport-trust fallback、不触网。
- tar spy（:420）：先 touch `TAR_SPY_MARK` 再 exec 真 tar——mark 不存在即证明 tar 从未运行。
- 用例矩阵（:433-548）：
  - pass（:433）：安装成功 + 断言 gh 恰好调用 1 次且**精确形态**为 `attestation verify <tmp>/<NAME>.tar.gz --repo rtwsvj/hukou --cert-identity-regex ^https://github\.com/rtwsvj/hukou/\.github/workflows/release\.yml@refs/tags/v[0-9][^ ]*$`（:439-446）。
  - fail（:452）/fail+required（:467）：非零退出、stderr 含 `attestation verification failed`（:461）、**无 tar spy mark、无 prefix（因此无解压产物与临时安装文件）**。
  - unauth 默认回退（:479）/unauth+required fail-closed（:487-496，含 spy 断言）。
  - `TRUE`（大小写不敏感强制，:501）、`no`（显式不强制，:507）、`requird` 拼写错误 → die 且 stderr 含 `invalid HUKOU_REQUIRE_ATTESTATION value`（:513-521）。
  - missing gh：curated PATH（:527；**含 gzip/gunzip**（:529），GNU tar `-z` 需从 PATH 找 gzip，Ubuntu 门禁可靠）默认回退成功（:538）/required fail-closed（:543-552，含 spy 断言）。

## 2. release.yml

`.github/workflows/release.yml`
- 新增 `tag-guard` job（:20，jobs 首位）：仅跑 git plumbing、不执行任何 make/构建/树代码。校验步骤（:35）自身显式 `git fetch --no-tags origin +refs/heads/main:refs/remotes/origin/main`（:39）保障引用存在，再做 is-ancestor + annotated-tag + tag/HEAD 一致校验。`verify` job `needs: tag-guard`（:48），任意 tag 的树代码在祖先校验通过前不会在 runner 执行。
- `package` job 中原祖先校验步骤删除，留注释指向 tag-guard（:86-87）。
- `anchore/sbom-action` pin 注释 `# v0` → `# v0.24.0`（:133；SHA `e22c3899…` 经 `gh api` 对账）。

## 3. 文档收账

- `docs/adr/ADR-0002-regular-file-activation.md`:29 — 临时文件命名更正为 `.hukou-txn-*`（常规文件提交）/`.hukou-txn-link-*`（遗留 symlink 拓扑），与 `internal/transaction/recover.go:402,451` 一致。
- `docs/specs/phase2-adopt-upgrade.md`:16 — adopt 命令行补 `[--dry-run] [--json]`。
- `docs/specs/phase2-adopt-upgrade.md`:103 — 已知限制追加 setuid/setgid/sticky 位不经事务保留（`validateState` 以 `mode&^0o777` 仅接受 rwx）。
- `docs/05-cli-reference.md`:107 — list 段注明事务未清/store 拓扑异常时返回非零退出码（脚本消费者须知）。
- `docs/07-testing-and-verification.md`:36 — 新增「L6 真实网络 smoke（后置，opt-in）」节；:117 新增 installer attestation 覆盖清单（--repo + 锚定 --cert-identity-regex）。
- **安装器信任语义同步**：`README.md`:87-95（attestation + 锚定 cert-identity-regex + 值域：1/true/yes 强制，空/0/false/no 合法不强制，其他报错）；`README.zh-CN.md`:71-76（同上，中文）；`docs/08-risk-and-debt.md`:40（安装器信任根行：锚定 regex 现状 + 不用 --signer-workflow 的原因 + attestation 仅 public 后产生的边界）；`docs/06-dev-setup.md`:65-72（安装器契约：subject 是 archive 而非 checksums.txt、完整 gh 命令、时序在 tar 之前）+ :82-89（env 取值矩阵与测试形态）。
- 术语：全部代码注释/文档使用 transport-trust fallback / 仅传输信任，不使用 TOFU（并未记录首次观察值，TOFU 不准确）。

## 4. Makefile + 网络 e2e 骨架

- `Makefile`:60 `verify-network` 目标（`.PHONY` :6）：`HUKOU_NETWORK_E2E=1 go test -tags network_e2e -run Network -count=1 ./cmd/...`；不进 verify/release-verify/CI。
- `cmd/network_e2e_test.go`（新增）：`//go:build network_e2e` 圈住；带标签编译时 `HUKOU_NETWORK_E2E!=1` 或缺 `GITHUB_TOKEN`/`GH_TOKEN` 则 `t.Skip`。骨架对 fixture repo（默认 `rtwsvj/hukou`，env 可覆盖）走一次真实 `ghrelease.Client.Latest`。

## 5. Codex 发现对账（一轮 10 项 + 二轮 3 项收口）

| # | 级别 | 发现 | 处置 |
|---|---|---|---|
| 1 | 高 | attestation 主体错配（对 checksums.txt 验证必失败） | 改验下载的归档；时序：下载→验归档→SHA 比对→解压安装（install.sh:309-316） |
| 2 | 高 | 缺 signer 约束；二轮：--signer-workflow 是未转义未锚定前缀正则 | **二轮收口**：改用锚定 `--cert-identity-regex`（install.sh:126-131），$REPO 经正则转义；接受/拒绝矩阵实测（tag ✓、branch ref ✗、近似 repo ✗、其他 workflow ✗、前缀注入 ✗）；mock 与 pass 用例同步断言 |
| 3 | 中 | REQUIRE 解析太松 | 1/true/yes（不区分大小写）=强制；空/0/false/no=不强制；其他值 die（install.sh:235-239）；typo 用例（install_test.sh:513） |
| 4 | 中 | gh mock 不校验实参 | mock 校验子命令/subject 存在且为归档/--repo/--cert-identity-regex；未知子命令与 flag 失败；pass 用例断言精确调用行（install_test.sh:42-101,439-446） |
| 5 | 高 | curated PATH 缺 gzip | CLEAN_TOOL 列表加入 gzip/gunzip（install_test.sh:529） |
| 6 | 低 | 失败用例未证明中止先于解压/安装 | tar spy mark + `[ ! -e prefix ]` + stderr 断言（install_test.sh:452-477,487-496,543-552） |
| 7 | 低 | TOFU 术语不准确 | 全部改为 transport-trust fallback/仅传输信任 |
| 8 | 低 | 文档未同步；二轮：README 值域把 0/false/no 写成非法 | **二轮收口**：双语 README 值域改为「1/true/yes=强制；空/0/false/no=不强制（合法）；其他值报错」（README.md:93-95、README.zh-CN.md:74-76）；其余同步见第 3 节 |
| 9 | 中 | 必须提交 | 一轮 `7b3df12`、二轮 `1100f9f`、三轮收口见本补提交（git log 首条） |
| 10 | 中 | 固定提交重跑门禁并写回 CARD；二轮：行号需按最终提交核对 | **二轮收口**：本文件全部行号已按三轮收口后的最终树逐条重新核对（见第 6 节） |

## 6. 验收输出

### 一轮固定提交 `7b3df12`（历史记录，行号已被三轮收口更替）

- `make verify` exit 0（fmt/mod-verify/vet/test/race/coverage 72.9%/build/license-check/install-test/release-test 全绿）；`bash scripts/install_test.sh` exit 0；`shellcheck scripts/*.sh` exit 0；release.yml 经 python3+PyYAML 校验（actionlint 未安装）：YAML 合法、needs 图 `tag-guard→verify→package→attest/publish` 解析通过；`go test -tags network_e2e -run Network ./cmd/` 编译通过且默认 SKIP。

### 三轮收口（最终树，本文件行号的依据）

| 门禁 | 命令 | 退出状态 | 关键输出 |
|---|---|---|---|
| 安装器测试 | `bash scripts/install_test.sh` | 0 | 末行 `install script tests passed`；pass 用例断言的精确 gh 调用行已含锚定 `--cert-identity-regex`；stderr 中 7 行 transport-trust 提示为默认 mock 未认证/missing-gh 用例的预期输出 |
| shell 静态检查 | `shellcheck scripts/*.sh` | 0 | 零输出、零告警 |
| 锚定正则矩阵 | `grep -Eq` 实测 | — | `refs/tags/v0.3.0` 接受；`refs/heads/main`、`rtwsvj/hukou-evil`、`ci.yml`、前缀注入（`xhttps...`）全部拒绝 |

三轮收口只改 `scripts/install.sh`、`scripts/install_test.sh`、双语 README、docs/06/07/08 与本文件；Go 代码与 workflow 未动，一轮 `make verify` 的 Go 侧结论不受影响，shell 侧已按上表在最终树复跑。
