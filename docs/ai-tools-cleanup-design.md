# Cleanzo 新模块设计 · AI 编码工具清理（Claude Code + Codex）

> 版本：v0.1 设计稿 ｜ 日期：2026-10-16
> 依据：竞品调研报告（同日）、本机实测数据（同日）
> 定位一句话：**帮开发者看清 Claude Code / Codex 在磁盘上占了多少、能安全腾出哪些空间，只删确认没用的东西——会话历史先归档、可恢复，绝不静默删。**

---

## 一、为什么聚焦这两个工具

| 维度 | Claude Code | Codex CLI |
|---|---|---|
| 主流程度 | 事实标准，用户量最大 | OpenAI 官方，增长最快 |
| 官方自动清理 | 有（30 天默认过期 + purge） | **完全没有（只有逐会话 delete）** |
| 官方体积管理 | 建议缩短保留期 | **未表态，5GB 级只能手删** |
| 本机实测占用 | ~667 MB | ~5 GB |
| Cleanzo 增量价值 | 体积可见、按项目批量清理、不误删凭证 | **批量清理 + 归档可恢复，补官方缺位** |

结论：Claude Code 是"安全底线对标"，Codex 是"价值主战场"（官方能力最缺失、用户最痛）。

---

## 二、本机实测基线（2026-10-16，识别器设计的真实依据）

### ~/.codex 总计 ~5 GB

| 路径 | 大小 | 是什么 | 安全级别 |
|---|---|---|---|
| `packages/app-server-daemon/` | 2.2 GB | Codex 桌面端/后台守护运行时 | 🟠 待确认（运行时，不默认勾） |
| `sessions/{年份}/{月份}/rollout-*.jsonl` | 1.0 GB（27 个文件） | 会话历史（明文 jsonl） | 🔴 敏感（默认不勾，可归档后删） |
| `thread_history_1.sqlite` | 508 MB | 会话索引/线程历史库 | 🔴 敏感（与会话同源） |
| `plugins/cache/` | 228 MB | 插件缓存 | 🟢 可再生（默认勾选） |
| `generated_images/` | 125 MB | 生成的图片 | 🟡 可再生但可能想留（默认不勾，提示） |
| `sqlite/`（含 logs_2.sqlite 91MB） | 102 MB | 日志库/杂项库 | 🟢 日志可清（重启再生） |
| `computer-use/` | 73 MB | 屏幕操作记录/截图 | 🔴 敏感（默认不勾） |
| `logs_2.sqlite + -wal` | 72 MB | 日志数据库 | 🟢 安全可清（社区验证重启再生） |
| `state_5.sqlite` | 13 MB | 工具状态 | ⛔ 保护（绝不碰） |
| `cache/` | 12 MB | 缓存 | 🟢 可再生 |
| `vendor_imports/` | 7.8 MB | 依赖导入 | 🟠 待确认 |
| `..codex-global-state.json.tmp-*` | 17 个 / 2.8 MB | 写入残留临时文件 | 🟢 安全可清 |
| `config.toml` / `auth.json` | — | 配置与登录凭证 | ⛔ 保护（绝不碰） |
| `archived_sessions/` | 1.8 MB（13 个） | 用户主动归档的会话 | ⛔ 用户主动动作，不主动碰 |

### ~/.claude 总计 ~667 MB

| 路径 | 大小 | 是什么 | 安全级别 |
|---|---|---|---|
| `projects/<项目>/<session>.jsonl` | 486 MB（295 个，含 subagents/） | 会话历史（明文 jsonl） | 🔴 敏感（默认不勾，可归档后删） |
| `plugins/` | 146 MB | 用户安装的插件本体 | ⛔ 保护（非卸载场景不碰） |
| `file-history/` | 18 MB（377 个） | 文件修改前快照（回退用） | 🟡 删除失去回退能力（默认不勾） |
| `skills/` | 12 MB | 自定义技能 | ⛔ 保护 |
| `history.jsonl` | 820 KB | 敲过的 prompt 历史 | 🟡 可再生（默认不勾，可勾） |
| `debug/` `telemetry/` | ~1.7 MB | 日志与遥测 | 🟢 安全可清 |
| `paste-cache/` `shell-snapshots/` | ~1 MB | 缓存 | 🟢 安全可清 |
| `backups/` | 940 KB | 工具自身备份 | 🟠 提示用户自行决定 |
| `todos/` `statsig/` | 20 KB | 状态 | ⛔ 保护 |
| `.claude.json` / `CLAUDE.md` / `settings.json` | — | 配置 | ⛔ 保护（绝不碰） |

---

## 三、安全分级体系（全模块统一）

沿用 Cleanzo「只删确认没人在用的东西」哲学，借鉴竞品最佳实践（Mole 三档 + CleanMyDev 徽章 + agentrinse 未知即拒绝）：

| 级别 | 颜色 | 含义 | 默认状态 | 操作 |
|---|---|---|---|---|
| ⛔ 保护 | 灰/锁 | 配置、凭证、技能、插件本体、运行中工具、最近 24h 会话 | 不可勾选（UI 锁死） | 无 |
| 🔴 敏感 | 红 | 会话历史、线程索引、操作记录、文件快照 | 默认不勾选，需展开单独确认 | 先归档到废纸篓，再删除 |
| 🟡 可再生但可后悔 | 橙 | 生成的图片、prompt 历史、file-history | 默认不勾选，可勾选 | 移到废纸篓（可恢复） |
| 🟢 安全 | 绿 | 日志、缓存、临时残留 | 默认勾选 | 直接移到废纸篓 |
| 🟠 待确认 | 蓝 | 运行时目录（app-server-daemon）、vendor_imports | 默认不勾选，附说明 | 仅展示，用户自查 |

**铁律**：
1. 一律先移到废纸篓，绝不直接 unlink（用户可恢复）
2. 检测到 `codex` / `claude` 进程在运行 → 对应工具整页只读提示，不做任何删除
3. 删除前弹窗明确说明「删了会失去什么」（每条都有后果文案）
4. 无 `--force`、无"全部删除"一键按钮（参考 agentrinse）
5. 会话历史默认按保留窗口筛选（最近 7/30/90 天），最近 24h 一律排除

---

## 四、识别器设计

### 4.1 规则文件（可更新，不写死路径）

`ai-tools.json`（随版本发布，路径/格式变化时只改规则）：

```json
{
  "tools": [
    {
      "id": "claude-code",
      "name": "Claude Code",
      "home": "~/.claude",
      "processNames": ["claude"],
      "items": [
        { "path": "projects/", "kind": "session", "level": "sensitive", "note": {"zh": "项目会话历史", "en": "Session transcripts"} },
        { "path": "history.jsonl", "kind": "history", "level": "renewable", "note": {"zh": "输入过的 prompt 历史", "en": "Prompt history"} },
        { "path": "file-history/", "kind": "snapshot", "level": "optional", "note": {"zh": "文件修改快照，删了不能回退", "en": "File snapshots, losing undo"} },
        { "path": "debug/", "kind": "log", "level": "safe", "note": {"zh": "调试日志", "en": "Debug logs"} },
        { "path": "telemetry/", "kind": "log", "level": "safe" },
        { "path": "paste-cache/", "kind": "cache", "level": "safe" },
        { "path": "shell-snapshots/", "kind": "cache", "level": "safe" },
        { "path": "skills/", "kind": "protected", "level": "protected" },
        { "path": "plugins/", "kind": "protected", "level": "protected" }
      ],
      "protectedFiles": [".claude.json", "settings.json", "CLAUDE.md", "*.md"]
    },
    {
      "id": "codex",
      "name": "Codex CLI",
      "home": "~/.codex",
      "processNames": ["codex"],
      "items": [
        { "path": "sessions/", "kind": "session", "level": "sensitive" },
        { "path": "thread_history_1.sqlite", "kind": "session-index", "level": "sensitive" },
        { "path": "archived_sessions/", "kind": "protected", "level": "protected" },
        { "path": "logs_2.sqlite", "kind": "log", "level": "safe" },
        { "path": "logs_2.sqlite-wal", "kind": "log", "level": "safe" },
        { "path": "sqlite/", "kind": "log", "level": "safe" },
        { "path": "cache/", "kind": "cache", "level": "safe" },
        { "path": "plugins/cache/", "kind": "cache", "level": "safe" },
        { "path": "generated_images/", "kind": "media", "level": "optional" },
        { "path": "computer-use/", "kind": "recording", "level": "sensitive" },
        { "path": "packages/app-server-daemon/", "kind": "runtime", "level": "unknown" },
        { "path": "vendor_imports/", "kind": "runtime", "level": "unknown" },
        { "path": "state_5.sqlite", "kind": "protected", "level": "protected" }
      ],
      "tmpPattern": "..codex-*.tmp-*",
      "protectedFiles": ["config.toml", "auth.json"]
    }
  ]
}
```

### 4.2 扫描器行为（Go 实现）

1. 读取 `ai-tools.json`，展开 `~`，逐个路径 `du` 大小 + 文件计数 + 最新修改时间
2. 会话类（sensitive）：额外按 `projects/<project>`（Claude）或 `sessions/<年份>/<月份>`（Codex）分组，统计每组大小/条数/最新活动时间
3. 进程检测：`pgrep -x claude / codex`（或 `ps` 匹配），运行中 → 该工具置「只读」态
4. 临时残留：用 `tmpPattern` glob 匹配（如 `..codex-*.tmp-*`）
5. 扫描结果缓存 30 秒（Cleanzo 现有风格，不频繁全盘）

### 4.3 更新机制

- 规则文件随 Cleanzo 版本发布；检测到未知新目录（如 Codex 新版本新增 `xxx.sqlite`）→ 归入「🟠 待确认」展示，不自动归级
- 与 app 自动更新同通道（mygo updater 已就绪）

---

## 五、功能清单

### MVP（v1.0）

| 功能 | 说明 |
|---|---|
| AI 工具页（侧栏新入口） | 两张工具卡片：Claude Code / Codex，各显示总占用、可安全释放预估、最近活动 |
| 分类列表 | 每类：大小 / 数量 / 安全级别 / 后果说明；绿默认勾、黄红默认不勾 |
| 会话历史专项 | 按项目（Claude）/ 按月（Codex）分组；保留窗口筛选 7/30/90 天；最近 24h 排除；批量勾选 |
| 运行检测 | 工具进程运行中 → 只读提示 + 跳过删除 |
| 安全确认 | 弹窗逐条说明「删了会失去什么」，确认后才执行 |
| 删除执行 | 移到废纸篓（Finder 可恢复），完成后显示释放了多少 |
| 保护清单 | config/auth/skills/插件本体/最近会话 在 UI 上置灰锁定 |

### v1.1（快速跟进）

- **归档压缩**：Claude/Codex 会话 jsonl 无损瘦身（去重复字段、截断超大 tool 输出占位），实测可 -88% 且会话仍可恢复（借鉴 brtkwr 脚本 + llm-lens-web），这是差异化技术点
- **卸载残留检测**：检测 ~/.claude、~/.codex 是否属于"工具已卸载但目录残留"（对照应用/CLI 是否存在）
- 体积增长预警（可选设置：超过 N GB 提示一次）

### v2（后续）

- Gemini CLI（`~/.gemini`）——第三方空白点
- 过时 CLI 版本残留清理（Codex/Claude 旧构建，Mole 实测 3.5GB 里 2.6GB 是旧版本）
- 自动清理计划（**默认关闭**，用户显式开启后按窗口跑，仍先入废纸篓）

---

## 六、界面与交互（融入 Cleanzo 现有风格）

- 入口：侧栏新增「AI 工具」，位置在「系统状态」与「垃圾清理」之间
- 页面结构（对齐现有产品页设计语言：卡片 + 深浅色）：

```
┌ AI 工具 ──────────────────────────────┐
│ 帮你看清 Claude Code 和 Codex 占了   │
│ 多少空间，只清确认没用的。            │
│                                        │
│ [Claude Code 667 MB ▾]  [Codex 5 GB ▾]│
│  ┌ 会话历史 486 MB  🔴 默认不勾    ┐ │
│  │  ▸ 展开：按项目分组+勾选        │ │
│  ├ 日志 1.7 MB  🟢 已勾选          │ │
│  ├ 缓存 1 MB   🟢 已勾选          │ │
│  ├ file-history 18 MB 🟡          │ │
│  └ 技能/插件（锁，置灰）           │ │
│                                        │
│ [清理选中项（N 项 · 约 X GB）]        │
└────────────────────────────────────────┘
```

- 会话历史分组展开后：每项目/每月一行「大小 · 条数 · 最后活动 · 勾选框」，顶部有保留窗口筛选（最近 7/30/90 天）
- 删除确认弹窗：逐条「将删除 → 后果说明」，按钮「移到废纸篓」/「取消」
- 完成后 toast + 重新扫描显示新占用

---

## 七、风险与边界

1. **绝对不碰**：登录凭证（auth.json）、配置（config.toml、.claude.json）、用户技能/插件本体、正在运行的进程、最近 24h 活动会话
2. **thread_history_1.sqlite 与会话 jsonl 同删**：Codex 索引一致性——MVP 阶段两者都进废纸篓（同一时间点删除，若用户后悔可一并恢复）；v1.1 评估调用官方 `codex delete/archive` 保证索引（Mole 已验证此路径）
3. **app-server-daemon 2.2GB**：运行时目录，若 Codex 未安装桌面端仍存在（本机疑为旧版本残留）→ 归 🟠 待确认，用户自查后再决定
4. **明文存储警示**（顺带价值）：扫描时提示"会话历史为明文，可能含密钥/代码，删除仅本地生效"（对齐 agentscrub 的诚实边界）
5. 不做「一键全清」、不做自动静默清理——这是社区用 30 天静默删除事件换来的教训，也是 Cleanzo 的差异化护城河

---

## 八、待确认事项（动手前需要你拍板）

1. **会话历史默认是否勾选**：建议"默认不勾选、展开后手动勾"，你可改成"默认勾选 30 天前"（更激进但违背安全叙事，不推荐）
2. **保留窗口默认值**：建议默认 30 天窗口筛旧会话（Mole 教育过市场），可改 7/90
3. **app-server-daemon 处理**：先归 🟠 待确认展示，还是你确认它是旧版本残留后直接归 🟢？
4. **MVP 是否含归档压缩**（v1.1 项）：若你想首发就有技术亮点，可提进 MVP
