# Cleanzo 0.4.1 Windows 实测环境

- 日期：2026-11-04，Asia/Shanghai。远端任务栏显示当天日期；没有调整系统时间。
- Windows 11 Pro 25H2，10.0.26200，64-bit；AMD EPYC 9Y24，16 vCPU，32 GB RAM，磁盘 149.9 GB。
- 连接：复用用户打开的内置浏览器 tab 1，一台桌面一个操作者。报告不含连接令牌。
- 分辨率菜单 1366×768，实际 canvas 1408×768。测试时外层视口 1408×900，截图中远端画面约 x=0、y=54、1408×792；已恢复外层默认视口。未独立核实 OS DPI 设置。
- 升级：原安装 0.2.4 → 正式发布 0.4.1，安装器检测并关闭旧进程，沿用现有默认安装位置。安装完成并实际启动，未看到 SmartScreen 拦截。
- 安装路径：`C:\Users\cocoon\AppData\Local\Programs\Cleanzo\Cleanzo.exe`。
- 官方安装包：`Downloads/cleanzo-setup-0.4.1-amd64.exe`，4,739,456 字节，来源 https://github.com/echoVic/cleanzo/releases/tag/v0.4.1 。
- 安装包 SHA256：`df84350eb3e9b86a258ad8b57025891798500d15ba68e9f896b9754fae8ac46a`，与本地发布包一致。
- 已安装 exe SHA256：`8a64d648dbea5e1159c5670ac2980f8d713433b253237e8af7fb8be534840a24`，与本地发布 exe 一致；对应发布提交 `98f68be5e760e2d3b2a9ec4bb8bcc885b87363fe`。
- 运行版本：侧栏、About、应用列表均 0.4.1。初次新版本进程 PID 4620；正常退出后查无 Cleanzo 主进程，重开后有实际 UI 证据。
- 原设置：System 外观（浅色），System 语言（English）；六类扫描范围全部勾选，自定义文件夹为空。结束恢复这些设置。
- 安全备份：早期 `Downloads/cleanzo-qa-041-data-backup` 保存 Roaming 窗口状态；旧进程运行时 Cookies 被锁，早期 WebView 备份不完整。正常退出 0.4.1 后已完成 `Downloads/cleanzo-qa-041-closed-backup` 的 Local/Cleanzo 全量目录复制。Cookies 源/备份 SHA256 均 `8da294027e6ff01d4253e19f529e9af6f4ddf89ae08a4b50225a007ddffb43e4`。未用全量覆盖恢复用户数据。
- 输入：远端 typeText 批量仅收到首字符，改用 ASCII 逐键输入。切换焦点时需确认中英文模式；反斜线传输不稳定，只读命令改用 Join-Path。列表显示微信输入法 2.1.0.12；当前实际 IME 厂商没有独立核实，未做中文组字验收。
- 已有软件作为只读数据源：TRAE、WPS、Chrome、Edge、飞书等。不运行安装、卸载或终止这些用户软件。
- 局限：只覆盖此 Win11 x64 真实目录；未测 Win10、ARM64、多屏、多 DPI、长时间稳定性、故障注入、自动更新链路、清理/回收站恢复或卸载。

准备阶段的旧版记录保留在 `environment-preparation-0.2.4.md` 和 `report-preparation-0.2.4.jsonl`，与本次 0.4.1 结果分开。
