# Cleanzo 支持 Windows · 设计

- 日期：2026-10-20
- 目标版本：0.2.0（版本号与发版时机由维护者决定）
- 状态：已评审，执行中
- 实施计划：`docs/superpowers/plans/2026-10-20-windows-support.md`

## 一、为什么

Cleanzo 目前只支持 macOS。Windows 用户同样面临缓存、崩溃转储、安装包和开发工具缓存不断堆积的问题。mygo 0.5.0 已经提供了 WebView2 后端：它是纯 Go 实现，Mac 上就能交叉编译，NSIS 安装包和按平台区分的自动更新也已经具备。因此，移植的主要工作量在 Cleanzo 自身对 macOS 的假设上，而不在框架层。

## 二、目标与不做

**目标**
- Windows 10/11 x64：可安装（NSIS，按用户安装）、可自动更新。
- 概念上能移植的功能都能用：状态页、垃圾清理、AI 工具、技能管理、全盘扫描、大文件、相似照片、应用（已安装）、设置。
- macOS 行为一字不变。
- Windows 上的删除安全性不低于 macOS。

**不做（首版）**
- arm64 原生包。ARM 机器通过 x64 仿真运行。
- 应用的「残留」页签。在 Windows 上，「哪个文件夹属于哪个应用」的判断依据太弱，误删的后果太大，所以先隐藏。
- 需要管理员权限的系统级清理：`C:\Windows\Temp`、`SoftwareDistribution`、`ProgramData` 下的 WER。
- Store/MSIX 应用的卸载。只提供 `ms-settings:appsfeatures` 跳转。
- HEIC 照片、INetCache、代码签名、Linux。

## 三、基线（2026-10-20 实测）

- **编译**：mygo 本身对 `windows/amd64` 和 `windows/arm64` 都能编过（`CGO_ENABLED=0`）。Cleanzo 只有 11 个编译错误，都是 darwin 专有的 syscall：`main.go` 里的 `Stat_t`、`Statfs`、`Lstat`，以及 `tools.go` 里的 `Setpgid`、`Kill`。
- **macOS 耦合**：
  - 约 15 个系统命令：lsof、lsappinfo、plutil、mdls、du、find、sips、ps、open、system_profiler、sqlite3，以及 brew、xcrun 等工具。
  - `native.go` 通过 libSystem 和 AppKit 读取系统数据。
  - 多处路径比较写死了 `/`，例如 `within()`、`aitools.go` 的受保护部件检查、`crashpad.go`、`monitor.go`。
- **前端**：从不判断操作系统。路径工具假定 `^/Users/[^/]+`，文案里到处是「废纸篓」「访达」「Mac」「完全磁盘访问」。

## 四、架构

- **构建标签**：OS 调用放进 `*_darwin.go` 和 `*_windows.go`，darwin 代码原样搬过去，不改语义。纯逻辑和数据表（包括 Windows 规则表）不带构建标签，Mac 上可以测；Windows 专属代码由 GitHub Actions 的 windows-latest 来测。
- **绑定**：绑定方法只写在不带标签的文件里，平台差异下沉到小写的 helper 函数中。原因是 `mygo generate` 按宿主机生成 `src/mygo.ts`，各平台的绑定签名和 JSON 类型必须完全一致。这一点由一条 AST 测试强制。
- **Windows API**：优先用 `golang.org/x/sys/windows` 和 `windows/registry` 的现成封装，少手写结构体布局。这是本次唯一新增的依赖。
- **兜底**：`native_other.go` 改为 `!darwin && !windows` 的兜底实现。

## 五、Windows 安全模型

原则与 macOS 一致：只修改解析后的 `%USERPROFILE%`，以及位于其内的 temp。

1. **按最终目标判断路径。** Go 1.27 把 junction 报告为 `ModeIrregular`，`EvalSymlinks` 也不会跟随 junction；而现有守卫只拦 `ModeSymlink`（`junk.go:1205/1300/637`）。因此：
   - Windows 上的 `resolvePath` 改用 `GetFinalPathNameByHandleW`。
   - 统一使用掩码 `ModeSymlink|ModeIrregular`。
   - `removable()` 逐级检查路径上的重解析点，CleanPaths、TrashPath、AI 工具、技能都会经过它。
   - 云占位符本身也是重解析点，所以会一并被拦住。
2. **拒绝特殊路径形式。** 包括：备用数据流（盘符之后再出现 `:`）、`\\?\`、`\\.\`、UNC 路径。
3. **temp 根必须在 profile 内。** 否则 `sys.temp` 和 `$TMPDIR` 根都不可用。`changedAt` 取 LastWrite 和 Creation 中较晚的一个，因为解压出来的文件会保留旧的修改时间。
4. **受保护区域。** 列表写在变量初始化里（测试不会运行 `main()`），对比时统一用 `filepath.FromSlash` 转换。
   - 只保护根目录本身：已知文件夹（Desktop、Documents、Downloads、Pictures、Music、Videos、Favorites、Saved Games），以及 AppData、AppData\Local、AppData\Roaming、AppData\LocalLow。
   - 保护整棵子树：
     - `OneDrive*`：按第一级路径的前缀判断，同时覆盖 `%OneDrive%`、`%OneDriveConsumer%`、`%OneDriveCommercial%` 所指的位置。
     - `AppData\Roaming\Microsoft`。
     - AppData\Local 下的：`Packages`、`Programs`、`Microsoft\WindowsApps`、`Microsoft\Credentials`、`Microsoft\Vault`、`wsl`、`Docker`。
     - 用户目录下的：`NTUSER*`、`.ssh`、`.gnupg`。
     - Cleanzo 自身的数据：`%APPDATA%\Cleanzo`，以及 `%LOCALAPPDATA%\Cleanzo`（WebView2 的 profile 在这里）。
5. **回收站移动失败时保留文件。** mygo 0.6.2 的 `Shell.TrashItem` 会拒绝永久删除，Cleanzo 在调用前保留自己的安全边界：
   - Windows 上 `trashItem` 仍由 `recycle` 接管：依次检查 `cleanable`、`platformRemovalSafe`、解析后卷的固定盘类型，以及 connected-files，随后通过可注入的 `shellTrashItem` 调用 mygo 0.6 的 `Shell.TrashItem`。原有安全检查仍返回 `blocked`。
   - mygo 在主线程以 IFileOperation 移入回收站，进度 sink 拒绝永久删除；无法回收时不弹对话框、保留原文件。所有 Shell 失败统一返回 `not-recycled`，不依赖 mygo 的错误文本。
   - Windows 可能联动移动网页及同名附属文件夹，因此双向拒绝这类选择，返回 `connected-files`。网页扩展名为 `.htm`、`.html`、`.shtml`、`.shtm`、`.xhtml`、`.mht`、`.mhtml`；连接后缀采用最小英文集合 `_files`、`.files`、`-files`、` files`，均忽略大小写，不猜测本地化后缀。无法检查元数据时拒绝移动。
   - 两个新错误码沿用垃圾清理、CleanPaths、TrashPath、AI 工具和技能的失败结果，并提供中英文解释。`sys.trash` 的 SHQueryRecycleBinW/SHEmptyRecycleBinW 查询和清空规则保持。
   - AppData 下的路径不能从大文件、全盘扫描、TrashPath 手动移入回收站，只能由规则清理。
6. **占用保护。** Chromium 以 share-delete 方式打开缓存文件，应用运行中照样删得掉，所以文件锁不能当作保护。改为基于进程快照判断：
   - 快照内容：exe 所在目录、命令行中的绝对路径（`--user-data-dir=` 等）、exe 名、产品名。
   - 双向匹配：路径在某个快照路径之内算占用，某个快照路径在该路径之内也算占用；文件夹名与 exe 名或产品名一致同样算占用。
   - 快照失败时 fail-closed，一律不删。
   - 识别 Cleanzo 自身时，改用 PID 和 exe 路径，替代原来的 bundle ID。
7. **其他。**
   - `fullDiskAccess()` 在 Windows 上返回 true。
   - `configPath` 改为 `os.UserConfigDir()`/Cleanzo/config.json，在 macOS 上路径不变。

## 六、功能设计

**垃圾清理**：新增不带构建标签的 `windows_rules.go`。
- **模式根**：新增 `$LOCALAPPDATA` 和 `$APPDATA`，展开后仍必须位于 home 内。概念相同的规则复用现有 ID 和文案。
- **系统**：
  - `sys.temp`：7 天未改动才清。
  - `sys.crashdumps`：Crashpad 目录。
  - `sys.wer`：WER 的 ReportArchive、ReportQueue，以及 `CrashDumps`。
  - `sys.shadercache`：D3DSCache，以及 NVIDIA、AMD 的着色器缓存。
  - `sys.trash`：清空回收站。原生实现，默认不选，并在本轮清理中最先执行。只针对 home 所在卷；清理前重新调用 `SHQueryRecycleBinW` 查询，数量或大小与扫描时不同就跳过。
- **浏览器**：只清缓存，包括 profile 下的 `Cache` 目录。覆盖 Chrome、Edge、Brave、Vivaldi、Opera、Firefox、QQ 浏览器。`Owners` 写 exe 名。
- **应用**：`%APPDATA%\*` 下 Electron/Chromium 的通用缓存目录，按进程判断归属。
- **更新**：`update.updaters`，即 `*-updater\pending`。
- **开发**：
  - 缓存目录：npm、npx、pnpm、yarn、bun、node-gyp、electron、typescript、playwright、pip、uv、poetry、cargo、go-build、android、gradle、maven、nuget。
  - 命令：`go clean -modcache`、`pnpm store prune`。按 PATHEXT 查找工具；PATH 用 `;` 拼接，并保留 SystemRoot 和 node 所在目录；用 `CREATE_NO_WINDOW` 避免弹出控制台窗口，用作业对象结束整个进程树。
- **待审**：Downloads、Desktop、Documents 中的 `*.msi`、`*.msix*`、`*.appx*`、`*setup*.exe`、`*install*.exe`；Downloads 中的 `.crdownload`、`.part`、`.partial`。

**状态页**：Windows 版的 `realStatusSource`，不改绑定类型。
- 内存：`GlobalMemoryStatusEx` 加 `GetPerformanceInfo`。页面文件写入现有的 Swap 字段，Compressed 置 0。Pressure 由一个纯函数根据可用内存和提交量换算。
- CPU：`GetSystemTimes`。
- 电池：`GetSystemPowerStatus`；台式机返回 nil。
- 应用：有可见顶层窗口的进程算作应用；没有窗口的进程沿父进程链归到最近的有窗口祖先，并用创建时间防止 PID 被复用造成误判。应用名取 FileDescription。
- 退出应用：仅向可见、无 owner、非 `WS_EX_TOOLWINDOW` 且未被 DWM cloaked 的顶层窗口异步发送 `WM_SYSCOMMAND / SC_CLOSE`（与标题栏 X / Alt+F4 一致），保留应用的保存询问流程；DWM 查询失败按未 cloaked 处理。没有合格窗口时不操作并返回 false，不回退到隐藏或内部窗口，绝不强制终止。不允许退出 explorer.exe、Cleanzo 自身、可执行文件解析后位于 `GetWindowsDirectory` 返回目录（同样解析、忽略大小写）内的系统组件，以及 Microsoft 发布的 MSIX 打包应用（publisher id 为 `8wekyb3d8bbwe` 或 `cw5n1h2txyewy`，包括 Windows Terminal）；这些应用仍显示在列表中，但无退出按钮，`workspaceTerminate` 同样拒绝。包身份优先通过 `GetPackageFamilyName` 查询，查询失败时回退解析任意盘符下 `WindowsApps` 的包全名目录；第三方打包应用仍可正常退出。
- `Info()`：CPU 名称和系统版本读注册表；build ≥ 22000 即为 Windows 11。

**文件类页面**：
- 遍历器用纯 Go 实现，不穿越重解析点，不跨卷。
- 全盘扫描只扫 home，AppData 展开一层。
- 「在资源管理器中显示」使用 mygo 的 `explorer /select`。
- 照片不调用 sips：跳过 HEIC，跳过云占位符和 OneDrive。

**应用**：
- 已安装列表：读取 HKCU 和 HKLM（含 WOW6432Node）的 Uninstall 键，跳过 `SystemComponent` 和带 `ParentKeyName` 的条目。
- 卸载：前端只传键 ID，后端从注册表读取 UninstallString，通过 ShellExecuteEx 启动；MsiExec 的 `/I{GUID}` 规整为 `/X{GUID}`。
- 绝不把 InstallLocation 移入回收站。

**前端**：
- 新增 `src/platform.ts`：`isWindows()` 读取 `window.mygo.runtime().platform`；平台感知的路径工具统一放在这里，替换掉约 8 处重复实现。
- i18n：Windows 上 `t()` 先查 `key@win` 覆盖键，查不到再套用术语表，长词优先：

  | macOS 用语 | Windows 用语 |
  |---|---|
  | 这台 Mac | 这台电脑 |
  | 废纸篓 | 回收站 |
  | 访达 / Finder | 文件资源管理器 / File Explorer |
  | macOS | Windows |
  | 系统设置 | Windows 设置 |

  完全磁盘访问、Dock、活动监视器相关的文案写显式覆盖。另有一条测试在 Windows 模式下扫描所有文案，保证不残留任何禁用词。
- Windows 上隐藏：完全磁盘访问相关界面、技能的软链去重。
- 状态页的标题和内存卡片换成 Windows 标签。
- 字体栈补上 `"Segoe UI Variable", "Microsoft YaHei UI"`。

## 七、打包、更新与发版

- `mygo.config.ts` 增加 `windows: {}` 块，并注明签名配置的位置（`certificate` 或 `signCommand`）。
- `package.json` 增加 `build:windows`，即 `mygo build -platform windows/amd64`。
- 启动时如果 `mygo.WebViewAvailable()` 为 false，弹出原生对话框并给出下载链接。原因是 NSIS 安装包不附带 WebView2 引导程序。
- 发版仍在本地手动进行，一台 Mac 打两个平台的包。更新签名密钥 `MYGO_UPDATER_PRIVATE_KEY` 不进入 CI。
- 已知限制：自更新不会改写注册表里的版本号（「设置 → 应用」里仍显示首次安装的版本）；未签名的安装包会触发 SmartScreen 提示。

## 八、测试与验证

- **Mac（每轮）**：
  - `gofmt -l .`
  - `go vet ./...`
  - `go test -race -count=1 ./...`
  - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`（会连同测试一起编译）
  - `bun run typecheck && bun run test:web && bun run build:web`
- **CI（`.github/workflows/ci.yml`）**：
  - macOS 跑全量测试。
  - windows-latest 跑 `go vet` 和 `go test`，其中包括对抗性测试，所用夹具有：
    - 指向 home 外的 junction
    - `Application Data`
    - ADS 路径
    - UNC 路径
    - 带 `attrib +O` 的离线文件
    - OneDrive 目录
    - 被锁定的文件
    - 带 `--user-data-dir` 的假进程
  - 在种子化的假 profile 上做破坏性端到端清理，确认受保护的夹具完好无损。
  - 真机只读扫描，并附带断言。
  - smoke 页面巡检：首选 Playwright `connectOverCDP`；不可用时改用环境变量开关的 `CapturePage` 截图模式。截图逐页人工核对。
- **测试隔离**：Windows 上 TestMain 把 USERPROFILE、APPDATA、LOCALAPPDATA、TEMP、TMP 都重定向到临时目录，重定向失败就拒绝运行。`t.Setenv("HOME")` 统一改为 `setHome`。
- **macOS 回归**：darwin 测试只允许移动文件和做 `setHome` 机械替换，不得修改断言。最后在本地跑一次完整的签名、公证构建。

## 九、风险

| 风险 | 应对 |
|---|---|
| 没有 Windows 真机 | 靠 CI 对抗性测试、端到端清理和逐页截图覆盖 |
| 交叉编译只做了类型检查，Win32 结构体布局要到 CI 上才第一次真正执行 | 优先用 x/sys 封装，每段封装在 CI 上配一条冒烟断言 |
| CI runner 与真实用户环境不同：runner 是管理员且关闭了 UAC，与真实用户不同；TEMP 是 8.3 短路径；Defender 会短暂占用文件 | 测试只能容忍这些差异，不能依赖它们 |
| WebView2 的远程调试端口未必可用（mygo 绕过了 Loader） | A 轮先探测；不可用就改用 CapturePage 兜底 |
