# Windows 0.4.1 实测发现

## D01 · P2 · 深色主题原生下拉菜单选项对比度不足

环境：Windows 11 Pro 25H2 x64，Cleanzo 0.4.1，系统原外观浅色，应用主动选择 Dark。已安装二进制与发布包一致。

复现步骤：

1. 打开 Settings，将 Appearance 设为 Dark。
2. 打开 Language 原生下拉菜单。
3. 在中文界面打开应用页的排序原生下拉菜单。

预期：所有未选中选项清楚可读，弹出菜单颜色与文字对比足够。

实际：弹出菜单白底，未选中文字近白色；Language 的中文/English 和应用排序的名称难以辨认，当前选中项灰底深字可读。主页面深色内容正常，浅色菜单观察正常。

影响：用户依赖颜色辨认菜单项时难以操作。主题切换、语言切换、排序和设置持久化本身仍有效。本轮没有观察到数据损失。

复現范围：同一桌面在语言和排序两个控件各复现一次，共两处实测，计一个共同视觉缺陷。W08/W09 两条失败用例共同引用 D01，不计两个缺陷。

源码对应：设置语言为 `src/react/settings/SettingsPage.tsx:406` 的原生 select；应用排序为原生 select。`src/style.css:9` 是 `color-scheme: light dark`，仅按系统偏好不足以证明与应用主动 Dark 设置同步。可作为后续诊断方向；本轮没有更改源码，也没有验证根因或修复。

证据：

- [深色语言菜单原图](screenshots/11-dark-language-menu-raw.png)
- [深色语言菜单桌面裁剪](screenshots/11-dark-language-menu-desktop.png)
- [深色应用排序菜单原图](screenshots/28-dark-sort-menu-raw.png)
- [重启后深色中文仍保存](screenshots/34-restart-dark-zh-raw.png)

## O01 · 待核对 · 升级环境桌面和任务栏仍显示旧蓝色图标

实际：桌面快捷方式和任务栏图标蓝色，窗口、侧栏和检查更新弹窗绿色；正常退出并重开后现象持续。

已核对：已安装 exe 与发布 exe 哈希一致；从同哈希本地发布 exe 只读提取的 PE group icon（资源类型 14，ID 1，语言 1033）及 7 个尺寸为新绿色图标。桌面快捷方式 TargetPath 指向该已安装 exe，IconLocation 为 `,0`，没有指向另一个旧图标路径。

结论边界：确认用户可见不一致，未确认具体成因。Windows Shell 缓存是目前的可能解释；不能据此断言资源打包错误或缓存刷新一定可修复。没有清缓存、重启 Explorer 或更改系统设置。本用例 W05 记 partial。

最小后续核验：在隔离的新用户桌面/干净安装环境比较同一安装包的 Shell 图标；若干净环境一致，再验证升级安装后 Shell 缓存失效策略。

证据：[桌面旧图标](screenshots/30-desktop-icon-raw.png)、[窗口新图标和桌面旧图标](screenshots/31-window-moved-raw.png)、[快捷方式目标](screenshots/40-shortcut-target-raw.png)、[发布 exe 嵌入图标](artifacts/release-exe-embedded-icon.png)。最后一个文件是本地发布 exe 的资源提取产物，不是远端截图。
