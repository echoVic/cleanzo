package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

const mcpConfigLimit = 12 << 20

var mcpWriteMu sync.Mutex
var mcpNames = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)

type mcpClient struct{ ID, Path, Field string }

type MCPConfig struct {
	Tool      string      `json:"tool"`
	Path      string      `json:"path"`
	Revision  string      `json:"revision"`
	Exists    bool        `json:"exists"`
	ReadOnly  bool        `json:"readOnly"`
	CanToggle bool        `json:"canToggle"`
	HasBackup bool        `json:"hasBackup"`
	Error     string      `json:"error"`
	Servers   []MCPServer `json:"servers"`
}

type MCPServer struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Endpoint  string   `json:"endpoint"`
	Enabled   bool     `json:"enabled"`
	Issue     string   `json:"issue"`
	AlsoIn    []string `json:"alsoIn"`
}

type MCPChange struct {
	Tool        string   `json:"tool"`
	Revision    string   `json:"revision"`
	Action      string   `json:"action"`
	Name        string   `json:"name"`
	Transport   string   `json:"transport"`
	Endpoint    string   `json:"endpoint"`
	Args        []string `json:"args"`
	ReplaceArgs bool     `json:"replaceArgs"`
	Enabled     bool     `json:"enabled"`
}

func mcpClients() []mcpClient {
	h := homeDir()
	if h == "" {
		return nil
	}
	codex := os.Getenv("CODEX_HOME")
	if !filepath.IsAbs(codex) {
		codex = filepath.Join(h, ".codex")
	}
	gemini := os.Getenv("GEMINI_CLI_HOME")
	if !filepath.IsAbs(gemini) {
		gemini = h
	}
	homeConfig := os.Getenv("OPENCODE_CONFIG_DIR")
	if !filepath.IsAbs(homeConfig) {
		base := os.Getenv("XDG_CONFIG_HOME")
		if !filepath.IsAbs(base) {
			base = filepath.Join(h, ".config")
		}
		homeConfig = filepath.Join(base, "opencode")
	}
	clients := []mcpClient{
		{"claude", filepath.Join(h, ".claude.json"), "mcpServers"},
		{"codex", filepath.Join(codex, "config.toml"), "mcp_servers"},
		{"gemini", filepath.Join(gemini, ".gemini", "settings.json"), "mcpServers"},
	}
	if custom := os.Getenv("OPENCODE_CONFIG"); filepath.IsAbs(custom) {
		if filepath.Ext(custom) == ".jsonc" || filepath.Ext(custom) == ".json" {
			return append(clients, mcpClient{"opencode", custom, "mcp"})
		}
	}
	regular, commented := filepath.Join(homeConfig, "opencode.json"), filepath.Join(homeConfig, "opencode.jsonc")
	if fileExists(commented) {
		if fileExists(regular) {
			clients = append(clients, mcpClient{"opencode-json", regular, "mcp"})
		}
		return append(clients, mcpClient{"opencode-jsonc", commented, "mcp"})
	}
	return append(clients, mcpClient{"opencode", regular, "mcp"})
}

func (s mcpClient) codex() bool    { return s.ID == "codex" }
func (s mcpClient) opencode() bool { return strings.HasPrefix(s.ID, "opencode") }
func (s mcpClient) backupPath() string {
	sum := sha256.Sum256([]byte(filepath.Clean(s.Path)))
	return filepath.Join(filepath.Dir(configPath()), "mcp-backups", s.ID+"-"+hex.EncodeToString(sum[:8])+".bak")
}

func readMCPConfig(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return nil, nil, errors.New("unsupported-file")
	}
	target, err := os.Stat(path)
	if err != nil || !target.Mode().IsRegular() {
		return nil, nil, errors.New("unsupported-file")
	}
	if target.Size() > mcpConfigLimit {
		return nil, nil, errors.New("config-too-large")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, mcpConfigLimit+1))
	if len(body) > mcpConfigLimit {
		return nil, nil, errors.New("config-too-large")
	}
	return body, info, err
}

func mcpRevision(data []byte, exists bool) string {
	if !exists {
		return "missing"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseMCPConfig(client mcpClient, raw []byte) (map[string]any, error) {
	root := map[string]any{}
	if client.codex() {
		if err := toml.Unmarshal(raw, &root); err != nil {
			return nil, errors.New("invalid-config")
		}
	} else {
		if len(raw) == 0 {
			raw = []byte(`{}`)
		}
		value, err := hujson.Parse(bytes.Clone(raw))
		if err != nil || !mcpUniqueJSONKeys(&value) {
			return nil, errors.New("invalid-config")
		}
		value.Standardize()
		if err := decodeMCPJSON(value.Pack(), &root); err != nil || root == nil {
			return nil, errors.New("invalid-config")
		}
	}
	value, exists := root[client.Field]
	if !exists {
		return map[string]any{}, nil
	}
	servers, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid-server-list")
	}
	if client.opencode() {
		if nested, ok := openCodeMCPServers(servers); ok {
			for name, entry := range servers {
				if legacy, ok := entry.(map[string]any); name != "servers" && ok && openCodeLegacyEntry(legacy) {
					return nil, errors.New("unsupported-layout")
				}
			}
			return nested, nil
		}
		if entry, exists := servers["servers"]; exists {
			legacy, ok := entry.(map[string]any)
			if !ok || !openCodeLegacyEntry(legacy) {
				return nil, errors.New("unsupported-layout")
			}
		}
		legacyCount := 0
		for _, value := range servers {
			if entry, ok := value.(map[string]any); ok && openCodeLegacyEntry(entry) {
				legacyCount++
			}
		}
		if legacyCount == 0 {
			return map[string]any{}, nil
		}
		if legacyCount != len(servers) {
			return nil, errors.New("unsupported-layout")
		}
	}
	return servers, nil
}

func mcpUniqueJSONKeys(value *hujson.Value) bool {
	unique := true
	value.Range(func(v *hujson.Value) bool {
		if object, ok := v.Value.(*hujson.Object); ok {
			keys := map[string]bool{}
			for _, member := range object.Members {
				name, ok := member.Name.Value.(hujson.Literal)
				if !ok || keys[name.String()] {
					unique = false
					return false
				}
				keys[name.String()] = true
			}
		}
		return true
	})
	return unique
}

func openCodeLegacyEntry(value map[string]any) bool {
	switch mcpString(value["type"]) {
	case "local", "remote":
		return true
	}
	_, override := value["enabled"].(bool)
	return override
}

func openCodeMCPServers(mcp map[string]any) (map[string]any, bool) {
	servers, ok := mcp["servers"].(map[string]any)
	return servers, ok && !openCodeLegacyEntry(servers)
}

func decodeMCPJSON(raw []byte, root *map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(root)
}

func mcpString(value any) string { text, _ := value.(string); return text }

func mcpCommand(client mcpClient, entry map[string]any) (command string, args []string) {
	values, _ := entry["args"].([]any)
	if client.opencode() {
		commandArgs, _ := entry["command"].([]any)
		if len(commandArgs) == 0 {
			return "", nil
		}
		command, values = mcpString(commandArgs[0]), commandArgs[1:]
	} else {
		command = mcpString(entry["command"])
	}
	for _, value := range values {
		args = append(args, mcpString(value))
	}
	return command, args
}

func mcpRemote(entry map[string]any) string {
	if endpoint := mcpString(entry["httpUrl"]); endpoint != "" {
		return endpoint
	}
	return mcpString(entry["url"])
}

func mcpExecutable(command string) bool {
	if strings.HasPrefix(command, "~/") || strings.HasPrefix(command, `~\`) {
		command = filepath.Join(homeDir(), command[2:])
	}
	if _, err := exec.LookPath(command); err == nil {
		return true
	}
	if filepath.Base(command) != command || runtime.GOOS == "windows" {
		return false
	}
	for _, dir := range []string{filepath.Join(homeDir(), ".local/bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if _, err := exec.LookPath(filepath.Join(dir, command)); err == nil {
			return true
		}
	}
	return false
}

func describeMCPServer(client mcpClient, name string, entry map[string]any) (MCPServer, string) {
	result := MCPServer{Name: name, Transport: "unknown", Enabled: entry["enabled"] != false && (!client.opencode() || entry["disabled"] != true), AlsoIn: []string{}}
	if command, args := mcpCommand(client, entry); command != "" {
		result.Transport, result.Endpoint = "stdio", filepath.Base(command)
		if !mcpExecutable(command) {
			result.Issue = "missing-command"
		}
		key, _ := json.Marshal(append([]string{command}, args...))
		return result, "stdio:" + string(key)
	}
	if address := mcpRemote(entry); address != "" {
		result.Transport = "http"
		u, err := url.Parse(address)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			result.Issue = "invalid-url"
			return result, ""
		}
		// URLs may contain auth in userinfo, paths, queries or fragments. Only
		// expose the origin while grouping by the configured address in memory.
		result.Endpoint = u.Scheme + "://" + u.Host
		return result, "remote:" + address
	}
	result.Issue = "unknown-transport"
	return result, ""
}

// ListMCPConfigs reports global MCP registrations and local command availability.
// It does not load credentials, invoke a server or include config bodies in the bridge.
func (Cleaner) ListMCPConfigs(ctx context.Context) []MCPConfig {
	configs := []MCPConfig{}
	type location struct{ config, server int }
	matches := map[string][]location{}
	for _, client := range mcpClients() {
		if ctx.Err() != nil {
			break
		}
		body, info, err := readMCPConfig(client.Path)
		row := MCPConfig{Tool: client.ID, Path: client.Path, Exists: info != nil, Revision: mcpRevision(body, info != nil), CanToggle: client.codex() || client.opencode(), HasBackup: fileExists(client.backupPath()), Servers: []MCPServer{}}
		if err != nil {
			row.Error = mcpReadError(err)
			configs = append(configs, row)
			continue
		}
		row.ReadOnly = info != nil && info.Mode()&os.ModeSymlink != 0
		entries, err := parseMCPConfig(client, body)
		if err != nil {
			row.Error = err.Error()
		}
		names := make([]string, 0, len(entries))
		for name := range entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			entry, _ := entries[name].(map[string]any)
			server, key := describeMCPServer(client, name, entry)
			if key != "" {
				matches[key] = append(matches[key], location{len(configs), len(row.Servers)})
			}
			row.Servers = append(row.Servers, server)
		}
		configs = append(configs, row)
	}
	for _, group := range matches {
		for _, where := range group {
			server := &configs[where.config].Servers[where.server]
			for _, other := range group {
				if other.config != where.config {
					tool := configs[other.config].Tool
					if !slices.Contains(server.AlsoIn, tool) {
						server.AlsoIn = append(server.AlsoIn, tool)
					}
				}
			}
		}
	}
	return configs
}

func mcpReadError(err error) string {
	if err.Error() == "config-too-large" || err.Error() == "unsupported-file" {
		return err.Error()
	}
	return "read-config"
}
