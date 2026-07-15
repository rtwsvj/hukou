# 卡 D：供应链收尾 + 文档收账（DONE，含 Codex 复核返工）

- 分支：`card-d-20260715`
- 日期：2026-07-15
- 约束遵守：生产代码/注释英文；shell 过 shellcheck 零告警；无新第三方依赖（网络 e2e 只用既有 `internal/ghrelease` 与 cobra）
- Codex 独立核验 10 项发现已全部返工，见第 5 节对账表

## 1. 供应链：installer 消费 attestation（传输信任残留收口）

`scripts/install.sh`
- 新增 `verify_attestation()`（:101）：**验证对象是下载的 release archive**（attestation 的真实 subject——release.yml 的 `actions/attest` 用 `subject-checksums` 证明的是 checksums.txt 清单里列出的归档文件，不是 checksums.txt 本身）。时序：下载 checksums.txt + 归档（:257-260）→ `verify_attestation "$ARCHIVE_PATH"`（:264）→ SHA-256 比对 → tar 检查/解压安装。验证失败发生在任何 tar 调用之前。
  - `gh` 可用且 `gh auth status` 通过 → `gh attestation verify <archive> --repo "$REPO" --signer-workflow "${REPO}/.github/workflows/release.yml"`（:120-122；signer-workflow 钉死 release workflow，仅 `--repo` 会接受同仓库任何 workflow 的证明）；失败即 `die`（:123）。
  - `gh` 不可用（:104）或未认证（:112）→ 一行警告后继续（transport-trust fallback：HTTPS + 同源 checksums.txt 的 SHA-256）。
  - `HUKOU_REQUIRE_ATTESTATION` 严格解析（:227-231）：大小写不敏感 `1/true/yes`=强制（gh 缺失/未认证即 die，:106/:114）；空或 `0/false/no`=不强制；**其余任何取值 die 并提示合法值**（:230），拼写错误不许静默降级。解析在参数校验阶段执行，dry-run 也会 fail fast。
  - usage()（:67-75）与 dry-run 输出（:265）同步描述。

`scripts/install_test.sh`
- gh mock（:35 起）严格校验实参：子命令必须是 `attestation verify`（未知子命令失败）、subject 必须是**存在的 `*.tar.gz` 归档**、`--repo`/`--signer-workflow` 值必须精确匹配（默认 `rtwsvj/hukou` / `rtwsvj/hukou/.github/workflows/release.yml`）、未知 flag 失败；实参行写入 `GH_MOCK_LOG`。默认「present 但未认证」，既有用例全走 transport-trust fallback、不触网。
- tar spy（:415）：先 touch `TAR_SPY_MARK` 再 exec 真 tar——mark 不存在即证明 tar 从未运行。
- 用例矩阵（:428-543）：
  - pass（:428）：安装成功 + 断言 gh 恰好调用 1 次且**精确形态**为 `attestation verify <tmp>/<NAME>.tar.gz --repo rtwsvj/hukou --signer-workflow rtwsvj/hukou/.github/workflows/release.yml`（:434-441）。
  - fail（:447）/fail+required（:462）：非零退出、stderr 含 `attestation verification failed`（:454）、**无 tar spy mark、无 prefix（因此无解压产物与临时安装文件）**。
  - unauth 默认回退（:474）/unauth+required fail-closed（:482，含 spy 断言）。
  - `TRUE`（大小写不敏感强制，:496）、`no`（显式不强制，:502）、`requird` 拼写错误 → die 且 stderr 含 `invalid HUKOU_REQUIRE_ATTESTATION value`（:508-518）。
  - missing gh：curated PATH（:522；**含 gzip/gunzip**，GNU tar `-z` 需从 PATH 找 gzip，Ubuntu 门禁可靠）默认回退成功（:533）/required fail-closed（:538，含 spy 断言）。

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
- `docs/07-testing-and-verification.md`:36 — 新增「L6 真实网络 smoke（后置，opt-in）」节；:117 新增 installer attestation 覆盖清单。
- **安装器信任语义同步（Codex 8）**：`README.md`:86-93、`README.zh-CN.md`:71-75（attestation 验证 + signer-workflow 钉定 + `HUKOU_REQUIRE_ATTESTATION` 语义）；`docs/08-risk-and-debt.md`:40（安装器信任根行更新：现状 + attestation 仅 public 后产生的边界）；`docs/06-dev-setup.md`:65-71 安装器契约（subject 是 archive 而非 checksums.txt、时序在 tar 之前）+ :80-88（env 取值矩阵与测试形态）。
- 术语（Codex 7）：全部代码注释/文档使用 transport-trust fallback / 仅传输信任，不再使用 TOFU（并未记录首次观察值，TOFU 不准确）。

## 4. Makefile + 网络 e2e 骨架

- `Makefile`:60 `verify-network` 目标（`.PHONY` :6）：`HUKOU_NETWORK_E2E=1 go test -tags network_e2e -run Network -count=1 ./cmd/...`；不进 verify/release-verify/CI。
- `cmd/network_e2e_test.go`（新增）：`//go:build network_e2e` 圈住；带标签编译时 `HUKOU_NETWORK_E2E!=1` 或缺 `GITHUB_TOKEN`/`GH_TOKEN` 则 `t.Skip`。骨架对 fixture repo（默认 `rtwsvj/hukou`，env 可覆盖）走一次真实 `ghrelease.Client.Latest`。

## 5. Codex 10 项发现对账

| # | 级别 | 发现 | 处置 |
|---|---|---|---|
| 1 | 高 | attestation 主体错配（对 checksums.txt 验证必失败） | 改验下载的归档；时序：下载→验归档→SHA 比对→解压安装（install.sh:257-264） |
| 2 | 高 | 缺 `--signer-workflow` 约束 | 已加 `${REPO}/.github/workflows/release.yml`（install.sh:122），mock 强制断言该值 |
| 3 | 中 | REQUIRE 解析太松 | 1/true/yes（不区分大小写）=强制；空/0/false/no=不强制；其他值 die（install.sh:227-231）；typo 用例（install_test.sh:508） |
| 4 | 中 | gh mock 不校验实参 | mock 校验子命令/subject 存在且为归档/--repo/--signer-workflow；未知子命令与 flag 失败；pass 用例断言精确调用行（install_test.sh:37-96,434-441） |
| 5 | 高 | curated PATH 缺 gzip | CLEAN_TOOL 列表加入 gzip/gunzip（install_test.sh:524） |
| 6 | 低 | 失败用例未证明中止先于解压/安装 | tar spy mark + `[ ! -e prefix ]` + stderr 断言（install_test.sh:447-472,482-494,538-543） |
| 7 | 低 | TOFU 术语不准确 | 全部改为 transport-trust fallback/仅传输信任 |
| 8 | 低 | 文档未同步 | README.md/README.zh-CN.md/docs/06/docs/07/docs/08 已同步（见第 3 节） |
| 9 | 中 | 必须提交 | 见下方提交哈希 |
| 10 | 中 | 固定提交重跑门禁并写回 CARD | 见第 6 节验收输出 |

## 6. 固定提交验收输出

（本节由固定提交上的重跑结果补齐，见补提交。）
