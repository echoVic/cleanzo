# 系统状态页 · 设计

- 日期：2026-10-15
- 目标版本：0.1.7
- 状态：待评审

## 1. 为什么做

Cleanzo 的每个页面都是先找出来、再处理。系统状态页也照这个做：只放能接上处理办法的指标，不做第二个活动监视器。

在这台 Mac（M2 Pro、32 GB、460 GB）上只读查到：

- **交换文件占了 22–29 GB 磁盘**，在 `/System/Volumes/VM`。空间分析不扫这个位置，磁盘卡片「已用」里的这部分说不清来源。这两天里它时多时少：系统会按需增加交换文件，也会删掉用不到的。
- **内存压力是「紧张」级别**（内核的 warning）：已使用 27.2 GB，其中被压缩的就有 15 GB。
- **Chrome 的 60 多个进程合计约 21 GB**（活动监视器「内存」列的口径）。同一时刻 `ps` 的常驻内存只算出 3.7 GB，因为被压缩和换出的部分它不算。

## 2. 目标与不做的事

**目标**
1. 一眼看清内存、CPU、磁盘、电池的状态。
2. 内存紧张或磁盘不够时，说清原因并给出处理办法：退出应用、去清理。
3. 只在页面打开时采样，离开页面就停。
4. 数字与活动监视器、系统设置同一口径，用户可以对照。

**不做**
- 「一键释放内存」：`purge` 需要管理员权限，效果也只是暂时的。
- 温度、风扇：Apple 芯片上要用私有接口。
- 网速、按进程的网络和磁盘读写。
- 历史记录：只保留页面打开期间最近 1 分钟的 CPU 曲线。
- 强制退出。
- 菜单栏常驻：以后要加时，采样代码直接复用。
- 单个进程的列表：只按应用合并显示。

## 3. 侧栏与页面

```
 ✦ 垃圾清理
 ◠ 系统状态        ← 新增，和垃圾清理一样是总览页
 空间
   ◔ 空间分析
   …
```

```
系统状态
Apple M2 Pro · 32 GB 内存 · macOS 26.6.2 · 已运行 4 天 10 小时

 ⚠ 内存紧张：系统正在压缩内存、写交换文件，应用会变慢。交换文件占了 22 GB 磁盘。
   退出不用的应用能缓解。                                  [查看占内存的应用]

 ┌ 内存    紧张 ┐ ┌ 交换区        ┐ ┌ CPU           ┐ ┌ 磁盘           ┐
 │ 27.2 / 32 GB │ │ 已用 21.5 GB   │ │ 23%  ╱╲_╱‾    │ │ 可用 91 GB     │
 │ ▇▇▇▇▇▇▇▇▇░░  │ │ 占用磁盘 22 GB │ │ 负载 8.3 5.3 4.9 │ │ 共 460 GB · 已用 80% │
 │ App 内存 · 联动内存 · 被压缩 │ │   │ │ 12 核          │ │ 去清理 ›        │
 │ 另有已缓存文件 4.1 GB  │ │        │ │                │ │                 │
 └──────────────┘ └───────────────┘ └───────────────┘ └────────────────┘
 电池   100% · 已充满 · 最大容量 83% · 循环 170 次           （没有电池的 Mac 不显示）

 占用最多的应用                                     [按内存] [按 CPU]
   G  Google Chrome    62 个进程     20.5 GB     11%   [退出]
   A  Arc              14 个进程     11.7 GB     31%   [退出]
   T  TRAE CN          79 个进程      6.1 GB     10%   [退出]
   …                                                 （共 8 行）
 统计了你自己的 740 个进程，系统进程不在其中 · CPU 和活动监视器一样按单核计算
```

- 卡片四列，窗口窄时两列。卡片沿用现有的 `card` 样式；内存条按「App 内存、联动内存、被压缩」分三段，名称与活动监视器一致。
- 内存卡片的标签：压力正常时不显示，「紧张」「严重」时显示。
- 磁盘卡片的「去清理」跳到垃圾清理。
- 图标沿用应用卸载页的做法，用名字首字母。

## 4. 指标与数据来源

| 指标 | 来源 | 口径 |
|---|---|---|
| 内存 | `host_statistics64(HOST_VM_INFO64)` | App 内存 = internal − purgeable；联动内存 = wired；被压缩 = compressor 占用的页；**已使用 = 三者之和**，同活动监视器的「已使用内存」；已缓存文件 = external + purgeable |
| 内存压力 | `sysctl kern.memorystatus_vm_pressure_level` | 1/2/4 → 正常/紧张/严重；其他值当作正常 |
| 交换区 | `sysctl vm.swapusage` | used 为已用；total 就是交换文件占的磁盘（实测与 `/System/Volumes/VM` 的用量一致） |
| CPU | `host_statistics(HOST_CPU_LOAD_INFO)` 两次读数之差；`sysctl vm.loadavg` | 占全部核心的百分比，同活动监视器底部的「用户 + 系统」；负载为 1/5/15 分钟 |
| 磁盘 | 现有的 `diskStat()` | 与空间分析页相同 |
| 电池 | `/usr/sbin/system_profiler -json SPPowerDataType` | 电量、是否在充电、是否充满、是否接了电源、循环次数、最大容量、状况，都直接取系统信息给出的值 |
| 应用 | `proc_listallpids`、`proc_pidpath`、`proc_pid_rusage(RUSAGE_INFO_V2)`、`responsibility_get_pid_responsible_for_pid`；`NSWorkspace.runningApplications` | 内存取 phys_footprint，同活动监视器「内存」列；CPU 取 user + system 时间，按 `mach_timebase_info` 换算成纳秒；用 proc_start_abstime 识别复用的 pid |
| 机器 | `sysctl machdep.cpu.brand_string`、`hw.memsize`、`hw.logicalcpu`、`kern.osproductversion`、`kern.boottime` | 页面打开时读一次 |

**为什么在进程里直接调用，不跑命令**（在这台 Mac 上实测）：

| 做法 | 每次耗时 | 问题 |
|---|---|---|
| `ps -Ao pid,rss,pcpu,comm` | 90–130 ms | 常驻内存漏掉被压缩和换出的部分：同一时刻 Chrome 3.7 GB 对 21.1 GB |
| `top -l 2` | 3.5–4.7 s | 太慢 |
| `lsappinfo list` | 390 ms | 太慢 |
| `vm_stat`、`sysctl` | 30–50 ms | 可以用，但要解析文本 |
| **purego 调用系统接口** | 750 个进程共 10–15 ms；`NSWorkspace` 热调用 0.4 ms | — |

- purego v0.11.1 本来就在依赖图里（mygo 依赖它，mygo 自己的 `internal/idlemem` 也这样读进程内存）。这次只是从间接依赖变成直接依赖，不下载新东西，也不改版本。
- 实测上面这些在开启 hardened runtime 的二进制里都能读，读数与 `vm_stat`、`sysctl` 的文本输出一致。
- **电池是例外**，原因有两个：
  - 通过 IOKit 读，要写约 100 行 CoreFoundation 转换代码；
  - 系统设置里的「最大容量」是系统自己算的：用 `ioreg` 原始数据算出 82.5%，系统显示 83%。直接读 `system_profiler` 才能对得上。

  它每次要 0.1–0.3 秒，好在电池变化慢，每 60 秒读一次就够了。
- 只能读到当前用户自己的进程。其他用户和 root 的进程（约 250 个，比如 WindowServer、kernel_task）不提权读不到，列表脚注说明。

## 5. 按应用合并

每个进程按以下顺序归属：

1. 可执行文件在某个 `.app` 里：归**最外层**的 `.app`。Chrome 的各个 Helper、DoubaoWork 里面的 `DoubaoWork Browser.app`，都归到外层应用。
2. 不在任何 `.app` 里：归它的**负责进程**（responsible process）所在的最外层 `.app`。这是 macOS 自己记录的「谁启动、谁负责」：
   - 编辑器启动的 node 语言服务归编辑器；
   - 终端里运行的命令归终端应用；
   - WebKit 的网页进程归使用它的应用，包括 Cleanzo 自己。
3. 都没有：按可执行文件名合并，比如 launchd 启动的 node。

显示名称：优先用这个 `.app` 里正在运行的 Dock 应用的本地化名字（飞书、微信、豆包工作）；没有就用 `.app` 文件夹名；再没有就用可执行文件名。

这台 Mac 上的实际结果：

| 应用 | 进程数 | 内存 | 说明 |
|---|---|---|---|
| Google Chrome | 62 | 20.5 GB | 各 Helper 都归到外层 |
| Arc | 14 | 11.7 GB | |
| TRAE CN | 79 | 6.1 GB | 含它启动的 node 1.4 GB |
| 飞书 | 23 | 4.7 GB | |
| 豆包工作 | 35 | 3.8 GB | Helper 的负责进程是内嵌的 `DoubaoWork Browser.app`，不是 Dock 里的豆包工作，所以要先按最外层 `.app` 合并 |
| cmux | 66 | 3.7 GB | 终端应用，含在里面运行的 claude、node、zsh |

- **CPU**：一个应用所有进程在这段时间里的 CPU 时间之和，除以采样间隔。和活动监视器一样按单核计算：100% 就是占满一个核，可以超过 100%。
  - 这样一个卡在死循环里的单线程应用会显示 100%，而不是 12 核里的 8%。
  - 总 CPU 卡片是全部核心的百分比，所以各行相加可能超过它，和活动监视器一样。
- 刚出现的进程，或者 pid 被复用的进程（启动时间变了），这一轮不计 CPU。
- 后端只发两部分：内存最多的 8 个，加上 CPU 最多的 8 个，合并去重。

## 6. 退出应用

- **哪些行有「退出」按钮**：这一组里有出现在 Dock 里的应用（`activationPolicy` 为 regular），它的 bundle 路径等于这一组的 `.app` 路径或在它里面，并且它不是访达，也不是 Cleanzo 自己。菜单栏应用、输入法、后台进程没有按钮。
- **确认框**：沿用现有的确认框。
  - 标题：退出「Google Chrome」？
  - 正文：和在 Dock 里选「退出」一样，有没存的内容时，它会先问你。
  - 按钮：退出 / 取消。
- **后端** `QuitApp(path)`：
  - 找出 bundle 路径等于 `path`、或者在 `path` 里面的 Dock 应用。同一个应用开了几个实例，就全部退出。
  - 排除访达和自己，逐个调用 `NSRunningApplication.terminate`。
  - 最多等 5 秒，看它们是否都已经退出。等待期间按钮显示「正在退出…」。
  - 实测：在开启 hardened runtime、由 LaunchServices 启动的应用里调用，不弹授权框，不需要 entitlement，0.13 秒退出。
- **结果**：
  - 都退出了：提示「已退出「X」」。
  - 5 秒后还在：提示「「X」还没有退出，可能在等你处理没存的内容」。
  - 已经找不到了（`not-running`）：提示「「X」已经退出了」。
  - 退不了（`cannot-quit`：访达、Cleanzo 自己，或者系统没有接受请求）：提示「没能退出「X」」。
- 不提供强制退出。

## 7. 发现问题与提示

| 条件 | 提示 | 按钮 |
|---|---|---|
| 内存压力为紧张或严重 | 内存紧张（严重时为「内存严重不足」）：系统正在压缩内存、写交换文件，应用会变慢。退出不用的应用能缓解。交换文件 ≥ 1 GB 时，加一句「交换文件占了 X 磁盘」 | 查看占内存的应用：切到「按内存」，滚到列表 |
| 内存压力正常，交换文件 ≥ 8 GB | 交换文件占了 X 磁盘空间。系统会陆续删掉用不到的交换文件，重启会全部清空 | — |
| 磁盘可用 < 20 GB | 磁盘只剩 X 可用。macOS 更新和交换文件都需要空间 | 去清理：跳到垃圾清理 |

- 前两条互斥，所以最多同时出现两条提示。
- 阈值写在前端，与提示文案放在一起；后端只报原始数字。
- 电池状况不是「正常」（系统信息给出 Good / Normal 以外的值）时，电池那一行显示「建议维修」标签，不出提示。

## 8. 采样与生命周期

- **后端** `WatchStatus(ctx, ch)`：
  - 第一份等两件事：CPU 的第二次读数（0.5 秒后），和电池的第一次读取（通常 0.1–0.3 秒），最多等 3 秒。电池超时就当作这次没读到，之后的几份不带电池，60 秒后再试。这样电池那一行不会在页面打开后才突然冒出来。
  - 之后每 2 秒一份。电池在后台每 60 秒刷新一次，不拖慢采样；每一份都带上最近一次读到的电池信息。
  - `ctx` 取消或发送失败就返回。
- **前端**：
  - 进入页面时开始；切到别的页面、窗口隐藏（`visibilitychange`）时关掉通道；回来时重新开始。
  - CPU 曲线保留最近 30 个点，也就是 1 分钟；重新开始时清空。
  - 鼠标停在列表上、或键盘焦点在列表里时，行的集合和顺序都不变，只更新数字，免得要点的「退出」按钮跑开。已经退出的应用那一行变淡、去掉按钮；鼠标移开后再重排。
- **开销**：每次采样约 10–15 ms，页面打开时约占单核的 0.6%；电池每分钟一次，约 0.06 秒 CPU。万一窗口最小化时 WebView 不报告 `hidden`，最坏也就是这个开销。

## 9. 接口与代码结构

**新的 `Cleaner` 方法**
- `WatchStatus(ctx, ch *mygo.Channel[StatusSample]) error`
- `QuitApp(path string) (bool, error)`：返回是否都已经退出；错误码 `not-running`、`cannot-quit`

**扩展现有的 `SystemInfo`**：它本来就描述「应用和它所在的机器」，加上 `chip`、`memory`、`cores`、`osVersion`、`bootTime`。`Info()` 每次多读几个 sysctl，耗时以微秒计。不新增方法。

```go
type StatusSample struct {
	Memory  MemoryStatus   // total, used, app, wired, compressed, cached, pressure, swapUsed, swapFiles
	CPU     CPUStatus      // usage（占全部核心的百分比）, load（1/5/15 分钟）
	Disk    DiskStat
	Battery *BatteryStatus // percent, charging, full, plugged, cycles, maxCapacity, condition；没有电池或没读到时为 nil
	Apps    []AppUsage     // key, name, path, memory, cpu（单核百分比）, procs, quit
	Procs   int            // 统计到的进程数
}
```

**文件**
- `native.go`：purego 绑定，包括 sysctl、mach host 统计、libproc、`NSWorkspace` 和 `NSRunningApplication`。只负责读取和转换，不含业务规则。
  - objc 调用都包在 autorelease pool 里。
  - 正式代码通过 `mygo.RunOnMain` 在主线程调用 `NSWorkspace` 和 `NSRunningApplication`，每 2 秒约 0.5 ms，不卡界面。函数本身不依赖在哪个线程，测试可以直接调用。
- `monitor.go`：类型、采样循环、按应用合并、CPU 差值、电池 JSON 解析、`QuitApp`。
- `monitor_test.go`。
- `main.go`：只改 `SystemInfo` 和 `Info()`。新方法挂在 `Cleaner` 上，现有的 `mygo.Bind(Cleaner{})` 会一并绑定。
- 前端：
  - `src/status.ts`：页面；
  - `index.html`：侧栏项和页面；
  - `src/i18n.ts`、`src/style.css`；
  - `tests/status.test.ts`。

**依赖注入**：采样需要的读取函数集中在一个结构体里：

```go
type statusSource struct {
	procs   func() []procSample
	apps    func() []runningApp
	memory  func() MemoryStatus
	cpu     func() (busy, total uint64)
	battery func(ctx context.Context) *BatteryStatus
	disk    func() DiskStat
}
```

正式代码用读系统的那一份，测试传入假的。这和 `cleanHooks` 的做法一致。

## 10. 错误处理

- 某一项读不到（比如 sysctl 失败）：对应数字显示「—」，其他照常。
- 电池的某个字段缺失（旧版 macOS 的系统信息可能没有）：这个字段显示「—」。
- 别的用户的进程读不到：不计入，脚注说明。
- 通道出错：页面显示错误和「重试」，沿用 `errorState`。
- 退出出错：`not-running`、`cannot-quit` 两个错误码翻译成提示。

## 11. 测试与验证

**Go**
- 合并：表驱动的测试数据，覆盖：
  - 嵌套的 Helper 归到外层；
  - 不在 `.app` 里的进程按负责进程归属；
  - 按可执行文件名合并；
  - Dock 应用的 bundle 在这一组的 `.app` 里面（名字和退出按钮都取它）；
  - 同一应用两个实例合成一行；
  - 访达和自己不能退出；
  - 内存前 8 与 CPU 前 8 合并去重。
- CPU：mach 时间换算、单核百分比、pid 复用、进程在两次读数之间退出。
- 内存：页数换算成字节；压力等级的映射，包括未知值。
- 电池：
  - 解析这台笔记本的 `system_profiler` JSON；
  - 台式机没有电池；
  - 字段缺失；
  - 充电中、已充满、使用电池、接电源未充电四种状态；
  - 状况不是 Good / Normal。
- 采样循环：用假的读取函数和时钟，验证：
  - 首份延迟；
  - 电池第一次读取超时；
  - 2 秒间隔；
  - 电池 60 秒刷新、不拖慢采样；
  - `ctx` 取消和发送失败时退出。
- `QuitApp`：
  - 路径匹配：完全相同、在里面、前缀相同但不是同一个应用（`Arc.app` 与 `Arcade.app`）；
  - 排除访达和自己；
  - 等到退出与超时。
- 真机只读冒烟测试：
  - 读得到自己的进程，phys_footprint 在合理范围；
  - 内存总量等于 `hw.memsize`；
  - 已使用 ≤ 总量。

**前端（bun test）**：
- 卡片和三条提示的阈值；
- 页签切换；
- 「退出」先确认再调用 `quitApp(path)`，以及三种结果的提示；
- `quit` 为 false 的行没有按钮；
- 鼠标停在列表上时顺序不变；
- 切换页面时关掉通道；
- 英文界面。

**真机**：在 `bun run dev` 里打开页面：
- 与活动监视器对照内存、交换区和 CPU，与系统设置对照电池；
- 用「国际象棋」测试退出；
- 截图检查中英文和深浅色。

## 12. 实施顺序

1. 后端读取与合并：`native.go` 和 `monitor.go` 的采样部分，加测试。
2. `WatchStatus`、`QuitApp`、`SystemInfo`，加测试。
3. 前端页面、侧栏、i18n、样式，加测试。
4. 真机验证、README、CHANGELOG、版本号。发版前再找你确认。

## 13. 风险

- **私有接口**：`responsibility_get_pid_responsible_for_pid` 将来可能被去掉。先用 `Dlsym` 判断，没有就跳过第 5 节第 2 条，不在 `.app` 里的进程按名字合并。
- **结构体偏移**：`rusage_info_v2`、`vm_statistics64` 的偏移写死在代码里。这两个都是带版本号的结构体，新版本只在末尾追加字段；冒烟测试对照 `hw.memsize` 和自己的 footprint，偏移错了会失败。
- **`system_profiler` 的字段名**：不同 macOS 版本可能不同。缺失的字段显示「—」，不会出错。
- **终端名下的占用比预期大**：在终端里运行的命令行工具都算在终端应用名下。每行显示进程数，脚注说明。
- **AppKit 与线程**：`NSWorkspace.runningApplications` 只在主线程的 run loop 运行时更新。所以这两个类都放到主线程调用（第 9 节），不赌它们在后台线程的行为。
- **归属认错**：只用路径和系统记录的负责进程，不按名字猜。宁可分成两行，也不乱合并。
- **嵌套的应用**：按最外层 `.app` 合并，所以藏在别的应用包里的应用会算在外层名下，比如 Xcode 里的 Simulator。这正是它们的归属，可以接受。
