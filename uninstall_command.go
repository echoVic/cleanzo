package main

import (
	"regexp"
	"strings"
)

// uninstallCommand separates the executable from its native Windows parameters.
type uninstallCommand struct {
	exe  string
	args string
}

// msiProduct matches only MSI product-code operations, never arbitrary /I words.
var msiProduct = regexp.MustCompile(`(?i)(^|[\t ])/[ix](\s*)(\{[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\})([\t ]|$)`)

// rundllEntry accepts a DLL and exported entry point, excluding script protocols.
var rundllEntry = regexp.MustCompile(`(?i)^(?:"([^"\r\n]+\.dll)"|([^",\r\n]+\.dll)),[a-z_#][a-z0-9_@#]*(?:[\t ]|$)`)

// windowsAbsoluteExe recognizes drive-absolute paths without host filepath semantics.
func windowsAbsoluteExe(path string) bool {
	return len(path) > 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' && (path[2] == '\\' || path[2] == '/') && !strings.ContainsAny(path[2:], `:"`) && strings.HasSuffix(strings.ToLower(path), ".exe")
}

// uninstallExeName takes a Windows basename on any test host.
func uninstallExeName(path string) string {
	return strings.ToLower(path[strings.LastIndexAny(path, `\/`)+1:])
}

// parseUninstallString parses only native executable commands, without shell evaluation.
// exists is injected so ambiguous unquoted paths can be tested on every host.
func parseUninstallString(raw string, exists func(string) bool) (uninstallCommand, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return uninstallCommand{}, errBlocked
	}
	var cmd uninstallCommand
	if raw[0] == '"' {
		end := strings.IndexByte(raw[1:], '"') + 1
		if end < 1 || (end+1 < len(raw) && raw[end+1] != ' ' && raw[end+1] != '\t') {
			return cmd, errBlocked
		}
		cmd = uninstallCommand{exe: raw[1:end], args: strings.TrimSpace(raw[end+1:])}
	} else {
		// Prefer the longest existing .exe prefix, rather than executing C:\Program.exe.
		lower := strings.ToLower(raw)
		for offset := 0; offset < len(raw); {
			i := strings.Index(lower[offset:], ".exe")
			if i < 0 {
				break
			}
			end := offset + i + 4
			prefix := raw[:end]
			if (end == len(raw) || raw[end] == ' ' || raw[end] == '\t') &&
				((windowsAbsoluteExe(prefix) && exists(prefix)) || strings.EqualFold(prefix, "msiexec.exe") || strings.EqualFold(prefix, "rundll32.exe")) {
				cmd = uninstallCommand{exe: prefix, args: strings.TrimSpace(raw[end:])}
			}
			offset = end
		}
	}
	name := uninstallExeName(cmd.exe)
	absolute := windowsAbsoluteExe(cmd.exe)
	if cmd.exe == "" || (absolute && !exists(cmd.exe)) || (!absolute && name != "msiexec.exe" && name != "rundll32.exe") ||
		(!absolute && cmd.exe != name && !strings.EqualFold(cmd.exe, name)) {
		return uninstallCommand{}, errBlocked
	}
	switch name {
	case "cmd.exe", "powershell.exe", "pwsh.exe", "wscript.exe", "cscript.exe", "mshta.exe", "bash.exe", "sh.exe":
		return uninstallCommand{}, errBlocked
	case "msiexec.exe":
		loc := msiProduct.FindStringSubmatchIndex(cmd.args)
		if loc == nil {
			return uninstallCommand{}, errBlocked
		}
		// The match starts with a boundary captured before the operation.
		op := loc[3]
		if cmd.args[op+1] == 'i' || cmd.args[op+1] == 'I' {
			cmd.args = cmd.args[:op+1] + "X" + cmd.args[op+2:]
		}
	case "rundll32.exe":
		match := rundllEntry.FindStringSubmatch(cmd.args)
		if match == nil {
			return uninstallCommand{}, errBlocked
		}
		dll := match[1]
		if dll == "" {
			dll = match[2]
		}
		if strings.Contains(dll, ":") {
			if !windowsAbsoluteExe(dll[:len(dll)-4]+".exe") || !exists(dll) {
				return uninstallCommand{}, errBlocked
			}
		} else if strings.ContainsAny(dll, `\/`) {
			return uninstallCommand{}, errBlocked
		}
	}
	return cmd, nil
}
