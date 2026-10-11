# 垃圾清理规则表、侧栏与设置页重构 · 设计

- 日期：2026-10-11
- 目标版本：0.1.3（包含第一期和第二期）
- 状态：待评审

## 1. 为什么改

现在首页预设 9 个分类：应用缓存、日志、临时文件、废纸篓、npm/pnpm、Go、pip、Homebrew、Xcode。在这台 Mac 上只读统计的结果说明，这套预设清理不到真正占地方的东西：

- **开发工具一半是固定的，又不全。** 预设覆盖的开发缓存约 17 GB，却漏了约 22 GB：`~/.cache/uv` 8.1 GB、`~/.npm/_npx` 7.7 GB、`~/.bun/install/cache` 2.1 GB、pnpm store 1.9 GB、`~/.cargo/registry` 1.4 GB、`~/.cache/puppeteer` 1.0 GB。Xcode 那一行在这台机器上是空的。
- **很多应用的缓存不在 `~/Library/Caches`。** Electron/Chromium 应用把缓存放在 `~/Library/Application Support/<应用>/Cache`、`Code Cache`、`GPUCache` 等目录：Trae CN 5.2 GB、Claude 705 MB、语雀 543 MB，现在完全扫不到。647 个沙盒应用的缓存（`~/Library/Containers/<id>/Data/Library/Caches`）也扫不到。
- **「应用缓存」是一个 9.8 GB 的大桶。** 旧版本更新包（两个 `*.ShipIt` 约 2.1 GB）、浏览器缓存、各应用缓存混在一起，只能整桶删。想按应用挑，要去另一个「应用缓存」页，那边还是移到废纸篓，处理方式不一致。
- **空分类照样占一行；设置页只能改这 9 个分类的路径。**

## 2. 竞品怎么做

| 产品 | 结构 | 要点 |
|---|---|---|
| 柠檬清理 | XML 规则表：分类 → 条目 → 路径与过滤器 | 条目带「推荐」（默认勾选）和 `bundleid`（装了才出现）；Xcode、微信、邮件有专门扫描器；iOS 备份、废纸篓、下载默认不勾 |
| Mole | 按区块扫描，开发工具约 150 条规则 | 只报告找到的；遍历用户缓存、沙盒应用缓存、Chromium 缓存目录；有官方命令就用（`pnpm store prune`、`uv cache prune`、`brew cleanup`），工具在运行就跳过；有硬保护名单 |
| CleanMyMac | 系统垃圾 16 类 | 大多默认勾选；磁盘映像、文档版本、部分 Spotify/Gradle 缓存、部分 Xcode 内容默认不勾；按类型分组，可逐项查看 |
| Pearcleaner / DevCleaner | 开发环境 27 种；Xcode 专项 | 只列存在的；DeviceSupport 默认勾选，但每个系统保留最新一版 |

共同点：
- 一张很大的已知位置规则表，分组和条目两级；
- 只显示这台机器上有的；
- 只默认勾选能重新生成的；
- 应用缓存按应用逐个列出；
- 工具自己能清的交给工具。

## 3. 目标与不做的事

**目标**
1. 清到真正占地方、并且能重新生成的东西，不管用户是不是开发者。
2. 每一项都说清楚是什么、清了之后会怎样，用户能逐项决定。
3. 只显示这台机器上存在的条目；增加一种工具或应用，只是在规则表里加一条。
4. 安全性不低于现在：现有的保护规则全部保留。

**不做**
- 删除应用的语言文件、精简通用二进制：会修改应用包、破坏签名。
- 需要管理员权限的系统目录（`/Library/Caches`、`/private/var/log`）：仍然只动主目录和用户临时目录。
  - 唯一例外是 `brew cleanup`：它在 Homebrew 前缀里删除旧版本，由 Homebrew 自己执行。
- Docker、虚拟机磁盘、模型文件（Ollama、huggingface）。
- 聊天软件的图片和视频（微信、QQ 等），留到第三期另做调研。

## 4. 侧栏（信息架构）

```
 Cleanzo
 ───────────────
 ✦ 垃圾清理

 空间
   ◔ 空间分析        （原「全盘扫描」）
   ▤ 大文件
   ▣ 相似照片

 应用
   ▦ 应用卸载        页签：已安装 | 残留
 ───────────────
 ⚙ 设置
                     Cleanzo v0.1.3
```

- 「应用缓存」页删除，内容并入垃圾清理的「应用缓存」组。
- 「残留清理」页删除，内容成为应用卸载页的「残留」页签，页签标题带数量。
- 分组标题（空间、应用）只是标签，不可点击。
- 页面内部行为不变，只改入口：
  - 空间分析就是原来的全盘扫描；
  - 「残留」页签就是原来的残留页（列表、汇总、勾选工具栏、移到废纸篓都一样）。
- 侧栏的「运行中」转圈照旧，残留页签扫描时转圈显示在「应用卸载」上。

## 5. 垃圾清理：规则表

### 5.1 规则模型（Go）

规则写在 Go 代码里，是一张数据表，不是一段段函数。字段如下：

| 字段 | 含义 |
|---|---|
| `ID` | 稳定的键，比如 `dev.npm`、`browser.chrome`；忽略列表和配置迁移都用它 |
| `Group` | `system`、`apps`、`browsers`、`updates`、`dev`、`review`；另有 `custom`，来自设置 |
| `Targets` | 路径模式（`~` 开头，可含 `*`），以及作用方式：<br>`entries` 清理目录里的条目；<br>`self` 清理这个路径本身；<br>`match` 只清理名字匹配某个模式的条目，比如 `*.ipsw` |
| `Owners` | 所属应用的 bundle ID，用于两件事：没装、也没有数据时可不显示；应用正在运行时整项跳过 |
| `Default` | 是否默认勾选 |
| `Delete` | `delete` 永久删除；`trash` 移到废纸篓 |
| `MinAge` | 多久没改动才算，比如临时文件 7 天、旧安装包 30 天 |
| `Command` | 可选，工具自带的清理命令，见 5.6 |
| `Detail` | 展开后是否给出可单独勾选的子项（5.7），默认只列出占用最多的条目 |

扫描结果统一成「条目」：

```go
type JunkItem struct {
	ID, Group, Name string // Name 只用于动态条目（如应用名）；规则条目的名称和说明走前端 i18n
	Size, Files     int64
	Default         bool   // 默认勾选
	Delete          string // "delete" | "trash"
	Running         bool   // 所属应用正在运行，整项跳过
	InUse           int    // 因正在使用而跳过的条目数
	Denied          bool   // 需要完全磁盘访问权限才能读
	Top             []TopItem // 占用最多的条目
	Subs            []SubItem // 可单独勾选的子项（备份、Archives、安装包…）
	Note            string    // 例如 "keep-latest"、"estimate"
}
```

### 5.2 分组与默认

| 分组 | 内容 | 默认 | 删除方式 |
|---|---|---|---|
| 系统垃圾 | 日志、临时文件（7 天）、macOS 自带服务的缓存、废纸篓 | 勾选，废纸篓除外 | 删除 |
| 应用缓存 | 每个应用一行（5.4） | 勾选，少数例外 | 删除 |
| 浏览器缓存 | 只清缓存，不碰历史、Cookie、登录、书签 | 勾选；浏览器在运行就跳过 | 删除 |
| 更新残留 | 应用自动更新留下的旧安装包 | 勾选 | 删除 |
| 开发工具 | 只列检测到的 | 能重新生成的勾选；重新下载代价大的不勾 | 删除，或调用工具命令 |
| 需要确认 | 备份、归档、安装包、模拟器等 | 不勾 | 按规则，用户数据移到废纸篓 |
| 自定义 | 设置里加的目录 | 勾选 | 移到废纸篓 |

### 5.3 规则清单

下表中的路径都相对主目录。「检测」一栏为空表示「路径存在且不为空」。

**系统垃圾**

| ID | 名称 | 目标 | 默认 | 备注 |
|---|---|---|---|---|
| `sys.logs` | 用户日志 | `Library/Logs/*` | ✓ | |
| `sys.temp` | 临时文件 | `$TMPDIR/*`，7 天 | ✓ | 现有规则 |
| `sys.apple` | 系统服务缓存 | `Library/Caches/com.apple.*` | ✓ | 排除硬保护名单；Safari、Xcode 由别的规则认领 |
| `sys.trash` | 废纸篓 | `.Trash/*` | | 需要完全磁盘访问权限；沿用「先清废纸篓、只清扫描时已在的」规则 |

**应用缓存**：动态生成，见 5.4。

**浏览器缓存**（`Owners` 为浏览器的 bundle ID；浏览器正在运行时整项跳过，并显示「退出 Chrome 后可清理」）

| ID | 目标 |
|---|---|
| `browser.chrome` | `Library/Caches/Google/Chrome/*`；`Library/Application Support/Google/Chrome/*/{Code Cache,GPUCache,DawnCache,GrShaderCache,GraphiteDawnCache}` |
| `browser.chromium` | `Library/Caches/Chromium/*` |
| `browser.edge` | `Library/Caches/com.microsoft.edgemac/*`、`Library/Caches/Microsoft Edge/*`；Application Support 下同上的子目录 |
| `browser.brave` | `Library/Caches/BraveSoftware/Brave-Browser/*`；Application Support 下同上的子目录 |
| `browser.arc` | `Library/Caches/company.thebrowser.Browser/*`；`Library/Application Support/Arc/User Data/*/{Code Cache,GPUCache,…}` |
| `browser.dia` | `Library/Caches/company.thebrowser.dia/*`、`Library/Caches/Dia/User Data/*/{Cache,Code Cache}` |
| `browser.safari` | `Library/Caches/com.apple.Safari/*`（可能需要授权） |
| `browser.firefox` | `Library/Caches/Firefox/Profiles/*/cache2`（以及 Zen 浏览器的同类目录） |
| `browser.opera` / `browser.vivaldi` / `browser.qq` | `Library/Caches/com.operasoftware.Opera/*`、`com.vivaldi.Vivaldi/*`、`com.tencent.QQBrowser*/*`、`QQBrowser2/*` |

**更新残留**

| ID | 目标 | 说明 |
|---|---|---|
| `update.shipit` | `Library/Caches/*.ShipIt` | Squirrel 更新器暂存的安装包（GitHub Desktop、Trae…） |
| `update.sparkle` | `Library/Caches/*/org.sparkle-project.Sparkle` | Sparkle 下载的更新包 |
| `update.updaters` | `Library/Caches/` 下名字以 `Updater`、`.updater`、`-updater` 结尾的目录 | GoogleUpdater、Zoom 和 electron-updater 的下载缓存 |

**开发工具**（不勾 = 默认不勾选）

| ID | 目标 | 默认 |
|---|---|---|
| `dev.npm` | `.npm/_cacache/*`、`.npm/_logs/*` | ✓ |
| `dev.npx` | `.npm/_npx/*` | ✓ |
| `dev.pnpm` | `Library/Caches/pnpm/*`，外加命令 `pnpm store prune`（5.6） | ✓ |
| `dev.yarn` | `Library/Caches/Yarn/*`、`.yarn/berry/cache/*` | ✓ |
| `dev.bun` | `.bun/install/cache/*` | ✓ |
| `dev.corepack` | `.cache/node/corepack/*` | ✓ |
| `dev.node-gyp` | `Library/Caches/node-gyp/*`、`.cache/node-gyp/*` | ✓ |
| `dev.electron` | `Library/Caches/electron/*`、`Library/Caches/electron-builder/*`、`.cache/electron/*` | ✓ |
| `dev.typescript` | `Library/Caches/typescript/*` | ✓ |
| `dev.puppeteer` | `.cache/puppeteer/*` | 不勾（项目测试要用，需重新下载浏览器） |
| `dev.playwright` | `Library/Caches/ms-playwright/*` | 不勾（同上） |
| `dev.pip` | `Library/Caches/pip/*` | ✓ |
| `dev.uv` | `.cache/uv/*` | ✓（已建好的虚拟环境不受影响） |
| `dev.poetry` | `Library/Caches/pypoetry/{cache,artifacts}/*`（不含 `virtualenvs`） | ✓ |
| `dev.conda` | `{miniconda3,anaconda3,miniforge3}/pkgs/` 里的 `*.conda`、`*.tar.bz2`（不碰解压后的目录） | ✓ |
| `dev.cargo` | `.cargo/registry/{cache,src}/*`、`.cargo/git/checkouts/*` | ✓ |
| `dev.go-build` | `Library/Caches/go-build/*` | ✓ |
| `dev.go-mod` | `go/pkg/mod`，用命令 `go clean -modcache` | 不勾（所有项目要重新下载依赖） |
| `dev.xcode` | `Library/Developer/Xcode/DerivedData/*`、`Library/Caches/com.apple.dt.Xcode/*`；Xcode 在运行就跳过 | ✓ |
| `dev.simulator-caches` | `Library/Developer/CoreSimulator/Caches/*` | ✓ |
| `dev.cocoapods` / `dev.swiftpm` / `dev.carthage` | `Library/Caches/{CocoaPods,org.swift.swiftpm,org.carthage.CarthageKit}/*` | ✓ |
| `dev.android` | `.android/cache/*`、`.android/build-cache/*`、`Library/Caches/Google/AndroidStudio*/*` | ✓ |
| `dev.gradle` / `dev.maven` | `.gradle/caches/*` / `.m2/repository/*` | 不勾（重新下载代价大） |
| `dev.homebrew` | `Library/Caches/Homebrew/downloads/*` | ✓ |
| `dev.homebrew-old` | 命令 `brew cleanup --prune=30`（5.6） | ✓ |

**需要确认**（默认都不勾；展开后可以逐个勾选子项）

| ID | 目标 | 方式 | 子项 |
|---|---|---|---|
| `review.ios-backups` | `Library/Application Support/MobileSync/Backup/*`（需要授权） | 移到废纸篓 | 每个备份一行，读 `Info.plist` 显示设备名和备份日期 |
| `review.ipsw` | `Library/iTunes/{iPhone,iPad} Software Updates/*.ipsw` | 删除 | 每个固件 |
| `review.xcode-archives` | `Library/Developer/Xcode/Archives/*/*.xcarchive` | 移到废纸篓 | 每个归档，显示名称和日期 |
| `review.device-support` | `Library/Developer/Xcode/{iOS,watchOS,tvOS,visionOS,macOS} DeviceSupport/*` | 删除 | 每个版本；每个平台最新的一版默认不选（标「保留最新」） |
| `review.simulators` | 命令 `xcrun simctl delete unavailable` | 删除 | 列出运行时已不可用的模拟器和大小 |
| `review.installers` | `Downloads/*.{dmg,pkg,mpkg,xip}`，30 天没动 | 移到废纸篓 | 每个文件 |
| `review.partial-downloads` | `Downloads/*.{download,crdownload,part}`，1 天没动、没有被打开 | 移到废纸篓 | 每个文件 |
| `review.mail-downloads` | `Library/Containers/com.apple.mail/Data/Library/Mail Downloads/*`（需要授权） | 移到废纸篓 | 占用最多的条目 |
| `review.leftovers` | 只是提示：「已卸载应用的残留 N 项 · X」，点了跳到应用卸载的「残留」页签 | — | — |

**硬保护名单**（任何规则、任何通用扫描都不碰）：
- `Library/Caches/com.apple.{Spotlight,spotlight,FontRegistry,finder}*`
- `Library/Caches/CloudKit*`
- `Library/Caches/pypoetry/virtualenvs*`
- `Library/Caches/org.R-project.R/R/renv*`
- `Library/Caches/JetBrains*`、`Library/Caches/com.jetbrains.toolbox*`
- `Library/Mobile Documents*`
- `.ollama/models`、`.cache/huggingface`、`.cache/codex-runtimes`
- 现有 `safeToClean` 的全部规则保留：主目录与资源库本身、钥匙串、iCloud、邮件、信息、照片图库、`~/.ssh`、应用自己管理的资料库等。

### 5.4 应用缓存（按应用）

**来源**（都按「所属应用」合并成一行）：
- **A**：`Library/Caches/<条目>`。不包括 `com.apple.*`、被具体规则认领的路径、硬保护名单。
- **B**：`Library/Containers/<bundle ID>/Data/Library/Caches/*`。只在有完全磁盘访问权限时扫描（5.5），不包括 `com.apple.*` 的沙盒。
- **C**：`Library/Application Support/<应用>/{Cache,Code Cache,GPUCache,DawnCache,DawnGraphiteCache,DawnWebGPUCache,GrShaderCache,CachedData,logs}`，也就是 Electron/Chromium 应用的标准缓存目录。不包括 `Service Worker`，那里可能存着网页的离线数据。

**认领**：具体规则认领的路径，从通用来源里排除。如果某个条目里面有被认领的子路径（比如 `Library/Caches/Google` 里的 `Google/Chrome`），就沿用现在 `excludeNested` 的做法：不整条拿走，而是深入一层，只取没被认领的部分。

**归属**：用已安装应用建一张索引，把 bundle ID、显示名、`CFBundleName`、可执行文件名、`.app` 文件夹名统一转成小写后指向同一个应用。来源条目按以下顺序找主人：
1. 条目名就是某个已安装应用的 bundle ID（也接受它的前缀，比如 `com.vendor.app.helper`）；
2. 条目名等于某个已安装应用的名字之一（比如 `Trae CN`、`yuque-desktop`）；
3. 都找不到，用条目名本身。

条目 ID 取 `app:<bundle ID>`；没有主人时取 `app:dir:<名字>`。

**运行中**：主人正在运行时，整行显示「运行中」，不可勾选。找不到主人的条目，靠现有的打开文件检查（lsof）逐项跳过。

**默认不勾**：缓存里存着离线内容的应用——Spotify（`com.spotify.client`）、网易云音乐（`com.netease.163music`）、QQ 音乐（`com.tencent.QQMusicMac`）。它们仍然可以手动勾选。

**展示**：按大小排序，先显示前 15 个，然后是一行「其余 N 个应用（共 X）」，点了展开。

### 5.5 完全磁盘访问权限

- 检测方法：尝试读取几个受保护的位置（`Library/Safari`、`Library/Mail`、`Library/Messages`、`.Trash`），任何一个能读就算已授权。
- 没授权时：
  - 不碰沙盒应用的数据。访问其他应用的数据会弹出系统授权框，我们不在扫描过程中意外弹框。
  - 废纸篓、iOS 备份、邮件附件都显示为「需要授权」，不计入大小。
  - 列表顶部显示一条提示：「授予完全磁盘访问权限后，还能扫描：废纸篓、N 个沙盒应用的缓存、iOS 备份、邮件附件 [去授权]」。沙盒应用的数量只靠列出 `~/Library/Containers` 的目录名得到，不进入里面。

### 5.6 工具命令（第二期）

| 规则 | 命令 | 估算大小 | 运行条件 |
|---|---|---|---|
| `dev.homebrew-old` | `brew cleanup --prune=30` | 解析 `brew cleanup --prune=30 --dry-run` 的「would free approximately X」 | brew 存在；没有 brew 进程在跑；5 分钟超时 |
| `dev.pnpm` | `pnpm store prune` | 无法预估：显示 store 的大小，标「最多」，清理后按实际释放计 | pnpm 存在；没有 pnpm 进程在运行；`pnpm store path` 的结果在主目录里 |
| `dev.go-mod` | `go clean -modcache` | 模块缓存目录大小 | go 存在；没有 go 进程在跑 |
| `review.simulators` | `xcrun simctl delete unavailable` | `xcrun simctl list devices unavailable -j` 列出的设备目录大小之和 | Xcode 存在；Simulator 没在运行 |

- **找工具**：从 Finder 启动的应用 PATH 很短，所以按固定位置找：
  - `/opt/homebrew/bin`、`/usr/local/bin`、`/usr/bin`
  - `~/.local/bin`、`~/.cargo/bin`、`~/.bun/bin`、`~/.volta/bin`
  - `~/.nvm/versions/node/*/bin`（取最新的）、`~/Library/pnpm`
  - 不运行用户的登录 shell。
- **执行**：命令用 `exec.CommandContext` 加超时；停止清理时取消。
- **结果**：「已释放」按清理前后目录大小的差值计算（brew 用它自己的输出）。

### 5.7 子项

`Detail` 规则（备份、固件、Archives、DeviceSupport、安装包、未下载完的文件）在扫描结果里带上子项，包括路径、大小、显示名和次要说明（设备名、日期、版本）。
- 勾选整项 = 勾选它的默认子项。DeviceSupport 的默认子项是「除各平台最新版以外的全部」。
- 展开后可以逐个勾选。
- 清理时把选中的子项路径传给后端，后端逐个核对它们仍在这个条目的目标范围内，才会动它们。

## 6. 扫描与清理流程

**扫描** `ScanJunk(ctx, ch)`，替换现在的 `ScanStream`，按顺序推送事件：
1. **准备**：读设置（关闭的分组、忽略的条目、自定义目录），建立已安装应用索引，探测正在使用的文件和运行中的应用，检测完全磁盘访问权限。
2. **展开规则**：通过检测的规则生成条目，通用来源生成按应用的条目，应用认领和保护规则。推送 `start`，里面是全部条目，大小待定，前端立刻画出分组和各行，每行显示「扫描中」。
3. **测量**：并发测量各条目，套用正在使用、最短时间、受保护标记、`removable` 等检查，每完成一条推送一个 `item`。工具命令的估算和残留统计并行进行，各自推送。
4. **完成**：推送 `done`，带上权限状态和「没有发现的规则数」。

被忽略的条目、关闭的分组根本不进入第 2 步。

**清理** `CleanJunk(ctx, selections, scannedAt, ch)`：
- `selections` 是一组「条目 ID 加可选的子项路径」。
- 进度事件与现在相同：每项开始、正在处理的条目、已清理多少、每项结果。「停止」按钮照旧。
- 废纸篓先清；每一项开始前重新探测正在使用的文件；运行中的应用整项跳过。
- 工具命令按 5.6 的条件执行。

**过期提示**：设置改动后，如果首页已经有结果，在列表上方显示一行「设置已更改 · 重新扫描」，不自动重扫。

**接口**：
- 删除：`Scan`、`Clean`、`ScanStream`、`CleanStream`、`ScanAppCaches`、`GetCategories`、`SaveCategories`。
- 新增：`ScanJunk`、`CleanJunk`、`GetSettings`、`SaveSettings`、`PickFolder`、`FullDiskAccess`。

## 7. 垃圾清理页

```
┌ 横幅：已发现 / 已清理 / 清理完成（与现在相同：进度条、停止、重新扫描）───────┐
└──────────────────────────────────────────────────────────────────┘
 ⓘ 授予完全磁盘访问权限后，还能扫描：废纸篓、312 个沙盒应用的缓存…  [去授权]

 ▾ ☑ 系统垃圾                                   共 1.2 GB · 已选 1.2 GB
     ☑ 系统服务缓存   macOS 自带服务的缓存，会自动重建        820 MB  ⋯  ▸
     ☑ 用户日志       应用日志与崩溃报告                       48 MB  ⋯  ▸
     ☐ 废纸篓         需要授权                                  —
 ▾ ☑ 应用缓存                                   共 9.6 GB · 已选 9.1 GB
     ☑ 语雀           ~/Library/Application Support/yuque-desktop   543 MB  ⋯  ▸
     ☐ Trae CN  运行中                                         5.2 GB
     …                                                       其余 74 个应用（共 1.3 GB） ▸
 ▾ ☑ 开发工具                                   共 31.8 GB · 已选 30.4 GB
     ☑ npm 缓存       下次安装依赖时重新下载                   13.6 GB  ⋯  ▸
     ☑ uv 缓存        已建好的虚拟环境不受影响                  8.1 GB  ⋯  ▸
 ▸ ☐ 需要确认                                   共 6.3 GB
 已检查 64 项，没有发现的不显示 · 已忽略 2 项 管理
```

- 横幅的「已发现」只算前五组；「需要确认」组的总量单独写在横幅副标题里（「另有 6.3 GB 需要你确认」），免得一个 iOS 备份把总数撑大。
- 分组卡片可以折叠。组头是三态勾选框，显示「共 X · 已选 Y」。
- 每行依次是：勾选框、图标（规则用分组图标，应用用首字母）、名称和标签、一行说明、大小、`⋯` 菜单、展开箭头。
  - 标签：运行中、需要授权、移到废纸篓、保留最新、估算。
  - `⋯` 菜单：在 Finder 中显示、不再清理（加入忽略列表，这一行立刻消失）。
  - 展开后：普通规则列出占用最多的条目；`Detail` 规则列出可勾选的子项。
- 扫描中、清理中、清理后的行状态，沿用 0.1.3 已做好的那套：等待、清理中、完成、已停止、未清理，以及变淡。
- 没有任何结果时显示「很干净，没有发现可清理的东西」。

## 8. 应用卸载页

页签为「已安装 | 残留（N）」。
- 「已安装」就是现在的卸载列表，包括排序和运行中标记。
- 「残留」就是现在的残留页：第一次切到这个页签时开始扫描，之后不自动重扫。
- 首页「需要确认」组里的残留提示（数量和大小由 `ScanJunk` 自己统计）直接跳到这个页签；页签还没扫描过就开始扫描。

## 9. 设置页

单页分区，改动即时保存，不再有「保存设置」按钮。

```
设置
 通用
   界面语言                    [跟随系统 ▾]
 垃圾清理
   扫描范围                    ☑ 系统垃圾  ☑ 应用缓存  ☑ 浏览器缓存
                               ☑ 更新残留  ☑ 开发工具  ☑ 需要确认的项目
   不再清理的项目              Spotify 缓存          [恢复]
                               Gradle 缓存           [恢复]
                               （在垃圾清理里点「⋯ → 不再清理」加入）
   自定义目录                  ~/Downloads/installers   ☑ 启用   [移除]
                               [＋ 添加目录…]  清理时移到废纸篓
 权限
   完全磁盘访问权限            ● 已授权 / ○ 未授权   [打开系统设置]
                               用于：废纸篓、沙盒应用缓存、iOS 备份、邮件附件
 关于
   版本                        0.1.3
```

- **自定义目录**：「添加目录…」打开系统的文件夹选择框（mygo `Dialog.Open`，`Directory: true`）。
  - 路径校验沿用现在的规则：受保护的路径、重复的路径不行。不通过时在这一行下面显示原因（已翻译的错误码）。
  - 名称自动取文件夹名，可以改。
- 每次改动调用 `SaveSettings`，保存失败就回滚这一项并提示。
- 侧栏底部的版本号保留，关于里再显示一次。

## 10. 配置文件 v3 与迁移

```json
{
  "version": 3,
  "disabledGroups": ["browsers"],
  "ignored": ["app:com.spotify.client", "dev.gradle"],
  "custom": [{ "name": "旧安装包", "path": "/Users/me/Downloads/installers", "enabled": true }]
}
```

从 v1/v2（分类数组）读取时：
- **路径没改过的内置分类**：
  - 启用的直接丢弃，由规则表接管。
  - 被停用的，换成忽略对应的规则：`logs→sys.logs`、`temp→sys.temp`、`trash→sys.trash`、`npm→dev.npm 和 dev.npx`、`go→dev.go-build`、`pip→dev.pip`、`homebrew→dev.homebrew`、`xcode→dev.xcode`；`caches` 换成关闭「应用缓存」组。
- **路径改过的内置分类**和**自定义分类**：每个路径一条自定义目录，保留名称和启用状态（它们本来就是移到废纸篓）。
- 迁移在读取时完成，下次保存写成 v3；写入仍然原子替换。

## 11. 测试与验证

- **规则**：
  - 每组规则一张表驱动测试。在临时主目录里造出目录树，确认条目出现、大小正确；目录不存在时条目不出现。
  - 认领与嵌套：Chrome 规则认领 `Google/Chrome` 后，通用条目「Google」只剩其余部分。
  - 保护名单、最短时间、受保护标记、运行中整项跳过、打开的文件跳过。
- **按应用归属**：用 `makeApp` 造已安装应用，覆盖 bundle ID、名字、前缀、找不到主人四种情况；三个来源合并成一行。
- **权限**：用 `chmod 000` 模拟读不到的目录；没授权时不进入任何沙盒应用的目录（断言没有读过）。
- **工具命令**：在临时 bin 目录放假的 `brew`、`pnpm`、`go`、`xcrun` 脚本，记录参数、输出 dry-run 文本；验证找工具的固定位置、超时、取消，以及进程在运行时跳过。
- **子项**：DeviceSupport 保留最新、备份读 `Info.plist`、安装包 30 天规则、清理时子项路径越界会被拒绝。
- **配置**：v1、v2 迁移到 v3 的各种情况；`SaveSettings` 的校验。
- **清理流**：选择、子项、删除与移到废纸篓、进度、停止、废纸篓先清。
- **前端**：在浏览器里用模拟后端检查：
  - 分组、隐藏空条目、应用列表截断、子项勾选、`⋯ → 不再清理`、权限提示、过期提示；
  - 侧栏分组、应用卸载页签、设置页（即时保存、文件夹选择、校验）；
  - 中英文和深色模式。
- **真机只读探测**：在这台 Mac 上只跑扫描、不清理，和第 1 节的统计对照：开发缓存应多出约 22 GB，Application Support 里应多出约 6.7 GB，更新残留有 ShipIt、Sparkle 和 updater。

## 12. 实施顺序

每一步都由 Codex 实现，我审查、跑测试、在浏览器里核对后单独提交。

1. **后端引擎**：规则模型和第一期规则（系统、应用缓存三个来源、浏览器、更新残留、开发工具）、归属、认领、保护、权限检测；`ScanJunk` 和 `CleanJunk`；设置 v3、迁移、`PickFolder`；删除旧接口；测试。
2. **后端第二期**：需要确认组（子项、保留最新、最短时间）、工具命令、忽略和关闭分组、残留统计；测试。
3. **首页前端**：对接新接口，按第 7 节实现。
4. **侧栏、应用卸载页签、设置页**：按第 4、8、9 节实现。
5. **收尾**：README、版本号 0.1.3、完整验证、真机只读探测。发版前再找你确认。

## 13. 风险与待定

- **扫描变慢**：条目更多。沙盒应用只在授权后才扫；测量沿用有上限的并发池；第一屏先出条目，大小陆续填上。
- **归属认错**：可能把文件夹认到错误的应用名下。认不出就显示文件夹名，宁可不合并，也不乱合并。
- **`brew cleanup` 在主目录以外删除**：这是 Homebrew 自己的维护命令，只删旧版本和下载，固定的版本会跳过。可以在设置里关掉开发工具组或忽略这一项。
- **规则会过时**：应用改了缓存位置，我们就扫不到，但不会多删。规则表集中在一个文件里，方便以后补充。
