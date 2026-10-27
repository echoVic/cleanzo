package main

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// processSupported reports whether the process page can sample: macOS only
// in this version.
func processSupported() bool { return true }

// processPsTimeout bounds one ps run.
var processPsTimeout = 3 * time.Second

// processPsSpawn runs ps. Tests point it at a stand-in.
var processPsSpawn = spawnOutput

// Tests replace path lookups independently of the ps command.
var processPidPath = nativePidPath

// processIdentityAge is how many readings a remembered path and name are
// kept before they're read again, as a backstop for identity changes the
// executable UUID does not distinguish; 15 readings is 30 s.
const processIdentityAge = 15

// processOwnReadings reads this user's processes. Tests replace it.
var processOwnReadings = nativeProcsKnown

// ownProcsReader returns this user's processes for one sampling session.
// It remembers nonempty paths and their names by pid, start time and
// executable UUID, and reads them again after processIdentityAge readings.
// The loop calls it from one goroutine.
func ownProcsReader() func() []procReading {
	type key struct {
		pid   int32
		start uint64
		uuid  [16]byte
	}
	type entry struct {
		path, name string
		read       int // the reading that looked them up
	}
	known := map[key]entry{}
	n := 0
	return func() []procReading {
		n++
		readings := processOwnReadings(func(pid int32, start uint64, uuid [16]byte) (string, string, bool) {
			e, ok := known[key{pid, start, uuid}]
			return e.path, e.name, ok && n-e.read < processIdentityAge
		})
		next := make(map[key]entry, len(readings))
		for _, r := range readings {
			if r.Path == "" {
				continue
			}
			k := key{r.Pid, r.Start, r.UUID}
			e, ok := known[k]
			if !ok || n-e.read >= processIdentityAge {
				e = entry{path: r.Path, name: r.Name, read: n}
			}
			next[k] = e
		}
		known = next
		return readings
	}
}

// processAppsEvery is how often, in readings, NSWorkspace's running-app list
// is read again. With about 320 running apps a read took about 25 ms of
// main-thread time (in-app profile, 2026-10-27), about 1.3% of a core at one
// read per 2 s reading, and the list rarely changes. A just-launched app gets
// its name, bundle ID and quit button within 10 s; quitting reads its own
// fresh list.
const processAppsEvery = 5

// processAppList reads the running apps. Tests replace it.
var processAppList = workspaceList

// appsReader returns the running-app list for one sampling session: read at
// the first reading and every processAppsEvery-th after it, and retried at
// the next reading when a read fails (nil). The loop calls it from one
// goroutine.
func appsReader() func() []workspaceApp {
	var apps []workspaceApp
	age := processAppsEvery // the first call reads
	return func() []workspaceApp {
		if age >= processAppsEvery || apps == nil {
			if fresh := processAppList(); fresh != nil {
				apps, age = fresh, 0
			}
		}
		age++
		return apps
	}
}

// realProcessSource wires the loop to the native reads: libproc for this
// user's processes, the setuid /bin/ps for everyone else's.
func realProcessSource() processSource {
	return processSource{
		procs:       ownProcsReader(),
		others:      otherProcsReader(),
		apps:        appsReader(),
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		bundleID:    func(bundle string) string { return readAppIdentity(bundle).bid },
		pressure:    func() uint64 { return nativeSysctlUint64("kern.memorystatus_vm_pressure_level") },
		cpu:         cpuBusyTotal,
	}
}

// otherPath is the full path proc_pidpath gave for a pid that ps listed
// under name.
type otherPath struct{ name, path string }

// otherProcsReader returns the other-accounts reader for one sampling
// session. It remembers each pid's full path while ps keeps listing that
// pid under the same short name, so proc_pidpath runs once per process
// rather than on every reading. A failed ps keeps the memory. The loop calls
// it from one goroutine.
func otherProcsReader() func(context.Context) []otherProc {
	var paths map[int32]otherPath
	listAll := false
	return func(ctx context.Context) []otherProc {
		var uids []uint32
		if !listAll {
			var err error
			uids, err = processForeignUIDs()
			if err != nil {
				uids = nil // ps -ax still applies the uid filter below
			}
		}
		others, next := readOtherProcsWith(ctx, uids, paths)
		if others == nil && len(uids) > 0 && ctx.Err() == nil {
			// A process can run under a uid without an account. ps -u rejects
			// the whole list: e.g. "ps: No user name or ID matches '4242424'".
			// Retry immediately with -ax and keep using it for this session.
			listAll = true
			others, next = readOtherProcsWith(ctx, nil, paths)
		}
		if others != nil {
			paths = next
		}
		return others
	}
}

// processForeignUIDs lists the effective uids of other accounts' processes.
// Tests replace it.
var processForeignUIDs = foreignUIDs

// foreignUIDs returns, sorted, every effective uid other than this user's
// that a process runs as: the processes libproc can't read. One sysctl
// (kern.proc.all) of about 0.4 ms.
func foreignUIDs() ([]uint32, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	me := uint32(os.Geteuid())
	seen := map[uint32]bool{}
	var out []uint32
	for _, p := range procs {
		if u := p.Eproc.Ucred.Uid; u != me && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	slices.Sort(out)
	return out, nil
}

// psArgs is the ps command for the accounts in uids: -u by effective uid,
// which costs about a third of -ax because ps then inspects only those
// accounts' processes. Without uids it falls back to -ax.
func psArgs(uids []uint32) []string {
	args := []string{"/bin/ps", "-o", "pid=,uid=,pcpu=,rss=,ucomm="}
	if len(uids) == 0 {
		return append(args, "-ax")
	}
	list := make([]string, len(uids))
	for i, u := range uids {
		list[i] = strconv.FormatUint(uint64(u), 10)
	}
	return append(args, "-u", strings.Join(list, ","))
}

// readOtherProcsWith runs ps, which is setuid root and so can report every
// account's processes; libproc refuses other accounts' (EPERM). The
// numbers are ps's, a decaying CPU average and resident memory, hence
// estimates. ps only supplies short names; libproc supplies full UTF-8
// paths, reused from known for a pid still listed under the same name.
// uids selects effective uids; nil lists all accounts with -ax.
// en_US.UTF-8 keeps a decimal dot and readable fallback names. It returns
// the reading and the paths to remember; nil when ps failed.
func readOtherProcsWith(ctx context.Context, uids []uint32, known map[int32]otherPath) ([]otherProc, map[int32]otherPath) {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	argv := psArgs(uids)
	out, self, err := processPsSpawn(ctx, argv[0], argv, withEnv(os.Environ(), "LC_ALL=en_US.UTF-8"))
	if ctx.Err() != nil || err != nil {
		return nil, nil
	}
	procs := parsePsOutput(out, uint32(os.Getuid()))
	// A non-nil empty slice is a successful read with no other processes;
	// nil means unavailable and tells the loop to keep its last reading.
	others := make([]otherProc, 0, len(procs))
	paths := make(map[int32]otherPath, len(procs))
	for _, p := range procs {
		if p.Pid == self { // ps itself: setuid, so listed as root
			continue
		}
		seen, ok := known[p.Pid]
		if !ok || seen.name != p.Name {
			seen = otherPath{name: p.Name, path: processPidPath(p.Pid)}
		}
		paths[p.Pid] = seen
		if seen.path != "" {
			p.Path = seen.path
		}
		others = append(others, p)
	}
	return others, paths
}

// systemExecutable reports programs that ship with macOS: daemons, agents
// and framework services. Command-line tools in /bin and /usr/bin are not
// counted, so a shell or yes started in Terminal gets a command-line row.
func systemExecutable(path string) bool {
	for _, prefix := range []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/", "/Library/Apple/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
