# hukou（户口）

给机器上所有 CLI 工具上户口：盘点、溯源、收编、安全升级。

> brew 管不着的那些工具，归我管。

## 定位

现有工具全是"从今往后经我手装"（mise/aqua/stew/eget），没有一个做**存量收编**：
扫描 `$PATH` 里已有的二进制 → 反查安装来源 → 对无主工具匹配上游 repo 并接管升级。
hukou 补的就是这个空位，和 topgrade（升级调度）、mise（声明式安装）互补而非竞争。

## 四大功能支柱

1. **scan（上户口）**：遍历 PATH，给每个二进制定归属——brew / cargo / go（读内嵌 buildinfo，`go version -m`）/ npm / pipx / uv / mise / 无主。
2. **adopt（收编）**：无主二进制匹配 GitHub repo（Go buildinfo 直读；其余靠名字匹配 + release 资产哈希校验；兜底手动 `hukou adopt <bin> <repo>`）。
3. **upgrade / rollback（安全升级）**：收编的工具从 GitHub release 升级；本地保留 N 个旧版本目录 + 软链切换，支持一键回滚。已被各家管理器认领的工具**只记录版本快照，升级转发给原主**，不抢所有权。
4. **风险层（后期）**：升级前后版本清单快照、changelog diff、供应链风险提示。

## 设计纪律

- 只收编无主的，永远不抢 brew/cargo/npm 已管工具的升级权。
- 注册为 topgrade 的 custom command，搭现有生态便车。
- 支持导出清单到 mise/Brewfile，不锁死用户。
- macOS/Linux 优先，Windows 后置。

## 代码复用来源与许可证纪律

参考/复制来源（均为宽松许可，可 fork/vendor/改写）：

| 项目 | 许可证 | 复用点 |
|---|---|---|
| [zyedidia/eget](https://github.com/zyedidia/eget) | MIT | 资产检测启发式（detect.go，277 行零依赖）、archive 抽象、二进制定位 |
| [marwanhawari/stew](https://github.com/marwanhawari/stew) | MIT | lockfile 字段设计（binaryHash）、GitHub API 下载姿势、darwin-arm64 回退 |
| [nao1215/gup](https://github.com/nao1215/gup) | Apache-2.0 | Go 二进制 buildinfo 溯源 |
| [houseabsolute/ubi](https://github.com/houseabsolute/ubi) | Apache-2.0 | 资产自动决胜思路（musl/gnu、扩展名偏好） |
| [pkgforge/soar](https://github.com/pkgforge/soar) | MIT | 状态库 schema / 架构参考 |

**禁抄名单（GPL 传染）**：topgrade（GPL-3.0）、pacaptr（GPL-3.0）、meta-package-manager（GPL-2.0）——只可借鉴思路或作为外部命令调用，不得复制代码。

## 状态

- [x] 竞品全景调研（2026-07-11）
- [x] 五个参考项目许可证核查 + 源码静态评估
- [ ] 拼好码执行报告审批
- [ ] MVP Phase 1：`hukou scan`
- [ ] Phase 2：adopt + upgrade
- [ ] Phase 3：快照 + rollback
- [ ] Phase 4：changelog / 风险提示
