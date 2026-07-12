# PHASE2-FIXES-DONE

五路评审后修复任务落实记录（分支 `phase2-adopt`）。  
验收：`GOPATH=/tmp/gopath GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/gomod go build ./... && go vet ./... && go test ./...` 全绿。

约束：允许改 `internal`；`internal/provenance/gobin.go`、`internal/assetpick/detect.go` 等 vendor 文件未动。

---

## P0 正确性

### 1. AdoptOriginal 后必须 Activate 新版本

| 项 | 位置 |
|---|---|
| 问题 | original 缺失走 `AdoptOriginal` 后软链停在 `original/`，manifest 却记新 tag |
| 修复 | `cmd/upgrade.go:223-230`：`AdoptOriginal` 成功后立即 `s.Activate(e.Name, release.TagName, e.Path)` |
| 测试 | `cmd/e2e_test.go` `TestE2E_AdoptOriginalBranchActivatesNewVersion` |

### 2. Prune 保护当前激活版本

| 项 | 位置 |
|---|---|
| 解析激活版本 | `internal/store/store.go:298-340` `activeVersionTag`（解析软链目标，兼容 macOS `/var`→`/private/var`） |
| Prune 签名 | `internal/store/store.go:348-389` `Prune(name, keep, linkPath string)`；`linkPath` 指向的版本与 `original` 同级永不删 |
| 调用处 | `cmd/upgrade.go:266` `s.Prune(e.Name, 3, e.Path)` |
| 测试 | `internal/store/store_test.go` `TestPruneProtectsActiveVersion`；`cmd/e2e_test.go` `TestE2E_RollbackThenUpgradePruneKeepsActive` |

### 3. 升级输出「旧 tag → 新 tag」

| 项 | 位置 |
|---|---|
| 保存旧 tag | `cmd/upgrade.go:213` `oldTag := e.Tag`（在改写 `e.Tag` 之前） |
| 输出 | `cmd/upgrade.go:270` `已升级 %s: %s → %s` 使用 `oldTag, release.TagName` |
| 测试 | `TestE2E_AdoptLocalAndRepo` 断言 `已升级 fakebin: v1.0.0 → v2.0.0` |

### 4. `--all` 部分失败汇总并返回非零 error

| 项 | 位置 |
|---|---|
| 收集失败 | `cmd/upgrade.go:80-90` |
| 结束汇总 | `cmd/upgrade.go:93-98` 打印失败清单，`return fmt.Errorf("%d upgrade(s) failed", ...)` |
| 测试 | `TestE2E_AllPartialFailureNonZero` |

---

## P0 安全

### 5. ghrelease host 白名单 + 重定向 + Authorization 隔离

| 项 | 位置 |
|---|---|
| 白名单 | `internal/ghrelease/client.go:27-32` `allowedHosts` |
| URL 校验 | `internal/ghrelease/client.go:315-344` `validateURL` / `hostAllowed`（生产仅 https + 白名单；自定义 `BaseURL` 宿主放行以支持 httptest） |
| CheckRedirect | `internal/ghrelease/client.go:346-358` 最多 5 跳；每跳校验白名单；非 auth host 剥除 `Authorization` |
| Auth 仅 API | `internal/ghrelease/client.go:289-310` `applyHeaders` + `authHostAllowed`（`api.github.com` 或自定义 BaseURL 宿主） |
| 默认客户端 | `internal/ghrelease/client.go:79-90` `New` 设置 `CheckRedirect` |
| 测试 | `TestDownloadRejectsDisallowedHost`、`TestRedirectToDisallowedHostRejected`、`TestAuthorizationNotLeakedOnDownloadRedirect`、`TestAuthorizationNotSentOnDownloadRequest` |

### 6. Timeout + 429 Retry-After

| 项 | 位置 |
|---|---|
| API Timeout | `client.go:19` `apiTimeout=30s`；`New` 中 `HTTPClient.Timeout` |
| 下载总限 | `client.go:20` `downloadTimeout=10m`；`Download` 用 `context.WithTimeout` + `NewRequestWithContext`（`client.go:118-122`） |
| 429 Retry-After | `client.go:231-254` `retryDelay`：优先 `Retry-After`（上限 60s），否则指数退避 |
| 测试 | `TestRetry429RespectsRetryAfter`、`TestRetry429RetryAfterCappedAt60`、`TestNewClientHasTimeout` |

### 7. 下载 / 解压大小防护

| 项 | 位置 |
|---|---|
| Download 签名 | `client.go:109` `Download(url, destDir, expectedSize int64)` |
| LimitReader + 校验 | `client.go:137-163`：`expectedSize>0` 限 `size+1` 并精确匹配；`=0` 全局 512MiB |
| 调用方传 Size | `cmd/upgrade.go:148-167` 资产；`291` checksum |
| archive 上限 | `internal/archive/archive.go:22-25` `MaxEntryBytes`/`MaxTotalBytes` = 512MiB；`writeLimited` `:264-302` |
| 测试 | `TestDownloadSizeMismatch`、`TestDownloadExceedsLimit`、`TestE2E_DownloadOversizeAborted` |

### 8. defer 清理最终 assetPath（含 rename 后）

| 项 | 位置 |
|---|---|
| 闭包 | `cmd/upgrade.go:172-182` `finalAssetPath` + `defer func(){ os.Remove(finalAssetPath) }`，rename 后更新变量 |

### 9. store name/tag 校验 + Activate 目标在 Root 内

| 项 | 位置 |
|---|---|
| 校验函数 | `internal/store/store.go:39-56` `validateNameTag`（禁空、`..`、路径分隔符） |
| Put | `store.go:91-95` |
| Activate | `store.go:191-195`；目标 `ensureUnderRoot` `:60-85`、`:223-226` |
| AdoptOriginal | `store.go:240-242` |
| 测试 | `TestValidateNameTagRejectsTraversal` |

---

## P1

### 10. 临时软链放 linkPath 同目录 + 规格修订

| 项 | 位置 |
|---|---|
| 实现 | `internal/store/store.go:431-456` `atomicSymlink` 使用 `CreateTemp(linkDir, ".hukou-tmp-*")` 再 `Rename` |
| 规格 | `docs/specs/phase2-adopt-upgrade.md:63` 安全红线：临时软链允许 linkPath 同目录 |
| 测试 | `TestActivateTmpSymlinkSameDir` |

### 11. rollback 错误传播 + manifest 回写

| 项 | 位置 |
|---|---|
| 错误包装返回 | `cmd/rollback.go:67-75` `fail(fmt.Errorf(...))` 不吞错 |
| manifest 回写 | `cmd/rollback.go:77-88` 回滚后重算 sha256，写回 `e.Tag`/`e.SHA256`/`UpdatedAt` 并 `saveManifest` |
| 测试 | `TestE2E_AdoptLocalAndRepo`、`TestE2E_RollbackThenUpgradePruneKeepsActive` |

### 12. findChecksumAsset 排除签名资产

| 项 | 位置 |
|---|---|
| 实现 | `cmd/upgrade.go:305-313` 匹配 `checksums` 时跳过 `.sig`/`.asc`/`.pem` 后缀 |
| 测试 | `TestFindChecksumAssetSkipsSig` |

---

## 测试补强（全部 httptest / 临时目录）

| 场景 | 测试 |
|---|---|
| 资产下载 404 | `TestE2E_AssetDownload404` |
| 校验不匹配 → PATH + manifest 不变 | `TestE2E_ChecksumMismatchAborts` |
| Activate 失败（只读目录）→ 原安装可用 | `TestE2E_ActivateFailureKeepsInstall` |
| `--all` 部分失败 → 非零 error | `TestE2E_AllPartialFailureNonZero` |
| AdoptOriginal 分支升级后链到新版本 | `TestE2E_AdoptOriginalBranchActivatesNewVersion` |
| 回滚再升级 + Prune 不删激活版本 | `TestE2E_RollbackThenUpgradePruneKeepsActive` |
| 下载超限中止 | `TestE2E_DownloadOversizeAborted` + ghrelease 单测 |
| 重定向到白名单外 host 被拒 | `TestRedirectToDisallowedHostRejected` |
| Authorization 不外泄 | `TestAuthorizationNotSentOnDownloadRequest`、`TestAuthorizationNotLeakedOnDownloadRedirect` |

---

## 主要改动文件

- `cmd/upgrade.go`
- `cmd/rollback.go`
- `cmd/e2e_test.go`
- `internal/store/store.go` / `store_test.go`
- `internal/ghrelease/client.go` / `client_test.go`
- `internal/archive/archive.go`
- `docs/specs/phase2-adopt-upgrade.md`
- `docs/records/PHASE2-FIXES-DONE.md`（本文件）
