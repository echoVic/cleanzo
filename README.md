# Cleanzo

一个 **macOS 深度清理工具**：按规则扫描这台 Mac 上的垃圾，逐项查看和选择后清理；还能分析磁盘空间、整理大文件和相似照片、卸载应用及清理残留。
原生 Go 桌面应用，单二进制约 8 MB。

## 功能

- 垃圾清理按系统垃圾、应用缓存、浏览器缓存、更新残留、开发工具、需要确认、自定义目录分组；不存在的目标不显示，测量后为零的条目隐藏
- 应用缓存按应用合并 `~/Library/Caches`、沙盒容器和 Application Support 下的 Electron/Chromium 缓存；优先显示已安装应用的名字，认不出时显示目录名。Spotify、网易云音乐、QQ 音乐的缓存默认不勾选
- 浏览器规则只清缓存，不碰历史、Cookie、登录和书签；更新残留覆盖 ShipIt、Sparkle 和 updater 暂存目录
- 开发工具覆盖 npm/npx、pnpm、Yarn、Bun、uv、pip、Cargo、Go、Xcode 等缓存；下载代价大的 Playwright、Puppeteer、Go 模块、Gradle、Maven 缓存默认不勾选
- 工具自己管理的数据交给 `brew cleanup --prune=30`、`pnpm store prune`、`go clean -modcache`、`xcrun simctl delete unavailable`；报告估算或大小上限，工具缺失、忙碌、失败或超时不阻塞其他条目
- 需要确认的备份、固件、Xcode Archives、DeviceSupport、旧安装包、未完成下载、邮件附件和不可用模拟器默认不勾选；备份、归档等可展开逐个选择，DeviceSupport 各平台最新版默认保留
- 扫描和清理显示逐项结果、占用最多的目录、进度与跳过原因，可随时停止；清理前确认永久删除和移到废纸篓的内容
- 空间分析：一进页面就显示磁盘用量；按占用排行展示大目录，可展开、勾选后移入废纸篓（流式进度，du 并发加速，可随时停止）
- 中英文界面：设置页可切换「跟随系统 / 中文 / English」
- 大文件专清：扫描全盘找出大于所选大小（100 MB / 500 MB / 1 GB / 5 GB）的单个文件，可一键移入废纸篓（流式进度）
- 应用卸载页分「已安装 / 残留」页签；残留页找出已卸载应用遗留的 Library 数据，移入废纸篓前有安全提示，垃圾清理首页也显示残留汇总并可跳转
- 相似照片：找出完全相同和近似的照片分组，可点开看大图、前后翻看后再决定移走哪张
- 移入废纸篓后列表原地更新（父文件夹大小随之减少），不用重新扫描；正在使用的项保持勾选
- 设置即时保存：开关扫描分组、忽略或恢复条目，用系统文件夹选择框添加自定义目录；保存失败回滚，修改后提示重新扫描。配置写为 v3，读取旧 v1/v2 分类时保留停用选择和改过的路径
- 应用卸载：枚举 /Applications 与 ~/Applications，显示大小与上次打开时间，可按名称 / 大小 / 上次打开排序，标出正在运行的应用；卸载时连同偏好设置、缓存、容器等关联文件一起移入废纸篓（可恢复）
- 安全保护：文件删除只在用户主目录和用户临时目录内进行；唯一例外是 Homebrew 自己执行的清理命令。拒绝主目录、~/Library、文稿 / 桌面等用户文件夹本身，以及钥匙串、iCloud Drive、邮件、信息、照片图库、~/.ssh；判断前先解析符号链接并忽略大小写，`/etc`、`/tmp`、`/Users/me/library` 这类别名绕不过去
- 具体规则认领的路径不再计入通用应用缓存；忽略规则或关闭分组后，它们的路径仍然保留，不会被另一项清走。Spotlight、CloudKit、JetBrains、虚拟环境和模型目录等受硬保护名单保护
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
#   build/darwin-arm64/Cleanzo 0.1.3.dmg
```

版本号只写在 `mygo.config.ts` 一处：应用运行时从这里读（侧栏和设置页「关于」显示的版本），dmg 挂载后的卷名也带着它（「Cleanzo 0.1.3」），同时挂着几个版本也分得清。

### 签名与公证

`bun run build` 用 Developer ID 签名 .app 和 dmg（hardened runtime + 时间戳），把 dmg 提交苹果公证，再把公证票据装订到 dmg 和 .app 上，配置在 `mygo.config.ts` 的 `macos` 字段。构建机需要：

- 钥匙串里有 `Developer ID Application: Zijie Hu (8LH7PFQJ89)` 的证书和私钥（`security find-identity -v -p codesigning` 能列出来）
- notarytool 凭据 profile `notary`（`xcrun notarytool store-credentials notary --apple-id <Apple ID> --team-id 8LH7PFQJ89`，需要 App 专用密码）

公证通常要几分钟。没有证书的机器构建未签名版本：`bun run build -- -sign=- -skip-notarize`。

检查产物：

```sh
spctl -a -vvv -t exec build/darwin-arm64/Cleanzo.app   # source=Notarized Developer ID
xcrun stapler validate "build/darwin-arm64/Cleanzo 0.1.3.dmg"
```

## 测试

```sh
gofmt -l .         # 应无输出
go vet ./...
go test -race -count=1 ./... # 扫描/清理逻辑单测（临时目录，不清理真实文件）
bun run generate   # 从 Go 服务重新生成绑定
bun run typecheck  # 前端类型检查
bun run build:web  # 前端生产构建
bun run test:web   # 前端单测（bun test，无浏览器的事件夹具）
```

## 注意事项

- 可再生的缓存、日志、临时文件、更新残留等**直接删除**；自定义目录、备份、归档、旧安装包、未完成下载、邮件附件，以及大文件、残留、照片和应用卸载都**移到废纸篓**，可以恢复。确认弹窗会分别列出两部分
- 同时勾选「废纸篓」时会先清空废纸篓，这次移进去的文件仍可恢复
- 没有「完全磁盘访问权限」时不进入其他应用的沙盒数据；废纸篓、iOS 备份、邮件附件显示需要授权，首页提示还有多少沙盒应用可扫描，设置页可查看权限并打开系统设置
- 仅支持 macOS
- 扫描耗时取决于目录和文件数量；规则先展开显示，目录测量在有上限的并发池内进行

## 技术要点

- Go 侧 `Cleaner` 服务暴露给前端，自动生成类型化 TS 客户端（`src/mygo.ts`），前端调用 `Cleaner.scanJunk()` / `Cleaner.cleanJunk()`，经 Channel 边做边报进度
- 规则表集中在 `rules.go`；`junk.go` 负责认领、展开、测量和清理，`owners.go` 负责应用归属，`tools.go` 负责工具命令
- 清理边界由规则的目标模式决定：目录内容保留根目录，匹配目标可移除目标本身；自定义目录只把内容移到废纸篓。子项选择必须仍是该条目的当前候选路径
- `settings.go` 负责设置校验、v1/v2 迁移和原子保存；前端垃圾清理与设置分别在 `src/junk.ts` 和 `src/settings.ts`

## 后续可扩展

- 增量扫描和测量缓存
- 更多已知缓存位置；Docker、虚拟机、模型文件和聊天媒体不在当前清理范围内
- 磁盘占用可视化（占比图）
- 清理历史与撤销（移入废纸篓而非直接删除）
