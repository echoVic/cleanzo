package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// processSupported reports whether the process page can sample: macOS only
// in this version.
func processSupported() bool { return true }

// processPsTimeout bounds one ps run.
var processPsTimeout = 3 * time.Second

// Tests replace this command boundary without running the real ps.
var processPsCommand = exec.CommandContext

// realProcessSource wires the loop to the native reads: libproc for this
// user's processes, the setuid /bin/ps for everyone else's.
func realProcessSource() processSource {
	return processSource{
		procs:       nativeProcs,
		others:      readOtherProcs,
		apps:        workspaceList,
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		bundleID:    func(bundle string) string { return readAppIdentity(bundle).bid },
		pressure:    func() uint64 { return nativeSysctlUint64("kern.memorystatus_vm_pressure_level") },
		cpu:         cpuBusyTotal,
	}
}

// readOtherProcs runs ps, which is setuid root and so can report every
// account's processes; libproc refuses other accounts' (EPERM). The
// numbers are ps's, a decaying CPU average and resident memory, hence
// estimates. en_US.UTF-8 preserves Unicode paths and keeps a decimal dot.
func readOtherProcs(ctx context.Context) []otherProc {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	cmd := processPsCommand(ctx, "/bin/ps", "-axww", "-o", "pid=,uid=,pcpu=,rss=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	out, err := cmd.Output()
	if ctx.Err() != nil || err != nil {
		return nil
	}
	procs := parsePsOutput(out, uint32(os.Getuid()))
	// A non-nil empty slice is a successful read with no other processes;
	// nil means unavailable and tells the loop to keep its last reading.
	others := make([]otherProc, 0, len(procs))
	for _, p := range procs {
		if p.Pid != int32(cmd.Process.Pid) {
			others = append(others, p)
		}
	}
	return others
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
