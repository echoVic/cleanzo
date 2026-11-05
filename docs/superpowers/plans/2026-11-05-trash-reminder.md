# 废纸篓提醒与独立清理弹窗

**Goal:** macOS 废纸篓超过阈值后，在独立弹窗确认并清理，不打开主界面。
**Architecture:** 独立配置与提醒时间文件；后台定时测量，仅扫描当前用户废纸篓。复用现有清理安全检查，原生 MyGo 窗口承载确认、进度、取消和结果。
**Tech Stack:** Go、MyGo v0.7.1、React 设置页。
**Spec:** 用户已确认本聊天方案，并要求直接在弹窗执行。

## Constraints

- 默认启用、1 GB，设置可关、可改为 1/2/5/10 GB；超过阈值，每 24 小时最多一次，重启不重置。
- 5 分钟检查一次，启动后延迟 1 分钟；用户正在扫描清理时推迟。
- 确认按钮明确为永久删除。取消停止后续项，已删除不可恢复。
- 仅当前用户 ~/.Trash；无权限不提醒清理，设置显示授权说明。
- 不并行清理、不重复弹窗，不删除提醒后新增或变化的项目。
- 关闭主窗口后继续检查；已有菜单栏和开机启动设置与此兼容。Windows 保持现有行为。

## Review Focus

- 冷却期跨重启、文件写入失败、时钟回拨。
- 用户取消、关闭窗口或退出期间的工作与异步回调。
- 弹窗之后新增文件、目录内新增文件、软链和权限失败。
- 垃圾清理正在执行时的互斥，以及配置关闭时不再弹窗。
- 中英文、深浅主题、确认与结果不把部分清理显示为全部成功。

## Tasks

- [x] 1. 添加配置/冷却/扫描快照/限定范围清理测试，运行观察 RED；实现后台监控与配置 API，验证 GREEN。
- [x] 2. 添加原生弹窗 Tester 测试，观察 RED；实现确认/进度/取消/结果、独立窗口和常驻接入，验证 GREEN。
- [x] 3. 添加设置控件测试，观察 RED；生成绑定并接入设置开关、阈值、说明和中英文本。
- [x] 4. Go 全量、针对性 race、MyGo vet、前端测试/构建，原生渲染检查并记录结果。

执行：本聊天内直接实现。当前仓库没有受版本管理的未提交改动，在现有检出工作；不提交、不发布。用户已授权实施，不再插入设计确认。

## 验证记录（2026-11-05）

- 已观察配置/监控 API、原生弹窗和设置控件缺失时的 RED。额外回归测试复现了「清理过程中向后续候选目录的深层子目录添加文件，该文件仍被删除」；现于每次实际删除之前再次遍历检查变化，回归测试通过。
- `CGO_ENABLED=0 go test ./... -count=1`：通过，202.877 秒；日志 `/tmp/cleanzo-trash-go-tests.log`。覆盖冷却期、设置及常驻、并发提醒、权限失败、取消、新增顶层及深层文件、软链根目录拒绝等。
- `go test -race ./... -run '^Test(TrashReminder|ResidentOn|SaveMenuBar|SaveGrowthConfig)' -count=1`：通过，3.060 秒；日志 `/tmp/cleanzo-trash-race.log`。
- `bun run generate`、`bun run typecheck`、`bun run build:web`：通过。构建仍有大于 500 kB 的 bundle 提示。
- `bun run test:web`：18 个测试文件共 271 项通过，0 失败；日志 `/tmp/cleanzo-trash-web-tests.log`。
- `go tool mygo vet .`：当前工具链未注册该 Go tool，改用仓库安装的等价 CLI `bunx --no-install mygo vet .`，通过；日志 `/tmp/cleanzo-trash-mygo-vet.log`。
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /tmp/cleanzo-trash-windows.exe .`：交叉构建通过；不代表 Windows 运行验证。
- `CLEANZO_TRASH_PREVIEWS=/tmp/cleanzo-trash-popup-previews CGO_ENABLED=0 go test ./... -run '^TestTrashReminderPopup' -count=1`：通过。生成中文/英文、浅色/深色的确认、清理、取消、部分完成、完成共 20 张原生测试渲染。人工查看中文浅色确认/进度、英文浅色确认和英文深色部分完成，未见截断或按钮溢出。
- 附加运行 `bun test tests/native-i18n.test.ts`：中英文原生键集合一致性通过；原生/网页原始英文译文相等检查失败。比较 HEAD 确认 14 处英文差异已存在（中文 0 处），涉及原有电池等文案，本次保留。该检查不在 `test:web` 中。
- `git diff --check`：通过。未对真实用户废纸篓执行删除；删除验证使用临时测试目录，弹窗验证为 MyGo Tester/离屏渲染，尚未验证打包后 macOS 真实窗口、权限和开机启动流程。

确认、清理、取消、结果全部由独立原生窗口承载，`showTrashReminder` 不调用 `openMainWindow` 或主窗口导航。关闭主窗口仍保留后台监控，退出应用停止。
