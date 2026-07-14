# 发布流程

## 当前正式版本

[`v0.2.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.2.0) 是当前正式版本，包含 Darwin/Linux × amd64/arm64 四个归档与 `checksums.txt`；[`v0.1.0`](https://github.com/rtwsvj/hukou/releases/tag/v0.1.0) 保持不变。本次 GitHub-hosted CI/tag workflow 因账户 payment/spending limit 在 runner 调度前失败；发布仅在两个全新 Go/Linux 容器运行正式脚本得到逐字节一致结果、三平台 buildinfo smoke 通过、远端重新下载与本地产物逐字节一致后，使用同一 annotated tag 手动完成。该例外不改变未来正常流程的 runner gate 要求。

V0.3 当前是已完成 local/private readiness 验收的 private RC 分支。没有
`v0.3.0`/prerelease tag，没有 V0.3 GitHub Release，也没有改变 repository visibility。
本页后续 V0.3 内容描述 subject `1fa45a0` 已验证的发布契约；该结论不等于公开发布。
本轮只创建 draft private PR；即使本地门禁通过，合并仍需单独 Go/No-Go，不能由
“测试通过”自动授权。

## 版本策略

- 正式版本使用稳定 SemVer tag，例如 `v0.3.0`；RC 可使用 `v0.3.0-rc.1`。
- 发布/安装脚本严格接受 SemVer 2.0.0：core 与纯数字 prerelease identifier
  都不得有前导零，点分 prerelease identifier 不得为空；本项目不接受 build
  metadata。含 `-` 的 tag 会创建 GitHub prerelease。
- 上述是 shell 发布入口的 v-prefix 契约；Go update policy 另行接受可排序的
  `X.Y.Z`/`vX.Y.Z` 与合法 build metadata。两者用途不同，测试矩阵也分开维护。
- 历史 `phase1` / `phase2` tag 保留，但不作为可安装发布版本。
- 已发布 tag 不移动；修复使用新的 patch 版本。

## 构建契约

`scripts/release.sh` 从固定 commit 读取 commit SHA 与 commit timestamp，并向以下变量注入：

- `github.com/rtwsvj/hukou/internal/buildinfo.Version`
- `github.com/rtwsvj/hukou/internal/buildinfo.Commit`
- `github.com/rtwsvj/hukou/internal/buildinfo.Date`

构建使用 `CGO_ENABLED=0`、`-trimpath`、`-buildvcs=false`。脚本固定 umask 和文件 mode；GNU tar 固定成员顺序、owner/group、mtime；gzip 使用 `-n` 去除时间戳。Release workflow 会在同一受控 runner 上独立构建两次并逐文件比较，然后才使用产物。

脚本默认拒绝 dirty worktree，避免用某个 commit 的版本信息包装未提交代码。本地实验可显式设置 `ALLOW_DIRTY=1`，但这种产物不得发布。

产物：

```text
dist/
├── hukou_<version>_darwin_amd64.tar.gz
├── hukou_<version>_darwin_arm64.tar.gz
├── hukou_<version>_linux_amd64.tar.gz
├── hukou_<version>_linux_arm64.tar.gz
└── checksums.txt
```

每个 V0.3 archive 配置为包含 `hukou`、`README.md`、`README.zh-CN.md`、
根 `LICENSE`、`THIRD_PARTY_NOTICES.md` 与 `LICENSES/*.txt`。仓库虽仍 private，
但公开准备已经加入 Apache-2.0 根许可证；这不等于仓库已公开。

## 本地 snapshot

```bash
VERSION=v0.3.0-rc.1 ALLOW_DIRTY=1 bash scripts/release.sh  # 仅未提交开发树实验
```

要求 GNU tar；macOS 默认 BSD tar 时安装并使用 `gtar`。生成物位于被忽略的 `dist/`。

## GitHub Actions

`.github/workflows/release.yml` 支持：

- 手动运行：在 Linux/macOS 上执行完整门禁并上传 snapshot artifact，不创建 Release。
- 推送 `v*` tag：在 Linux/macOS 上执行同一门禁，随后打包、验证 tag 并创建 GitHub Release。

第三方 GitHub Actions 使用经官方 API 核对的不可变 commit SHA，并在行尾记录对应版本，避免可移动主版本 tag 改变发布执行内容。

常规 verify 包括 fmt/module/vet/test/race/coverage/build/license/installer 与
release-version matrix；package job
再执行 shellcheck 与固定 `govulncheck@v1.5.0`。tag 发布还会强制检查：tag 为
annotated tag、目标 commit 已在 `origin/main`、四个 archive 与 `checksums.txt` 的
四条文件名逐一对应且校验通过、Linux amd64 二进制输出的 version/commit/build
date 与当前提交完全一致。打包 job 只有 `contents: read`；仅在 Linux/macOS 验证和
全部打包门禁成功后，独立 publish job 获得 `contents: write` 并创建 Release。

package job 会解开 Linux amd64 archive 并运行 `hukou version`，确认 archive
布局和 ldflags 注入；随后从四个 archive 各提取一个平台二进制到隔离 scan root，
用固定 Syft 1.46.0 生成 SPDX JSON SBOM，并强制断言 hukou 与三项直接依赖各出现
4 次、files 为 4。四个 archive、`checksums.txt` 与 SBOM 作为 workflow artifact 上传。

截至 2026-07-15，subject `1fa45a0` 已完成 direct uncached ordinary/race 各
641 tests / 21 packages，命令级 mirror override 下 `make release-verify` exit 0
（coverage 72.9%、govuln 无已知漏洞），并在 non-root Linux/arm64 + GNU tar 1.34
环境通过全仓 ordinary/race 与 installer/release tests。四目标独立构建两次逐字节
一致，4/4 checksums、archive root/mode、buildinfo 与 installer smoke 通过。Syft 1.46.0
最终 SBOM 为 SPDX 2.3、21 packages/4 files；验收发现并修复了旧方案 1 package/0 files
的空壳 SBOM。默认 `proxy.golang.org` IPv6 timeout 仍如实保留。

Artifact attestation 只在 repository visibility 为 public 时运行。public tag 发布
必须等 build provenance 与 SBOM attestation 成功；private tag 跳过 attest 仍可进入
publish，因此 private snapshot/Release 不能声称拥有 GitHub attestation。CodeQL 同样
只对 public repository 启用。Draft PR #6 的 CI run `29352308455` 五个 job 已确认
`steps=[]`，被 billing/spending limit 在执行前阻断；必须记录为 external
infrastructure gate，不能称作远端 CI 绿色。

## 发布清单

1. 工作树干净，目标 commit 已进入 `main`；private RC 阶段只允许分支/draft PR，不打正式 tag。draft PR 不能在缺少独立 Go/No-Go 时合并。
2. 当前变更 verification report 为 pass，`pinhaoma-review` claims-vs-evidence 无未关闭 blocker。
3. CI 的 Linux/macOS test、race、build、coverage 与 quality/vulnerability gates 全绿；若 billing 阻断，Go/No-Go 必须显式接受 external gate，不能写成绿色。
4. 双构建 snapshot 逐字节一致，四个 archive 可解压，`version` 正确。
5. `checksums.txt` 可验证全部 archive；archive 包含 license/notices/双语 README/依赖许可证。
6. SPDX JSON SBOM 可解析并对应目标 commit/产物；public 时 attest 必须成功。
7. 更新 changelog/release notes，并重新核对 README 仍把 v0.2.0 写成当前正式版本直到实际发布完成。
8. 获得独立公开 Go/No-Go 后，才可创建并推送 annotated SemVer tag；workflow 再验证 tag 指向 `main` 历史。
9. release workflow 成功后核对远端资产、prerelease 标记和可见性；不得移动已发布 tag。

## 回滚

- tag 前失败：删除本地 `dist/`，修复后重跑，不创建 tag。
- 若人工流程创建了 draft/未发布 Release：删除 draft 后重跑；当前自动 workflow 通过后会直接发布，不创建 draft。
- 已发布 Release：不覆盖资产、不移动 tag；标注问题并发布 patch 版本。
