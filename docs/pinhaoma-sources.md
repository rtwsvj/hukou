# 拼好码来源记录

目的：调试、回溯、同步上游、替换实现、排查问题。评估时间：2026-07-11。

| 来源 repo | 评估 commit | 许可证 | 使用方式 | 本地对应（规划） |
|---|---|---|---|---|
| https://github.com/zyedidia/eget | 0983dea | MIT | 复制改写 detect.go / archive.go / BinaryChooser / verify.go | internal/assetpick/ internal/archive/ |
| https://github.com/marwanhawari/stew | 8a9a3ea | MIT | 复制改写 lib/github.go 资产匹配骨架、lockfile 结构、http 下载 | internal/ghrelease/ internal/manifest/ |
| https://github.com/nao1215/gup | 952fb83 | Apache-2.0 | 复制改写 Go buildinfo 溯源模块 | internal/provenance/gobin.go |
| https://github.com/houseabsolute/ubi | edfac51 | Apache-2.0 | 借鉴资产决胜规则（musl/gnu、扩展名偏好），Rust 实现不搬代码 | internal/assetpick/（规则参考） |
| https://github.com/pkgforge/soar | cc0526e | MIT | 借鉴状态库 schema 与架构 | internal/statedb/（设计参考） |

已知坑（评估中发现，抄代码时必须处理）：

- eget extract.go 目录提取无 `../` 路径穿越防御（extract.go:240,257），搬运时补。
- eget --upgrade-only 用 mtime 判新旧，不可照抄；hukou 用清单里的显式 tag 比较。
- stew upgrade 对全 prerelease 仓库会数组越界 panic（cmd/upgrade.go:82-84），抄骨架时修。
- stew 用的 mholt/archiver v3.1.1 已归档且有 zip-slip 前科，换 mholt/archives 或标准库。
- stew 无多版本/软链/回滚，该能力自研，参考 mise/aqua shims。
