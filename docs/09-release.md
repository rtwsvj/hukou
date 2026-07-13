# 发布流程

## 版本策略

- 正式版本使用稳定 SemVer tag，例如 `v0.1.0`；当前发布脚本不接受 prerelease 或 build metadata。
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

每个 archive 包含 `hukou`、`README.md` 与 `LICENSES/*.txt`。仓库当前 private，不包含原创代码根 LICENSE。

## 本地 snapshot

```bash
VERSION=v0.1.0 ALLOW_DIRTY=1 bash scripts/release.sh  # 仅当前未提交开发树实验
```

要求 GNU tar；macOS 默认 BSD tar 时安装并使用 `gtar`。生成物位于被忽略的 `dist/`。

## GitHub Actions

`.github/workflows/release.yml` 支持：

- 手动运行：在 Linux/macOS 上执行完整门禁并上传 snapshot artifact，不创建 Release。
- 推送 `v*` tag：在 Linux/macOS 上执行同一门禁，随后打包、验证 tag 并创建 GitHub Release。

第三方 GitHub Actions 使用经官方 API 核对的不可变 commit SHA，并在行尾记录对应版本，避免可移动主版本 tag 改变发布执行内容。

tag 发布还会强制检查：tag 为 annotated tag、目标 commit 已在 `origin/main`、四个 archive 与 `checksums.txt` 的四条文件名逐一对应且校验通过、Linux amd64 二进制输出的 version/commit/build date 与当前提交完全一致。打包 job 只有 `contents: read`；仅在 Linux/macOS 验证和全部打包门禁成功后，独立 publish job 获得 `contents: write` 并创建 Release。

发布 job 会解开 Linux amd64 archive 并运行 `hukou version`，确认 archive 布局和 ldflags 注入。

## 发布清单

1. 工作树干净，目标 commit 已进入 `main`。
2. H1 verification report 为 pass。
3. CI 的 Linux/macOS test、race、build、coverage 全绿。
4. 手动 snapshot archive 可解压，`version` 正确。
5. `checksums.txt` 可验证全部 archive。
6. 更新 changelog/release notes。
7. 创建并推送 annotated SemVer tag；workflow 会再次验证 tag 指向 `main` 历史。
8. release workflow 成功后核对远端资产。

## 回滚

- tag 前失败：删除本地 `dist/`，修复后重跑，不创建 tag。
- 若人工流程创建了 draft/未发布 Release：删除 draft 后重跑；当前自动 workflow 通过后会直接公开，不创建 draft。
- 已公开 Release：不覆盖资产、不移动 tag；标注问题并发布 patch 版本。
