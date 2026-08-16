# hukou Homebrew 配方（人话版）

> 状态：**已上架（2026-08-16）**。v0.3.0 正式发布后：四个真实指纹已填入配方；
> tap `rtwsvj/hukou` 已公开（`brew tap rtwsvj/hukou && brew trust rtwsvj/hukou &&
> brew install hukou`）；homebrew-core 官方仓库 PR #299130 已提交待审。

## 现状

- 配方文件：`homebrew/hukou.rb`，已通过本地两项检查：
  - `ruby -c`（语法检查）✅
  - `brew style`（官方代码风格检查）✅
- **故意没做**的两件事（原因写明）：
  - `brew install`——会在你这台机器上真装 hukou，没经你同意不做；
  - `brew audit --strict`——需要公开发布存在，而 v0.3.0 还没正式发布。

## 发布时你需要做的三步（都在你机器上、需要网络和 GitHub 账号）

1. **填指纹**：v0.3.0 正式发布后，算出四个安装包的真实指纹，填进配方里替换那四行全 0：

   ```sh
   shasum -a 256 dist/hukou_0.3.0_darwin_arm64.tar.gz   # 四个包各跑一次
   ```

2. **本地真装验证一次**（此时才执行安装，验证配方真的能装、能跑）：

   ```sh
   brew install --formula homebrew/hukou.rb
   hukou version
   brew test homebrew/hukou.rb   # 跑配方自带的冒烟测试
   ```

3. **提交配方**（对外动作，需你决定）：把 `hukou.rb` 放进
   `homebrew/homebrew-core` 的 `Formula/h/hukou.rb`，按 Homebrew 官方流程开
   Pull Request（提交前记得：仓库已公开、发布页已就绪、公式里的说明文字
   符合官方风格）。

## 提交前检查清单（对外前提，都未满足）

- [ ] v0.3.0 正式 tag + Release 已发布（A 队完成）
- [ ] 仓库已公开
- [ ] 四个 sha256 已填入配方（第 1 步）
- [ ] `brew test` 通过（第 2 步）
