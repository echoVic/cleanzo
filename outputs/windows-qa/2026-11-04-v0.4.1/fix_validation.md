# 0.4.1 Windows 实测问题后续核验

日期：2026-11-04。原始 [测试报告](test_report.md) 和 [18 项结果日志](report.jsonl) 保持当时的结论：8 通过、2 失败、8 部分完成。本文件记录后续调查及源码修复，不将已发布的 0.4.1 标为全部通过。

## D01 · 深色原生下拉菜单

根因：`src/style.css` 原先声明 `color-scheme: light dark`，而 MyGo 0.7.1 的 `internal/windows/system.go` 用 WebView2 `Emulation.setEmulatedMedia` 覆盖页面的 `prefers-color-scheme`。Windows 系统浅色、应用主动深色时，页面文字与原生弹出菜单可能选择不同的调色板。语言和应用排序两处症状一致。

修复：默认明确 `color-scheme: light`，深色媒体查询明确 `dark`。Windows 冒烟测试同时检查根元素和各页面原生 select 的计算样式，要求仅一个匹配的调色板。

回归：`tests/native-select.browser.py` 在修改前失败：

```text
AssertionError: dark: ambiguous native palette:
{'id': 'theme-select', 'scheme': 'light dark', 'value': 'system', 'options': 3}
```

修改后通过：设置页 dark → light → dark、实际 en/zh 选择、应用排序 light/dark 的计算调色板均正确。`tests/smoke/hook.test.ts` 验证模糊调色板被拒绝，单一匹配调色板被接受。

边界：浏览器回归验证 CSS/交互合同。云桌面仍安装公开 0.4.1，尚未安装源码修复构建；**新构建原生弹出菜单的像素效果仍需在该桌面复测**。Windows CI 冒烟使用独立的安装构建，不等同于原云桌面的完整复测。

## O01 · 升级后桌面与任务栏旧图标

已安装程序与发布 exe 哈希一致，7 个 PE icon 尺寸均为新绿色图标；快捷方式目标正确，原 `IconLocation` 为 `,0`。见 [原始发现](findings.md)。

早期尝试包括目标文件通知、Shell 缓存通知、`ie4uinit.exe -show` 和指定快捷方式图标路径。截图 42–50 保留，但后续发现恢复桌面后的远端焦点没有重新落到 PowerShell：截图 48 中没有出现新命令。**这些动作不能证明命令执行，也不能作为刷新无效的证据；没有证据证明快捷方式已被修改。**

重新点击终端确认焦点，执行下列已有 Windows API：

```powershell
[IconQA]::SHChangeNotify(0x08000000,0x2000,$null,$null)
```

`IconQA` 是本轮临时 PowerShell P/Invoke 类，调用 `shell32.dll` 的 `SHChangeNotify`。参数为 `SHCNE_ASSOCCHANGED`、`SHCNF_IDLIST | SHCNF_FLUSH`，两个项目指针为空。没有删除缓存文件、重启 Explorer、更改用户固定项或系统设置。

- [截图 51](screenshots/51-shell-notify-executed-raw.png)：命令实际出现在 PowerShell 历史中并返回提示符。
- [截图 52](screenshots/52-after-confirmed-shell-notify-raw.png)：桌面快捷方式变为绿色，当前任务栏仍旧蓝色。
- 正常 Alt+F4 退出并从现有快捷方式重开；[截图 53](screenshots/53-reopen-after-confirmed-shell-notify-raw.png) 和 [截图 54](screenshots/54-green-taskbar-after-reopen-raw.png)：任务栏绿色，窗口和应用内图标绿色，桌面图标已在前一步核对。

结论：在本升级环境中，Shell 缓存失效并正常重开可恢复图标一致性。单次通知不替换已经运行的任务栏图标。

源码修复：Windows 在创建首个窗口前执行同一通知，成功后写入单独的 `shell-icon-version` 标记；同版本启动不重复刷新，新版本再刷新，失败不写成功标记。覆盖安装器以及完整包/差分更新后启动，不更改快捷方式或文件关联。macOS 不执行该 Windows 操作。未打包、无版本的测试构建不刷新。

`TestShellIconRefreshAfterUpgrade` 和 `TestShellIconRefreshRetriesFailure` 修改前分别以调用 0 次、没有返回失败错误而失败；实现后通过，同时覆盖无版本构建不刷新。原生 API 效果在本桌面手动调用得到实证；新增启动钩子尚待修复构建在该桌面安装复测。

## 验证与材料

- 前端单元测试：270 通过；前端类型检查及生产构建通过。
- 侧栏浏览器回归通过：固定页脚、低高度滚动、键盘导航、动态选中态。
- hover 浏览器回归通过：图标、禁用更新按钮、四种 Button 的深浅色状态；启用 hover 保留。
- 原生控件浏览器回归通过；冒烟辅助测试 3 通过。
- Windows amd64 交叉 `go vet ./...` 通过。
- 原始结果审计：18 项计划全部有记录，证据存在，无缺失用例；审计仅检查记录完整性，不代替截图判读。

源码修复仍使用 0.4.1 版本号，此次没有发布新安装包。持续集成结果以修复提交对应的 GitHub Actions 为准。

结束状态：临时 PowerShell 已关闭，应用正常重开并显示系统状态；System 外观与 System 语言保留，云桌面连接保留。新增原始截图 42–54 同目录保存，原始报告截图没有覆盖。
