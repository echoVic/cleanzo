package main

import (
	"context"
	"os"
	"strings"
	"time"
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

// realProcessSource wires the loop to the native reads: libproc for this
// user's processes, the setuid /bin/ps for everyone else's.
func realProcessSource() processSource {
	return processSource{
		procs:       nativeProcs,
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
	argv := []string{"/bin/ps", "-ax", "-o", "pid=,uid=,pcpu=,rss=,ucomm="}
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
