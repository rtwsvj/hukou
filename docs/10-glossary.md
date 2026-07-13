# 术语

| 术语 | 含义 |
|---|---|
| 户口 / manifest | hukou 管理条目的 JSON 清单 |
| 收编 / adopt | 登记现有二进制并保存 original 备份 |
| active binary | 用户 PATH 中当前执行到的常规文件；旧版 symlink 只作为兼容输入 |
| asset | GitHub Release 中下载的归档或裸文件 |
| asset hash | 下载资产本体的 SHA-256 |
| active hash | 解压并激活后二进制的 SHA-256 |
| original | 收编时保留的原始二进制版本 |
| store | `<dataRoot>/store` 下的版本目录 |
| shadowed | PATH 中同名但优先级低、不会被 shell 首先执行的文件 |
| fail closed | 证据缺失或校验异常时停止，不降级为继续安装 |
| 补偿 | 激活后后续步骤失败时恢复旧路径与 manifest 状态 |
| H1 | 2026-07-13 安全硬化与首个 SemVer 发布里程碑 |
