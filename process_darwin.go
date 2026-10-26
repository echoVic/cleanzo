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
// estimates. LC_ALL=C keeps the decimal point a dot.
func readOtherProcs(ctx context.Context) []otherProc {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,uid=,pcpu=,rss=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if ctx.Err() != nil || (err != nil && len(out) == 0) {
		return nil
	}
	return parsePsOutput(out, uint32(os.Getuid()))
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
