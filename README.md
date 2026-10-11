# Cleanzo

一个 **macOS 深度清理工具**（MVP）：扫描缓存、日志、临时文件等分类垃圾并一键清理；还能列出已安装应用、查看关联文件后卸载（移入废纸篓）。
原生 Go 桌面应用，单二进制约 8 MB。

## 功能

- 9 个清理分类：应用缓存 / 日志 / 临时文件 / 废纸篓 / npm·pnpm / Go 构建缓存 / pip / Homebrew / Xcode DerivedData；嵌套在一起的分类（如 `~/Library/Caches` 里的 go-build）各算各的，不会重复计算
- 扫描显示每个分类的占用大小与文件数
- 单分类清理或一键清理全部（删除前二次确认弹窗）
- 每个分类展示占用最多的目录明细，清理前清楚知道删了什么
- 全盘扫描：扫描整个磁盘，按占用排行展示大目录，可一键移入废纸篓（流式进度，du 并发加速，可随时停止）
- 中英文界面：设置页可切换「跟随系统 / 中文 / English」
- 大文件专清：扫描全盘找出大于 1GB 的单个文件，可一键移入废纸篓（流式进度）
- 残留清理：找出已卸载应用遗留的 Library 残留，移入废纸篓前有安全提示
- 分类可配置：设置页可增删改分类与扫描目录，持久化到配置文件
- 应用卸载：枚举 /Applications 与 ~/Applications，显示大小与上次打开时间，卸载时连同偏好设置、缓存、容器等关联文件一起移入废纸篓（可恢复）
- 安全保护：只清理用户主目录和用户临时目录；拒绝主目录、~/Library、文稿 / 桌面等用户文件夹本身，以及钥匙串、iCloud Drive、邮件、信息、照片图库、~/.ssh；判断前先解析符号链接并忽略大小写，`/etc`、`/tmp`、`/Users/me/library` 这类别名绕不过去
- 不碰正在使用的文件：被进程打开的文件和 socket（lsof）、以运行中应用的 bundle ID 命名的缓存都会跳过；临时目录只清理 7 天内没有改动、也没有被移入的条目；正在运行的应用不能卸载
- 「移到废纸篓」、照片清理和应用卸载走同一套安全检查；照片图库、iMovie 等应用自己管理的资料库不会被拆

## 开发运行

```sh
bun run dev      # 开发模式，带热更新
```

## 构建打包

```sh
bun run build
# 产物：
#   build/darwin-arm64/Cleanzo.app
#   build/darwin-arm64/Cleanzo 0.1.1.dmg
```

### 签名与公证

`bun run build` 用 Developer ID 签名 .app 和 dmg（hardened runtime + 时间戳），把 dmg 提交苹果公证，再把公证票据装订到 dmg 和 .app 上，配置在 `mygo.config.ts` 的 `macos` 字段。构建机需要：

- 钥匙串里有 `Developer ID Application: Zijie Hu (8LH7PFQJ89)` 的证书和私钥（`security find-identity -v -p codesigning` 能列出来）
- notarytool 凭据 profile `notary`（`xcrun notarytool store-credentials notary --apple-id <Apple ID> --team-id 8LH7PFQJ89`，需要 App 专用密码）

公证通常要几分钟。没有证书的机器构建未签名版本：`bun run build -- -sign=- -skip-notarize`。

检查产物：

```sh
spctl -a -vvv -t exec build/darwin-arm64/Cleanzo.app   # source=Notarized Developer ID
xcrun stapler validate "build/darwin-arm64/Cleanzo 0.1.1.dmg"
```

## 测试

```sh
go test ./...       # 扫描/清理逻辑单测（临时目录，不碰真实文件）
bun run typecheck   # 前端类型检查
go vet ./...
```

## 注意事项

- 内置分类（缓存、日志、临时文件等可再生的垃圾）**直接删除**，立即释放空间；设置里自定义的分类、大文件、残留、照片和应用卸载都**移到废纸篓**，可以恢复。确认弹窗会分别列出两部分
- 同时勾选「废纸篓」时会先清空废纸篓，这次移进去的文件仍可恢复
- 没有「完全磁盘访问权限」时读不到废纸篓，界面会提示去系统设置授权
- 仅支持 macOS
- MVP 扫描为全量遍历，首次约 30s（后续可做并行扫描提速）

## 技术要点

- Go 侧 `Cleaner` 服务绑定暴露给前端，自动生成类型化 TS 客户端（src/mygo.ts），前端调用 `Cleaner.scan()` / `Cleaner.clean()`
- 清理策略：只删除分类目录的**内容**，保留目录本身
- 分类表集中在 `buildCategories()`，加新分类只需加一行

## 后续可扩展

- 并行扫描 / 增量缓存，把扫描时间降到秒级
- 更多分类：Docker、浏览器单独细分、AI 工具残留数据
- 磁盘占用可视化（占比图）
- 清理历史与撤销（移入废纸篓而非直接删除）
