# Windows QA 环境与准备记录

- Windows 11 Pro，10.0.26200，64-bit；现有 Cleanzo 0.2.4。
- 连接：复用内置浏览器 tab 1；令牌不写入报告。实际 canvas 1408×768，选项 1366×768。
- 进程：PID 6996；路径 C:\Users\cocoon\AppData\Local\Programs\Cleanzo\Cleanzo.exe。
- 原始语言 System，界面 English，浅色；六类扫描范围均勾选，无自定义文件夹。未更改应用设置。
- 安装包 Downloads/cleanzo-setup-0.4.1-amd64.exe，4,739,456 字节；下载来源 GitHub echoVic/cleanzo v0.4.1。
- SHA256 df84350eb3e9b86a258ad8b57025891798500d15ba68e9f896b9754fae8ac46a，与本地发布包一致。
- APPDATA/Cleanzo/config.json 不存在。Roaming 窗口状态备份位于 Downloads/cleanzo-qa-041-data-backup/Roaming/window-state.json，源/备份 SHA256 一致：fef25a8ad7bf11884a5538448aa7ea33d9a3ff440c5f47d451a0db991cb6cdf0。
- WebView 数据备份不完整：运行中的 Cookies 文件被锁定。Alt+F4 关闭主窗口后 PID 6996 仍在；未强制结束进程。
- 输入链路：typeText(abc) 只收到 a；逐键 ASCII 校准通过。切换窗口后 IME 中文导致一次命令错误，已切回英文，单独记录为操作链路问题。
- 尚未运行安装程序；0.4.1 功能测试未开始。旧版空白 Software Update 窗口已关闭；连接保留，辅助 PowerShell 留作安装下一步。
