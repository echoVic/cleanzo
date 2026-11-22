# Claude Code 页的「运行中」和 agent worktree 移除 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Claude Code 页多一个「运行中」标签，列出开着的会话和已关闭会话留下的进程，可以手动结束；进程页把 Claude Code 合成一行并加跳转标签；Claude Code 页和 Codex 页可以把整个 agent worktree 移到废纸篓。

**Architecture:**
- **运行中。** 一张原生读出的快照（`kern.proc.all`、`kern.procargs2`、libproc、`~/.claude/sessions/*.json`）交给纯函数 `attributeClaude` 归属。细节（占用、文件夹、端口）只对要列出的进程读。结束只对单个 pid 发信号。读取都经 `claudeSource` 注入，规则和结束的测试不碰真进程。
- **worktree。** `aiTool.Worktrees` 让单个工具的扫描多出一项 `<tool>.worktrees`，一个 worktree 一个 `AIGroup`。清理走自己的 `removeWorktree`：改名、`git worktree remove`、改回、进废纸篓。
- **先落合约。** Task 0 落下导出类型、绑定方法的空实现、内部快照类型和 `src/mygo.ts`，其余任务从那个提交分四路并行。

**Tech Stack:** Go 1.27（purego、`golang.org/x/sys` 已有，不加依赖），React 19 + TypeScript，Bun，mygo 0.9.0。

**Spec:** `docs/superpowers/specs/2026-11-21-claude-running-and-worktrees-design.md`（中文）。动手前先读：证据、每条规则和裁定都在里面，下文的「§五」这类编号指它。计划和规格不一致时以规格为准并在报告里说明；计划比规格多定的事只有文末「计划补的决定」那几条。

## Global Constraints

- **工作区。** 只在自己的 git worktree 里做。先 `git reset --hard <BASE>`，BASE 由派发时给出。不 push，不切换或改动别的分支，不改主检出，不再派发子代理。
- **文件。** 只改本任务点名的文件和它们的测试。确实要动别处，改最小的一处并写进报告。并行的任务不进对方的函数。
- **合约。** Task 0 落下的类型、字段、函数签名不改名、不加减。`src/mygo.ts` 是生成的，只有 Task 0 和收尾的人重新生成。
- **平台。** 只做 macOS，Windows 上什么都不多出来，但要照常编译、测试通过。
  - 导出的声明和 `Cleaner` 的方法放不带平台标记的文件（`TestBindingsArePlatformNeutral`）。
  - 不带平台标记的 `.go` 文件里，带 `/` 的路径字面量要以 `// slash-ok: 原因` 结尾（`TestNoSlashPathLiterals`）。
  - 用例里有 macOS 路径的测试放 `_darwin_test.go`：Windows 的 CI 也跑测试。
- **不加依赖**，Go 和 npm 都不加。不运行要联网的命令。
- **进程的安全线**（§五、§七，数字照抄）：
  - 环境变量只取 `CLAUDECODE`、`CLAUDE_CODE_SESSION_ID`、`CLAUDE_PID`。缓冲区用完就丢，其余变量不保存、不返回、不写日志、不进错误信息。
  - 信号只发给单个 pid，永远不发给进程组（不出现负的 pid）。不碰 pid 0 和 1，不碰别的账户的进程。
  - `SIGTERM`，每 100 毫秒看一次、最多 2 秒；还活着的重新核对 pid 和启动时间，再 `SIGKILL`，最多再等 1 秒。
  - 空闲满 2 小时才计入提示；运行满 60 秒的命令才列在开着的会话下面；页面每 5 秒读一次；端口 `lsof` 3 秒为限，30 秒内同一组 pid 不重读；已关闭会话的标题只读对话记录末尾 2 MB；「跟着子进程」最多 5 轮。
- **git 的安全线**（§九）：
  - 只运行 `status`、`for-each-ref`、`worktree unlock`、`worktree remove`。不用 `--force`，不运行 `prune`、`gc`，不删分支。
  - 文件夹还在原路径上时，绝不运行 `git worktree remove`。实测（Apple Git 2.54.0）：仓库设了 `status.showUntrackedFiles=no` 时，不带 `--force` 的 `worktree remove` 会把带着没跟踪文件的 worktree 直接删掉。
  - 每条命令都去掉继承来的 `GIT_*`，加 `GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0`、`-c core.fsmonitor=false`，有时间限制：`status` 10 秒，其余 5 秒。
  - worktree 一律经 `trashWithContext` 进废纸篓，从不预先勾选。
- **测试不碰真的东西。** 不读真的主目录，不清真的文件，不给真的进程发信号（测试自己启动的子进程除外）。用现有的 `setHome`、`t.TempDir`、`writeFile`、`writeText`、`mustMkdir`、`fakeTool`、`aiFixture`、`installedGit`、`gitIn`、`gitCommit`。git 仓库在临时主目录里用真的 `git init` 建，没装 git 就跳过。
- **先写测试。** 每个勾选框是一组：先写这一组列的测试，跑一遍，看它因为所写的原因失败；再实现；再跑通；提交一次。单跑一组：`go test -run '<测试名的正则>' -count=1 .`，网页的是 `bun test tests/<文件>`。
- **文案。** 新文案都进 `src/i18n.ts`，中英文同样的键、同样的占位符，照文末「文案」表写。`tests/i18n-windows.test.ts` 没有豁免：文案里不能出现 `Dock`、`.app`、`Mac`、`活动监视器`、`~/Library`；`废纸篓`、`Trash`、`macOS` 会被词表换掉，可以用。各任务把自己的键作为一整块加在点名的锚点后面，并行的任务才不会撞。
- **隐私。** 这个仓库是公开的。代码、测试、mock、注释、提交信息里不出现任何一台机器上真的项目名、分支名、会话标题或路径，一律用编出来的名字。
- **注释。** Go 注释用英文，TS/TSX 跟着文件用中文。现在时，说代码做什么或为什么。不提计划、规格、评审，也不提代码以前是什么样。
- **提交。** 风格照 `git log`（`feat(ai): …`、`fix(process): …`、`test: …`），不带署名尾注。
- **报告前全部跑一遍：**
  - `test -z "$(gofmt -l .)"`、`go vet ./...`、`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`、`GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./...`
  - 自己动过的测试，然后 `go test -race -count=1 -skip '^TestNativeSysctlRawProbes$' ./...` 一次（约四分钟；机器上有别的代理在跑，计时类的失败先单独重跑一次再当真）
  - `bun install --frozen-lockfile`、`bun run typecheck`、`bun run test:web`、`bun run build:web`
- **报告。** 完整报告写到自己 worktree 里的 `.superpowers/sdd/2026-11-21-claude-running/task-N-report.md`，这个文件夹不提交。内容：每一组改了什么、先失败后通过的测试、跳过了什么和原因、验证输出。回复只写：状态（DONE / DONE_WITH_CONCERNS / BLOCKED / NEEDS_CONTEXT）、worktree 路径和分支、提交范围、一行测试结果、顾虑。

## Review Focus

规格没写、但用起来最容易撞上的五种情况，最可能的在前。每条的测试都已经写进所属的任务。

1. **很长或带换行的命令。** 包命令的那层 shell 把整段脚本放在一个参数里。页面上的命令应该是一行、不超过 300 个字符、没有控制字符。（Task 1）
2. **列表和点击之间会话变了。** 弹出确认框时还空闲，点确认时已经在干活，或者 `/clear` 换了会话 ID。应该不结束，结果写「已经变了」。（Task 3）
3. **带空格、中文、`#` 的仓库和 worktree 路径。** git 的每一步、改名、「在用」的比较都应该照常工作；git 给记录目录起的名字和文件夹名可以不一样。（Task 4）
4. **移走到一半被打断。** 改了名、还没改回时 Cleanzo 退出或崩溃。下一次扫描应该先把文件夹放回原名再判定，不留一个 git 认为丢了的 worktree。（Task 4）
5. **两个 Claude Code 进程是同一个会话 ID**（同一个会话被 `--resume` 了两份）。应该是两行、各按 pid 认；结束一个不动另一个。（Task 1、Task 3）

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `clauderun.go` | 新建 | 导出类型、阈值、`claudeSource`、四个绑定方法、读数和摘要的拼装 |
| `clauderun_rules.go` | 新建 | 快照类型，`attributeClaude`，记录校验，那层 shell 的识别，`maskCommand` |
| `clauderun_end.go` | 新建 | 结束进程和会话：核对、发信号、等待、结果 |
| `clauderun_darwin.go` | 新建 | 真的读取：进程表、参数和标记、输出文件、会话记录、端口、已关闭会话的标题 |
| `clauderun_other.go` | 新建 | `!darwin` 的空实现 |
| `native.go` | 改 | 注册 `proc_pidinfo`、`proc_pidfdinfo`；`nativeCwd`、`nativeFdPath`、`nativeFootprint`、`nativeProcName` |
| `process.go` | 改 | `processName` 的 `versions` 规则 |
| `worktrees.go` | 新建 | 找 worktree、判状态、移走 |
| `aitools.go` | 改 | `aiTool.Worktrees`、新字段、扫描多出一项、清理分流 |
| `src/mygo.ts` | 重新生成 | `bun run generate` |
| `src/react/ai/running.ts` | 新建 | 「运行中」的纯逻辑：数字、时长、目标、冻结、提示 |
| `src/react/ai/RunningPanel.tsx` | 新建 | 「运行中」标签的内容 |
| `src/react/ai/AIToolPage.tsx` | 改 | 页内标签、数字、「看运行中」；worktree 的选择、确认、失败提示 |
| `src/react/ai/PartRow.tsx`、`types.ts` | 改 | worktree 的分组行和能不能勾 |
| `src/react/ai/api.ts`、`entry.ts`、`mount.tsx` | 改 | 新绑定；「开在哪个标签」的请求 |
| `src/react/process/format.ts`、`ProcessPage.tsx`、`api.ts`、`entry.ts`、`mount.tsx` | 改 | 两种小标签和跳转 |
| `src/main.ts`、`src/shared.ts` | 改 | 跳到 Claude Code 页的「运行中」；三个失败代码的文案 |
| `src/i18n.ts`、`src/dev/mock-mygo.ts`、`package.json` | 改 | 文案；样例；`test:web` 加新测试文件 |
| `README.md`、`README.en.md`、`llms.txt`、`CHANGELOG.md` | 改 | 功能说明，写明思路来自 seshka（MIT） |

## Execution

| 波 | 任务 | 怎么跑 | 独占 |
|---|---|---|---|
| 1 | 0 合约 | 单独 | 全部导出类型和签名、`src/mygo.ts` |
| 2 | 1 归属规则 | A 路 | `clauderun_rules.go` |
| 2 | 4 worktree 后端 | B 路 | `worktrees.go`、`aitools.go` |
| 2 | 5 「运行中」标签 | C 路 | `src/react/ai/running.ts`、`RunningPanel.tsx`、`AIToolPage.tsx` 的页头 |
| 2 | 6 进程页的标签 | D 路 | `src/react/process/*`、`src/main.ts` |
| 3 | 2 读取、读数、摘要、名字 | A 路，Task 1 合入后 | `clauderun.go`、`clauderun_darwin.go`、`native.go`、`process.go` |
| 3 | 3 结束 | A 路，Task 1 合入后，和 Task 2 并行 | `clauderun_end.go` |
| 3 | 7 worktree 的行 | C 路，Task 5 合入后 | `PartRow.tsx`、`types.ts`、`AIToolPage.tsx` 的选择和确认 |
| 4 | 8 说明文档、合并、验证 | 收尾的人 | cherry-pick、`bun run generate`、真机只读核对 |

---

## Task 0: 合约

**Files:** 新建 `clauderun.go`、`clauderun_rules.go`、`clauderun_end.go`、`clauderun_darwin.go`、`clauderun_other.go`、`worktrees.go`、`clauderun_test.go`；改 `aitools.go`（只加字段）、`src/react/ai/entry.ts`、`src/dev/mock-mygo.ts`；重新生成 `src/mygo.ts`。

**Produces:** 下面的全部。这一任务不写任何行为：函数体返回零值，Task 1 到 7 各自填。

- [ ] **导出类型和阈值**，放 `clauderun.go`：

```go
// Thresholds of the Claude Code page's Running tab.
const (
	claudeIdleNote   = 2 * time.Hour    // an idle session counts in the badge and the process page tag from here
	claudeCommandAge = 60 * time.Second // an open session lists what it started once that ran this long
	claudeTermWait   = 2 * time.Second  // how long a process gets after SIGTERM
	claudeKillWait   = time.Second      // how long it gets after SIGKILL
	claudePoll       = 100 * time.Millisecond
	claudePortsEvery = 30 * time.Second // ports are read again after this long, or when the listed pids change
)

// ClaudeRunning is one reading of the Claude Code page's Running tab.
type ClaudeRunning struct {
	Sessions  []ClaudeSession `json:"sessions"`  // open sessions: idle longest first, working ones last
	Left      []ClaudeLeft    `json:"left"`      // closed sessions with processes still running, oldest first
	Uncertain int             `json:"uncertain"` // marked processes left out: their session may still be open
	NoRecords bool            `json:"noRecords"` // a Claude Code process shows no session id, so nothing is called left behind
	At        int64           `json:"at"`        // unix seconds
}

// ClaudeSession is one open Claude Code session.
type ClaudeSession struct {
	ID       string       `json:"id"`       // "" when its record cannot be read
	Pid      int32        `json:"pid"`      // its Claude Code process
	Start    int64        `json:"start"`    // that process's start, unix microseconds; with Pid, its identity
	Project  string       `json:"project"`  // folder name of Cwd
	Cwd      string       `json:"cwd"`
	Title    string       `json:"title"`
	Status   string       `json:"status"`   // "idle", "busy" or ""
	Since    int64        `json:"since"`    // when Status last changed, unix seconds; 0 when unknown
	Host     string       `json:"host"`     // the app it runs in; "" when unknown
	Entry    string       `json:"entry"`    // the record's entrypoint, "cli" for a terminal; "" when unknown
	Memory   uint64       `json:"memory"`   // its process, sub-agents and helpers
	Own      bool         `json:"own"`      // Cleanzo runs under it, so it cannot be ended here
	Commands []ClaudeProc `json:"commands"` // what it started that has run for a minute or longer, oldest first
}

// ClaudeLeft is a closed session and the processes it left running.
type ClaudeLeft struct {
	ID      string       `json:"id"`
	Project string       `json:"project"` // "" when unknown
	Title   string       `json:"title"`   // "" when unknown
	Procs   []ClaudeProc `json:"procs"`   // oldest first
}

// ClaudeProc is one listed process, with what is folded into its row.
type ClaudeProc struct {
	Pid      int32          `json:"pid"`
	Start    int64          `json:"start"`    // unix microseconds; with Pid, its identity
	Command  string         `json:"command"`  // for display: assignments and secret values removed
	Cwd      string         `json:"cwd"`
	Worktree string         `json:"worktree"` // the linked worktree Cwd is in; "" for none
	Ports    []int          `json:"ports"`    // listening TCP ports, ascending
	Memory   uint64         `json:"memory"`   // footprint, folded processes included
	Folded   []ClaudeTarget `json:"folded"`   // the processes folded into this row
}

// ClaudeTarget names a process to end by pid and start time.
type ClaudeTarget struct {
	Pid   int32 `json:"pid"`
	Start int64 `json:"start"` // unix microseconds
}

// ClaudeEnded is what ending did to one process.
type ClaudeEnded struct {
	Pid    int32  `json:"pid"`
	Result string `json:"result"` // "ended", "forced", "running" or "changed"
}

// ClaudeEndResult is what EndClaudeProcesses and EndClaudeSession return.
type ClaudeEndResult struct {
	Procs []ClaudeEnded `json:"procs"`
	Freed uint64        `json:"freed"` // footprint, read before ending, of the processes that are gone
}

// ClaudeRunSummary is what the tab's badge and the process page's tags show.
type ClaudeRunSummary struct {
	Idle int            `json:"idle"` // open sessions idle for claudeIdleNote or longer
	Left int            `json:"left"` // rows of processes closed sessions left behind
	Rows []ClaudeRowTag `json:"rows"` // per process page row, ordered by key
}

// ClaudeRowTag is the tag of one process page row.
type ClaudeRowTag struct {
	Key  string `json:"key"`  // ProcessRow.Key
	Idle int    `json:"idle"` // long-idle sessions whose Claude Code process is in the row
	Left int    `json:"left"` // processes in the row that closed sessions left behind
}
```

- [ ] **绑定方法**，放 `clauderun.go`。切片一律非 nil，JSON 里是 `[]`。

```go
func (Cleaner) ClaudeRunning(ctx context.Context) (ClaudeRunning, error)
func (Cleaner) ClaudeRunSummary() ClaudeRunSummary
func (Cleaner) EndClaudeProcesses(ctx context.Context, session string, targets []ClaudeTarget) (ClaudeEndResult, error)
func (Cleaner) EndClaudeSession(ctx context.Context, target ClaudeTarget, session, status string) (ClaudeEndResult, error)
```

`claudeRunSupported()` 为假时，三个带 `error` 的返回 `codeError{Code: "unsupported"}`，`ClaudeRunSummary` 返回空的摘要（`Rows` 是空切片）。否则转给 `claudeRunningWith`、`claudeSummaryWith`、`endClaudeProcsWith`、`endClaudeSessionWith`。读取来源经包一级的 `var claudeSourceFn = realClaudeSource` 取，测试可以换掉。`ClaudeRunSummary` 先看 `aiRootDir` 对 `claude` 这个工具是不是空：没装 Claude Code 也返回空的摘要，不调用 `claudeSourceFn`。

- [ ] **内部类型和签名。** `clauderun_rules.go`：

```go
// claudeProc is one process of a snapshot.
type claudeProc struct {
	Pid, Ppid, Pgid int32
	UID             uint32
	Start           int64     // unix microseconds
	Path            string    // the executable as proc_pidpath gives it; "" when unreadable
	Args            []string  // nil when unreadable
	Env             claudeEnv
	Out             string    // the session whose output file its stdout or stderr is; ""
}

// claudeEnv is all that is kept of a process's environment.
type claudeEnv struct {
	Seen    bool   // an environment could be read
	Marked  bool   // CLAUDECODE=1
	Session string // CLAUDE_CODE_SESSION_ID
	Pid     int32  // CLAUDE_PID; 0 when absent or not a number
}

// claudeRecord is ~/.claude/sessions/<pid>.json. Every field is optional.
type claudeRecord struct {
	SessionID, Cwd, Name, Status, ProcStart, Entrypoint string
	StatusUpdatedAt                                     int64 // milliseconds
}

// claudeSnapshot is everything the rules read, taken at one moment.
type claudeSnapshot struct {
	Procs   []claudeProc           // every account's, zombies left out; Path, Args, Env and Out only for UID's
	Records map[int32]claudeRecord // by the pid in the file name
	Apps    []workspaceApp
	Self    int32 // Cleanzo
	UID     uint32
}

// claudeRow is one listed process and what is folded into it.
type claudeRow struct {
	Lead   claudeProc
	Folded []claudeProc
}

type claudeOpen struct {
	Proc     claudeProc   // the Claude Code process
	Record   claudeRecord // zero when it has no valid one
	ID       string
	Members  []claudeProc // sub-agents and helpers, ended with the session
	Commands []claudeRow  // what it started, of any age
	Own      bool
}

type claudeLeftBehind struct {
	ID   string
	Rows []claudeRow
}

type claudeAttribution struct {
	Sessions  []claudeOpen
	Left      []claudeLeftBehind
	Uncertain int
	NoRecords bool
}

func attributeClaude(s claudeSnapshot) claudeAttribution
func claudeRecordValid(p claudeProc, r claudeRecord, uid uint32) bool
func claudeWrapperCommand(args []string) (inner string, ok bool)
func maskCommand(args []string) string
```

`clauderun.go`：

```go
// claudeSource is everything the Running tab reads and does, injectable for tests.
type claudeSource struct {
	snapshot    func() (claudeSnapshot, error)
	started     func(pid int32) (start int64, ok bool) // one process's start, unix microseconds; ok false when it is gone or a zombie
	footprint   func(pid int32) uint64
	cwd         func(pid int32) string
	identity    func(pid int32) (path, name string) // proc_pidpath and proc_name, as the process page reads them
	responsible func(pid int32) int32
	system      func(path string) bool
	ports       func(ctx context.Context, pids []int32) map[int32][]int // nil when they cannot be read
	closed      func(session string) (project, title string)            // a closed session, from its transcript
	signal      func(pid int32, sig syscall.Signal) error
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) error
}

func claudeRunningWith(ctx context.Context, src claudeSource) (ClaudeRunning, error)
func claudeSummaryWith(src claudeSource) ClaudeRunSummary
```

`clauderun_end.go`：

```go
func endClaudeProcsWith(ctx context.Context, src claudeSource, session string, targets []ClaudeTarget) (ClaudeEndResult, error)
func endClaudeSessionWith(ctx context.Context, src claudeSource, target ClaudeTarget, session, status string) (ClaudeEndResult, error)
```

`clauderun_darwin.go` 和 `clauderun_other.go`（`//go:build !darwin`）各有 `claudeRunSupported() bool`、`realClaudeSource() claudeSource`、`claudeWorkingFolders() worktreeUsage`。`clauderun_other.go` 的每个字段都是返回零值的函数，没有 nil，写法照 `process_other.go`。darwin 这边 Task 0 就接好六个现成的：`started`（`unix.SysctlKinfoProc("kern.proc.pid", pid)`，出错或 `P_stat == 5` 算不在）、`signal`（`syscall.Kill`）、`responsible`（`nativeResponsiblePid`）、`system`（`systemExecutable`）、`now`（`time.Now`）、`sleep`（`sleepCtx`）；其余六个返回零值，留给 Task 2。

- [ ] **worktree 的合约。** `aitools.go` 加字段：

```go
// in aiTool
Worktrees string // where its agents keep worktrees: "claude" (<repo>/.claude/worktrees/*), "codex" (<Home>/worktrees/*/*); "" for none

// in AIGroup
Repo    string `json:"repo,omitempty"`    // worktree rows: the repository's folder
Branch  string `json:"branch,omitempty"`  // worktree rows: the branch checked out; "" when detached
State   string `json:"state,omitempty"`   // worktree rows: one of the worktree states
Changes int    `json:"changes,omitempty"` // worktree rows in state "dirty": uncommitted entries

// in AIPart
Worktrees bool `json:"worktrees,omitempty"` // the part lists agent worktrees, one group each

// in AISelection
Seen []string `json:"seen,omitempty"` // worktree parts: each group's State as the user saw it, in the order of Groups
```

`worktrees.go`：

```go
// Worktree states. Only wtClean and wtDirty can be removed.
const (
	wtClean      = "clean"
	wtDirty      = "dirty"
	wtInUse      = "in-use"
	wtSubmodule  = "submodules"
	wtMidOp      = "mid-operation"
	wtUnbranched = "unbranched-commits"
	wtNoGit      = "git-unavailable"
	wtIndexOnly  = "index-only"
	wtUnknown    = "unrecognized"
)

// Outcomes of removing one worktree, besides success.
const (
	wtFailChanged = "wt-changed" // it is not what the user saw any more; nothing was changed
	wtFailGit     = "wt-git"     // git's record could not be cleared; nothing was changed
	wtFailTrash   = "wt-trash"   // git's record is cleared, the folder is still in place
)

// worktreeTemp is appended to a worktree's folder name while git clears its record.
const worktreeTemp = ".cleanzo-removing"

// agentWorktree is one worktree an agent made.
type agentWorktree struct {
	Path, Name    string
	Repo          string // the repository's folder
	Admin, Common string // git's record of it, and the repository's git folder
	Branch        string
	State         string
	Changes       int
	Size, Last    int64
}

// worktreeUsage is what tells a worktree is in use. OK is false when it could
// not be read, and every worktree then counts as in use.
type worktreeUsage struct {
	Cwds     []string // working folders of this user's processes
	Sessions []string // working folders of the open Claude Code sessions
	OK       bool
}

var worktreeUse = claudeWorkingFolders // tests replace it

// worktreePidStart is when a process started; ok is false when it is not running. Tests replace it.
var worktreePidStart = func(pid int32) (time.Time, bool) {
	start, ok := realClaudeSource().started(pid)
	return time.UnixMicro(start), ok
}

func findWorktrees(ctx context.Context, kind string) []agentWorktree
func removeWorktree(ctx context.Context, w agentWorktree, seen string, guard *usageGuard) (freed int64, code string)
```

- [ ] **前端的桩。** `src/react/ai/entry.ts` 加 `export type AITab = "space" | "running"` 和空的 `export function requestAITab(toolId: string, tab: AITab) {}`（Task 5 实现，Task 6 调用）。
- [ ] **生成和 mock。** `bun install --frozen-lockfile && bun run generate`。`src/dev/mock-mygo.ts` 里：`"Cleaner.ClaudeRunSummary"` 加在 `"Cleaner.WatchProcesses"` 之后，返回 `{ idle: 0, left: 0, rows: [] }`；`"Cleaner.ClaudeRunning"`、`"Cleaner.EndClaudeProcesses"`、`"Cleaner.EndClaudeSession"` 加在 `"Cleaner.CleanAITools"` 之后，返回空的读数和 `{ procs: [], freed: 0 }`。
- [ ] **测试**，`clauderun_test.go`（不带平台标记）：`TestClaudeRunningUnsupportedOffMacOS`，在 darwin 上跳过；别的平台上三个方法的错误是 `codeError{Code: "unsupported"}`，`ClaudeRunSummary()` 的 `Idle`、`Left` 是 0，`Rows` 是空切片而不是 nil。写法照 `TestWatchProcessesUnsupportedOffMacOS`。
- [ ] 全套检查通过。Go 的改动和重新生成的 `src/mygo.ts` 分两次提交。

---

## Task 1: 归属规则

**Files:** `clauderun_rules.go`；新建 `clauderun_darwin_test.go`。

**Interfaces:** Consumes Task 0 的类型。Produces `attributeClaude`、`claudeRecordValid`、`claudeWrapperCommand`、`maskCommand` 的行为，以及测试夹具（Task 2、3 复用）。

规则就是 §五 从「Claude Code 进程」到「合并成一行」。规格没定、这里定下的：

- **记录算数**：进程是本账户的，并且 `time.UnixMicro(p.Start).UTC().Format("Mon Jan _2 15:04:05 2006")` 和 `r.ProcStart` 各自把连续空白压成一个空格后相等。
- **Claude Code 进程**：本账户的进程，有算数的记录，或者 `filepath.Base(Args[0]) == "claude"`。上面没有别的 Claude Code 进程的，各算一个会话。
- **候选**去掉三类：Claude Code 进程；`Self`、它的后代和它到 pid 1 的每一个祖先；`Apps` 里 `Regular` 且 `Path` 不空的 App，以及 `Path` 在那个 App 文件夹里的进程。
- **父进程不在快照里**的，按「碰到别的东西」处理，不列出。
- **脱开又开着**的进程归哪一行：`Env.Pid` 指着的 Claude Code 进程所在的会话；否则会话 ID 相同的会话里启动最早的那个，再相同取 pid 小的。
- **`Own`**：这个会话的 Claude Code 进程是 `Self` 的祖先。
- **`NoRecords`**：有单算会话的 Claude Code 进程 `ID` 是空的。这时 `Left` 是空的，本该进去的都计入 `Uncertain`。
- **`Uncertain`** 只数「说不准」那一类，不数「链上碰到别的东西」的。
- **合并的顺序**：先并 App 的辅助进程（`outermostBundle(Path)` 相同、并且是那一行的后代），再并那层 shell。
- **那层 shell**：`Pid == Pgid`，`filepath.Base(Args[0])` 去掉开头的 `-` 后是 `zsh`、`bash` 或 `sh`，有一个参数里先出现 ` eval '`，后面出现 `' < /dev/null` 或 `' \< /dev/null`。里面的命令是两者之间的文字，`'\''` 还原成 `'`。它并进同一个进程组里启动最早的那一行；组里没有别的行时它自己是一行。这个文件不带平台标记，`/dev/null` 这样的字面量要带 `// slash-ok:`。
- **`maskCommand`**：
  1. 参数是那层 shell 的，换成里面的命令按空白切开；否则 `Args[0]` 只留文件名。
  2. 去掉开头的 `NAME=值`。值按 shell 的词算：可以用 `'…'` 或 `"…"` 括起来，否则到下一个空白为止。
  3. 以 `-` 开头、名字里有 `key`、`token`、`secret`、`password`（不分大小写）的参数：`-名字=值` 的值换成 `…`；没有 `=` 的，下一个参数换成 `…`。
  4. 拼成一行：连续的空白（含换行、制表符）变成一个空格，其余控制字符去掉，超过 300 个字符截断并以 `…` 结尾。

- [ ] **夹具**，写在 `clauderun_darwin_test.go` 开头：

```go
const testUID = 501

func ccProc(pid, ppid int32) claudeProc                        // Args {"claude"}, Pgid = pid, own uid, Env not readable
func recordOf(p claudeProc, session string) claudeRecord       // valid for p; Status "idle"
func marked(pid, ppid, pgid int32, session string) claudeProc  // Env readable, CLAUDECODE=1, that session
func hidden(pid, ppid, pgid int32) claudeProc                  // Env not readable
func plain(pid, ppid, pgid int32) claudeProc                   // Env readable, no marker
func wrapper(pid, ppid int32, inner string) claudeProc         // hidden, leads its own group, Args in the eval form around inner
func snap(records map[int32]claudeRecord, procs ...claudeProc) claudeSnapshot // adds launchd (1) and Cleanzo (900, under 1); UID testUID
```

每个夹具给进程一个各不相同、随 pid 递增的 `Start`。

- [ ] **挂在会话下面的进程。** 下表的 `cc` 是 `ccProc(100, 1)`，带 `S1` 的记录。

| 测试 | 给定 | 期望 |
|---|---|---|
| `TestClaudeCommandUnderItsSession` | `wrapper(110, 100, "npm run dev")`；`marked(111, 110, 110, "S1")` | `Sessions[0].Commands` 一行：`Lead` 111，`Folded` 是 110；`Left` 空 |
| `TestClaudeOldSessionIDStaysWithItsParent` | 同上，111 带的是 `"S0"` | 仍在 100 的 `Commands` 里 |
| `TestClaudeHelpersJoinTheSession` | `marked(101, 100, 100, "S1")`、`marked(102, 100, 100, "S1")`、`hidden(103, 100, 100)` | `Members` 是 101、102、103；`Commands` 空 |
| `TestClaudeSubAgentIsNotASession` | `ccProc(105, 100)` | 一个会话；105 在 `Members` 里 |
| `TestClaudeUnmarkedIsNeverListed` | `plain(160, 100, 100)`；同组但挂在 launchd 下的 `plain(161, 1, 100)` 和 `hidden(162, 1, 100)` | 哪里都没有它们：同一个进程组不算线索 |
| `TestClaudeCleanzoFamilyIsLeftOut` | `hidden(890, 100, 890)` 是 Cleanzo（900）的父进程；`marked(901, 900, 901, "S1")` | `Commands` 空；`Sessions[0].Own` 为真 |

- [ ] **脱开的进程。**

| 测试 | 给定 | 期望 |
|---|---|---|
| `TestClaudeDetachedOpenByID` | `cc`；`marked(120, 1, 120, "S1")` | 在 100 的 `Commands` 里 |
| `TestClaudeDetachedOpenByClaudePid` | `cc`；120 带 `"S0"`，`Env.Pid` 是 100 | 在 100 的 `Commands` 里 |
| `TestClaudeDetachedClosed` | `cc`；`marked(120, 1, 120, "S9")` | `Left` 是 `{ID: "S9"}`，一行，`Lead` 120 |
| `TestClaudeOutputFileClue` | `cc`；`hidden(130, 1, 130)`，`Out` 是 `"S9"` | `Left` 里 `S9` 一行 |
| `TestClaudeHiddenParentFollowsChild` | `cc`；`hidden(140, 1, 140)`（不是那层 shell）；`marked(141, 140, 141, "S9")` | `Left` 里 `S9` 两行：140、141 |
| `TestClaudeHiddenChildFollowsParent` | `cc`；`marked(150, 1, 150, "S9")`；`hidden(151, 150, 151)` | `Left` 里 `S9` 两行：150、151 |
| `TestClaudeFollowsChildrenFiveRoundsOnly` | `cc`；`marked(149, 148, 149, "S9")`，上面一串 `hidden`，最上面那个挂在 launchd 下 | 上面 5 层时 `Left` 里 `S9` 六行；上面 6 层时一行都不列出 |
| `TestClaudeWrapperAloneIsARow` | `cc`；`wrapper(170, 1, "npm run dev")`，`Out` 是 `"S9"` | `Left` 里一行，`Lead` 170 |
| `TestClaudeAppHelpersFoldIntoTheApp` | `cc`；`marked(180, 1, 180, "S9")` 的 `Path` 在一个不在 `Apps` 里的 `.app` 的 `Contents/MacOS` 下；`marked(181, 180, 180, "S9")` 的 `Path` 在同一个 `.app` 的 `Contents/Frameworks` 下 | 一行：`Lead` 180，`Folded` 是 181 |

- [ ] **不列出和说不准。**

| 测试 | 给定 | 期望 |
|---|---|---|
| `TestClaudeDockAppIsNeverListed` | `Apps` 里一个 `Regular` 的 App（pid 200）；`marked(200, 1, 200, "S1")` 的 `Path` 在它里面；它的辅助进程 201；201 下面的 `marked(202, 201, 202, "S1")` 和 202 下面的 203 | 有 `cc` 时四个都不在任何地方；去掉 `cc` 再算一次，`Left` 空，`Uncertain` 是 0 |
| `TestClaudeChainThroughAnotherSession` | `cc`；`marked(181, 1, 181, "S2")`；`marked(180, 181, 180, "S1")` | 180 不列出；`Left` 里 `S2` 一行（181） |
| `TestClaudeMissingParent` | `marked(190, 999, 190, "S9")`，快照里没有 999 | 不列出，`Uncertain` 是 0 |
| `TestClaudeNoRecordMeansNothingIsLeftBehind` | `ccProc(100, 1)` 没有记录；`marked(120, 1, 120, "S9")` | `NoRecords` 为真，`Left` 空，`Uncertain` 是 1 |
| `TestClaudePidTakenByAnOlderProcess` | `cc`；120 带 `"S9"`、`Env.Pid` 300；`plain(300, 1, 300)` 的 `Start` 比 120 早 | `Uncertain` 是 1，`Left` 空。把 300 的 `Start` 改成比 120 晚：`Left` 里 `S9` 一行 |

- [ ] **记录和同一个会话 ID。**

| 测试 | 给定 | 期望 |
|---|---|---|
| `TestClaudeRecordMustMatchProcStart` | 记录的 `ProcStart` 是另一个时刻 | `claudeRecordValid` 为假；进程仍是 Claude Code 进程，`ID` 空，`NoRecords` 为真 |
| `TestClaudeRecordSqueezesSpaces` | 启动在某月 4 日，`ProcStart` 写成 `"Wed Nov  4 09:12:33 2026"` | 算数 |
| `TestClaudeRecordWithoutClaudeArgv` | `Args` 是 `{"2.1.321"}`，有算数的记录 | 是 Claude Code 进程 |
| `TestClaudeRecordFieldsAreOptional` | 记录只有 `SessionID` 和 `ProcStart` | 会话列出，`Record.Status` 空 |
| `TestClaudeOtherAccountIsNotASession` | `UID` 是 0、`Args` 是 `{"claude"}` 的进程 | `Sessions` 空 |
| `TestClaudeTwoProcessesOneSession` | `ccProc(100, 1)`、`ccProc(101, 1)`，记录都是 `S1`；`marked(120, 1, 120, "S1")` | 两个会话；120 在 100（启动早的）的 `Commands` 里。120 的 `Env.Pid` 设成 101：在 101 下面 |

- [ ] **命令的显示。**

| 测试 | 给定 | 期望 |
|---|---|---|
| `TestClaudeWrapperCommand` | 表下面代码块里的参数 | `npm run dev -- --host '0.0.0.0'`，`ok` 为真；把 `' < /dev/null` 换成 `' \< /dev/null` 结果一样；`{"/bin/zsh", "-l"}` 的 `ok` 为假 |
| `TestMaskCommandKeepsThePlainCommand` | `{"/opt/homebrew/bin/node", "/p/node_modules/.bin/vite", "--port", "5173"}` | `node /p/node_modules/.bin/vite --port 5173` |
| `TestMaskCommandHidesSecrets` | `{"curl", "--api-key=hunter2", "--Token", "abc", "-password=x", "https://example.invalid"}` | `curl --api-key=… --Token … -password=… https://example.invalid` |
| `TestMaskCommandDropsLeadingAssignments` | 那层 shell，里面是 `API_KEY="a b" TOKEN='c d' node x.js` | `node x.js` |
| `TestMaskCommandIsOneShortLine` | 一个 5000 个字符的参数；一个带 `\n`、`\t` 和 `\x1b` 的参数 | 不超过 300 个字符，以 `…` 结尾；没有控制字符，空白都是单个空格 |

```go
// TestClaudeWrapperCommand
[]string{"/bin/zsh", "-c", "-l", `source /x/snap.sh && eval 'npm run dev -- --host '\''0.0.0.0'\''' < /dev/null && pwd -P >| /x/cwd`}
```

---

## Task 2: 读取、读数、摘要、进程页的名字

**Files:** `clauderun_darwin.go`、`clauderun.go`、`native.go`、`process.go`；新建 `clauderun_native_darwin_test.go`、`clauderun_view_darwin_test.go`；改 `process_darwin_test.go`。

**Interfaces:** Consumes `attributeClaude`、`maskCommand`、Task 1 的夹具。Produces `realClaudeSource()` 剩下的六个字段、`claudeRunningWith`、`claudeSummaryWith`、`claudeWorkingFolders`。

- [ ] **libproc 的四个读取**，`native.go`。大小和偏移是在这台机器的 SDK 头文件上用 `sizeof`、`offsetof` 量的：

| 函数 | 调用 | 取什么 |
|---|---|---|
| `nativeCwd(pid int32) string` | `proc_pidinfo(pid, 9, 0, buf, 2352)`，返回值必须是 2352 | 偏移 152 起、以 0 结尾、最长 1024 的路径 |
| `nativeFdPath(pid, fd int32) string` | `proc_pidfdinfo(pid, fd, 2, buf, 1200)`，返回值必须是 1200 | 偏移 176 起的路径；管道、套接字返回空 |
| `nativeFootprint(pid int32) uint64` | 现有的 `proc_pid_rusage` | `riFootprint` |
| `nativeProcName(pid int32) string` | 现有的 `proc_name` | 和 `nativeProcsKnown` 读名字的写法一样 |

  测试 `TestNativeCwdAndFdPath`：对测试进程自己，`nativeCwd` 等于 `os.Getwd()` 的 `resolvePath`；打开一个临时文件，`nativeFdPath` 等于它的 `resolvePath`；`os.Pipe()` 的一端返回空；pid 1 两个都返回空，不崩。

- [ ] **参数和标记**，`parseProcargs2(buf []byte) (args []string, env claudeEnv)`：缓冲区依次是 `argc`（小端 int32）、可执行文件路径、若干个 0、`argc` 个参数、环境变量，以空串结束。只认三个变量；读到至少一条环境变量 `Seen` 才为真；`CLAUDECODE` 的值必须是 `1`；`CLAUDE_PID` 不是数字算 0。
  测试 `TestParseProcargs2`：拼一段缓冲区，参数 `node`、`server.js`，环境 `PATH=/bin`、`SECRET_TOKEN=hunter2`、`CLAUDECODE=1`、`CLAUDE_CODE_SESSION_ID=S1`、`CLAUDE_PID=4242`。期望参数两个、`env` 是 `{true, true, "S1", 4242}`，并且 `fmt.Sprintf("%+v", …)` 两个返回值里搜不到 `hunter2`、`SECRET_TOKEN`、`PATH`。没有环境变量时 `Seen` 为假。长度不足 4、`argc` 是负数或大得离谱的缓冲区返回 `nil` 和零值，不崩。

- [ ] **输出文件**，`claudeOutputSession(path string) string`，放 darwin 文件：路径以 `/claude-<数字>/<一段>/<uuid>/tasks/<一段>` 结尾时返回那个 uuid（8-4-4-4-12 的小写十六进制），否则空。
  测试 `TestClaudeOutputSession`：`/private/tmp/claude-501/-Users-me-app/11111111-2222-3333-4444-555555555555/tasks/b1.output` 得到 uuid；少一段、uuid 不合格、不在 `tasks` 下的都返回空。

- [ ] **会话记录**，`readClaudeRecords(dir string) map[int32]claudeRecord`：读 `dir` 里的 `<pid>.json`，每个文件最多读 64 KB，按 `map[string]any` 取字段，类型不对的字段当没有；`statusUpdatedAt` 是数字或数字写成的字符串都认。
  测试 `TestReadClaudeRecords`（临时主目录）：`100.json` 完整；`abc.json` 不读；`101.json` 是截断的 JSON，结果里没有 101；`102.json` 的 `statusUpdatedAt` 是 `"1700000000000"`，读成 1700000000000。

- [ ] **快照**，`realClaudeSource().snapshot`：
  1. `unix.SysctlKinfoProcSlice("kern.proc.all")`：`Proc.P_pid`、`Eproc.Ppid`、`Eproc.Pgid`、`Eproc.Ucred.Uid`、`Proc.P_starttime`（秒乘一百万加微秒）。`Proc.P_stat == 5` 的是僵尸进程，不进快照。
  2. 本账户的每个进程：`unix.SysctlRaw("kern.procargs2", pid)` 交给 `parseProcargs2`；`Path` 用 `nativePidPath`；`Env.Seen` 为假的，`Out` 取 `claudeOutputSession(nativeFdPath(pid, 1))`，空的话再看 fd 2。
  3. `Records` 读 `~/.claude/sessions`；`Apps` 用 `workspaceList()`；`Self` 是 `os.Getpid()`；`UID` 是 `os.Getuid()`。
  测试 `TestClaudeSnapshotSeesThisProcess`：快照里有测试进程自己，`Ppid` 等于 `os.Getppid()`，`Args[0]` 是测试二进制，`Path` 不空。测试再把测试二进制自己当子进程启动一次（`-test.run` 一个只睡眠的辅助测试，写法照 `TestProcessPsHelper`），环境里带 `CLAUDECODE=1`、`CLAUDE_CODE_SESSION_ID=S-test`：快照里它的 `Env` 是 `{true, true, "S-test", 0}`，`Ppid` 是测试进程。结束时杀掉这个子进程。

- [ ] **端口**，`parseLsofListen(out []byte) map[int32][]int` 和 `ports`：`spawnOutput` 运行 `/usr/sbin/lsof -nP -iTCP -sTCP:LISTEN -a -p <逗号分隔的 pid> -Fpn`，环境加 `LC_ALL=en_US.UTF-8`，3 秒为限。`p` 开头的行是 pid，`n` 开头的行取最后一个 `:` 后面的数字，每个 pid 的端口去重、升序。没有匹配时 lsof 以 1 退出：输出是空的就返回空表；超时或起不来返回 `nil`。
  测试 `TestParseLsofListen`：`"p123\nn*:5173\nn127.0.0.1:3000\nn*:5173\np456\nn[::1]:8000\n"` 得到 `{123: [3000, 5173], 456: [8000]}`；空输入得到空表。

- [ ] **已关闭会话的项目和标题**，`closed`：在 `~/.claude/projects/*/<会话 ID>.jsonl` 里找文件；项目是 `jsonlField(path, "cwd")` 的文件夹名；标题是文件末尾 2 MB 里最后一条 `type` 为 `ai-title` 的行的 `aiTitle`。会话 ID 不是 uuid 的样子就什么都不读。
  测试 `TestClosedSessionProjectAndTitle`：三条 `ai-title` 取最后一条；标题行在末尾 2 MB 之前的文件，标题为空、项目照常；找不到文件两个都空；会话 ID 是 `../x` 时不读任何文件。

- [ ] **读数**，`claudeRunningWith`。Task 1 给出归属，这里补细节：
  - 会话的顺序：`Status` 是 `idle` 的按 `Since` 从早到晚，然后是状态未知的，`busy` 的最后；同一档按 pid。
  - `Status` 只认 `idle` 和 `busy`，别的值当空。`Since` 是 `StatusUpdatedAt / 1000`，比现在晚就取现在。
  - `Project` 是记录里 `Cwd` 的文件夹名；没有记录就用 `src.cwd(pid)`。`Title` 是记录的 `Name`，`Entry` 是 `Entrypoint`。
  - `Host`：`src.responsible(pid)` 在 `Apps` 里按 pid 对上的那个 App 的 `Name`；对不上时看负责进程的 `Path` 在哪个 App 的文件夹里；都没有就空。
  - `Memory`：会话是 Claude Code 进程加 `Members`；一行是 `Lead` 加 `Folded`。
  - `Commands` 只留 `now - Start >= claudeCommandAge` 的，启动早的在前。`Left` 按每组最早的进程从早到晚。
  - `Worktree`：从 `Cwd` 往上最多 8 层，找到 `.git` 是普通文件、并且 `gitDirOf` 给出的路径的上一级叫 `worktrees` 的那个文件夹，取它的名字。
  - 端口一次读完所有列出的 pid（含并进去的），记在包一级、带锁的缓存里：pid 的集合没变并且距上次不满 `claudePortsEvery` 就不重读。`ports` 返回 `nil` 时各行没有端口，不报错。
  - 快照读失败返回 `codeError{Code: "unreadable"}`。

| 测试（夹具用一个假的 `claudeSource`） | 期望 |
|---|---|
| `TestClaudeRunningOrdersSessions` | 空闲 7 天、空闲 1 小时、状态未知、在干活，按这个顺序 |
| `TestClaudeRunningListsCommandsAfterAMinute` | 启动 59 秒的命令不在 `Commands` 里，60 秒的在 |
| `TestClaudeRunningRowDetails` | 一行的 `Memory` 是两个进程占用之和；`Ports`、`Cwd`、`Worktree`、`Folded` 都对；参数里有 `--api-key=hunter2` 时，整个读数的 JSON 里搜不到 `hunter2` |
| `TestClaudeRunningReadsPortsOncePerPidSet` | 同一组 pid 连读两次，`ports` 只被调用一次；多了一个 pid 再调用一次；时钟往后 30 秒再调用一次；`ports` 返回 `nil` 时不报错 |
| `TestClaudeRunningClosedSession` | `Left[0].Project`、`Title` 来自 `src.closed` |
| `TestClaudeRunningClampsFutureStatusTime` | `StatusUpdatedAt` 比现在晚一小时，`Since` 等于现在 |
| `TestClaudeRunningSnapshotFailure` | 错误是 `codeError{Code: "unreadable"}` |

- [ ] **摘要**，`claudeSummaryWith`：不读端口、不读标题。`Idle` 是 `Status` 为 `idle` 且 `now - Since >= claudeIdleNote` 的会话数；`Left` 是残留的行数。`Rows`：每个长时间空闲的会话的 Claude Code 进程、每个残留进程（含并进去的），用 `src.identity` 读路径和名字，拼成 `procReading`，交给 `ownProcessKey`（`cur` 里放这些进程和它们的负责进程），按 key 计数、排序。
  测试 `TestClaudeSummary`：两个空闲 3 小时、一个空闲 1 小时的会话，路径都在 `…/claude/versions/2.1.321`；两行残留是 `node`，一行在某个 `.app` 里带一个并进去的辅助进程。期望 `Idle` 2、`Left` 3，`Rows` 是 `app:…` 那一行 `Left` 2、`cli:claude` 的 `Idle` 2、`cli:node` 的 `Left` 2。
  测试 `TestClaudeSummarySkipsWithoutClaudeCode`：临时主目录里没有 `.claude`，`claudeSourceFn` 换成一调用就让测试失败的函数；`Cleaner{}.ClaudeRunSummary()` 的 `Idle`、`Left` 是 0，`Rows` 是空的。

- [ ] **`claudeWorkingFolders`**：`Cwds` 是本账户每个进程的 `nativeCwd`（空的不要），`Sessions` 是每条算数的记录的 `Cwd`；进程表读不出来时 `OK` 为假。

- [ ] **进程页的名字**，`process.go` 的 `processName`：可执行文件在名叫 `versions` 的文件夹里、文件名等于内核短名字时，名字取 `versions` 上一级文件夹的名字，去掉开头的点；那一级没有名字就照旧。
  `TestGroupProcessesUsesKernelNames` 加两行，原有三行不动：

```go
{"versioned CLI", "/Users/me/.local/share/claude/versions/2.1.321", "2.1.321", "cli:claude", "claude", "claude"},
{"versioned CLI in a dot folder", "/Users/me/.claude/versions/2.1.304", "2.1.304", "cli:claude", "claude", "claude"},
```

- [ ] **真机用的两个测试**，默认跳过，收尾的人在沙箱外跑：
  - `TestClaudeRunningOnThisMac`，要 `CLEANZO_PROBE=1`：读一次真的读数，只打印个数（会话、空闲满 2 小时的、残留的行、说不准的、`NoRecords`）和每个会话的状态与空闲时长。不打印命令、路径、标题。
  - `TestClaudeRunningBudget`，要 `CLEANZO_PERF=1`：60 秒里每 5 秒读一次，用 `rusageCPU` 量，超过一个核的 3% 就失败。写法照 `TestProcessSamplingBudget`。

---

## Task 3: 结束

**Files:** `clauderun_end.go`；新建 `clauderun_end_darwin_test.go`。

**Interfaces:** Consumes `attributeClaude`、Task 1 的夹具、`claudeSource` 的 `snapshot`、`started`、`footprint`、`signal`、`sleep`。Produces 两个结束函数的行为。

两个函数都先读一张新的快照、重新归属，再动手（§七）。

- **`endClaudeProcsWith`**：每个目标必须 pid 大于 1、是本账户的、`Start` 没变，并且是 `session` 这个会话下某一行的 `Lead` 或 `Folded`。「下」指开着的会话的 `Commands`（不论运行了多久），或者 `Left` 里 `ID` 相同的那一组。不满足的结果是 `changed`，不发信号。
- **`endClaudeSessionWith`**：目标必须是一个单算会话的 Claude Code 进程，`Start` 没变，`ID` 等于 `session`；`status` 是 `idle` 时，记录现在的 `Status` 也必须是 `idle`。不满足时结果只有这一个 pid，写 `changed`。`Own` 的会话返回 `codeError{Code: "blocked"}`。通过以后，要结束的是这个进程加它的 `Members`，`Commands` 不动。
- **动手**：先读每个目标的占用；逐个 `SIGTERM`（返回 `ESRCH` 直接算 `ended`）；每 `claudePoll` 用 `src.started` 看一次，最多 `claudeTermWait`；还在的重新核对 `Start`，没变才 `SIGKILL`，再等最多 `claudeKillWait`。结果按目标的顺序：`ended`、`forced`、`running`、`changed`。`Freed` 只加 `ended` 和 `forced` 的。`ctx` 取消时不再等，还在的写 `running`。

- [ ] **夹具**：一个假的 `claudeSource`，进程表可以写「收到 `SIGTERM` 就退出」「只有 `SIGKILL` 才退出」「都不退出」「等待期间 pid 被顶替」；`sleep` 只拨假时钟；`signal` 记下每一次调用。每个测试最后断言：没有一次调用的 pid 是负数、0 或 1。

- [ ] **结束进程。**

| 测试 | 期望 |
|---|---|
| `TestEndClaudeProcsEndsWhatIsListed` | `S9` 留下的一行（一个 `Lead`、一个 `Folded`）：两个 pid 各收到一次 `SIGTERM`，结果都是 `ended`，`Freed` 是动手前读到的两个占用之和 |
| `TestEndClaudeProcsEndsACommandOfAnOpenSession` | 开着的会话下一个只运行了 10 秒的命令也能结束 |
| `TestEndClaudeProcsSkipsWhatChanged` | 六种目标都是 `changed` 且没收到信号：`Start` 不同；已经不在列表里；列在另一个会话下；是会话的辅助进程；pid 是 1；是别的账户的 |
| `TestEndClaudeProcsEscalates` | 不理 `SIGTERM` 的：假时钟走满 2 秒后收到 `SIGKILL`，结果 `forced`；连 `SIGKILL` 也不退出的结果 `running`，不计入 `Freed`；从头到尾假时钟不超过 3 秒 |
| `TestEndClaudeProcsRechecksBeforeKill` | 等待期间进程退出、pid 被新进程顶替：不发 `SIGKILL`，结果 `ended` |
| `TestEndClaudeProcsAlreadyGone` | `signal` 返回 `ESRCH`：`ended` |
| `TestEndClaudeProcsStopsWaitingWhenCanceled` | `ctx` 在等待中取消：马上返回，还在的写 `running` |

- [ ] **结束会话。**

| 测试 | 期望 |
|---|---|
| `TestEndClaudeSessionLeavesItsCommands` | 会话 100 有 `Members` 101、102 和一条命令 111：信号只发给 100、101、102 |
| `TestEndClaudeSessionRefusesItsOwn` | `Own` 的会话：`codeError{Code: "blocked"}`，没有信号 |
| `TestEndClaudeSessionThatChanged` | 记录里的会话 ID 变成了 `S2`：`changed`，没有信号。看到时是 `idle`、现在是 `busy`：`changed`。看到时是 `busy`、现在是 `idle`：照常结束。看到时是空：照常结束 |
| `TestEndClaudeSessionSharingAnID` | 100 和 101 都是 `S1`：结束 100，101 和它的 `Members` 没收到信号 |
| `TestEndClaudeSessionNotASession` | 目标是子 agent，或者不是 Claude Code 进程：`changed` |

---

## Task 4: worktree 后端

**Files:** `worktrees.go`、`aitools.go`；新建 `worktrees_darwin_test.go`；改 `aitools_darwin_test.go`。

**Interfaces:** Consumes Task 0 的 worktree 合约和 `worktreeUse`（测试里换掉）。Produces `findWorktrees`、`removeWorktree`、扫描和清理的接入。

**git 怎么调。** 一个可以在测试里换掉的入口，子命令只有四个，写法在临时仓库里试过（带空格和中文的路径也试过）：

```go
// worktreeGit runs one git command for the worktree rules. Tests replace it.
var worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error)
```

| 用途 | 参数 |
|---|---|
| 状态 | `-C <worktree> -c core.fsmonitor=false status --porcelain=v1 --untracked-files=normal`，10 秒 |
| HEAD 在不在某个分支上 | `--git-dir=<公共目录> -c core.fsmonitor=false for-each-ref --contains <sha> --count=1 refs/heads refs/tags refs/remotes`，5 秒 |
| 解锁 | `--git-dir=<公共目录> worktree unlock <原路径>`，5 秒 |
| 清记录 | `--git-dir=<公共目录> worktree remove <原路径>`，5 秒 |

环境照 `gitCheckIgnore`：去掉 `GIT_*`，加 `GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0`，`GIT_CEILING_DIRECTORIES` 设成 worktree 的上一级。git 用 `findTool("git")` 找；找不到，或者是 `/usr/bin/git` 而 `developerToolsInstalled` 为假，都算没有 git。

**什么算一个 worktree**（§九）。文件夹里的 `.git` 是普通文件，内容 `gitdir: P`，`P` 是相对路径时相对这个文件夹；`P` 的上一级叫 `worktrees`，`P` 是个文件夹（记录目录，名字是 git 起的，不一定等于文件夹名）；记录目录里 `gitdir` 文件的内容（相对路径时相对记录目录）和 `<文件夹>/.git` 过 `resolvePath` 后相等；`commondir` 文件的内容（相对路径时相对记录目录）是公共目录。`Repo` 是公共目录的上一级，公共目录不叫 `.git` 时就是它自己。不满足的 `State` 是 `wtUnknown`。`safeToClean` 不放行的文件夹不列出。

**状态**按 §九 的表判，命中就停。规格没定、这里定下的：

- 「在里面」的比较：两边都过 `resolvePath` 再转小写，用 `within`。
- `worktreeUse().OK` 为假：`wtInUse`。
- `locked`：内容匹配 `\(pid (\d+)\)` 时，用 `worktreePidStart` 看那个 pid；活着并且启动时间早于 `locked` 的修改时间才算在用，否则是过期的锁。不匹配：在用。
- 记录目录的 `HEAD` 不以 `ref: refs/heads/` 开头时，内容必须是 40 或 64 位十六进制，否则 `wtNoGit`。`for-each-ref` 出错也是 `wtNoGit`。
- `status` 的输出按行看：有一行第一列不是空格、`?`、`!`，并且第二列不是空格，是 `wtIndexOnly`；否则有任何一行是 `wtDirty`，`Changes` 是行数；没有输出是 `wtClean`。
- `Branch` 取自 `HEAD` 里 `refs/heads/` 之后的部分。`Last` 用 `lastChange`，读不出来是 0。`Size` 用 `measureAI`。
- 各个 worktree 的 git 调用并行，并行数 `scanConcurrency`。

**去哪里找。**

- `claude`：三处的并集，同一个文件夹只算一次。
  1. `~/.claude/projects` 里每个项目文件夹，取第一份对话记录的 `jsonlField(…, "cwd")`。
  2. `worktreeUse().Sessions`。
  3. 现有主目录遍历走到的每个 `.claude/worktrees`。遍历的深度和跳过的规则不变，`findBuildFolders` 的返回值一个不多一个不少，它的测试照旧通过；怎么共用代码由实现者定。环境用 `pathJunkEnv(homeDir())` 和空的 `claimSet{}`。

  前两处给的是文件夹：往上找到带 `.git` 的那一级，`.git` 是文件时顺着记录目录的 `commondir` 找到仓库，再列 `<仓库>/.claude/worktrees/*`。
- `codex`：`~/.codex/worktrees/*/*`，用 `subfolders`。
- **先把打断的放回去。** 列 worktree 所在的那一层文件夹时（`.claude/worktrees`，或 Codex 的 `worktrees/<id>`），名字以 `worktreeTemp` 结尾、去掉后缀的名字不存在、并且它的 `.git` 指向的记录目录里 `gitdir` 正指着去掉后缀的那个路径：先改回原名，再判定。

**移走**，`removeWorktree`（§九的七步）：

1. 按路径重新判一次。找不到、不是 `wtClean` 或 `wtDirty`、或者和 `seen` 不一样：`wtFailChanged`。
2. `guard.inUse(resolvePath(path))`：返回代码 `in-use`。
3. 有过期的锁：解锁。失败：`wtFailGit`。
4. 量大小；把文件夹改名成 `<路径>` 加 `worktreeTemp`。那个名字已经存在，或改名失败：`wtFailGit`。
5. 清记录。失败：改回原名，`wtFailGit`。
6. 改回原名。改不回去就把带后缀的那个当作要移走的文件夹。
7. `trashWithContext`。失败：`wtFailTrash`。成功返回第 4 步量的大小。

**接进 `aitools.go`。**

- 工具表：`claude` 的 `Worktrees` 是 `"claude"`，`codex` 的是 `"codex"`。零件表 `Parts` 不加条目。
- `scanAITool` 多一个参数 `worktrees bool`：`ScanAITool` 传真，`ScanAITools` 传假。传假时一切和现在一样，`~/.codex/worktrees` 仍是 `unknown:worktrees`。
- 传真并且找到了 worktree：加一项 `AIPart{ID: tool.ID + ".worktrees", Level: aiSensitive, Worktrees: true, RunSafe: tool.Worktrees == "claude", Running: running[tool.ID]}`。`Size` 是各 worktree 之和，`Files` 是个数，`Last` 取最新的。`Path`：codex 是 `~/.codex/worktrees`，claude 是空。每个 worktree 一个 `AIGroup`：`Key` 和 `Path` 是它的路径，`Label` 是文件夹名，`Count` 是 1，`Size`、`Last`、`Repo`、`Branch`、`State`、`Changes` 照填；按大小从大到小。codex 这时把 `~/.codex/worktrees` 记进 `covered`，不再出现 `unknown:worktrees`。
- `AITool.Size` 的算法不动。
- `cleanAIPart` 开头分流：`sel.Part` 是某个工具的 `<id>.worktrees` 时走 `cleanAIWorktrees`。
  - 工具在运行并且不是 `claude`：`Skipped` 是 `running`。
  - `Groups` 是空的，或 `Seen` 和 `Groups` 不一样长：`Skipped` 是 `blocked`。
  - 每个 key 必须等于现在 `findWorktrees` 找到的某一个的 `Path`（不信页面传来的路径），否则记一条 `wt-changed`。
  - 成功 `Moved` 加一、`Freed` 加大小；`in-use` 时 `Kept` 加一；三种失败记进 `Failed`，写成 `displayHome(路径) + ": " + 代码`。什么都没移走、没有失败、`Kept` 大于 0 时 `Skipped` 是 `in-use`。

- [ ] **夹具**，`worktrees_darwin_test.go`：`wtRepo(t)` 在临时主目录的 `code/app` 建一个有一次提交的仓库并换上能用的 git（照 `useGit` 的写法）；`addWorktree(t, git, repo, name, branch)` 建 `<repo>/.claude/worktrees/<name>`；`stubWorktreeUse(t, cwds, sessions)`；`stubPidStart(t, map[int32]time.Time)`。

- [ ] **状态**，`TestWorktreeStates`，表驱动，每行一个 worktree：

| 做了什么 | `State` |
|---|---|
| 什么都没做 | `clean` |
| 一个没跟踪的文件，一个改过的文件 | `dirty`，`Changes` 是 2 |
| `git add` 之后又改了同一个文件（`MM`） | `index-only` |
| 新文件 `git add` 之后从磁盘上删掉（`AD`） | `index-only` |
| 记录目录里放一个 `rebase-merge` 文件夹 | `mid-operation` |
| 记录目录里放一个 `MERGE_HEAD` 文件 | `mid-operation` |
| HEAD 游离，上面一个不在任何分支上的提交 | `unbranched-commits` |
| HEAD 游离在 `main` 的提交上 | `clean` |
| 记录目录里放一个 `modules` 文件夹 | `submodules` |
| `Cwds` 里有它下面的一个子文件夹 | `in-use` |
| `Sessions` 里是它自己 | `in-use` |
| `locked` 写着 `claude agent (pid 4242)`，4242 活着且比锁早 | `in-use` |
| 同上，4242 不在 | `clean` |
| 同上，4242 活着但比锁晚 | `clean` |
| `locked` 写着 `keep this` | `in-use` |
| `.git` 指向一个不存在的记录目录 | `unrecognized` |
| 记录目录的 `gitdir` 指着别的文件夹 | `unrecognized` |
| `worktreeUse` 的 `OK` 为假 | `in-use` |

- [ ] **git 不可用和 git 的配置。**

| 测试 | 期望 |
|---|---|
| `TestWorktreeGitUnavailable` | 找不到 git、`/usr/bin/git` 而开发者工具没装、`worktreeGit` 超时，三种都是 `git-unavailable` |
| `TestWorktreeStatusIgnoresUserConfig` | 仓库设了 `status.showUntrackedFiles=no`，只有一个没跟踪的文件：`dirty` |
| `TestWorktreeGitHardening` | 假的 git 记下环境和参数（照 `TestProjectGitHardening`），调用前设好 `GIT_DIR`、`GIT_OPTIONAL_LOCKS=1`。一次扫描加一次移走之后：每次调用 `GIT_DIR` 都没设，`GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0`；出现过的子命令只有 `status`、`for-each-ref`、`worktree`；参数里没有 `--force`、`prune` |

- [ ] **去哪里找。**

| 测试 | 期望 |
|---|---|
| `TestWorktreeDiscoveryClaude` | 三个仓库各一个 worktree：一个只在对话记录的 `cwd` 里（在主目录下第 8 层，遍历走不到），一个只在 `Sessions` 里，一个只靠遍历。三个都找到，各出现一次；`findBuildFolders` 的结果和接入前一样 |
| `TestWorktreeDiscoveryCodex` | `~/.codex/worktrees/ab12/app` 被找到 |
| `TestWorktreeOutsideHomeIsNotListed` | 仓库在主目录之外的临时文件夹里：不列出 |
| `TestWorktreeFolderThatIsNotOne` | `.claude/worktrees/` 下一个没有 `.git` 的文件夹：列出，`unrecognized` |
| `TestWorktreePathsWithSpacesAndUnicode` | 仓库 `code/my repo 仓库`，worktree `修复 bug #1`：`clean`，`Branch` 对；移走成功；`Cwds` 给的是过了 `resolvePath`、大小写不同的同一路径时是 `in-use` |
| `TestWorktreeInterruptedRemovalIsRestored` | 把一个 worktree 的文件夹改名成带 `worktreeTemp` 的，记录不动：`findWorktrees` 之后文件夹回到原名，结果里是原路径，`clean` |

- [ ] **移走。**

| 测试 | 期望 |
|---|---|
| `TestRemoveWorktree` | 废纸篓里有这个文件夹和里面的文件；原路径没了；记录目录没了；`refs/heads/<分支>` 的 sha 没变；在仓库里 `git switch <分支>` 成功；返回的大小是移走前量的 |
| `TestRemoveWorktreeKeepsEveryFile` | 没跟踪的 `notes.txt`、被忽略的 `.env` 和 `node_modules/x/index.js` 都在废纸篓里 |
| `TestRemoveWorktreeNeverLetsGitDelete` | 包一层 `worktreeGit`：运行 `worktree remove` 的那一刻原路径不存在 |
| `TestRemoveWorktreeChangedSinceSeen` | `seen` 是 `clean`、现在 `dirty`：`wt-changed`，文件夹和记录都没动。`seen` 是 `dirty`、现在 `index-only`：同样 |
| `TestRemoveWorktreeWhenGitFails` | `worktree remove` 出错：`wt-git`；文件夹在原路径，在里面 `git status` 成功，记录目录还在 |
| `TestRemoveWorktreeWhenTheTempNameIsTaken` | 带 `worktreeTemp` 的名字已经存在：`wt-git`，什么都没动 |
| `TestRemoveWorktreeWhenTrashFails` | `trashWithContext` 出错：`wt-trash`；文件夹在原路径、文件都在；记录目录没了；分支能切 |
| `TestRemoveWorktreeWithAStaleLock` | 锁着、写的 pid 不在：解锁后移走成功 |
| `TestRemoveWorktreeWithAnOpenFile` | `guard` 说里面有打开的文件：代码 `in-use`，什么都没动 |

- [ ] **扫描和清理的接入**，`aitools_darwin_test.go`：

| 测试 | 期望 |
|---|---|
| `TestScanAIToolListsWorktrees` | `ScanAITool("claude")` 有 `claude.worktrees`：`Level` 是 `sensitive`、`Worktrees` 和 `RunSafe` 为真、`Path` 空、每个 worktree 一组；`AITool.Size` 和没有 worktree 时一样 |
| `TestScanAIToolsLeavesWorktreesOut` | Codex 的夹具加一个 worktree：`ScanAITools` 里有 `unknown:worktrees`、没有 `codex.worktrees`；`ScanAITool("codex")` 反过来 |
| `TestCleanAIToolsWorktrees` | 带 `Groups` 和 `Seen` 的选择把 worktree 移进废纸篓，`Moved` 1；`Seen` 少一个：`blocked`；key 不是找到的路径：`Failed` 里一条以 `: wt-changed` 结尾；Codex 在运行：`running`；Claude Code 在运行：照常移走 |
| `TestCleanAIToolsWorktreeInUse` | 一个在用、一个干净：`Moved` 1、`Kept` 1 |

---

## Task 5: 「运行中」标签

**Files:** 新建 `src/react/ai/running.ts`、`src/react/ai/RunningPanel.tsx`、`tests/running-react.test.ts`；改 `src/react/ai/AIToolPage.tsx`、`api.ts`、`entry.ts`、`mount.tsx`、`src/i18n.ts`（锚点：`"ai.running"` 之后）、`src/dev/mock-mygo.ts`、`src/style.css`、`package.json`（`test:web` 末尾加 `&& bun test tests/running-react.test.ts`）、`tests/aitools-react.test.ts`。

**Interfaces:** Consumes Task 0 生成的类型和四个方法。Produces `requestAITab` 的实现。

页面测试用的夹具不执行子组件（见 `tests/page-harness.ts`）：一行一行的内容要么直接写在 `RunningPanel` 里，要么拆成的小组件单独当根来测。定时用 `window.setTimeout`，每次读完再排下一次，测试换掉它来驱动。

- [ ] **桥**，`src/react/ai/api.ts`：

```ts
export const runningApi = {
  read: () => Cleaner.claudeRunning(),
  summary: () => Cleaner.claudeRunSummary(),
  endProcs: (session: string, targets: ClaudeTarget[]) => Cleaner.endClaudeProcesses(session, targets),
  endSession: (target: ClaudeTarget, session: string, status: string) => Cleaner.endClaudeSession(target, session, status),
};
```

- [ ] **纯逻辑**，`running.ts`，测试在 `tests/running-react.test.ts`。`T` 是 `ReturnType<typeof useT>`。

```ts
export const RUNNING = { idleNoteSec: 7200, refreshMs: 5000 } as const;
export function badgeCount(r: ClaudeRunning, nowSec: number): number;
export function durationText(sec: number, t: T): string;
export function procTargets(p: ClaudeProc): ClaudeTarget[];
export function leftTargets(g: ClaudeLeft): ClaudeTarget[];
export function sessionName(s: { project: string; title: string }, t: T): string;
export function freezeRunning(shown: ClaudeRunning, next: ClaudeRunning): ClaudeRunning;
export function endToast(res: ClaudeEndResult, t: T, human: (n: number) => string): { text: string; kind: "ok" | "err" };
```

| 函数 | 期望 |
|---|---|
| `badgeCount` | 残留的行数，加上 `status` 是 `idle` 且空闲满 7200 秒的会话数。7199 秒不算；没有 `status` 的不算 |
| `durationText` | 不满 1 小时写分钟（最少 1），不满 24 小时写小时，否则写天，都往下取整：59 秒是 `1 分钟`，7200 秒是 `2 小时`，7.6 天是 `7 天` |
| `procTargets` | 这一行自己加 `folded`，自己在最前 |
| `leftTargets` | 这一组每一行的 `procTargets` 接起来 |
| `sessionName` | `项目 · 标题`；没有标题只写项目；都没有是 `ai.run.unknownSession` |
| `freezeRunning` | 行和顺序照 `shown`，数字按 pid 换成 `next` 的；`next` 里没有的行留着；`next` 里新出现的不加进来 |
| `endToast` | 都结束了：`ai.run.done`，`ok`。有 `running` 或 `changed` 的：后面接 `ai.run.still`、`ai.run.changed`，用 ` · ` 连，`err`。一个都没结束时不写 `ai.run.done` |

- [ ] **`RunningPanel`**，props 是 `{ active: boolean; visit: number; refresh: number; onCount: (n: number) => void }`。内容照 §六：
  - 两段：「开着的会话」和「已关闭的会话留下的进程」，各带个数和合计占用。
  - 会话行：名字、`空闲 N` 或「正在工作」（没有状态什么都不写）、`在 X 里`（有 `host` 才写）、占用、「结束」。`own` 的会话没有「结束」。有 `commands` 的可以展开。行的 key 用 pid，不用会话 ID。
  - 进程行：命令、端口、所在文件夹的名字（有 `worktree` 写 worktree 的名字）、运行了多久、占用、「结束」。留下的进程按会话分组，组上有「全部结束」。
  - 空的时候：两段都空写 `ai.run.none`；只有第二段空写 `ai.run.left.none`；`noRecords` 时第二段写 `ai.run.left.noRecords`。
  - 页脚：`ai.run.foot`，`uncertain` 大于 0 时接 `ai.run.foot.uncertain`，再接 `ai.run.foot.idle`。
  - 读取：`active` 变真时读一次；`refresh` 变了马上读一次；之后窗口没隐藏就每 `RUNNING.refreshMs` 读一次；不 `active` 或窗口隐藏时不读、不排下一次。每次读完调用 `onCount(badgeCount(…))`。
  - 有确认框开着或正在结束时，新读数过 `freezeRunning` 再显示。
  - 读失败：显示 `ai.run.error` 和「重试」，留着上一次的列表不清空。
  - 四种确认框，文案照 §七 的表。「全部结束」的确认框逐个列出这一组每个进程的命令和端口。会话有 `commands` 时加 `ai.run.confirm.keeps`。会话的 `entry` 不空也不是 `cli`、并且有 `host` 时加 `ai.run.confirm.host`。不是空闲的会话，确认按钮用 `destructive`。
  - 确认后：进程和「全部结束」调 `runningApi.endProcs(会话 ID, 目标)`，会话调 `runningApi.endSession({pid, start}, id, status)`；回来以后用 `endToast` 提示，并马上再读一次。

| 测试 | 期望 |
|---|---|
| 两段都画出来 | 两个标题、个数和合计都对；会话的顺序和读数里一样 |
| 会话 ID 相同的两个会话 | 画成两行，各有自己的「结束」，结束时传的是各自的 pid |
| 空的三种 | 三种文案各在对的时候出现 |
| 页脚 | `uncertain` 是 2 时有那一句，是 0 时没有 |
| 没有状态的会话 | 没有 `空闲`，也没有「正在工作」 |
| `own` 的会话 | 没有「结束」 |
| 四种确认框 | 点进程、点「全部结束」、点空闲的会话、点在干活的会话，标题和正文各是对的那一组；「全部结束」的那一个列出了每个进程的命令和端口；`entry` 是 `t3-code`、`host` 是 `T3 Code` 时多出那一句；会话有 2 条命令时写着这 2 条会留着 |
| 确认以后 | `endProcs` 收到会话 ID 和 `procTargets` 的结果；`endSession` 收到 `{pid, start}`、`id`、`status`；之后又读了一次 |
| 定时 | 进来读一次；触发排好的那一次又读一次；`refresh` 加一又读一次；`active` 为假或 `document.hidden` 时不排；每次读完 `onCount` 被调用 |
| 冻结 | 确认框开着时来了一份顺序不同的读数：行的顺序不变，数字换了 |
| 读失败 | 显示 `ai.run.error`，上一次的行还在；点「重试」再读 |

- [ ] **页内标签**，`AIToolPage.tsx`、`entry.ts`、`mount.tsx`：
  - `requestAITab(toolId, tab)` 记下请求（每次 nonce 加一），`mountAITool` 把 `request: { tab: AITab | null; nonce: number }` 传给页面。写法照进程页的 `requestProcess`。`request` 是必填的，现有测试里的 `aiPage` 补上 `{ tab: null, nonce: 0 }`。
  - `toolId === "claude"` 并且不是 Windows 时，页头右侧是分段标签「空间 | 运行中」，用进程页那一套（Radix Tabs、`segTrigger`、`useIndicator`）。别的工具和 Windows 上没有标签，页面和现在一样。
  - 每次进这一页（`visit` 变了）：有新的请求就开在请求的标签，否则开在「空间」。
  - 标签上的数字：每次进页面调一次 `runningApi.summary()`，显示 `idle + left`；`RunningPanel` 的 `onCount` 来了就换成它的；0 不显示。样式用 `pr-badge`。
  - 「运行中」标签下只渲染 `RunningPanel`；「空间」的内容和状态留着，切回来不用重扫。页头的按钮跟着标签换：「空间」是现有的「重新扫描」，「运行中」是 `ai.run.refresh`，点了把传给 `RunningPanel` 的 `refresh` 加一。
  - 扫描后那条 `ai.running` 提示的末尾，在 Claude Code 页加一个链接 `ai.run.see`，点了切到「运行中」。

| 测试（`tests/aitools-react.test.ts`） | 期望 |
|---|---|
| 标签只在该有的地方 | `claude` 有标签；`codex` 没有；平台设成 `win32` 时 `claude` 也没有 |
| 默认和请求 | 默认在「空间」；`request` 是 `{ tab: "running", nonce: 1 }` 时渲染了 `RunningPanel`；同一个 nonce 再进一次回到「空间」 |
| 数字 | `summary` 返回 `{ idle: 4, left: 2 }` 显示 6；都是 0 不显示；`onCount(3)` 之后显示 3；`codex` 页不调用 `summary` |
| 「看运行中」 | 工具在运行时有这个链接，点了切标签 |
| 页头的按钮 | 「空间」标签扫描过以后是「重新扫描」；「运行中」标签是「刷新」，点了 `RunningPanel` 收到的 `refresh` 变了 |

- [ ] **mock 样例**，`src/dev/mock-mygo.ts`：一个在干活的会话（`entry` 不是 `cli`、带 `host`）、三个空闲的（其中一个满 2 小时并带两条有端口的命令）、一个 `own` 的、两个已关闭会话留下的三行（带端口，一行有 `folded`）、`uncertain` 是 2。结束的两个方法等 600 毫秒，从样例里去掉对应的行并返回 `ended`；有一个固定的 pid 返回 `running`，用来看错误提示。名字都用编的。

---

## Task 6: 进程页的小标签和跳转

**Files:** `src/react/process/format.ts`、`ProcessPage.tsx`、`api.ts`、`entry.ts`、`mount.tsx`、`src/main.ts`、`src/i18n.ts`（锚点：`"process.tag.week"` 之后）、`src/dev/mock-mygo.ts`（只改 `"Cleaner.ClaudeRunSummary"` 那一处）、`tests/process-react.test.ts`。

**Interfaces:** Consumes `Cleaner.claudeRunSummary`、`requestAITab`（Task 0 的桩就够编译）。

- [ ] **数据**：`processApi.claudeSummary = () => Cleaner.claudeRunSummary()`；`format.ts` 加 `claudeTags(s: ClaudeRunSummary | null): Map<string, ClaudeRowTag>`，按 `key` 建表，`idle` 和 `left` 都是 0 的不进表。
- [ ] **行**：`ProcessRowView` 多两个 props，`claude: ClaudeRowTag | null` 和 `onClaude: () => void`。`claude.idle > 0` 时显示 `process.tag.idleSessions`，`claude.left > 0` 时显示 `process.tag.left`，都是 `pr-tag` 按钮，点了调 `onClaude`。它们排在增长标签后面，可以同时出现。
- [ ] **读取**：和增长摘要同一个时机，每次进页面读一次，失败当没有；不随每 2 秒的采样读。
- [ ] **跳转**：`entry.ts` 里 `export interface ProcessHandlers extends GrowthHandlers { goRunning: () => void }`，`setProcessHandlers` 和 `ProcessPageProps` 改用它。`src/main.ts` 里 `goRunning: () => { requestAITab("claude", "running"); switchView("aitools", "claude"); }`。

| 测试 | 期望 |
|---|---|
| `claudeTags` | 两条都进表；全 0 的不进；`null` 得到空表 |
| 行上的标签 | `{ idle: 6, left: 0 }` 只有「6 个空闲会话」；`{ idle: 0, left: 2 }` 只有「2 个是已关闭会话留下的」；同时有增长标签时三个都在；点新标签调 `onClaude`，点增长标签仍调 `onTag` |
| 读取的时机 | 进页面读一次；来三条采样不再读；`visit` 加一再读一次；读取出错时页面照常、没有标签 |
| 跳转 | `ProcessPage` 把 `goRunning` 接到行的 `onClaude` 上 |

mock 的摘要给两行：进程样例里 Claude Code 那一行 `idle` 是 3（样例里没有就加一行 `cli:claude`），`cli:node` 那一行 `left` 是 2。

---

## Task 7: worktree 的行

**Files:** `src/react/ai/types.ts`、`PartRow.tsx`、`AIToolPage.tsx`（选择、确认框、失败提示）、`api.ts`、`src/shared.ts`、`src/i18n.ts`（锚点：`"ai.group.gone"` 之后，零件的三个键在 `"ai.part.claude.sessions.lose"` 和 `"ai.part.codex.sessions.lose"` 之后）、`src/dev/mock-mygo.ts`、`tests/aitools-react.test.ts`。

**Interfaces:** Consumes `AIPart.worktrees`、`AIGroup.repo/branch/state/changes`、`AISelection.seen`。在 Task 5 合入之后开始。

- [ ] **能不能勾**，`types.ts`：
  - `isSessions(p)` 改成 `p.level === "sensitive" && !p.worktrees`；`AIToolPage.tsx` 里自己那份 `isSessionsPart` 删掉，改用它。
  - `groupSelectable`：`p.worktrees` 时只看 `g.state === "clean" || g.state === "dirty"`。
  - 新增 `worktreeSeen(p: AIPart, keys: string[]): string[]`，按 `keys` 的顺序给出各组的 `state`。

| 测试 | 期望 |
|---|---|
| `groupSelectable` | 九种状态里只有 `clean` 和 `dirty` 能勾；和保留窗口、`recent` 无关 |
| `isSessions` | worktree 这一项是假；对话记录仍是真 |
| `canPick` | 一组都不能勾时是假；Codex 在运行（`running` 真、`runSafe` 假）时是假 |
| `worktreeSeen` | 顺序跟着 `keys` |

- [ ] **行**，`PartRow.tsx`（当根来测）：`part.worktrees` 时
  - 这一项自己没有勾选框（位置空着），worktree 只能一个一个勾。
  - 展开后没有保留窗口那一排，没有 `ai.sessionHint`。
  - 每组一行：勾选框、文件夹名、第二行 `ai.wt.note`（仓库取 `repo` 的最后一段，分支为空写 `ai.wt.detached`，天数是 `(scannedAt - last) / 86400` 往下取整；`last` 是 0 时改用 `ai.wt.noteNoAge`）、状态说明、大小。点行在访达里显示。
  - 状态说明：`clean` 不写；`dirty` 写 `ai.wt.state.dirty`；其余七种写各自的 `ai.wt.state.<状态>`，勾选框禁用。
  - `part.path` 是空的时候不显示这一项的「在访达中显示」按钮。
  - 被运行中的工具锁住时和别的项一样：徽标换成「先退出」，每组的勾选框禁用。

| 测试 | 期望 |
|---|---|
| 一项的样子 | 没有这一项的勾选框；展开后没有窗口选择；徽标是「敏感」 |
| 每种状态 | 九种各一组：两种勾选框可用，七种禁用并写着对的说明；`dirty` 那组写着改动的处数 |
| 第二行 | `repo` 是 `/Users/a/code/app`、`branch` 是 `feat/x`、8 天没动：`app · feat/x · 8 天没动`；没有分支时写游离的 HEAD |
| 没有路径 | `path` 是空：没有显示按钮；Codex 那一项有 |
| Codex 在运行 | 徽标「先退出」，每组禁用 |

- [ ] **页面**，`AIToolPage.tsx`：
  - 扫描后不预先勾 worktree。页头的「可清理」不算 worktree 这一项；「清理」按钮上的数照常算勾了的。
  - 发给后端的选择：worktree 这一项多带 `seen: worktreeSeen(p, groups)`。`aiApi.clean` 的参数类型加可选的 `seen`。
  - 确认框里这一项：数量用 `ai.confirm.worktrees`，正文用 `ai.part.<id>.lose`；勾了 `dirty` 的再加一句 `ai.confirm.worktreesDirty`。
  - 清理回来：结果里有 `failed` 时，把第一条用错误提示显示出来，冒号后面的代码过 `errText`。`src/shared.ts` 的 `errText` 认 `wt-changed`、`wt-git`、`wt-trash`，对应 `ai.wt.fail.*` 三个键。

| 测试 | 期望 |
|---|---|
| 不预选、不计入可清理 | 扫描后 worktree 那一项没勾；「可清理」的数不含它；勾一个之后「清理」按钮的数加上它 |
| 选择 | 勾了一个 `clean`、一个 `dirty`：`clean` 收到 `{ tool, part, groups: [a, b], seen: ["clean", "dirty"] }`；别的项没有 `seen` |
| 确认框 | 有「2 个 worktree」、正文那一句，和「其中 1 个带着没提交的改动」 |
| 失败提示 | `failed` 是 `["~/code/app/.claude/worktrees/x: wt-git"]`：错误提示是路径加 `ai.wt.fail.wt-git` 的文案 |

- [ ] **mock 样例**：Claude Code 和 Codex 的样例各加一项 worktree，九种状态各一组；Codex 那一项带 `path`。`"Cleaner.CleanAITools"` 的 mock 对 worktree 的选择照常移走，但有一个固定的 key 返回一条 `wt-git`。名字都用编的。

---

## 文案

键、中文、英文。占位符照写。表里的 `\|` 只是为了表格能显示，写进代码是 `|`，比如 `{n|session/sessions}`。

| 键 | 中文 | 英文 |
|---|---|---|
| `ai.tabs` | 查看 | View |
| `ai.tab.space` | 空间 | Space |
| `ai.tab.running` | 运行中 | Running |
| `ai.run.see` | 看运行中 | See What's Running |
| `ai.run.refresh` | 刷新 | Refresh |
| `ai.run.open.title` | 开着的会话 | Open Sessions |
| `ai.run.open.sum` | {n} 个，共 {size} | {n} {n\|session/sessions}, {size} |
| `ai.run.left.title` | 已关闭的会话留下的进程 | Processes Left by Closed Sessions |
| `ai.run.left.sum` | {n} 个，共 {size} | {n} {n\|process/processes}, {size} |
| `ai.run.left.none` | 没有已关闭的会话留下的进程 | No closed session left a process running |
| `ai.run.left.noRecords` | 读不到 Claude Code 的会话记录，没法判断哪些进程是留下的 | Claude Code's session records can't be read, so there's no telling which processes were left behind |
| `ai.run.none` | Claude Code 没在运行，也没有留下进程。 | Claude Code isn't running and left no processes. |
| `ai.run.foot` | 只列出能确定属于 Claude Code 会话的进程。 | Only processes that certainly belong to a Claude Code session are listed. |
| `ai.run.foot.uncertain` | 另有 {n} 个带着它的标记、但说不准的，没有列出。 | {n} more {n\|carries/carry} its mark but can't be placed, and {n\|is/are} not listed. |
| `ai.run.foot.idle` | 空闲时间来自 Claude Code 自己的记录。 | Idle times come from Claude Code's own records. |
| `ai.run.idle` | 空闲 {time} | Idle {time} |
| `ai.run.busy` | 正在工作 | Working |
| `ai.run.in` | 在 {app} 里 | in {app} |
| `ai.run.unknownSession` | 未知会话 | Unknown session |
| `ai.run.port` | 端口 {ports} | port {ports} |
| `ai.run.dur.min` | {n} 分钟 | {n} min |
| `ai.run.dur.hour` | {n} 小时 | {n} {n\|hour/hours} |
| `ai.run.dur.day` | {n} 天 | {n} {n\|day/days} |
| `ai.run.end` | 结束 | End |
| `ai.run.endAll` | 全部结束 | End All |
| `ai.run.ending` | 正在结束… | Ending… |
| `ai.run.error` | 读取运行中的会话失败 | Couldn't read what's running |
| `ai.run.confirm.proc.title` | 结束这个进程？ | End this process? |
| `ai.run.confirm.proc.body` | 它是「{session}」启动的。 | "{session}" started it. |
| `ai.run.confirm.ports` | 它占着端口 {ports}，结束后这个端口停止服务。 | It holds port {ports}; ending it stops what's served there. |
| `ai.run.confirm.left.title` | 结束「{session}」留下的 {n} 个进程？ | End the {n} {n\|process/processes} "{session}" left? |
| `ai.run.confirm.session.title` | 结束会话「{session}」？ | End the session "{session}"? |
| `ai.run.confirm.idle.body` | 对话已经保存，之后可以用 claude --resume 接着聊。 | The conversation is saved; claude --resume picks it up later. |
| `ai.run.confirm.busy.body` | 它可能正在干活，结束会打断当前这一步。 | It may be working; ending it interrupts the current step. |
| `ai.run.confirm.keeps` | 它启动的 {n} 个命令会留着，之后出现在「已关闭的会话留下的进程」里。 | The {n} {n\|command/commands} it started keep running and then show under "Processes Left by Closed Sessions". |
| `ai.run.confirm.host` | 它是「{app}」启动的，结束后那边的这个会话会断开。 | "{app}" started it; ending it disconnects this session there. |
| `ai.run.confirm.ok` | 结束 | End |
| `ai.run.done` | 已结束 {n} 个进程，腾出 {size} | Ended {n} {n\|process/processes}, freed {size} |
| `ai.run.still` | {n} 个还在运行 | {n} still running |
| `ai.run.changed` | {n} 个已经变了，没有动 | {n} changed since listed and {n\|was/were} left alone |
| `process.tag.idleSessions` | {n} 个空闲会话 | {n} idle {n\|session/sessions} |
| `process.tag.left` | {n} 个是已关闭会话留下的 | {n} left by closed sessions |
| `ai.part.claude.worktrees.name`、`ai.part.codex.worktrees.name` | Agent worktree | Agent Worktrees |
| `ai.part.claude.worktrees.desc` | Claude Code 在各个仓库里建的 worktree，整个移走 | Worktrees Claude Code made in your repositories, removed whole |
| `ai.part.codex.worktrees.desc` | Codex 建的 worktree，整个移走 | Worktrees Codex made, removed whole |
| `ai.part.claude.worktrees.lose`、`ai.part.codex.worktrees.lose` | 文件夹进废纸篓；分支和已提交的内容留在仓库里。放回原处后文件都在，但它不再是 git 的 worktree：要接着做，在仓库里重新 git worktree add，或直接切到那个分支。 | The folder goes to the Trash; the branch and everything committed stay in the repository. Put back, the files are all there, but it is no longer a git worktree: to go on, run git worktree add in the repository again, or switch to that branch. |
| `ai.wt.note` | {repo} · {branch} · {n} 天没动 | {repo} · {branch} · untouched for {n} {n\|day/days} |
| `ai.wt.noteNoAge` | {repo} · {branch} | {repo} · {branch} |
| `ai.wt.detached` | 游离的 HEAD | detached HEAD |
| `ai.wt.state.dirty` | {n} 处没提交的改动，会一起进废纸篓 | {n} uncommitted {n\|change/changes}, going to the Trash with it |
| `ai.wt.state.in-use` | 在用 | In use |
| `ai.wt.state.submodules` | 含子模块，Cleanzo 不动 | Has submodules, so Cleanzo leaves it |
| `ai.wt.state.mid-operation` | rebase 或 merge 做到一半 | A rebase or merge is in progress |
| `ai.wt.state.unbranched-commits` | 有不在任何分支上的提交 | Has commits on no branch |
| `ai.wt.state.git-unavailable` | git 没能回答，先不动 | git couldn't answer, so it stays |
| `ai.wt.state.index-only` | 暂存区里有和磁盘上不一样的内容，移走会丢 | The staging area holds content the files don't; removing it would lose that |
| `ai.wt.state.unrecognized` | 认不出，Cleanzo 不动 | Not recognized, so Cleanzo leaves it |
| `ai.confirm.worktrees` | {n} 个 worktree | {n} {n\|worktree/worktrees} |
| `ai.confirm.worktreesDirty` | 其中 {n} 个带着没提交的改动。 | {n} of them {n\|holds/hold} uncommitted changes. |
| `ai.wt.fail.wt-changed` | 已经变了，没有动 | changed since the scan and was left alone |
| `ai.wt.fail.wt-git` | 没能清掉 git 的记录，什么都没动 | git's record could not be cleared; nothing was changed |
| `ai.wt.fail.wt-trash` | git 的记录已经清掉，文件夹还在原处，没能移到废纸篓 | git's record is cleared, but the folder is still in place: it could not be moved to the Trash |

---

## Task 8: 说明文档、合并、验证（收尾的人）

- [ ] 各路 cherry-pick 到 `feat/claude-running-worktrees`。`bun run generate` 之后 `src/mygo.ts` 没有变化。
- [ ] `README.md`、`README.en.md`、`llms.txt` 的功能说明加上这两样；`CHANGELOG.md` 最上面加一节 `## Unreleased`。都写明归属的思路来自 seshka（MIT）。不改版本号。
- [ ] Global Constraints 里的全套检查在合并后的分支上通过。
- [ ] 真机，只读，在沙箱外：
  - `CLEANZO_PROBE=1 go test -run '^TestClaudeRunningOnThisMac$' -count=1 .`：会话数和 `~/.claude/sessions` 里对得上的记录数一致；没有编辑器或别的带窗口的 App 被算成残留；看一个真的包命令的 shell，确认 `claudeWrapperCommand` 认的写法和它一样。
  - `CLEANZO_PERF=1 go test -run '^TestClaudeRunningBudget$' -count=1 .`（不带 `-race`）：不超过一个核的 3%。
  - 用一个临时的只读测试对真的主目录调一次 `ScanAITool("claude")`：列出规格第二节那一个 worktree，状态和大小对得上，扫描比原来多花的时间记进验证记录。测试用完删掉。
  - 开发版打开一次：标签、数字、两段列表、四种确认框（都点取消）、进程页的两种小标签和跳转、worktree 的行，中英文、浅色深色各看一遍。
- [ ] 不结束任何真的会话或进程，不移走任何真的 worktree。这两样是维护者的发版前检查（§十二）：结束一个空闲会话再用 `claude --resume` 接回来；在一个测试仓库里建一个 worktree 再移走。
- [ ] 把验证记录和执行中的裁定追加到本文件末尾，写法照 `2026-11-17-dev-space-batch.md`。

## 计划补的决定

规格没写、计划定下的，每条都可以推翻。

1. **结束会话时带上看到的会话 ID 和状态。** 对不上就不结束，结果是「已经变了」。看到的是在干活、现在空闲了，照常结束。
2. **命令显示成一行。** 最多 300 个字符，第一个词只留程序名，控制字符去掉。
3. **扫描时把打断的移走放回原名。** 这是扫描唯一会改磁盘的地方，只改回 Cleanzo 自己起的那个临时名字。
4. **两个进程同一个会话 ID 算两行。** 脱开的进程归启动早的那一个，除非 `CLAUDE_PID` 指着另一个。
5. **worktree 这一项自己没有勾选框。** 只能一个一个勾，免得一下勾中好几个带改动的。
6. **「AI 空间」完全不变。** Codex 的 `worktrees` 文件夹在那一页仍然算在「保留不动」里。
7. **读不到进程的当前目录时，所有 worktree 都算在用。** 和「列不出进程就当工具在运行」是同一个做法。
8. **僵尸进程不进快照**，结束时进程变成僵尸就算已结束。
9. **worktree 移走失败时用错误提示说明原因。** 现在这一页清理失败只显示「N 项已跳过」。
10. **git 的时间限制**：`status` 10 秒，其余 5 秒。超时的 worktree 不能勾。
11. **规则的测试只在 macOS 上跑。** 用例里全是 macOS 的路径，而 Windows 的 CI 也跑测试；规格里说的「任何平台都能跑」做不到。

---

## Validation ledger (2026-11-21)

分支 `feat/claude-running-worktrees-int`，从 `0c2e9d4`（master）起。执行方式：每个任务一个实现者（Opus），各在自己的 git worktree 里做，做完一次任务评审；Task 1 和 Task 4 各返工两轮；最后一次整分支评审（Fable），它指出的问题由主会话自己修，没有再派复审。执行到一半维护者问了为什么派这么多子代理，之后的小修正和复审都改由主会话自己做。下面的核对都在维护者的 Mac 上做：没有结束任何真的会话，没有移走任何真的 worktree；结束过的只有测试自己启动的进程。

- `test -z "$(gofmt -l .)"`、`go vet ./...`、两次 Windows 的 `go vet`：干净。`bun run generate` 之后 `src/mygo.ts` 没有变化。
- `go test -race -count=1 -skip '^TestNativeSysctlRawProbes$' ./...`：`ok`（整分支评审之前 329 秒；修完以后的结果见本节末尾）。机器负载高的时候（好几个代理同时跑测试），两个原有的一秒限时测试（`TestRunToolControlsEnvironmentAndCancellation`、`TestSkillGitCallsTimeOut`）会失败，单独重跑通过；master 上也一样。
- `bun run typecheck`：干净。`bun run test:web`：407 个通过，0 个失败。`bun run build:web`：通过。
- **真的读数**（`CLEANZO_PROBE=1 go test -run '^TestClaudeRunningOnThisMac$'`，连读四次）：11 个开着的会话，和 `~/.claude/sessions` 里的 11 份记录、11 个 Claude Code 进程对得上；7 个空闲满 2 小时（5 个约 190 小时，1 个约 129 小时，1 个约 8 小时）；已关闭会话留下的 0 行；说不准的 0 个；`noRecords` 四次都是假。没有编辑器或带窗口的 App 被列出。
- **开销**（`CLEANZO_PERF=1 go test -run '^TestClaudeRunningBudget$'`，不带 `-race`）：60 秒读 12 次，CPU 511 毫秒，一个核的 0.85%，上限 3%。有一行残留要列出时（这时会跑 `lsof`）：378 毫秒，0.63%。
- **`kern.procargs2` 的布局**：本账户 636 个进程，参数都正好从「路径加结尾的 0，补齐到 8 字节」的位置开始，没有例外。
- **结束**：`TestEndClaudeEndsARealChild` 用真的读取和信号结束测试自己启动的一个忽略 `SIGTERM` 的子进程：快照里的启动时间和核对时读到的一致，等满 2 秒后 `SIGKILL`，结果 `forced`。
- **残留，从头到尾走一遍**（临时测试，用完删了）：在 shell 里起一个带标记的 node 小服务（监听一个本机端口，会话 ID 是编的），让它脱开。真的读数把它列在「已关闭的会话留下的进程」下面，一行，带 1 个端口；摘要里 `cli:node` 那一行多了 1。用真的结束路径去结束它：换一个会话 ID、或者把启动时间改掉，结果都是「已经变了」，进程没动；按原样传，`SIGTERM` 后结果是「已结束」，进程没了，再读一次残留是 0。
- **worktree 扫描**（临时的只读测试，用完删了）：`ScanAITool("claude")` 列出规格第二节那一个 worktree，1018 MB，状态「在用」。核对过是真的在用：本账户有 5 个进程的当前目录在它里面，1 个开着的会话的目录在它里面（写规格时它闲置了约 8 周，今天有人在里面干活）。Codex 没有 worktree。
- **扫描多花的时间**：Claude Code 页的扫描原来 0.35 秒；带上 worktree，第一次（冷）4.7 秒，之后约多 1.3 秒（主目录遍历 0.2 到 0.4 秒，列出 0.2 秒，量那个 1 GB 的 worktree 0.8 秒）。
- **界面**（浏览器里的 mock，无界面 Chromium 截图）：「运行中」标签（中文浅色 1180 px，英文 900 px）；「全部结束」的确认框（中文浅色、英文深色）；在干活的会话和空闲会话的确认框（读页面上的文字）；进程页的两种小标签和跳转；Agent worktree 的十种状态（中文浅色、英文深色）。没有露出来的 i18n 键，没有发现布局问题。
- **整分支评审**（Fable，读完 595 KB 的差异）：没有必须修的严重问题；三个应该修的都修了，各有先失败后通过的测试：改写过参数区的进程会把环境变量露成参数（现在凡是 `名字=值` 都遮掉值）；会话启动的命令自己又跑了一个 Claude Code 时，命令和它的 shell 会跟着会话一起被结束（现在按上面最近的 Claude Code 进程的进程组认辅助进程）；仓库在主目录之外的 worktree，git 会去改主目录之外的记录（现在列出但不能勾）。
- **修完以后的全套检查**（分支 `fae1611`）：`gofmt`、`go vet`、两次 Windows 的 `go vet` 干净；`go test -race`：`ok`，301 秒；`bun run typecheck` 干净；`bun run test:web`：407 个通过；`bun run build:web` 通过。
- **没有核对的**：真的应用里的样子（只看了 mock）；单个进程的确认框；Codex 在运行时它那一项的「先退出」；Windows 上的测试（`TestClaudeRunningUnsupportedOffMacOS`、`TestAIWorktreesOnlyOnMacOS`、`runToolStdout` 只在 Windows CI 上跑，分支还没有 push）；维护者的两项发版前检查（结束一个空闲会话再 `claude --resume` 接回来；在测试仓库里建一个 worktree 再移走）。

## Known gaps

- `/clear` 之后带着旧会话 ID 的脱开进程：2026-11-22 加了规则（规格第十四节第 34 条），不再说成「已关闭的会话留下的」，改判说不准。还分不出来、照旧会列成残留的三种：在会话里用 `/resume` 换到一个更早的会话；留下来的进程全是 `/clear` 之后才生出来的；会话换过工作文件夹。从不预选，结束前逐个确认。
- 一个 Claude Code 进程 `/clear` 之后，同一个文件夹里别的会话在那之前留下的真残留也不列出，只在页脚计数，直到那个进程退出（同一条规则的代价）。
- 只要有一个 Claude Code 进程读不到会话 ID（刚启动、记录还没写出来，或者是不写会话记录的 `claude` 子命令），「已关闭的会话留下的进程」那一段就写「读不到会话记录」，直到那个进程有了记录或退出。
- 命令里别的写法的密钥遮不到（规格第十三节）。现在遮掉的：任何位置的 `NAME=值`、名字里有 key、token、secret、password 的参数的值、URL 里的账号密码。
- 结束进程时，快照和发 `SIGTERM` 之间没有再核对一次 pid 和启动时间（发 `SIGKILL` 之前有）。间隔是毫秒级的。
- 已关闭会话的对话记录每 5 秒重读一次（开头几行加末尾 2 MB），没有缓存。
- 改写过自己参数区的进程（比如 Node 设了一个比原命令行长的 `process.title`），头几条环境变量会被当成参数显示：值都遮掉了，变量的名字会显示出来。缓冲区里分不出这种情况。
- 记录目录里 `gitdir` 读不出来的记录，会让同一个仓库的每个 worktree 都读成「里面还有别的 worktree」，而其实没有可以先移走的。
- git 读不了的文件夹只在标准错误里报，现在不读标准错误：这样的 worktree 读成「干净」，移走时整个进废纸篓。
- 比主目录遍历的 6 层更深的仓库里，套在里面的 worktree 可能列不出来，外面那个会一直是「里面还有别的 worktree」。
- 提交 `865bf25` 单独检出时 `TestBindingsArePlatformNeutral` 不过，隔两个提交的 `5826a30` 修好。历史没有重写。

## Rulings made during execution

每条是：定了什么 — 为什么 — 错了的代价。规则上的改动已经写回规格（第五、七、九节里标着「实现时改」「实现时加」的地方，汇总在第十四节第 19 到 30 条）。

**怎么执行**

- 同一波的任务并行做，各在自己的检出里 — 计划的执行表就是这么排的，检出分开、文件各占各的 — 错了：cherry-pick 时有冲突（实际没有）。
- 实现者和任务评审用 Opus，整分支评审用 Fable — 11 月 17 日那批维护者要的是 Opus — 错了：比便宜的档位多花 token。
- 把「直接开发吧」当成选了并行子代理那种方式 — 交接时我建议的是这种，维护者没有点名 — 错了：一共派了 23 个子代理（12 个实现者、11 个评审），维护者中途问起；之后改成主会话自己修、自己复审，整分支评审之后的修正也没有再请人复审。
- 评审通过以后，又为剩下的小问题各派了一轮修正（归属规则、worktree 后端）— 它们改的是「给谁发信号」和「git 怎么跑」— 错了：多两个子代理；这一步本来可以主会话自己做。
- 21:26 发现另一个会话在主检出里改别的功能，主检出当时停在本分支上 — 之后不再碰主检出，合并改在独立检出的 `feat/claude-running-worktrees-int` 上做 — 错了：分支名和计划写的不一样；主检出的 `feat/claude-running-worktrees` 停在 `c725dfb`。
- 报告写到主检出的 `.superpowers/` 里，不写在各自的检出里 — 检出没了以后评审还要读 — 没有代价。

**补计划的缺口**

- AI 工具页多一个「是否在屏幕上」的信号（`active`），`src/main.ts` 切到别的视图时通知它（`leaveReactAITools`）— 原来没有任何东西告诉这一页用户已经离开，「运行中」每 5 秒的读取停不下来 — 错了：`src/main.ts` 多一行。
- 窗口从隐藏回到可见时，「运行中」马上读一次并重新排定时；同一时间只有一次读取 — 不然窗口隐藏过一次以后列表就不再更新 — 错了：多读一次。
- worktree 只在 macOS 上扫描；Task 4 可以最小地改 `projects.go` 来共用主目录遍历 — 计划只说「Windows 上什么都不多出来」「共用代码」— 错了：diff 大一点；`findBuildFolders` 的结果和测试都没变。
- mock 里进程页小标签的数字从「运行中」的样例里数出来 — 两个任务并行写，各写各的数，标签上会从 5 跳到 4 — 没有代价。

**归属规则**（都已写回规格第五节）

- 脱开的进程按会话 ID 整体判开着、说不准还是已关闭，挂在会话下面的进程带的 `CLAUDE_PID` 也算证据 — 逐个判的话，靠 `CLAUDE_PID` 判成开着的命令，它那些看不到环境变量的子进程会被说成残留，「全部结束」会杀掉它们 — 错了：更多进程进「说不准」，不列出。
- 任何一个 Claude Code 进程（子 agent 也算）读不到会话 ID，就不判定残留 — 「读不到只能少显示」— 错了：残留那一段更常显示「读不到会话记录」。
- 带 `CLAUDECODE=1` 但没有会话 ID 的进程当作没有标记；子进程带的 ID 不一致时不跟子进程 — 规格没写 — 错了：少列出几个。
- Dock 上的 App（或它的辅助进程）隔开的 Claude Code 进程单算一个会话；Claude Code 自己装在 Dock App 的包里时照样带着它的子 agent — 结束外层会话不该连 App 自己的会话一起结束 — 错了：子 agent 被当成单独的会话显示。
- 进程表里同一个 pid 出现两次时，只认第一份，并且什么都不判成残留 — 一次读取不会重复，重复说明快照不可靠 — 没有代价。
- 快照里父子成环、pid 0、自己是自己的父进程：不崩溃，这些进程不列出 — 快照不是一瞬间读完的 — 没有代价。
- 辅助进程按「上面最近的 Claude Code 进程的进程组」认；脱开的进程只比会话自己那个 Claude Code 进程的组（整分支评审指出）— 原来把子 agent 的进程组也算上，会话启动的命令自己跑了 `claude -p` 时，命令和它的 shell 就成了辅助进程，结束会话会连它们一起结束 — 错了：某个本该跟着会话结束的辅助进程留了下来，显示成命令。
- 带着会话 ID 的进程，不管自己列不列得出，它的 `CLAUDE_PID` 都算这个 ID 还开着的证据 — 减少把开着的会话的进程说成残留 — 错了：某个残留被归到开着的会话下面。
- Dock 上的 App 按 pid 排除，读不到包路径时也排除 — 没有代价。
- 命令的显示：任何位置的 `名字=值` 都遮掉值，不管名字是什么字符（包括 `sh -c` 的脚本里、紧跟在 `;`、`&`、`|`、`(` 后面的、引号里面的），URL 里的账号密码遮掉，被遮过的词不再当参数名判断 — 规格第三节写的是「命令里 `NAME=值` 这种写法」，没有说「开头的」；改写过参数区的进程还会把环境变量露成参数，变量名不一定是标识符（整分支评审指出）— 错了：多遮一点（`dd if=… of=…`、URL 里 `?a=` 后面的值）。

**读取**

- `kern.procargs2` 的参数从路径补齐到 8 字节的位置读起；布局对不上的缓冲区什么都不给 — 把路径后面的 0 全跳过的话，第一个参数是空串的进程，最后一个「参数」是它的第一条环境变量，会显示在页面上（用真的子进程复现了）— 错了：布局不同的进程当作看不到。
- 运行中的 App 列表读不到时，整张快照算读不到 — 没有它分不清 Dock 上的 App 和命令 — 错了：AppKit 出问题时「运行中」显示读取失败。
- `statusUpdatedAt` 比进程自己的启动时间还早的，也当作读不到状态 — 单位要是换成了秒，每个空闲会话都会显示成空闲几十年 — 错了：时钟往回拨过的机器上，个别会话不显示状态。
- 记录里没有 `statusUpdatedAt` 的会话当作读不到状态（实现者定的，评审认可）；页面上「空闲但不知道多久」只写「空闲」、不计数的那段判断留着当边界上的保险 — 不然显示成「空闲 2 万天」并计入标签 — 错了：这样的会话走更重的那个确认框。

**结束**

- 结束会话的请求带着看到的会话 ID 和状态；ID 是空的只对得上现在 ID 也是空的会话 — 计划补的决定第 1 条 — 没有代价。
- Cleanzo 自己所在的会话，不管 ID 或状态变没变，都回「blocked」；同一个 pid 传了两次按第一次算；调用前就取消了的请求照样发 `SIGTERM`、不等待也不升级（实现者定的，评审认可）— 都不多发信号 — 没有代价。
- 发 `SIGTERM` 之前不再核对一次（停着没做）— 规格没要求，间隔是毫秒级的，加了要改六个测试的计数 — 错了：见 Known gaps。

**worktree**（都已写回规格第九节）

- 多一种状态「里面还有别的 worktree」（`holds-worktrees`），不能勾：它的 `.claude/worktrees` 里有文件夹，或仓库里别的 worktree 记录指着它里面 — 移走外面那个会把里面的一起带进废纸篓，里面那个的记录还在，它的分支就切不过去了 — 错了：多一种状态和一条文案。
- 再多一种状态「仓库在主目录之外」（`repository-outside-home`），不能勾：worktree 的记录目录或仓库的 git 目录不在主目录里（整分支评审指出）— git 清记录时会改那里，而 Cleanzo 说过只在主目录和临时目录里动 — 错了：仓库在外接硬盘上的 Codex worktree，Cleanzo 移不走。先前我定的是「照常列出、照常能移走」，这一条把它推翻了。
- Cleanzo 连自己的当前目录都读不到时，所有 worktree 都算在用 — 那时别的进程的当前目录也不可信 — 没有代价。
- 记录目录旁边的文件（比如访达留下的 `.DS_Store`）不算记录 — 不然那个仓库的每个 worktree 都读成「里面还有别的 worktree」— 没有代价。
- 移走：整个过程持一把锁，扫描的「改回原名」也等这把锁；先量大小再重新判状态；第 5 步成不成看记录目录还在不在；改名之前取消就什么都不动，改名之后一直做到进废纸篓 — 另一次扫描在 git 启动的瞬间把文件夹改回原名的话，git 会把它直接删掉 — 错了：扫描要等正在进行的移走做完。
- 清理时只核对「这个路径是不是现在能找到的 worktree」，不把所有 worktree 重新判一遍 — 省掉一轮 `git status` 和量大小 — 没有代价。
- git 只读标准输出，并且仍在自己的进程组里跑、超时整组结束 — 警告混进来会被当成一行状态；进程组不能丢 — 错了：见 Known gaps 里读不了的文件夹那一条。
- 扫描把移到一半的放回原名，记录已经没了的也放回（放回以后是「认不出」）— 不然崩溃以后留下一个带临时后缀、永远列不对的文件夹 — 这是扫描唯一会改磁盘的地方。
- reftable 格式的仓库，记录目录的 `HEAD` 读不出真的分支，判成「git 没能回答」；`for-each-ref` 的输出按行认，不是「非空就算在分支上」（实现者定的，评审认可）— 两处都会把游离 HEAD 上的独有提交读成「干净」— 错了：这类 worktree 不能勾。
- 测试默认把「谁在用」换成什么都读不到（全部算在用）— 不然现有测试会读这台机器的进程表和会话记录 — 没有代价。

**页面**

- 一个都没法勾的 worktree（十种状态里除了「干净」「有改动」的八种，以及页面不认识的新状态）不能勾，并写「认不出」— 以后后端加状态时页面不会放行 — 没有代价。
- worktree 的勾选框用第二行（仓库 · 分支 · 几天没动）做说明；失败不止一个时提示「另有 N 项没能清理」— 同名的 worktree 读屏分不开；原来只说第一条 — 没有代价。
- 标签上的数字读失败时不显示数字 — 原来留着上一次的 — 没有代价。
