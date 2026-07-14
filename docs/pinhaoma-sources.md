# 拼好码来源记录

目的：调试、回溯、同步上游、替换实现、排查问题。评估时间：2026-07-11。

| 来源 repo | 评估 commit | 许可证 | 工程关系 | 当前本地对应 |
|---|---|---|---|---|
| https://github.com/zyedidia/eget | 0983dea | MIT | `detect.go` 改编；归档定位/校验作为设计参考 | `internal/assetpick/detect.go`（带来源头）、`internal/archive/`、`internal/verify/` |
| https://github.com/marwanhawari/stew | 8a9a3ea | MIT | GitHub 下载姿势、binary hash 与清单设计参考 | `internal/ghrelease/`、`internal/manifest/` |
| https://github.com/nao1215/gup | 952fb83 | Apache-2.0 | Go buildinfo 模块改编 | `internal/provenance/gobin.go`（带来源头） |
| https://github.com/houseabsolute/ubi | edfac51 | Apache-2.0 | 借鉴资产决胜规则；不搬 Rust 实现 | `internal/assetpick/pick.go` |
| https://github.com/pkgforge/soar | cc0526e | MIT | 版本寻址与切换架构参考 | `internal/store/`、`internal/manifest/` |

已知坑（评估中发现，抄代码时必须处理）：

- eget extract.go 目录提取无 `../` 路径穿越防御（extract.go:240,257），搬运时补。
- eget --upgrade-only 用 mtime 判新旧，不可照抄；hukou 用清单里的显式 tag 比较。
- stew upgrade 对全 prerelease 仓库会数组越界 panic（cmd/upgrade.go:82-84），抄骨架时修。
- stew 用的 mholt/archiver v3.1.1 已归档且有 zip-slip 前科，换 mholt/archives 或标准库。
- stew 无多版本/软链/回滚，该能力自研，参考 mise/aqua shims。

## 维护规则

- “改编”文件必须保留来源 commit、版权与对应 `LICENSES/` 路径。
- “设计参考”不等于逐行复制；若后续发现实质复制，应补来源头并更新本表。
- 发布归档必须携带根 `LICENSE`、`THIRD_PARTY_NOTICES.md`、双语 README 与
  `LICENSES/*.txt`；V0.3 release snapshot 尚待验证实际内容。
- Go module 版本以 `go.mod`/`go.sum` 为准；V0.3 新增
  `golang.org/x/mod/semver` 用于 ADR-0005 的纯 SemVer 选择，许可证与 PATENTS
  文本记录在 `LICENSES/` 和根 notices。
- 仓库当前 private，本表只做工程追踪，不替代许可证选择或法律判断。
