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
// kept before they're read again. A process can replace its program with
// exec and keep its pid and start time; 15 readings is 30 s.
const processIdentityAge = 15

// processOwnReadings reads this user's processes. Tests replace it.
var processOwnReadings = nativeProcsKnown

// ownProcsReader returns this user's processes for one sampling session.
// It remembers each process's path and name by pid and start time, and
// reads them again after processIdentityAge readings. The loop calls it
// from one goroutine.
func ownProcsReader() func() []procReading {
	type key struct {
		pid   int32
		start uint64
	}
	type entry struct {
		path, name string
		read       int // the reading that looked them up
	}
	known := map[key]entry{}
	n := 0
	return func() []procReading {
		n++
		readings := processOwnReadings(func(pid int32, start uint64) (string, string, bool) {
			e, ok := known[key{pid, start}]
			return e.path, e.name, ok && n-e.read < processIdentityAge
		})
		next := make(map[key]entry, len(readings))
		for _, r := range readings {
			k := key{r.Pid, r.Start}
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

// realProcessSource wires the loop to the native reads: libproc for this
// user's processes, the setuid /bin/ps for everyone else's.
func realProcessSource() processSource {
	return processSource{
		procs:       ownProcsReader(),
		others:      otherProcsReader(),
		apps:        workspaceList,
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
	return func(ctx context.Context) []otherProc {
		others, next := readOtherProcsWith(ctx, paths)
		if others != nil {
			paths = next
		}
		return others
	}
}

// readOtherProcs is one reading with nothing remembered.
func readOtherProcs(ctx context.Context) []otherProc {
	others, _ := readOtherProcsWith(ctx, nil)
	return others
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
// en_US.UTF-8 keeps a decimal dot and readable fallback names. It returns
// the reading and the paths to remember; nil when ps failed.
func readOtherProcsWith(ctx context.Context, known map[int32]otherPath) ([]otherProc, map[int32]otherPath) {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	uids, err := processForeignUIDs()
	if err != nil {
		uids = nil // ps -ax then lists everyone; the uid filter below still applies
	}
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
