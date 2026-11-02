# 「应用」页：一个列表管 App 的更新、占用、退出和卸载 — 设计

- 日期：2026-11-02
- 范围：macOS 做全；Windows 只跟着改名和改布局，不加新能力。
- 草图：`.superpowers/brainstorm/83525-1793626825/content/detail-layout.html`（不进仓库）。维护者在三种详情摆法里选了 C「行内展开」。

## 一、为什么

维护者原话：

- 「需要支持应用更新」，并确认指的是「更新本机的第三方 App」。
- 「其实我在想怎么把应用相关能力放到一个页面里，减少切换成本」。

现在和 App 有关的功能分在四处：「应用卸载」页（已安装 / 残留两个标签）、「进程」页（退出 App、磁盘占用增长）、「垃圾清理」页（应用缓存分组），以及这次要加的更新。用户想到的问题通常是围绕某一个 App 的：它多大、多久没用、要不要更新、缓存能不能清、要不要卸载。这些问题现在要跨三个页面才能回答完。

## 二、目标、非目标

**目标**

- 侧栏「应用卸载」改名「应用」，所有 App 放在一个列表里；点开一行就能看到这个 App 的版本和更新、占用和清缓存、退出、卸载。
- 检查本机第三方 App 有没有新版本（macOS），来源是 App Store、Homebrew cask、Sparkle、Electron（`app-update.yml`）。
- 「更新」交给各来源的官方渠道。只有 Homebrew 装的 App 由 Cleanzo 执行 `brew upgrade --cask`。

**非目标**

- Cleanzo 自己下载并替换 App 包（维护者选的是「查新版本 + 交给官方渠道」）。
- Homebrew 全量 cask 目录兜底（本文的方案 B，以后可以单独做）。
- Windows 的 winget 更新。
- 后台定时检查、通知、侧栏红点（维护者选的是「只在打开页签时查」；页签改成列表之后，就是「只在应用页开着时查」）。
- 批量更新。
- 「进程」页、「垃圾清理」页不动；「磁盘占用」增长列表仍然留在进程页（2026-10-26 定的）。

## 三、实测（这台 Mac，2026-11-02，/Applications 下 55 个 App）

| 来源 | 数量 | 说明 |
|---|---|---|
| App Store 收据 `Contents/_MASReceipt` | 0 | iTunes Lookup 按 bundle ID 能查到微信 |
| Sparkle 框架 | 10 | 只有 5 个在 Info.plist 写了 `SUFeedURL`，其中 ima.copilot 是 `http://0.0.0.0:8000/MacApp.xml` |
| `app-update.yml` | 9 | github 源 2 个（PicGo、Pilion）；可用的 generic 源 4 个；3 个是空 url 或 `http://127.0.0.1` |
| Homebrew cask | 29 | `brew info --cask --installed --json=v2` 约 3.5 秒；`brew outdated --cask` 约 12 秒 |

- **Homebrew 的已装版本靠不住**：Chrome、Arc 这类会自己更新的 App，brew 记的还是装上那一版（Chrome 记 155.0.8059.85，实际是 156.0.8078.76；Arc 记 1.169.0，实际是 1.170.0）。`brew outdated --greedy` 报了 19 个，大多是误报。所以要拿 App 包里的版本和 cask 的 `version` 比。
- Homebrew 全量目录 `formulae.brew.sh/api/cask.json`：7808 个 cask，解压后 19 MB，压缩传输 2 MB，在这里下载用了 54 秒。这是不做方案 B 的原因之一。

## 四、版本来源与比较（后端）

一个 App 按下面的顺序找来源，第一个命中的就用它：

| 顺序 | 来源 | 怎么认出来 | 最新版本从哪来 | 和什么比 |
|---|---|---|---|---|
| 1 | App Store | `Contents/_MASReceipt/receipt` 存在 | `https://itunes.apple.com/lookup?bundleId=<bid>&entity=macSoftware&country=<系统地区>`，没结果时用 `country=us` 再查一次 | `CFBundleShortVersionString` |
| 2 | Homebrew | 一次 `brew info --cask --installed --json=v2`，用 artifacts 里的 `app` 名对上 App 路径 | cask 的 `version` 字段，取逗号前的部分 | `CFBundleShortVersionString`（不用 brew 记的 installed） |
| 3 | Sparkle | Info.plist 里有 `SUFeedURL` | appcast 里版本最高的 `item`（只看没有 `sparkle:channel` 的，跳过 `sparkle:minimumSystemVersion` 高于本机系统的） | 有 `sparkle:version` 就和 `CFBundleVersion` 比，否则用 `sparkle:shortVersionString` 和 `CFBundleShortVersionString` 比 |
| 4 | Electron | `Contents/Resources/app-update.yml` | `provider: github`：`https://github.com/<owner>/<repo>/releases/latest/download/<channel>-mac.yml` 的 `version`（不用 API：2026-11-02 这台机器上 `api.github.com` 未登录直接 403 限流，下载链接不受限）；`generic`：`<url>/<channel>-mac.yml` 的 `version`。`channel` 取 yml 里的 `channel`，默认 `latest` | `CFBundleShortVersionString` |
| — | 都没有 | | | 状态 `unknown`，原因「没有更新源」 |

**版本比较 `compareVersions(a, b)`**

- 去掉开头的 `v`/`V`，丢掉第一个 `-`、`+`、`_`、`,`、`(` 或空格之后的内容，再按 `.` 拆成段。每一段都必须是纯数字，否则结果是「无法比较」。实测样例：QQ 的 cask 版本 `7.0.0_260812_01,9.9.33,…` 取 `7.0.0`；Arc 的 `1.171.0 (89666)` 取 `1.171.0`。比较复用 `junk.go` 现有的 `compareVersions([]int, []int)`。
- 段数不同就在后面补 0。
- 「无法比较」时状态是 `unknown`，原因「版本无法比较」，不当作有更新。

**网络约束**

- 只请求 `https://`。主机是 `localhost`、`0.0.0.0`、回环地址或私有地址的 URL，以及空 URL，直接判 `unknown`，原因「更新地址无效」，不发请求。
- 每个请求超时 10 秒，响应体最多读 2 MB，超出算错误。同时最多 6 个请求。整次检查共用一个 ctx，页面一关就取消。
- github.com 返回 403/429 记为 `error`，原因「GitHub 限流」。User-Agent 是 `Cleanzo`。重定向最多 5 次，每一跳都要过同样的 URL 校验。
- 请求头里不带 Cookie，也不带 App 的任何本地数据。

**类型和绑定**

```go
type AppUpdate struct {
    ID       string `json:"id"`       // 同 AppInfo.ID
    Source   string `json:"source"`   // "mas" | "brew" | "sparkle" | "electron" | ""
    Current  string `json:"current"`
    Latest   string `json:"latest"`
    State    string `json:"state"`    // "update" | "current" | "unknown" | "error"
    Reason   string `json:"reason"`   // unknown/error 的原因码，前端翻译
    NotesURL string `json:"notesUrl"` // appcast releaseNotesLink、GitHub release 页；只允许 https
    Action   string `json:"action"`   // "brew" | "open-store" | "open-app" | ""
}

// 流式：每查完一个 App 发一条，最后发 ID 为 "__done" 的哨兵（和 ScanOrphans 同样写法）。
// ids 是页面已有的 AppInfo.ID；Go 侧逐个校验是已安装的 App，只读 Info.plist，不重新量大小。
func (Cleaner) CheckAppUpdates(ctx context.Context, ids []string, ch *mygo.Channel[AppUpdate]) error
type AppUpgradeResult struct {
    ID      string `json:"id"`
    Done    bool   `json:"done"`    // brew：包里的版本已等于 Latest；open-*：已打开
    Version string `json:"version"` // 结束后重新读到的包内版本
    Code    string `json:"code"`    // "in-use" | "brew-failed" | "not-checked" | "unsupported" | ""
    Detail  string `json:"detail"`  // brew 输出最后一行
    Command string `json:"command"` // 失败时给「复制命令」的完整命令
}

// 只收 id；动作参数（cask token、trackId）只在 Go 侧，从本次检查的结果缓存里取。
func (Cleaner) UpgradeApp(ctx context.Context, id string) (AppUpgradeResult, error)
// 只把这个 App 的 kind=cache 目录移到废纸篓。
func (Cleaner) CleanAppCache(path string) UninstallResult
```

文件：

- `appupdates.go`：跨平台部分，包括类型、`compareVersions`、URL 校验、受限的 HTTP 读取、并发调度、结果缓存和三个绑定。
- `appupdates_darwin.go`：四个来源，每个实现 `detect(app AppInfo) bool` 和 `latest(ctx, app) (AppUpdate, error)`。
- `appupdates_other.go`：`CheckAppUpdates` 立即发出 `__done`，`UpgradeApp` 返回 `unsupported`。

## 五、「更新」按钮做什么

| Action | 做法 |
|---|---|
| `open-store` | `open macappstore://apps.apple.com/app/id<trackId>` |
| `brew` | 先用 `probeUsage` 检查，App 正在运行就拦下，返回 `in-use`。否则执行 `brew upgrade --cask <token>`，带上 `HOMEBREW_NO_AUTO_UPDATE=1`、`HOMEBREW_NO_INSTALL_CLEANUP=1`，超时 10 分钟。brew 的路径解析和 `toolCommand` 一致。结束后重新读 App 包里的版本：版本等于 Latest 算成功；否则返回 brew 输出的最后一行，前端提供「复制命令」，方便去终端执行（比如 pkg 类 cask 需要输入密码）。同一时间只跑一个 brew；正在跑时，别的 brew 行的「更新」按钮禁用。 |
| `open-app` | `open <App 路径>`。前端提示「在 App 菜单里选择『检查更新…』」 |

## 六、「应用」页（前端）

**改名与入口**

- 侧栏 `nav.apps` 改成「应用 / Apps」，`view-head` 的副标题也跟着改。
- 外部跳转保持可用：
  - 垃圾清理的「残留」动作：打开「残留」筛选。
  - 增长页、进程页的「卸载」：展开对应的行，滚动到「卸载…」并把焦点放上去。

**筛选条**：替换「已安装 / 残留」两个标签

- 「全部 n」「有更新 n」「很久没用」「残留 n」。
  - 「很久没用」：上次打开在 90 天以前，或者从来没打开过。
  - 「有更新」的计数要等检查结束才显示。检查进行中点它，列表上方会显示「还在检查（23/55）」。
  - 「残留」的计数在扫描之前不显示。
- 搜索和排序保留。排序可选：名称、大小、上次打开、7 天增长。只有 `GrowthSummary.Config` 开着时才有「7 天增长」这一项。

**行（收起）**

- 图标、名称，名称后面最多两个标签，按这个优先级：`当前 → 新版`、「运行中」、「7 天 +X GB」（超过提醒线时才有）。没有新版本时，名称后面照旧显示灰色版本号。
- 右侧：大小、上次打开时间。有新版本时多一个「更新」主按钮，不用展开就能更新。
- 进入页面时在后台开始检查更新，版本标签查到一个出现一个。

**行（展开）**：一次只展开一行，分三栏

1. **版本**：`当前 → 新版`、来源，还有「更新说明」链接（有的话）。不能检查时写出原因：没有更新源 / 更新地址无效 / 网络错误 / GitHub 限流 / 版本无法比较。
2. **占用**：展开时调用 `AppUninstallDetail` 量大小，显示「App 本体 / 数据 / 缓存」三项。
   - 缓存 = `kind=cache` 的条目；数据 = 除了 `app` 和 `cache` 之外的所有条目。
   - 打开增长监控时，再显示 7 天和 30 天的增长，数据来自 `GrowthState.apps`，按 `appPath` 对应。
   - 缓存旁边有「清理」按钮，调用 `CleanAppCache`。App 正在运行时，按钮禁用并提示先退出。
3. **运行**：
   - App 正在运行时显示「退出」，复用 `QuitApp`。5 秒后还没退出，经确认可以 `ForceQuitApp`，规则和进程页一样。这里不显示 CPU 和内存。
   - 最后是「卸载…」：打开现有的 `UninstallDialog`，只显示「确认」这一步，因为路径列表已经在展开的行里量好了。

**残留**

- 选「残留」筛选时，列表换成现在 `LeftoversTab` 的残留行，第一次进这个筛选才扫描，和现在的行为一样。

**说明文案**

- 页面底部写一行：「检查更新会访问各 App 自己的更新服务器」。

**Windows**

- 只有「全部」。展开的行只有「占用」（App 大小）和「卸载」（打开注册表登记的卸载器），没有更新、残留、清缓存和增长。

**组件拆分**（`src/react/apps/`）

- `AppsList.tsx`：合并 `InstalledTab` 和 `LeftoversTab`，包含筛选条和展开状态。
- `AppRowExpanded.tsx`，下面是 `VersionCell`、`UsageCell`、`RunCell`。
- `updates.ts`：检查用的 Channel、进度和 `Map<id, AppUpdate>`。离开页面时关掉 Channel，Go 侧的 ctx 随之取消。
- `UninstallDialog.tsx`：不改；列表打开它时直接进入「详情」阶段（路径列表已在展开行里量好），Windows 的「已打开卸载器」阶段照旧。
- `api.ts`：加上 `checkAppUpdates`、`upgradeApp`、`cleanAppCache`、`quitApp`、`forceQuitApp`。

## 七、测试与验证

**Go 单测**（不联网，用 `httptest` 和样例文件）

- `compareVersions`：`v` 前缀、`-beta`、`1.171.0,89666`、段数不同、非数字段。
- 四个来源的解析，样例按真实格式写：iTerm 风格的 appcast（带 `sparkle:version` 和 `releaseNotesLink`）、PicGo 风格的 GitHub release、generic 的 `latest-mac.yml`、iTunes Lookup 的 JSON、`brew info --cask --installed --json=v2` 的 JSON。
- 用 Chrome 的数据做用例：brew 记 155、包里是 156、cask 是 156.0.8078.80，期望结果是 `update`，Current 取包里的版本；cask 和包里版本相同时期望 `current`。
- 坏地址（`http://`、`0.0.0.0`、`127.0.0.1`、空串）：结果是 `unknown`，并且测试服务器一个请求都没收到。
- 响应超过 2 MB、超时、404、GitHub 403。
- `UpgradeApp`：App 在运行时被拦下；brew 失败时带回最后一行输出；id 不在缓存里时报错；动作不会接收前端传来的参数。
- `CleanAppCache`：只碰 `kind=cache` 的条目，App 在运行时被拦下。

**前端**（`tests/apps-react.test.ts`）

- 筛选和计数；检查进行中显示进度文案。
- 一次只展开一行；外部「卸载」请求会展开对应的行并聚焦「卸载…」。
- 第一次进「残留」筛选才扫描；Windows 不显示这几个筛选项。
- 离开页面会关掉 Channel。
- `mock-mygo.ts` 补上四个来源和四种状态的假数据。

**真机**（这台 Mac，`mygo dev`）

- 先手动查出 Arc、Chrome、Ghostty、iTerm、PicGo 的期望结果，再和页面上的显示对照。
- 每种来源都点一次「更新」：brew 选一个没在运行的 cask（比如 Ghostty）；Sparkle 选 iTerm；Electron 选 PicGo。这台机器上没有 App Store 的 App，这一路只靠单测，验证记录里要写明。
- smoke tour：`apps` 页的截图更新，`errors.json` 为 `[]`。
- README 的功能列表同步，写明「brew 的版本信息来自本地缓存，可能比最新晚一天」。
