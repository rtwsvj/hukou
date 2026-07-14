# 需求与安全不变量

## 功能需求

### R1：扫描

- 遍历进程 `PATH` 与重复 `--dir` 指定的目录。
- 保留 shadowed 二进制并标记。
- 输出人类表格或稳定 JSON。
- 支持 `--unknown-only`、`--source` 过滤。
- hukou manifest 中已登记的路径应优先归属为 `source=hukou`。

### R2：收编

- 只接受普通、可执行文件。
- 默认拒绝其他管理器已认领的文件；`--force` 必须由用户显式给出。
- `--local` 条目没有 GitHub repo，upgrade 自动跳过。
- 同名 manifest 冲突不得静默覆盖。
- 登记前计算当前二进制 SHA-256，并保留 original 备份。
- `--dry-run` 必须完成文件、来源、repo/tag、冲突和 SHA 检查，但不得创建 data root、lock、manifest、store、transaction 或 backup；`--json` 只允许与 dry-run 同用。

### R3：升级

- 只处理 manifest 中已收编且带 repo 的条目。
- `--dry-run` 只读取本地状态和 GitHub metadata，不创建数据目录、GC 或下载资产。
- 真升级必须依次完成：完整性闸门、release 查询、资产选择、下载、校验、解压、store 入库、激活、manifest 保存。
- 部分失败应返回非零状态，并列出失败项。
- `--all` 只处理 hukou manifest 条目，绝不代理其他管理器。
- release 候选与 `outdated` 必须使用同一 policy-aware checker；exact pin、channel 与 SemVer 降级边界不得在真实写路径中另写一套。

### R4：回滚

- 回滚前校验当前活跃文件。
- 默认按 manifest v2 activation parent 选择上一个逻辑版本；`--to <tag>` 只允许当前 lineage 中可证明的 ancestor，`--to original` 恢复不可变原件并终止可猜测 lineage。
- 激活与 manifest 更新必须作为一个可补偿操作处理。

### R5：发布

- `version` 输出版本、提交、构建时间。
- 发布四个平台 tar.gz 与统一 `checksums.txt`。
- 手动 workflow 只产出 snapshot；只有已推送的 `v*` tag 自动发布。

### R6：诊断与崩溃恢复

- adopt、upgrade、rollback 在跨 original/live/manifest 修改前必须持久化可恢复的 before/after 状态。
- 没有 durable COMMIT 的事务恢复到 before；存在有效 COMMIT 的事务前滚到 after。
- 恢复前必须先分类全部资源；预检或写入前复核时，任一路径既不匹配 before 也不匹配 after，必须零覆盖失败并保留 journal。非协作写入若发生在最后一次复核与系统调用之间，仍受已记录的 TOCTOU 边界约束。
- `doctor` 默认只读、零网络，支持人类文本与稳定 JSON；`--deep` 只扩大读取范围，不改变状态。
- doctor 不得把 retained rollback version 误判为 orphan；manifest 无效时，store tool 必须标为不可分类。

### R7：Trust-first 只读入口

- `explain <name|path>` 输出实际 PATH 命中、同名 shadowed 候选、real path、kind、source、package/version、confidence 与 evidence；零网络、零写入。
- `outdated [name ...]` 对 local 项不联网，对 drift 在联网前失败关闭；其余只查询 GitHub release metadata 并选择资产，不下载、不写本地状态。
- explain、adopt plan、outdated 等 JSON report 必须有独立 `schema_version`、英文字段和确定性排序。

### R8：更新与保留策略

- `policy show` 只读展示全局/entry 有效策略；`policy set` 只在 state lock 内原子保存 manifest，不触碰 live binary，存在 pending transaction 时拒绝而不是自动 recovery。
- 支持 `semver` 与兼容用 `github-latest`、`stable`/`prerelease`、exact pin、entry rollback depth。
- 显式切换到 `semver` 时，local entry 或当前 tag 不是严格 `X.Y.Z`（允许小写 `v` 前缀及合法 prerelease/build）的 entry 必须拒绝且零 manifest/live 变化。
- SemVer 模式选择最高合格版本、normalized equal 为 no-op、默认拒绝隐式降级；exact pin 是显式 desired state，可前进或回退。
- retention 保护 current、immutable original、已安装 exact pin artifact 和最近 N 个 activation ancestors；存在未完成 transaction 时整体跳过 prune、零删除；不得读取 mtime 作决策。

### R9：Manifest v2 与 activation lineage

- schema 0/1 只按当前 tag/SHA 迁移为 synthetic root，不猜历史；schema 2 必须显式包含有效 policy、retention 与 activation lineage。
- future schema、未知字段、重复 name/path、非法路径/摘要/时间/policy/history 必须拒绝。
- adopt、upgrade、rollback 产生不可变 event；active event 必须是最后一项并与 entry 当前 tag/SHA 一致。
- history 和当前状态必须进入同一 after-manifest，由现有 WAL 一起提交。

### R10：窄版 repair 与 support

- doctor 保持只读；repair 只支持 `recover-transaction` 与 `restore-manifest-backup` 两个 action。
- `repair plan --output` 只读 hukou 状态，只写显式 plan 文件（0600）；`repair apply` 持锁重算 data-root identity、fingerprint 和前置条件，不一致时不得修改业务状态。
- backup restore 只允许主 manifest 缺失/无效、backup schema/语义有效、transaction clean、且每个 live SHA 与 backup 一致的现场。
- `support bundle --format json` 零写，`--output` 只写 0600 文件；两者均离线、不上传，并排除原始路径、repo、用户名、HOME、环境变量、二进制、WAL payload 等私密值。

## 安全不变量

1. **scan 只读**：不得联网、写用户目录或执行包管理器命令。
2. **所有权边界**：upgrade/rollback 永不触碰 manifest 之外的路径。
3. **fail closed**：发现 checksum 文件却找不到所选资产条目时必须失败。
4. **双 hash**：下载资产 hash 用于来源审计，active binary hash 用于本地完整性闸门，二者不可混用。
5. **宿主隔离**：GitHub token 只发送给允许的 API host，不随下载或重定向泄露。
6. **大小限制**：下载和解压都有有界上限。
7. **路径限制**：name/tag 与归档条目不得逃逸各自根目录；store 子目录必须精确拼写且不得是 symlink，激活源必须位于 store；事务恢复旧 symlink 时只复现已捕获的原目标。
8. **错误补偿**：激活后任何可观测失败必须恢复旧路径拓扑与旧 manifest。
9. **进程互斥**：写 manifest/store 的命令必须串行；锁已被占用时立即失败并给出清晰错误，不无限等待。
10. **测试隔离**：测试不得读写真实 hukou 数据目录或真实用户二进制。
11. **保留命名空间**：`.tmp` 工具名与 `original` 版本标签（含大小写别名）必须在任何 store/manifest 持久化前拒绝。
12. **持久化事务决策**：资源 payload、journal、live/store/manifest 的 rename/link/remove 只有在文件及相关父目录同步后才算成功。
13. **只读诊断**：无 repair 参数的 doctor 不创建 data root、不获取会改写状态的锁、不 GC、不联网。
14. **计划不等于授权**：adopt dry-run、update plan 与 repair plan 都不能绕过真实写操作在锁内的现场重检。
15. **历史可证明**：rollback/retention 只消费经过 manifest 语义验证的 activation lineage；缺失、forward/cyclic 或 active 不一致的历史失败关闭。
16. **删除前完整验证**：Prune 在删除前验证 protected refs、immutable original 与 store 拓扑；journal 不 clean 时零删除。
17. **支持信息最小化**：support report 只输出匿名序号、枚举、计数和安全 build/platform 值，不自动上传。
18. **单一 schema 边界**：command、doctor 与 repair 必须共享 strict manifest decoder；unknown field 或语义无效 backup 不能在某条旁路被接受。

## 非功能需求

- Go 版本以 `go.mod` 为唯一工具链基准。
- 新增运行时第三方依赖必须有明确架构理由、锁定版本、许可证/notices 记录和对应测试；V0.3 的 SemVer 比较使用 `golang.org/x/mod/semver`，决策见 ADR-0005。
- Linux/macOS 都必须进入 CI。
- 发布产物必须使用 `-trimpath`，版本信息由固定提交注入。
- 未运行的验证不得记录为“通过”。
- 默认公共 CLI help、错误和正常输出使用英文；中文项目入口保留在 `README.zh-CN.md`。
