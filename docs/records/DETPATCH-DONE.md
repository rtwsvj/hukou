# DETPATCH DONE

探测器补丁：消灭本机 `scan` 一批 unknown。分支 `detectors-patch`。

## 验收结果

| 检查 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过（含 provenance 全套 + 新增 detpatch fixtures） |
| `go build -o bin/hukou .` | 通过 |
| `./bin/hukou scan` | 通过 |

### 本机 scan 摘要

```
summary: total=1901 sources=10 unknown=6 shadowed=10 skipped=9
by source: brew=574 curl-installer=8 local-project=1 macos-app=2 npm=19 pnpm=3 rustup=14 system=1268 unknown=6 uv=6
```

- **unknown: 20 → 6**（达标）
- 剩余 unknown（与目标一致）：
  - `editor` (`~/.local/bin/editor`)
  - `operator` (`~/.local/bin/operator`)
  - `open-design-tools` (`~/.local/bin/open-design-tools`)
  - `open-design-web` (`~/.local/bin/open-design-web`)
  - `publish-project-to-github` (`~/.local/bin/publish-project-to-github`)
  - `longbridge` (`/opt/homebrew/bin/longbridge`)

## 改动清单

### 新增文件

| 文件 | 说明 |
|---|---|
| `internal/provenance/macos_app.go` | 新探测器：`/Applications`、`~/Applications` 下 `.app` 内二进制 |
| `internal/provenance/local_project.go` | 新探测器：`~/Projects/<name>/` 下二进制 |
| `internal/provenance/detpatch_test.go` | 本批补丁 fixture 单测 + runner 顺序断言 |

### 修改文件

| 文件 | 说明 |
|---|---|
| `internal/provenance/env.go` | `ApplicationsDirs`、`ProjectsDir`；`CurlInstallerRoots` 增 grok/codex/hermes；DefaultEnv 填充 |
| `internal/provenance/curl_installer.go` | 三表项匹配；`codexVersionFromPath` / `codexVersionFromReleaseDir` 版本 helper |
| `internal/provenance/uv.go` | uv-managed cpython：`<LocalShare>/uv/python/cpython-<ver>-<platform>/` |
| `internal/provenance/brew.go` | `<prefix>/share/<pkg>/` 推断（inferred，Evidence 注明 share 树） |
| `internal/provenance/runner.go` | 注册 `macos-app`、`local-project`（curl-installer 之后、go/gobin 之前） |
| `internal/provenance/negative_test.go` | 负例套件加入 macos-app、local-project |

### 未改（约束）

- `internal/provenance/gobin.go`
- `internal/provenance/detect.go`（若存在 vendor 路径）

## 行为摘要

1. **curl-installer**
   - `~/.grok/downloads/` → `package=grok`
   - `~/.codex/packages/` → `package=codex`，版本从 `standalone/releases/<ver>-<platform>/` 提取（如 `0.142.3`）
   - `~/.hermes/` → `package=hermes`
2. **uv**：realpath 落在 `uv/python/cpython-<ver>-<platform>/` → `source=uv package=cpython version=<ver> Confidence=exact`
3. **macos-app**：`.app` 内 → `source=macos-app package=<App> Confidence=exact`，Evidence 含 `.app` 路径
4. **local-project**：`ProjectsDir/<name>/` → `source=local-project package=<name> Confidence=inferred`
5. **brew share**：非 Cellar/Caskroom 的 `share/<pkg>/` → `Confidence=inferred`

## 探测器链顺序（相关片段）

```
… → curl-installer → macos-app → local-project → pipx → uv → go → system → unknown
```

## 契约遵守

- 路径全部经 `Env` 注入；探测器无 `os.Getenv` / `exec` / 网络
- 仅 `DefaultEnv` 读环境变量
- 单测：`t.TempDir` + 假 Env；负例套件覆盖新探测器
