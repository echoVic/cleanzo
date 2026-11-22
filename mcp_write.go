package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tailscale/hujson"
)

// ApplyMCPChange applies an explicit edit to a supported global configuration.
// The revision is required so an edit cannot overwrite a change made after the read.
func (Cleaner) ApplyMCPChange(ctx context.Context, change MCPChange) error {
	mcpWriteMu.Lock()
	defer mcpWriteMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, client := range mcpClients() {
		if client.ID == change.Tool {
			return applyMCPConfig(client, change, client.backupPath())
		}
	}
	return errors.New("unknown-tool")
}

func applyMCPConfig(client mcpClient, change MCPChange, backupPath string) error {
	raw, info, err := readMCPConfig(client.Path)
	if err != nil {
		return errors.New(mcpReadError(err))
	}
	if change.Revision != mcpRevision(raw, info != nil) {
		return errors.New("config-changed")
	}
	if info != nil && !info.Mode().IsRegular() {
		return errors.New("linked-config")
	}
	fallback := []byte("{}\n")
	if client.codex() {
		fallback = []byte{}
	}
	old := raw
	if info == nil {
		old = fallback
	}
	var next []byte
	if change.Action == "restore" {
		var backupInfo os.FileInfo
		var backup []byte
		backup, backupInfo, err = readMCPConfig(backupPath)
		if err != nil || backupInfo == nil || !backupInfo.Mode().IsRegular() {
			return errors.New("backup-unavailable")
		}
		next, err = restoreMCPEntries(client, old, backup)
	} else if client.codex() {
		next, err = mutateMCPTOML(old, change)
	} else {
		next, err = mutateMCPJSON(client, old, change)
	}
	if err != nil {
		return err
	}
	if len(next) > mcpConfigLimit {
		return errors.New("config-too-large")
	}
	if _, err := parseMCPConfig(client, next); err != nil {
		return err
	}
	if bytes.Equal(next, old) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		return errors.New("backup-unavailable")
	}
	backupStage, err := stageMCPFile(backupPath, old, 0o600)
	if err != nil {
		return errors.New("backup-unavailable")
	}
	removeBackupStage := true
	defer func() {
		if removeBackupStage {
			_ = os.Remove(backupStage)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(client.Path), 0o700); err != nil {
		return errors.New("write-config")
	}
	permission := os.FileMode(0o600)
	if info != nil {
		permission = info.Mode().Perm()
	}
	err = writeMCPFile(client.Path, next, permission, func() error {
		live, current, err := readMCPConfig(client.Path)
		if err != nil || !bytes.Equal(live, raw) || (info == nil) != (current == nil) || (info != nil && (!current.Mode().IsRegular() || !os.SameFile(info, current))) {
			return errors.New("config-changed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.Rename(backupStage, backupPath); err != nil {
		removeBackupStage = false
		return fmt.Errorf("saved config, backup kept at %s", backupStage)
	}
	return nil
}

func writeMCPFile(path string, data []byte, mode os.FileMode, validate func() error) error {
	staged, err := stageMCPFile(path, data, mode)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if validate != nil {
		if err := validate(); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, path); err != nil {
		return errors.New("write-config")
	}
	return nil
}

func stageMCPFile(path string, data []byte, mode os.FileMode) (string, error) {
	staged, err := os.CreateTemp(filepath.Dir(path), ".cleanzo-mcp-*")
	if err != nil {
		return "", errors.New("write-config")
	}
	if err := staged.Chmod(mode); err != nil {
		staged.Close()
		os.Remove(staged.Name())
		return "", errors.New("write-config")
	}
	_, err = staged.Write(data)
	if err == nil {
		err = staged.Sync()
	}
	if closeErr := staged.Close(); err != nil || closeErr != nil {
		os.Remove(staged.Name())
		return "", errors.New("write-config")
	}
	return staged.Name(), nil
}

func mcpChangedFields(client mcpClient, servers map[string]any, change MCPChange) (map[string]any, error) {
	previous, exists := servers[change.Name]
	old, _ := previous.(map[string]any)
	if change.Action == "add" {
		if !mcpNames.MatchString(change.Name) {
			return nil, errors.New("invalid-name")
		}
		if exists {
			return nil, errors.New("name-exists")
		}
	} else if !exists {
		return nil, errors.New("server-missing")
	}
	fields := map[string]any{}
	switch change.Action {
	case "remove":
		return fields, nil
	case "toggle":
		if !client.codex() && !client.opencode() {
			return nil, errors.New("unsupported-change")
		}
		fields["enabled"] = change.Enabled
		return fields, nil
	case "add", "edit":
	default:
		return nil, errors.New("unsupported-change")
	}
	if change.Action == "edit" {
		server, _ := describeMCPServer(client, change.Name, old)
		if server.Transport != change.Transport {
			return nil, errors.New("unsupported-change")
		}
	}
	endpoint := strings.TrimSpace(change.Endpoint)
	if len(endpoint) > 8192 || strings.ContainsAny(endpoint, "\x00\r\n") || (change.Action == "add" && endpoint == "") {
		return nil, errors.New("invalid-endpoint")
	}
	switch change.Transport {
	case "stdio":
		if command, _ := mcpCommand(client, old); change.Action == "edit" && endpoint != "" && endpoint != command && hasMCPAuth(old, "env", "environment", "env_vars") {
			return nil, errors.New("credential-destination")
		}
		if len(change.Args) > 256 {
			return nil, errors.New("invalid-arguments")
		}
		for _, arg := range change.Args {
			if strings.ContainsRune(arg, 0) || len(arg) > 8192 {
				return nil, errors.New("invalid-arguments")
			}
		}
		if client.opencode() {
			if change.Action == "add" {
				fields["type"] = "local"
			}
			command, args := mcpCommand(client, old)
			if endpoint != "" {
				command = endpoint
			}
			if change.ReplaceArgs || change.Action == "add" {
				args = change.Args
			}
			if endpoint != "" || change.ReplaceArgs {
				fields["command"] = append([]string{command}, args...)
			}
		} else {
			if endpoint != "" {
				fields["command"] = endpoint
				if change.Action == "add" && client.ID == "claude" {
					fields["type"] = "stdio"
				}
			}
			if change.ReplaceArgs || len(change.Args) > 0 && change.Action == "add" {
				fields["args"] = append([]string{}, change.Args...)
			}
		}
	case "http":
		if endpoint != "" {
			probe, _ := describeMCPServer(client, "", map[string]any{"url": endpoint})
			if probe.Issue != "" {
				return nil, errors.New("invalid-endpoint")
			}
			if change.Action == "edit" && mcpHTTPOrigin(mcpRemote(old)) != mcpHTTPOrigin(endpoint) && hasMCPAuth(old, "headers", "httpHeaders", "http_headers", "env_http_headers", "bearer_token_env_var", "oauth", "oauth_resource", "auth") {
				return nil, errors.New("credential-destination")
			}
			field := "url"
			if client.ID == "gemini" && (change.Action == "add" || mcpString(old["httpUrl"]) != "") {
				field = "httpUrl"
			}
			fields[field] = endpoint
			if change.Action == "add" {
				switch client.ID {
				case "claude":
					fields["type"] = "http"
				}
				if client.opencode() {
					fields["type"] = "remote"
				}
			}
		}
	default:
		return nil, errors.New("unsupported-change")
	}
	return fields, nil
}

func hasMCPAuth(entry map[string]any, fields ...string) bool {
	for _, key := range fields {
		if value, exists := entry[key]; exists && value != nil && value != false && value != "" {
			return true
		}
	}
	return false
}

func mcpHTTPOrigin(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return strings.ToLower(u.Scheme+"://"+u.Hostname()) + ":" + port
}

func pointerMCP(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(raw, "~", "~0"), "/", "~1")
}

func restoreMCPEntries(client mcpClient, raw, backup []byte) ([]byte, error) {
	entries, err := parseMCPConfig(client, backup)
	if err != nil {
		return nil, errors.New("backup-unavailable")
	}
	if client.codex() {
		return restoreMCPTOML(raw, entries)
	}
	value, err := hujson.Parse(bytes.Clone(raw))
	if err != nil {
		return nil, errors.New("invalid-config")
	}
	path := "/" + pointerMCP(client.Field)
	var patchOperations []map[string]any
	if client.opencode() && openCodeJSONV2(value, path) {
		if value.Find(path) == nil {
			patchOperations = append(patchOperations, map[string]any{"op": "add", "path": path, "value": map[string]any{}})
		}
		path += "/servers"
	}
	patchOperations = append(patchOperations, map[string]any{"op": "add", "path": path, "value": entries})
	patch, err := json.Marshal(patchOperations)
	if err != nil {
		return nil, errors.New("invalid-config")
	}
	if err := value.Patch(patch); err != nil {
		return nil, errors.New("invalid-config")
	}
	value.Format()
	return value.Pack(), nil
}

func openCodeJSONV2(value hujson.Value, base string) bool {
	if nested := value.Find(base + "/servers"); nested != nil {
		duplicate := nested.Clone()
		duplicate.Standardize()
		var entry map[string]any
		if err := decodeMCPJSON(duplicate.Pack(), &entry); err == nil && entry != nil {
			return !openCodeLegacyEntry(entry)
		}
	}
	root := value.Find(base)
	if root == nil {
		return true
	}
	copy := root.Clone()
	copy.Standardize()
	var members map[string]any
	if err := decodeMCPJSON(copy.Pack(), &members); err != nil {
		return false
	}
	for _, member := range members {
		if entry, ok := member.(map[string]any); ok && openCodeLegacyEntry(entry) {
			return false
		}
	}
	return true
}

func mutateMCPJSON(client mcpClient, raw []byte, change MCPChange) ([]byte, error) {
	entries, err := parseMCPConfig(client, raw)
	if err != nil {
		return nil, err
	}
	fields, err := mcpChangedFields(client, entries, change)
	if err != nil {
		return nil, err
	}
	value, err := hujson.Parse(bytes.Clone(raw))
	if err != nil {
		return nil, errors.New("invalid-config")
	}
	rootPath := "/" + pointerMCP(client.Field)
	basePath := rootPath
	if client.opencode() && openCodeJSONV2(value, basePath) {
		rootPath += "/servers"
		if change.Action == "toggle" {
			delete(fields, "enabled")
			fields["disabled"] = !change.Enabled
		}
	}
	entryPath := rootPath + "/" + pointerMCP(change.Name)
	type patchOperation struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value,omitempty"`
	}
	patch := []patchOperation{}
	switch change.Action {
	case "add":
		if rootPath != basePath && value.Find(basePath) == nil {
			patch = append(patch, patchOperation{"add", basePath, map[string]any{}})
		}
		if value.Find(rootPath) == nil {
			patch = append(patch, patchOperation{"add", rootPath, map[string]any{}})
		}
		patch = append(patch, patchOperation{"add", entryPath, fields})
	case "remove":
		patch = append(patch, patchOperation{"remove", entryPath, nil})
	default:
		if old, ok := entries[change.Name].(map[string]any); ok {
			for field, val := range fields {
				if !reflect.DeepEqual(old[field], val) {
					patch = append(patch, patchOperation{"add", entryPath + "/" + pointerMCP(field), val})
				}
			}
		} else {
			return nil, errors.New("unsupported-change")
		}
	}
	if len(patch) == 0 {
		return raw, nil
	}
	encoded, err := json.Marshal(patch)
	if err != nil || value.Patch(encoded) != nil {
		return nil, errors.New("invalid-config")
	}
	value.Format()
	return value.Pack(), nil
}
