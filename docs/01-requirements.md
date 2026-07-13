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

### R3：升级

- 只处理 manifest 中已收编且带 repo 的条目。
- `--dry-run` 只读取本地状态和 GitHub metadata，不创建数据目录、GC 或下载资产。
- 真升级必须依次完成：完整性闸门、release 查询、资产选择、下载、校验、解压、store 入库、激活、manifest 保存。
- 部分失败应返回非零状态，并列出失败项。

### R4：回滚

- 回滚前校验当前活跃文件。
- 支持自动选上一个版本以及 `--to <tag|original>`。
- 激活与 manifest 更新必须作为一个可补偿操作处理。

### R5：发布

- `version` 输出版本、提交、构建时间。
- 发布四个平台 tar.gz 与统一 `checksums.txt`。
- 手动 workflow 只产出 snapshot；只有已推送的 `v*` tag 自动发布。

## 安全不变量

1. **scan 只读**：不得联网、写用户目录或执行包管理器命令。
2. **所有权边界**：upgrade/rollback 永不触碰 manifest 之外的路径。
3. **fail closed**：发现 checksum 文件却找不到所选资产条目时必须失败。
4. **双 hash**：下载资产 hash 用于来源审计，active binary hash 用于本地完整性闸门，二者不可混用。
5. **宿主隔离**：GitHub token 只发送给允许的 API host，不随下载或重定向泄露。
6. **大小限制**：下载和解压都有有界上限。
7. **路径限制**：name/tag、归档条目与软链目标不得逃逸各自根目录。
8. **错误补偿**：激活后任何可观测失败必须恢复旧路径拓扑与旧 manifest。
9. **进程互斥**：写 manifest/store 的命令必须串行；锁已被占用时立即失败并给出清晰错误，不无限等待。
10. **测试隔离**：测试不得读写真实 hukou 数据目录或真实用户二进制。

## 非功能需求

- Go 版本以 `go.mod` 为唯一工具链基准。
- 不增加运行时第三方依赖，除非有单独 ADR。
- Linux/macOS 都必须进入 CI。
- 发布产物必须使用 `-trimpath`，版本信息由固定提交注入。
- 未运行的验证不得记录为“通过”。
