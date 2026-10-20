package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// toolDirs offers no tools until Windows command execution is implemented.
var toolDirs = func() []string { return nil }

// psCommands returns the readable commands from one current-session snapshot.
var psCommands = func() ([]byte, error) {
	procs, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, p := range procs {
		lines = append(lines, p.CmdLine)
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// findTool disables cleanup commands until Task 13 implements Windows tool lookup.
func findTool(name string) string { return "" }

// toolPath supplies no executable search path to unsupported commands.
func toolPath(tool string) string { return "PATH=" }

// runToolCmd refuses to start a process until Task 13 implements tree cancellation.
func runToolCmd(cmd *exec.Cmd) ([]byte, error) { return nil, errUnsupported }

// snapshotProcesses allows tests to simulate an unavailable process probe.
var snapshotProcesses = processSnapshot

// createProcessSnapshot allows tests to exercise native snapshot failure propagation.
var createProcessSnapshot = windows.CreateToolhelp32Snapshot

// processSnapshot enumerates processes, retaining partial metadata for unreadable current-session entries.
func processSnapshot() ([]procInfo, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return nil, err
	}
	snapshot, err := createProcessSnapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var procs []procInfo
	for {
		var current uint32
		sessionErr := windows.ProcessIdToSessionId(entry.ProcessID, &current)
		// An unknown session is retained conservatively; a known other session is excluded.
		if sessionErr != nil || current == session {
			p := procInfo{Pid: entry.ProcessID, PPid: entry.ParentProcessID, Name: windows.UTF16ToString(entry.ExeFile[:])}
			h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.Pid)
			if err == nil {
				buf := make([]uint16, 32768)
				n := uint32(len(buf))
				if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
					p.Exe = windows.UTF16ToString(buf[:n])
				}
				p.CmdLine = processCommandLine(h)
				var created, exited, kernel, user windows.Filetime
				if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
					p.Created = time.Unix(0, created.Nanoseconds())
				}
				windows.CloseHandle(h)
			}

			procs = append(procs, p)
		}
		err = windows.Process32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return procs, nil
}

// processCommandLine reads ProcessCommandLineInformation using x/sys's UNICODE_STRING layout.
func processCommandLine(h windows.Handle) string {
	var size uint32
	windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, nil, 0, &size)
	if size < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) || size > 1<<20 {
		return ""
	}
	// uint64 storage keeps UNICODE_STRING naturally aligned on both supported architectures.
	storage := make([]uint64, (size+7)/8)
	if windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&storage[0]), size, &size) != nil {
		return ""
	}
	text := (*windows.NTUnicodeString)(unsafe.Pointer(&storage[0]))
	start, end := uintptr(unsafe.Pointer(&storage[0])), uintptr(unsafe.Pointer(&storage[0]))+uintptr(len(storage)*8)
	ptr := uintptr(unsafe.Pointer(text.Buffer))
	if text.Length%2 != 0 || text.Length > text.MaximumLength || ptr < start || ptr > end || uintptr(text.Length) > end-ptr {
		return ""
	}
	command := windows.UTF16ToString(unsafe.Slice(text.Buffer, int(text.Length)/2))
	runtime.KeepAlive(storage)
	return command
}

// readProcessProduct permits tests to count version-resource I/O independently of process enumeration.
var readProcessProduct = versionProduct

// productCache remembers resource names until the executable changes.
var productCache = struct {
	sync.Mutex
	entries map[string]productRecord
}{entries: map[string]productRecord{}}

// productRecord is one version-resource result at an executable modification time.
type productRecord struct {
	modified time.Time
	product  string
}

// processProduct lazily caches a product name by executable path and modification time.
func processProduct(exe string) string {
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	key := strings.ToLower(filepath.Clean(exe))
	productCache.Lock()
	defer productCache.Unlock()
	if record, ok := productCache.entries[key]; ok && record.modified.Equal(info.ModTime()) {
		return record.product
	}
	product := readProcessProduct(exe)
	productCache.entries[key] = productRecord{modified: info.ModTime(), product: product}
	return product
}

// versionProduct reads FileDescription, then ProductName, in any declared version-resource language.
func versionProduct(exe string) string {
	size, err := windows.GetFileVersionInfoSize(exe, nil)
	if err != nil || size == 0 || size > 16<<20 {
		return ""
	}
	data := make([]byte, size)
	if windows.GetFileVersionInfo(exe, 0, size, unsafe.Pointer(&data[0])) != nil {
		return ""
	}
	var translations *uint16
	var n uint32
	if windows.VerQueryValue(unsafe.Pointer(&data[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&translations), &n) != nil || n < 4 || n > size {
		return ""
	}
	langs := unsafe.Slice(translations, int(n)/2)
	for _, key := range []string{"FileDescription", "ProductName"} {
		for i := 0; i+1 < len(langs); i += 2 {
			var value *uint16
			var length uint32
			query := fmt.Sprintf(`\StringFileInfo\%04x%04x\%s`, langs[i], langs[i+1], key)
			if windows.VerQueryValue(unsafe.Pointer(&data[0]), query, unsafe.Pointer(&value), &length) == nil && value != nil && length > 0 && length <= size/2 {
				if text := strings.TrimSpace(windows.UTF16ToString(unsafe.Slice(value, int(length)))); text != "" {
					return text
				}
			}
		}
	}
	return ""
}
