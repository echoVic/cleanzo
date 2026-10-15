# Cleanzo 0.1.7 · 系统状态页设计优化稿

- 日期：2026-10-15 ｜ 目标版本：0.1.7 ｜ 状态：供研发实现
- 基线：《系统状态页 · 设计》（2026-10-15，待评审）。本稿不重复论证原文已定且成立的内容（指标口径、按应用合并规则、采样架构、退出协议、测试计划、风险缓解），只做三件事：**明确取舍、补全实现所需的状态与规格、把口径写死到可验收**。
- 读者：Cleanzo 研发。本稿即实现依据；实现时不动侧栏既有结构、不动设置页、不新增任何「垃圾清理规则」类表格。

> **评审确认（2026-10-15，胡子杰）**：① 四卡重组（M1）、口径前移（M4）、骨架加载（M6）、数值图例（M8）、原文安全退出协议（A4）——采纳；② 960/640 断点（M7）与排序会话记忆（M2）按本稿默认定案，不再追问；③ **退出反馈复用既有 toast 组件，不另造组件**（11.3 已按此修订）；④ 视觉稿的最终视觉复核由研发在本地实现阶段截图复核，设计评审不再以在线网页预览为验收依据。本稿 16 节为完整实现依据，实现请通读全文，不以摘要为准。

---

## 1. 优化摘要：采纳 / 修改 / 不做

| # | 原文设计点 | 决策 | 说明 |
|---|---|---|---|
| A1 | 采样架构：进程内 purego 调系统接口；首份等 CPU 二读（0.5s）与电池首读，上限 3s；之后每 2s 一份；电池后台 60s 刷新 | ✅ 采纳 | 实测开销约单核 0.6%，开销与实时性成立 |
| A2 | 按应用合并三步归属（最外层 .app → 负责进程 → 可执行名）与显示名规则 | ✅ 采纳 | 与系统「谁启动、谁负责」一致，不按名字猜 |
| A3 | CPU 双口径：卡片=占全部核心；应用=单核百分比、可 >100% | ✅ 采纳 | 与活动监视器一致；页面内须显式标注两处口径（见 M4） |
| A4 | 退出：确认框 → `QuitApp` → 最多等 5s → 四种结果；不提供强制退出 | ✅ 采纳 | 补充结果提示载体为 toast（见 M5） |
| A5 | 提示阈值写在前端、后端只报原始数字 | ✅ 采纳 | 文案与阈值同文件（`src/status.ts`），调整不动后端 |
| A6 | 测试计划（Go 表驱动 + bun test + 真机对照活动监视器/系统设置） | ✅ 采纳 | 展开为第 14 节验收检查表 |
| A7 | 风险缓解（Dlsym 探测私有接口、冒烟对照 hw.memsize、字段缺失容错） | ✅ 采纳 | 见第 15 节 |
| M1 | 四卡 = 内存 / **交换区** / CPU / 磁盘；电池为页尾一行 | 🔧 修改 | 改为 **内存 / CPU / 磁盘 / 电池** 四卡（对齐本次范围口径）。交换区降为内存卡内的次级指标行，并在「磁盘不足」提示里引用其数值。理由：交换区是「内存紧张的佐证」和「磁盘占用的解释」，不是并列的一级指标；电池要与系统设置同级对照，值得一个卡片位 |
| M2 | 列表排序切换 [按内存] [按 CPU] | 🔧 修改 | 升级为分段控件；会话内记忆选择；**确认框打开期间也冻结排序**（原文仅 hover / 键盘焦点） |
| M3 | 列表「共 8 行」 | 🔧 澄清 | 展示后端下发（内存 top8 ∪ CPU top8，合并去重）的**全部行**，通常 ≤10 行、上限 16 行；前端不做二次截断。原文的 8 行是该机器实况，不是规格 |
| M4 | 双口径仅在脚注说明 | 🔧 修改 | CPU 卡内直接标注「占总核心 · 最近 1 分钟」；列表 CPU 列头标注「单核」；脚注保留完整说明。把对照成本前移到阅读点 |
| M5 | 退出结果「提示」载体未指明 | 🔧 修改（已定案） | 退出结果提示**复用项目既有 toast 组件**，不另造组件（见 11.3）；确认框沿用现有 modal |
| M6 | 加载状态未定义 | 🔧 新增 | 首份采样到达前用**骨架屏**（见 8.1）；禁止显示 0 或假值；页头机器信息来自 `Info()`，可先行显示 |
| M7 | 响应式仅「四列 / 窄时两列」 | 🔧 细化 | 内容宽 ≥960px 4 列；640–959px 2 列；<640px 1 列；无电池时网格自动变 3 卡，不特殊处理（见 4.7） |
| M8 | 内存条三段仅靠颜色区分 | 🔧 修改 | 条下加图例行「App 内存 X · 联动 Y · 被压缩 Z」（数值+色点），不依赖纯色传达（WCAG 1.4.1） |
| X1 | 一键释放内存、温度/风扇、网络、长期历史、强制退出、菜单栏常驻、单进程列表 | ❌ 不做 | 与委派范围一致；页面不出现任何相关入口或暗示 |

---

## 2. 页面范围

**做**：
- 内存、CPU、磁盘、电池四项总览；
- 内存紧张 / 磁盘不足时，说清原因并给两条处理路径：**安全退出应用**（确认后走系统正常退出，无强制）与**去垃圾清理**（跳现有页面）；
- 只在页面打开时采样，切走 / 窗口隐藏即停；
- 数字口径与活动监视器、系统设置一致，可对照；
- CPU 曲线仅保留页面打开期间最近 1 分钟（30 点）；应用按应用聚合，无单进程列表。

**不做**（同 X1），另：不改侧栏既有项与结构（只插入一个新项）、不动设置页、不做垃圾清理规则表。

---

## 3. 信息层级

```
L0  页头      系统状态 · 副标题一句话
L0  机器行    Apple M2 Pro · 32 GB 内存 · macOS 26.6.2 · 已运行 4 天 10 小时（Info() 即时，先于采样显示）
L0.5 提示条   最多 2 条，解释 + 行动（内存紧张 / 磁盘不足），仅异常时出现
L1  指标卡    内存 · CPU · 磁盘 · 电池（无电池则 3 张）
L2  应用列表  占用最多的应用，按内存 / 按CPU 切换，可安全退出
L3  脚注      统计口径与范围说明（12px muted）
```

排序原则：**先「是什么状态」，再「哪不对、怎么办」，最后「谁占的、能不能退」**。一切到 L1 卡片的数字都必须能追溯到活动监视器 / 系统设置的对应字段（第 7 节）。

---

## 4. 布局与组件规格

沿用现有组件：`card`、`btn primary/ghost/danger small`、`tag`、`muted`、`small`、`overlay/modal`、`err-inline`、`spinner`、`errorState`。新页面只新增少量状态样式（第 5、11 节）。

### 4.1 页头
- H2「系统状态」20px；副标题 13px muted：「内存、CPU、磁盘和电池的状态，紧张时告诉你怎么办」。
- 机器行 12px muted，格式：`{chip} · {内存总量} · macOS {版本} · 已运行 {时长}`。时长格式：<1 小时 →「X 分钟」；<1 天 →「X 小时 Y 分钟」；≥1 天 →「X 天 Y 小时」。

### 4.2 提示条（callout）
- 全宽卡片，padding 12px 16px，圆角 10px；左侧 16px 状态图标；正文 13px；右侧动作按钮 `btn ghost small`。
- 背景用语义色 10% 着色（第 5 节）；正文用常规文字色，保证 ≥4.5:1。
- 最多 2 条同时出现：内存紧张（含严重）与磁盘不足；顺序固定「内存在前」。规则见第 9 节。

### 4.3 指标卡栅格
- CSS Grid：`repeat(auto-fill, minmax(226px, 1fr))`，间距 12px；卡 padding 16px。列数由内容宽自动推导：≥940px（=4×226+3×12）得 4 列，476–939px 为 2–3 列，<476px 自然降为 1 列；卡宽始终 ≥226px，无需媒体查询、无跳变死区。（评审后修正：原 960/640 三档硬断点与 auto-fill 规则数学不一致，统一收敛为纯公式推导。）
- 卡内结构：标题行（16px 图标 + 13px medium 名称 + 状态徽标）→ 主数值（24px/600，`tabular-nums`）→ 可视化 → 副行（12px muted）→ 行动。

### 4.4 四张卡逐卡规格

**内存卡**
- 标题「内存」+ 徽标：压力正常不显示；「紧张」amber、「严重不足」red（8% 着色底、12px semibold 语义色文字）。
- 主数值：`27.2 / 32 GB`（已使用 = App 内存 + 联动 + 被压缩，同活动监视器「已使用内存」）。
- 三段堆叠条：高 8px、圆角、段序「App 内存 / 联动内存 / 被压缩」；条下图例行 11px：色点 + 名称 + 数值，`App 内存 11.9 GB · 联动内存 3.8 GB · 被压缩 11.5 GB`。
- 次级行 1：`已缓存文件 4.1 GB`（12px muted）。
- 次级行 2（交换区，M1）：swapUsed > 0.1 GB 才显示，`交换文件 已用 21.5 GB · 占磁盘 22 GB`。swapUsed 为 0 时整行隐藏，不显示「—」。
- 无行动按钮；「查看占内存的应用」放在提示条里，不放卡内。

**CPU 卡**
- 标题「CPU」，无徽标。
- 主数值 `23%`；数值旁固定 caption（12px muted）：`占总核心 · 最近 1 分钟`（M4）。
- 迷你曲线：30 点面积图（最近 1 分钟），高 28px，0–100% 固定刻度；`prefers-reduced-motion` 时禁用绘制动画。重新开始采样时清空。
- 副行：`负载 8.3 · 5.3 · 4.9`（1/5/15 分钟）与 `12 核`。

**磁盘卡**
- 标题「磁盘」。
- 主数值 `可用 91 GB`；沿用现有 `.bar` 展示已用 80%（与空间分析页同一 `diskStat()` 数据）。
- 副行：`共 460 GB · 已用 80%`。
- 行动：`去清理 ›`（`btn ghost small`），触发与点击侧栏「垃圾清理」相同的视图切换。

**电池卡**（Battery == nil 时不渲染）
- 标题「电池」；状况非 Good/Normal 时标题行加 tag「建议维修」（amber），不弹提示条。
- 主数值 `100%`；状态行 13px 四态：已充满 / 正在充电 / 正在使用电池 / 已接电源·未在充电。
- 副行 12px muted：`最大容量 83% · 循环 170 次`。字段缺失显示「—」。

### 4.5 应用列表
- 头部：标题「占用最多的应用」14px semibold + 分段控件 [按内存 | 按 CPU]（默认按内存，会话内记忆；受提示条「查看占内存的应用」联动切换）。
- 行结构（高 44px，hairline 分隔）：
  - 图标 24px 圆形：应用名首字母，12px semibold，底色按应用名哈希取自品牌色板（沿用应用卸载页做法）；
  - 名称 14px，超宽省略号 + `title` 全名；
  - 进程数 12px muted，`62 个进程`；
  - 内存 13px `tabular-nums` 右对齐（列头「内存」）；
  - CPU 13px `tabular-nums` 右对齐，**列头「CPU · 单核」**（M4）；
  - 操作：`退出`（`btn ghost small`）；`quit=false` 的行不渲染按钮（菜单栏应用、输入法、后台进程、访达、Cleanzo 自己）。
- 空态：后端返回空列表（或全部读不到）→ 一行 muted「没有可统计的应用进程」。
- 行数：展示全部下发行，不截断（M3）。

### 4.6 脚注（列表下方，12px muted）
`已统计你账户下的 740 个进程，系统和其他账户的进程未计入。应用 CPU 与活动监视器一致，按单核计算，可以超过 100%。`

### 4.7 响应式（M7，评审后按 4.3 修正）
| 内容宽 | 指标卡列数 | 推导 |
|---|---|---|
| ≥940px | 4 列 | 4×226+3×12 = 940 |
| 476–939px | 2–3 列 | auto-fill 自然过渡，卡宽始终 ≥226px |
| <476px | 1 列 | auto-fill 自然结果 |

列表在各档下数值列全部保留（桌面 App 最小窗宽下不触发隐藏）。无电池的 Mac：电池卡不渲染，auto-fill 自动少一卡占位，不做专门样式。

---

## 5. 设计 Token 与亮暗

亮色值（浅）在前、暗色值在后。正文、卡片、侧栏背景沿用现有 `style.css` 变量，不新造；以下仅为本页新增语义 Token。

| Token | 浅色 | 暗色 | 用途 |
|---|---|---|---|
| `--status-ok` | `#34C759` | `#30D158` | 压力正常（保留，暂不展示徽标） |
| `--status-warn` | `#FF9500` | `#FF9F0A` | 「紧张」徽标、内存/磁盘提示条、电池「建议维修」 |
| `--status-critical` | `#FF3B30` | `#FF453A` | 「严重不足」徽标、严重提示条 |
| `--seg-app` | `#3B82F6` | `#60A5FA` | 内存条·App 内存 |
| `--seg-wired` | `#8B5CF6` | `#A78BFA` | 内存条·联动内存 |
| `--seg-compressed` | `#F59E0B` | `#FBBF24` | 内存条·被压缩 |
| `--callout-warn-bg` | `rgba(255,149,0,.10)` | `rgba(255,159,10,.12)` | 提示条底 |
| `--callout-critical-bg` | `rgba(255,59,48,.10)` | `rgba(255,69,58,.12)` | 严重提示条底 |

约束：
- 数值一律 `tabular-nums`，更新不跳宽；
- 徽标为 8% 语义色底 + 12px semibold 语义色文字，对比度 ≥4.5:1（浅色下 `#FF9500` 文字偏暗时允许加深至 `#C77700`，以实测为准）；
- 内存三段不只靠颜色（M8 图例）；`prefers-reduced-motion: reduce` 时关闭骨架 shimmer 与曲线动画；
- 图标：卡标题 16px、`stroke 1.8`、`currentColor`，与侧栏图标语言一致；侧栏「系统状态」项用「脉冲折线」图标（如 `M3 12h4l3-7 4 14 3-7h4`），17px。

---

## 6. 文案清单（zh / en）

| i18n 键 | 中文 | English |
|---|---|---|
| `nav.status` | 系统状态 | System Status |
| `status.sub` | 内存、CPU、磁盘和电池的状态，紧张时告诉你怎么办 | Memory, CPU, disk and battery at a glance, with what to do when things get tight |
| `status.card.memory` | 内存 | Memory |
| `status.card.cpu` | CPU | CPU |
| `status.card.disk` | 磁盘 | Disk |
| `status.card.battery` | 电池 | Battery |
| `status.mem.pressureWarn` | 紧张 | Under pressure |
| `status.mem.pressureCrit` | 严重不足 | Critical |
| `status.mem.appMem` | App 内存 | App Memory |
| `status.mem.wired` | 联动内存 | Wired Memory |
| `status.mem.compressed` | 被压缩 | Compressed |
| `status.mem.cached` | 已缓存文件 {size} | Cached files {size} |
| `status.mem.swap` | 交换文件 已用 {used} · 占磁盘 {size} | Swap in use {used} · {size} of disk |
| `status.cpu.caption` | 占总核心 · 最近 1 分钟 | of all cores · last minute |
| `status.cpu.load` | 负载 {l1} · {l5} · {l15} | Load {l1} · {l5} · {l15} |
| `status.cpu.cores` | {n} 核 | {n} cores |
| `status.disk.free` | 可用 {size} | {size} available |
| `status.disk.total` | 共 {total} · 已用 {pct} | {total} total · {pct} used |
| `status.disk.clean` | 去清理 | Clean Up |
| `status.battery.full` | 已充满 · 已接电源 | Fully charged · On power |
| `status.battery.charging` | 正在充电 · {pct} | Charging · {pct} |
| `status.battery.onBattery` | 正在使用电池 | Running on battery |
| `status.battery.plugged` | 已接电源 · 未在充电 | On power · Not charging |
| `status.battery.health` | 最大容量 {pct} · 循环 {n} 次 | Maximum capacity {pct} · {n} cycles |
| `status.battery.service` | 建议维修 | Service Recommended |
| `status.apps.title` | 占用最多的应用 | Top Apps |
| `status.apps.byMemory` | 按内存 | By Memory |
| `status.apps.byCpu` | 按 CPU | By CPU |
| `status.apps.procs` | {n} 个进程 | {n} processes |
| `status.apps.cpuCol` | CPU · 单核 | CPU · single core |
| `status.apps.quit` | 退出 | Quit |
| `status.apps.quitting` | 正在退出… | Quitting… |
| `status.apps.empty` | 没有可统计的应用进程 | No apps to list |
| `status.footnote` | 已统计你账户下的 {n} 个进程，系统和其他账户的进程未计入。应用 CPU 与活动监视器一致，按单核计算，可以超过 100%。 | Counting {n} processes owned by your account; system and other users' processes are excluded. Per-app CPU is single-core, matching Activity Monitor, and can exceed 100%. |
| `status.alert.memWarn` | 内存紧张：系统正在压缩内存、写交换文件，应用会变慢。退出不用的应用能缓解。 | Memory is under pressure: macOS is compressing memory and writing swap files, which slows apps down. Quitting apps you're not using helps. |
| `status.alert.memCrit` | 内存严重不足：系统正在压缩内存、写交换文件，应用会明显变慢。退出不用的应用能缓解。 | Your Mac is critically low on memory: apps will slow down noticeably. Quitting apps you're not using helps. |
| `status.alert.swapLine` | 交换文件占了 {size} 磁盘。 | Swap files are taking up {size} of disk. |
| `status.alert.swapOnly` | 交换文件占了 {size} 磁盘空间。系统会陆续删掉用不到的交换文件，重启会全部清空。 | Swap files are using {size} of disk. macOS removes them once unused; restarting clears them all. |
| `status.alert.diskLow` | 磁盘只剩 {size} 可用。macOS 更新和交换文件都需要空间。 | Only {size} of disk left. macOS updates and swap files both need room. |
| `status.alert.viewApps` | 查看占内存的应用 | Show memory-heavy apps |
| `status.quit.title` | 退出「{name}」？ | Quit “{name}”? |
| `status.quit.body` | 和在 Dock 里选「退出」一样，如有未保存的内容，它会先问你。 | Same as choosing Quit from the Dock. If there's unsaved work, {name} will ask you first. |
| `status.quit.ok` / `btn.cancel` | 退出 / 取消 | Quit / Cancel |
| `status.toast.quitDone` | 已退出「{name}」 | Quit “{name}” |
| `status.toast.quitPending` | 「{name}」还没有退出，可能在等你处理没保存的内容 | “{name}” hasn't quit yet — it may be waiting for you to handle unsaved work |
| `status.toast.notRunning` | 「{name}」已经退出了 | “{name}” has already quit |
| `status.toast.cannotQuit` | 没能退出「{name}」 | Couldn't quit “{name}” |
| `status.error` | 读取系统状态失败 | Couldn't read system status |
| `btn.retry` | 重试 | Retry |
| `status.uptime` | 已运行 {d} 天 {h} 小时 / {h} 小时 {m} 分钟 / {m} 分钟 | Up {d}d {h}h / {h}h {m}m / {m}m |

数字格式：字节 <1 GiB → MB 取整；≥1 GiB → GB 保留 1 位小数（与活动监视器一致按 2^30 进位）；磁盘沿用现有 `diskStat()` 的格式（与空间分析页一致）；百分比取整。英文界面下「GB」等单位不翻译。

---

## 7. 数值口径对照（验收基准）

| 页面数字 | 系统对照（用户可查） | 后端来源 | 备注 |
|---|---|---|---|
| 内存 已使用 27.2/32 GB | 活动监视器·内存页「已使用」 | `host_statistics64`：App 内存 = internal − purgeable；联动 = wired；被压缩 = compressor 页；三者之和 | 已使用 ≤ `hw.memsize`，冒烟断言 |
| 内存压力徽标 | 活动监视器·内存压力图颜色 | `sysctl kern.memorystatus_vm_pressure_level`：1/2/4 → 正常/紧张/严重；未知值当正常 | 原始等级直接下发前端映射 |
| 已缓存文件 | 活动监视器「已缓存的文件」 | external + purgeable | 不计入「已使用」 |
| 交换文件 已用/占磁盘 | 活动监视器「已使用交换空间」；磁盘上的 `/System/Volumes/VM` | `sysctl vm.swapusage` 的 used / total | total 即占磁盘量（原文实测一致） |
| CPU 卡片 % | 活动监视器窗口底部「用户+系统」 | `host_statistics(HOST_CPU_LOAD_INFO)` 两次差值，占全部核心 | 与列表各行相加可超 100%，属正常 |
| CPU 曲线 | — | 前端保留最近 30 个点（2s×30=1 分钟） | 仅页面打开期间，重启清空 |
| 负载 8.3/5.3/4.9 | `uptime` 命令 | `sysctl vm.loadavg` | 1/5/15 分钟 |
| 应用内存 20.5 GB | 活动监视器·内存列 | `proc_pid_rusage(RUSAGE_INFO_V2).phys_footprint` | 同列口径，含压缩与换出部分 |
| 应用 CPU 11% | 活动监视器·CPU 列（单核） | rusage user+system 按 `mach_timebase_info` 纳秒 / 采样间隔 | 可 >100%；新进程 / pid 复用该轮不计 |
| 磁盘 可用/已用 | 系统设置·储存 | 现有 `diskStat()` | 与空间分析页同源 |
| 电池全部字段 | 系统设置·电池 | `system_profiler -json SPPowerDataType` | 「最大容量」用系统算好的值（82.5%→83%），不自算 |
| 机器行 | 关于本机 | `machdep.cpu.brand_string`、`hw.memsize`、`hw.logicalcpu`、`kern.osproductversion`、`kern.boottime` | `Info()` 一次性读取 |

---

## 8. 状态设计

| 状态 | 触发 | 表现 |
|---|---|---|
| 8.1 加载中 | 进入页面、首份采样未到（≤3s） | 机器行立即显示（`Info()`）；四张卡与列表为骨架：卡标题灰块 + shimmer 占位条，列表 3 行 shimmer；**禁止显示 0、--、假值**；提示条区留空 |
| 8.2 错误 | `watchStatus` 通道 err | 沿用 `errorState`：「读取系统状态失败」+ `重试`（重试=重新建立通道，成功后回骨架态） |
| 8.3 单项缺失 | 某项读取失败（如 sysctl 失败） | 该数字显示「—」，其余照常；「—」带 `title`「暂时读不到」 |
| 8.4 无电池 | `Battery == nil`（台式机） | 电池卡不渲染，网格自动 3 卡；不做占位 |
| 8.5 电池字段缺失 | 旧 macOS 系统信息无字段 | 该字段「—」 |
| 8.6 权限（部分进程） | 其他用户/root 进程读不到（约 250 个，如 WindowServer） | 正常显示；列表与脚注说明范围；**不申请权限、不弹权限引导** |
| 8.7 压力未知值 | sysctl 返回非 1/2/4 | 当作「正常」：无徽标、无提示 |
| 8.8 退出中 | 点确认后 | 行按钮 →「正在退出…」disabled；列表冻结；上限 5s |
| 8.9 窗口隐藏/切页 | `visibilitychange` 或切换视图 | 关闭通道；回来重开并清空 CPU 曲线、保留排序选择 |
| 8.10 列表空 | 后端 Apps 为空 | 「没有可统计的应用进程」（仅真实为空；加载中不算） |

---

## 9. 提示（发现的问题）规则

阈值常量在前端 `src/status.ts`，与文案同文件；后端只发原始数字（压力等级、swap 字节、磁盘可用字节）。

| 条件（按序判定） | 文案 | 按钮 |
|---|---|---|
| 压力 = 2 | `memWarn` +（swapFiles ≥ 1 GB 时追加 `swapLine`） | `查看占内存的应用` → 分段控件切「按内存」、滚动到列表、列表头部 1s 高亮 |
| 压力 = 4 | `memCrit` +（同上追加条件） | 同上 |
| 压力正常 且 交换文件 ≥ 8 GB | `swapOnly`（解释系统会自动回收，无需动作） | 无按钮 |
| 磁盘可用 < 20 GB | `diskLow` | `去清理` → 切到垃圾清理视图（同侧栏点击） |

规则：前三条互斥取其一，加上磁盘条，**同屏最多 2 条**；顺序固定「内存条在上」。阈值：`swapMention = 1 GB`、`swapStandalone = 8 GB`、`diskFree = 20 GB`。电池「建议维修」只进电池卡 tag，不进提示条。

---

## 10. 应用列表：排序、冻结与边界

- **排序**：分段控件 [按内存 | 按 CPU]，默认按内存；会话内记忆（不持久化到设置）。「查看占内存的应用」强制切到按内存。
- **冻结**（防误点，采纳原文并扩展）：鼠标 hover 列表、键盘焦点在列表内、**退出确认框打开期间**、**退出等待期间**，行集合与顺序不变，只更新数字；已退出的行 `opacity .45` 且移除按钮；移开 / 关闭后恢复重排。
- **行数**：全部下发行（内存 top8 ∪ CPU top8 去重，通常 ≤10，上限 16），前端不截断。
- **按钮可见性**：组内存在 `activationPolicy = regular` 的 Dock 应用、bundle 路径等于或位于组 `.app` 内，且不是访达、不是 Cleanzo 自己 → `quit=true`，其余行无按钮。
- **图标**：首字母圆标（沿用应用卸载页做法），底色按名称哈希。
- 数值右对齐等宽；名称溢出省略 + title。

---

## 11. 退出应用流程

### 11.1 确认（沿用现有 modal）
- 标题：`退出「Google Chrome」？`；正文：`status.quit.body`；按钮：`退出`（danger）/ `取消`（ghost）。Esc / 点遮罩 = 取消。
- 打开确认框即冻结列表（10 节）。

### 11.2 执行
- 确认 → 调 `QuitApp(path)`；行按钮变「正在退出…」disabled；后端最多等 5s，逐个 `NSRunningApplication.terminate`，排除访达与自己。

### 11.3 结果（复用既有 toast）
- 退出结果提示**复用项目既有 toast 组件，不另造组件**；展示时长、并发行为、`aria-live` 语义均按该组件默认行为执行，本页不额外定制；若其默认不支持 `prefers-reduced-motion`，按既有组件现状即可，不为本页单独改造。
- 四种结果映射：

| 后端返回 | toast 文案 | 列表行为 |
|---|---|---|
| 已全部退出 | `已退出「X」` | 行淡出，恢复重排后移除 |
| `not-running` | `「X」已经退出了` | 行淡出 |
| 5s 超时仍在 | `「X」还没有退出，可能在等你处理没保存的内容` | 行恢复「退出」按钮 |
| `cannot-quit` | `没能退出「X」` | 行恢复按钮 |

- 不提供强制退出，任何状态都不出现「强制退出」入口。

---

## 12. 采样与生命周期（前端）

- 进入「系统状态」视图 → 建立 `watchStatus` 通道；切走 / `visibilitychange=hidden` → 关闭；回来 → 重开。
- 重开时清空 CPU 曲线（30 点环形缓冲），保留排序与冻结前的行集合。
- 电池由后端每 60s 后台刷新、随采样附带给；前端只按 nil 处理（8.4）。
- 首份等待：后端已合并 CPU 二读与电池首读（≤3s），前端在这段时间保持骨架态，不做数字逐个冒出的过渡（避免「电池行突然冒出来」的观感，前端配合即不冒头）。

---

## 13. 研发映射

| 文件 | 职责 | 关键点 |
|---|---|---|
| `native.go` | purego 绑定：sysctl、mach host 统计、libproc（`proc_listallpids`/`proc_pidpath`/`proc_pid_rusage(RUSAGE_INFO_V2)`/`responsibility_get_pid_responsible_for_pid`）、`NSWorkspace`、`NSRunningApplication` | 只做读取与转换，无业务规则；objc 调用包 autorelease pool；`NSWorkspace`/`NSRunningApplication` 经 `mygo.RunOnMain` 主线程调用（每 2s 约 0.5ms）；`responsibility_*` 用 `Dlsym` 探测，缺失则该步跳过 |
| `monitor.go` | `StatusSample`/`MemoryStatus`/`CPUStatus`/`BatteryStatus`/`AppUsage` 类型；`statusSource` 依赖注入结构；采样循环；按应用合并；CPU 差值与 pid 复用；电池 JSON 解析；`QuitApp` | 首份合并等待 ≤3s；2s 间隔；电池 60s 后台刷新；`ctx` 取消 / 发送失败即返回 |
| `monitor_test.go` | 原文 §11 的 Go 全部用例 | 合并归属 / CPU 换算与复用 / 内存页换算与压力映射 / 电池四态与缺失 / 采样循环（假时钟）/ QuitApp 路径匹配（`Arc.app` vs `Arcade.app`）与超时 / 真机冒烟 |
| `main.go` | `SystemInfo` 扩展 `chip`/`memory`/`cores`/`osVersion`/`bootTime`；新方法挂 `Cleaner`，`mygo.Bind(Cleaner{})` 自动绑定 | 不新增方法面 |
| `src/status.ts` | 页面逻辑、阈值常量+文案、骨架/错误/冻结、toast、退出流 | 阈值：`swapMention 1GB / swapStandalone 8GB / diskFree 20GB / 压力 1·2·4 映射` |
| `index.html` | 侧栏新项（垃圾清理之下、空间分组之上，顶层级）+ `#view-status` | 不动其他视图 |
| `src/i18n.ts` | 第 6 节全部键 | 中英成对提交 |
| `src/style.css` | status 样式、第 5 节 Token、toast | 亮暗成对 |
| `tests/status.test.ts` | 阈值与三条提示；页签切换；退出确认→调用→四结果 toast；`quit=false` 无按钮；hover 冻结；切页关通道；英文界面 | bun test |

**接口**（采纳原文）：
- `WatchStatus(ctx, ch *mygo.Channel[StatusSample]) error`
- `QuitApp(path string) (bool, error)`，错误码 `not-running` / `cannot-quit`
- `StatusSample{ Memory; CPU; Disk; Battery *BatteryStatus; Apps []AppUsage; Procs int }`

```ts
// src/status.ts —— 阈值与文案同文件
export const STATUS_ALERT = {
  pressure: { 1: 'ok', 2: 'warn', 4: 'crit' } as Record<number, string>,
  swapMentionGB: 1, swapStandaloneGB: 8, diskFreeWarnGB: 20,
}
```

依赖注入（采纳原文 `statusSource`，与 `cleanHooks` 一致）：正式代码接系统读取，测试传假实现。

---

## 14. 验收检查表

**功能 / 生命周期**
- [ ] 进入页面即开始采样，切走视图、窗口隐藏后停止（可用日志或 CPU 曲线不再增长验证）
- [ ] 首份 ≤3s 到达；期间骨架态、无 0/假值；机器行先显示
- [ ] 之后每 2s 刷新；CPU 曲线仅保留 30 点（1 分钟），重开清空
- [ ] 电池 60s 刷新不阻塞采样；首份含电池或之后补上，页面无「突然冒出」
- [ ] 单项读取失败显示「—」，其他照常；通道错误显示 errorState+重试，重试成功

**口径对照（真机，与活动监视器/系统设置并排截图）**
- [ ] 内存「已使用」= 活动监视器内存页「已使用」；已使用 ≤ 总量
- [ ] 三段图例之和 = 已使用；已缓存文件 = 活动监视器「已缓存的文件」
- [ ] 应用内存 = 活动监视器「内存」列（Chrome 量级为数十 GB，不是 ps 口径的 GB 级）
- [ ] CPU 卡 = 活动监视器底部「用户+系统」；应用 CPU 与其 CPU 列一致，可 >100%
- [ ] 交换文件 = 活动监视器交换空间；「占磁盘」与 `/System/Volumes/VM` 用量一致
- [ ] 磁盘 = 系统设置·储存 = 空间分析页；电池 = 系统设置·电池（含最大容量、循环）

**提示与路径**
- [ ] 压力紧张→提示条+swapLine（≥1GB）；严重→红条；正常+swap≥8GB→swapOnly；磁盘<20GB→diskLow
- [ ] 同屏 ≤2 条、内存在上；「查看占内存的应用」切排序+滚动+高亮；「去清理」跳垃圾清理
- [ ] 无电池 Mac：3 卡、无电池痕迹；电池字段缺失→「—」；状况异常→「建议维修」tag

**退出**
- [ ] 仅 `quit=true` 行有按钮；访达/Cleanzo/菜单栏应用无
- [ ] 确认框（标题/正文/退出/取消/Esc）→ 等待态 5s → 四结果 toast 文案正确
- [ ] 确认框打开与等待期间列表冻结；退出成功行淡出；无处出现「强制退出」

**样式 / 文案 / 无障碍**
- [ ] 亮暗两套、中英两套截图过目；数值 `tabular-nums` 更新不跳宽
- [ ] ≥940 / 476–939 / <476 三档内容宽截图核对列数推导；内存条图例非纯色传达；徽标对比度 ≥4.5:1
- [ ] reduce-motion 下无 shimmer / 曲线动画；toast `aria-live=polite`

**测试**
- [ ] Go 表驱动全过（合并、CPU、内存、电池、循环、QuitApp、冒烟）
- [ ] bun test 全过（阈值、切换、退出四结果、无按钮行、冻结、关通道、英文）
- [ ] 用「国际象棋」真机验证退出全流程

**不做项确认**
- [ ] 全页无「一键释放」「强制退出」「温度/风扇」「网速」「历史」任何入口或文案暗示

---

## 15. 沿用的风险与缓解（采纳原文）

- 私有接口 `responsibility_get_pid_responsible_for_pid` 可能被移除 → `Dlsym` 探测，缺失时按可执行名合并；
- `rusage_info_v2` / `vm_statistics64` 偏移写死 → 冒烟测试对照 `hw.memsize` 与自身 footprint；
- `system_profiler` 字段名随版本变化 → 缺失显示「—」不出错；
- 终端名下占用偏大 → 每行显示进程数 + 脚注说明；
- AppKit 线程约束 → `NSWorkspace`/`NSRunningApplication` 全部主线程；
- 归属宁可分两行不乱合并；嵌套 .app 归最外层（Simulator 归 Xcode，属预期）。

新增一条（评审后移除）：~~toast 为新组件，仅系统状态页使用，不进公共组件库~~ —— 评审定案改为复用既有 toast 组件，不另造组件，该项风险随之消除。

---

## 16. 视觉稿（交付页内呈现）

本节供交付页构建：以**可交互视觉稿**完整呈现重设计后的系统状态页，作为研发实现基准。

**框架**：复用现有主页面框架（侧栏品牌、导航样式、字体、按钮），侧栏插入高亮的「系统状态」项（脉冲折线图标），其余视图不需要真实功能。

**状态页内容（数据用原文实测）**：
- 机器行：`Apple M2 Pro · 32 GB 内存 · macOS 26.6.2 · 已运行 4 天 10 小时`
- 提示条 1 条（amber）：内存紧张文案 + `交换文件占了 22 GB 磁盘。` + 按钮「查看占内存的应用」
- 四卡：
  - 内存（紧张徽标）：`27.2 / 32 GB`；三段条按 App 内存 11.9 GB / 联动内存 3.8 GB / 被压缩 11.5 GB 比例；图例行；`已缓存文件 4.1 GB`；`交换文件 已用 21.5 GB · 占磁盘 22 GB`
  - CPU：`23%` + `占总核心 · 最近 1 分钟`；曲线示意（30 点）；`负载 8.3 · 5.3 · 4.9`；`12 核`
  - 磁盘：`可用 91 GB`；条 80%；`共 460 GB · 已用 80%`；`去清理 ›`
  - 电池：`100%`；`已充满 · 已接电源`；`最大容量 83% · 循环 170 次`
- 列表（按内存，列头「内存」「CPU · 单核」）：Google Chrome 62 个进程 20.5 GB 11%；Arc 14 个进程 11.7 GB 31%；TRAE CN 79 个进程 6.1 GB 10%；飞书 23 个进程 4.7 GB 3%；豆包工作 35 个进程 3.8 GB 2%；cmux 66 个进程 3.7 GB 5%——全部带「退出」按钮
- 脚注：`已统计你账户下的 740 个进程，系统和其他账户的进程未计入。应用 CPU 与活动监视器一致，按单核计算，可以超过 100%。`

**交互要求**：
1. 亮 / 暗主题切换（默认亮，两套都要能看）；
2. 排序分段控件可切换（按内存 ↔ 按 CPU 时 CPU 高的 Arc 排前）；
3. 点「退出」弹出确认框（复用现有 modal 样式），确认后该行变「正在退出…」并出现 toast「已退出「Google Chrome」」、行淡出（演示用，直接成功）；
4. 提示条「查看占内存的应用」点击后列表区高亮一下并滚动到位；
5. `prefers-reduced-motion` 尊重系统设置。

**状态一览（静态小样，四格并排）**：
- 加载骨架（卡标题灰块 + shimmer 占位条 ×4，列表 3 行 shimmer，机器行已显示）
- 错误态（errorState：读取系统状态失败 + 重试）
- 无电池（3 卡，无电池卡）
- 退出确认框（modal 近景）

以上视觉稿数据为示例机器（原文实测）读数，仅作视觉与布局基准，非运行时数据。
