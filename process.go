package main

// process.go holds the process page's rules: the rows the page shows, how
// processes are grouped into rows, the sampling loop and the force quit
// binding. System reads come in through processSource, so the tests run on
// any platform. The status page's own sampling in monitor.go is unchanged.

import (
	"bufio"
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// ---- types ----------------------------------------------------------------

// ProcessRow is one line of the process page's CPU and memory tabs: an
// app's processes, a command-line program's processes or a system
// program's processes, summed up. CPU is a single-core percentage like
// Activity Monitor's, so it can exceed 100.
type ProcessRow struct {
	Key       string         `json:"key"` // "app:<bundle path>" lower-cased, "cli:<name>" or "sys:<name>"
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`     // "app", "cli" or "system"
	Path      string         `json:"path"`     // the .app; "" for cli and system rows
	BundleID  string         `json:"bundleId"` // lower-cased; "" when unknown
	CPU       float64        `json:"cpu"`
	Memory    uint64         `json:"memory"`
	Procs     int            `json:"procs"`
	Estimated bool           `json:"estimated"` // some process was read through ps
	Quit      bool           `json:"quit"`      // a Dock app of this user that may be quit
	Children  []ProcessChild `json:"children"`  // by name, same-name processes merged
}

// ProcessChild is one name inside a row: its processes summed up.
type ProcessChild struct {
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	CPU       float64 `json:"cpu"`
	Memory    uint64  `json:"memory"`
	Estimated bool    `json:"estimated"`
}

// ProcessSample is one reading of the process page.
type ProcessSample struct {
	Rows     []ProcessRow `json:"rows"`     // ordered by key; the page sorts
	CPU      float64      `json:"cpu"`      // the whole machine, 0–100
	Pressure uint64       `json:"pressure"` // kern.memorystatus_vm_pressure_level: 1 normal, 2 warning, 4 critical, 0 unknown
	Own      int          `json:"own"`      // this user's processes, read exactly
	Others   int          `json:"others"`   // other accounts' processes, estimated from ps
}

// otherProc is another account's process as ps reports it.
type otherProc struct {
	Pid    int32
	UID    uint32
	CPU    float64 // ps %cpu, a decaying average
	Memory uint64  // resident size in bytes
	Path   string  // the executable's full path, or a bare name like kernel_task
	Name   string  // the short name from ps's ucomm column
}

// processSource is everything the loop reads, injectable for tests.
type processSource struct {
	procs       func() []procReading                  // this user's processes, exact
	others      func(ctx context.Context) []otherProc // other accounts' processes, from ps
	apps        func() []workspaceApp
	responsible func(pid int32) int32
	system      func(path string) bool     // ships with the OS: daemons, agents, framework services
	bundleID    func(bundle string) string // CFBundleIdentifier of an .app
	pressure    func() uint64
	cpu         func() (busy, total uint64) // cumulative ticks
	now         func() time.Time            // nil uses time.Now; tests inject a deterministic clock
}

// Timing knobs, variables so tests can shrink them.
var (
	processFirstDelay  = time.Second     // the first sample's CPU interval
	processInterval    = 2 * time.Second // one sample
	processOthersEvery = 3               // ps runs on every third sample, about every six seconds
)

type processBundleIDEntry struct {
	once sync.Once
	id   string
}

// Info.plist fallback reads survive pauses and page visits. The global
// lock only protects entry lookup; each path owns its one-time read.
var processBundleIDs = struct {
	sync.Mutex
	values map[string]*processBundleIDEntry
}{values: make(map[string]*processBundleIDEntry)}

func processBundleID(bundle string, read func(string) string) string {
	processBundleIDs.Lock()
	entry := processBundleIDs.values[bundle]
	if entry == nil {
		entry = &processBundleIDEntry{}
		processBundleIDs.values[bundle] = entry
	}
	processBundleIDs.Unlock()
	entry.once.Do(func() { entry.id = read(bundle) })
	return entry.id
}

// ---- bound methods --------------------------------------------------------

// WatchProcesses streams one ProcessSample every two seconds while the
// process page shows its CPU or memory tab and is not paused; the page
// closes the channel otherwise. The first sample comes after about a
// second, so CPU is measured over a real interval. Other accounts'
// processes are read through ps about every six seconds, and nothing is
// read after the channel closes. Off macOS it fails with "unsupported".
func (Cleaner) WatchProcesses(ctx context.Context, ch *mygo.Channel[ProcessSample]) error {
	if !processSupported() {
		return codeError{Code: "unsupported"}
	}
	return watchProcessesWith(ctx, ch.Send, realProcessSource())
}

// ForceQuitApp force-terminates the Dock apps at path, like Force Quit in
// the Dock menu: unsaved work is lost. The process page offers it only
// after QuitApp left the app running and the user confirmed. Matching,
// exclusions (Finder, Cleanzo) and results are QuitApp's.
func (Cleaner) ForceQuitApp(path string) (bool, error) {
	if !processSupported() {
		return false, codeError{Code: "unsupported"}
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimRight(path, string(filepath.Separator))), ".app") {
		return false, codeError{Code: "not-app"}
	}
	return quitAppWith(path, workspaceList, workspaceForceTerminate)
}

// ---- the sampling loop ----------------------------------------------------

// watchProcessesWith runs the loop against an injected sender and source.
func watchProcessesWith(ctx context.Context, send func(ProcessSample) error, src processSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := src.now
	if now == nil {
		now = time.Now
	}
	prevAt := now()
	prev := snapshotProcs(src.procs())
	prevBusy, prevTotal := src.cpu()
	others := src.others(ctx)
	othersFailures := 0
	if err := sleepCtx(ctx, processFirstDelay); err != nil {
		return err
	}
	bundleID := func(bundle string) string {
		return processBundleID(bundle, src.bundleID)
	}
	tick := time.NewTicker(processInterval)
	defer tick.Stop()
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n > 0 && n%processOthersEvery == 0 {
			if refreshed := src.others(ctx); refreshed != nil {
				others = refreshed
				othersFailures = 0
			} else {
				othersFailures++
				if othersFailures >= 2 {
					others = nil
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		curAt := now()
		elapsed := curAt.Sub(prevAt)
		cur := snapshotProcs(src.procs())
		busy, total := src.cpu()
		prevAt = curAt
		sample := ProcessSample{
			Rows:     groupProcesses(prev, cur, others, src.apps(), src.responsible, src.system, bundleID, elapsed),
			Pressure: src.pressure(),
			Own:      len(cur),
			Others:   len(others),
		}
		if total > prevTotal && prevTotal > 0 && busy >= prevBusy {
			sample.CPU = clampPct(float64(busy-prevBusy) / float64(total-prevTotal) * 100)
		}
		if err := send(sample); err != nil {
			return err
		}
		prev, prevBusy, prevTotal = cur, busy, total
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// snapshotProcs keys this user's readings by pid for the CPU deltas.
func snapshotProcs(procs []procReading) map[int32]procReading {
	out := make(map[int32]procReading, len(procs))
	for _, r := range procs {
		out[r.Pid] = r
	}
	return out
}

// ---- grouping -------------------------------------------------------------

// processGroup accumulates one row while grouping.
type processGroup struct {
	row      ProcessRow
	children map[string]*ProcessChild
}

// groupProcesses turns one reading into the page's rows:
//   - a process inside an .app belongs to that app's row, whatever account
//     runs it (Microsoft Defender's root daemon joins Microsoft Defender);
//   - this user's process that ships with the OS or is an XPC service, and
//     whose responsible process is inside an .app, works for that app and
//     joins its row (WebKit's web content processes);
//   - this user's other processes that ship with the OS get a system row by
//     name, and everything else gets a command-line row by name: a node or
//     yes started in Terminal is its own row, not Terminal's;
//   - another account's process outside any .app gets a system row by name.
//
// This user's CPU is the change since prev over elapsed; a new or reused
// pid counts none this round. Other accounts' numbers are ps's, marked
// estimated. Rows come ordered by key, children by name.
func groupProcesses(prev, cur map[int32]procReading, others []otherProc, apps []workspaceApp, responsible func(int32) int32, system func(string) bool, bundleID func(string) string, elapsed time.Duration) []ProcessRow {
	workspaceIDs := make(map[string]string, len(apps))
	for _, app := range apps {
		if app.Path != "" && app.BundleID != "" {
			workspaceIDs[strings.ToLower(app.Path)] = app.BundleID
		}
	}
	groups := map[string]*processGroup{}
	add := func(key, kind, bundle, name string, cpu float64, memory uint64, estimated bool) {
		g := groups[key]
		if g == nil {
			g = &processGroup{row: ProcessRow{Key: key, Kind: kind, Path: bundle}, children: map[string]*ProcessChild{}}
			groups[key] = g
		}
		g.row.CPU += cpu
		g.row.Memory += memory
		g.row.Procs++
		g.row.Estimated = g.row.Estimated || estimated
		c := g.children[name]
		if c == nil {
			c = &ProcessChild{Name: name}
			g.children[name] = c
		}
		c.Count++
		c.CPU += cpu
		c.Memory += memory
		c.Estimated = c.Estimated || estimated
	}
	for pid, r := range cur {
		if r.Path == "" {
			continue
		}
		cpu := 0.0
		if p, ok := prev[pid]; ok && p.Start == r.Start && elapsed > 0 {
			if now, was := r.UserNs+r.SysNs, p.UserNs+p.SysNs; now > was {
				cpu = float64(now-was) / float64(elapsed) * 100
			}
		}
		key, kind, bundle := ownProcessKey(r, cur, responsible, system)
		add(key, kind, bundle, processName(r.Path, r.Name, r.Pid), cpu, r.Footprint, false)
	}
	for _, o := range others {
		key, kind, bundle := otherProcessKey(o)
		add(key, kind, bundle, processName(o.Path, o.Name, o.Pid), o.CPU, o.Memory, true)
	}
	rows := make([]ProcessRow, 0, len(groups))
	for _, g := range groups {
		row := g.row
		if row.Kind == "app" {
			row.Name = displayName(row.Key, row.Path, apps, nil)
			id := workspaceIDs[strings.ToLower(row.Path)]
			if id == "" {
				id = bundleID(row.Path)
			}
			row.BundleID = strings.ToLower(id)
			row.Quit = canQuit(row.Path, apps)
		} else {
			row.Name = row.Key[strings.IndexByte(row.Key, ':')+1:]
		}
		row.Children = make([]ProcessChild, 0, len(g.children))
		for _, c := range g.children {
			row.Children = append(row.Children, *c)
		}
		sort.Slice(row.Children, func(i, j int) bool { return row.Children[i].Name < row.Children[j].Name })
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

// ownProcessKey places one of this user's processes; see groupProcesses.
func ownProcessKey(r procReading, cur map[int32]procReading, responsible func(int32) int32, system func(string) bool) (key, kind, bundle string) {
	if b := outermostBundle(r.Path); b != "" {
		return "app:" + strings.ToLower(b), "app", b
	}
	sys := r.Path != "" && system(r.Path)
	if sys || strings.Contains(r.Path, ".xpc"+string(filepath.Separator)) {
		if rpid := responsible(r.Pid); rpid > 0 && rpid != r.Pid {
			if rp, ok := cur[rpid]; ok {
				if b := outermostBundle(rp.Path); b != "" {
					return "app:" + strings.ToLower(b), "app", b
				}
			}
		}
	}
	if sys {
		return "sys:" + processName(r.Path, r.Name, r.Pid), "system", ""
	}
	return "cli:" + processName(r.Path, r.Name, r.Pid), "cli", ""
}

// otherProcessKey places another account's process: its .app's row, else a
// system row by name.
func otherProcessKey(o otherProc) (key, kind, bundle string) {
	if b := outermostBundle(o.Path); b != "" {
		return "app:" + strings.ToLower(b), "app", b
	}
	return "sys:" + processName(o.Path, o.Name, o.Pid), "system", ""
}

// processName prefers the executable's full file name, unless the kernel
// name identifies a differently named target (such as a versioned binary).
func processName(path, name string, pid int32) string {
	if path == "" {
		if name != "" {
			return name
		}
		return "pid " + strconv.Itoa(int(pid))
	}
	base := filepath.Base(path)
	if name != "" && !strings.HasPrefix(base, name) {
		return name
	}
	return base
}

// ---- other accounts through ps --------------------------------------------

// parsePsOutput reads `ps -ax -o pid=,uid=,pcpu=,rss=,ucomm=` and keeps
// the processes of accounts other than self. The short name is the last
// column and may contain spaces; malformed lines are skipped.
func parsePsOutput(out []byte, self uint32) []otherProc {
	var procs []otherProc
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields, path := psFields(sc.Text(), 4)
		if len(fields) < 4 || path == "" {
			continue
		}
		pid, errPid := strconv.ParseInt(fields[0], 10, 32)
		uid, errUID := strconv.ParseUint(fields[1], 10, 32)
		cpu, errCPU := strconv.ParseFloat(fields[2], 64)
		rss, errRSS := strconv.ParseUint(fields[3], 10, 64)
		if errPid != nil || errUID != nil || errCPU != nil || errRSS != nil || uint32(uid) == self {
			continue
		}
		procs = append(procs, otherProc{Pid: int32(pid), UID: uint32(uid), CPU: cpu, Memory: rss * 1024, Path: path, Name: path})
	}
	return procs
}

// psFields splits off the first n whitespace-separated fields of a ps line
// and returns the rest with its inner spaces kept.
func psFields(line string, n int) ([]string, string) {
	fields := make([]string, 0, n)
	rest := strings.TrimLeft(line, " \t")
	for len(fields) < n && rest != "" {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return append(fields, rest), ""
		}
		fields = append(fields, rest[:i])
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	return fields, strings.TrimRight(rest, " \t\r")
}
