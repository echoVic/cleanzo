package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPInventoryDoesNotReturnSecrets(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(home, ".config/opencode"))
	writeSkillFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"a":{"command":"a-missing-cleanzo-mcp-test","args":["SECRET_ARG"],"env":{"API_KEY":"SECRET_ENV"}}}}`)
	writeSkillFile(t, filepath.Join(home, ".gemini/settings.json"), `{"mcpServers":{"b":{"httpUrl":"https://SECRET_USER:SECRET_PASS@example.com/mcp?token=SECRET_QUERY","headers":{"Authorization":"SECRET_HEADER"}}}}`)
	configs := (Cleaner{}).ListMCPConfigs(context.Background())
	data, err := json.Marshal(configs)
	if err != nil || bytes.Contains(data, []byte("SECRET")) || !bytes.Contains(data, []byte("missing-command")) || !bytes.Contains(data, []byte("example.com")) {
		t.Fatalf("unexpected inventory: %s, %v", data, err)
	}
}

func TestMCPApplyPreservesOtherSettingsAndRejectsStaleRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	old := []byte("{\n  // Keep this comment\n  \"theme\": \"system\",\n  \"mcp\": {\"demo\": {\"type\": \"local\", \"command\": [\"node\", \"old.mjs\"], \"environment\": {\"TOKEN\": \"secret\"}}}\n}\n")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := mcpClient{ID: "opencode", Path: path, Field: "mcp"}
	update := MCPChange{Action: "edit", Name: "demo", Transport: "stdio", Endpoint: "node", Args: []string{"new.mjs"}, ReplaceArgs: true, Revision: mcpRevision(old, true)}
	if err := applyMCPConfig(cfg, update, filepath.Join(t.TempDir(), "backup")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	for _, part := range []string{"Keep this comment", "system", "secret", "new.mjs"} {
		if !bytes.Contains(got, []byte(part)) {
			t.Fatalf("missing %s after edit: %s", part, got)
		}
	}
	if err := applyMCPConfig(cfg, update, filepath.Join(t.TempDir(), "backup")); err == nil {
		t.Fatal("stale input accepted")
	}
}

func TestMCPCodexSurgicalTogglePreservesOtherTables(t *testing.T) {
	input := []byte("# user comment\nmodel = 'test'\n\n[mcp_servers.\"sample.with.dot\"]\ncommand = 'node'\nargs = [\"app.mjs\"]\n# preserve comments in this table\n\n[mcp_servers.\"sample.with.dot\".env]\nTOKEN = 'secret'\n\n[features]\nfoo = true\n")
	next, err := mutateMCPTOML(input, MCPChange{Action: "toggle", Name: "sample.with.dot", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"# user comment", "model = 'test'", "# preserve comments", "TOKEN = 'secret'", "[features]\nfoo = true", "enabled = false"} {
		if !bytes.Contains(next, []byte(part)) {
			t.Fatalf("missing %s: %s", part, next)
		}
	}
}

func TestMCPOpenCodeV2ManagesOneRegistration(t *testing.T) {
	client := mcpClient{ID: "opencode", Field: "mcp"}
	raw := []byte(`{"mcp":{"servers":{"first":{"type":"local","command":["node","first.js"]},"second":{"type":"remote","url":"https://example.com/mcp"}}}}`)
	toggled, err := mutateMCPJSON(client, raw, MCPChange{Action: "toggle", Name: "first", Enabled: false})
	if err != nil || !bytes.Contains(toggled, []byte(`"disabled": true`)) {
		t.Fatalf("toggle = %s, %v", toggled, err)
	}
	left, err := mutateMCPJSON(client, toggled, MCPChange{Action: "remove", Name: "first"})
	if err != nil || !bytes.Contains(left, []byte(`"second"`)) || bytes.Contains(left, []byte(`"first"`)) {
		t.Fatalf("remove = %s, %v", left, err)
	}
	if _, err := mutateMCPJSON(client, toggled, MCPChange{Action: "remove", Name: "servers"}); err == nil {
		t.Fatal("removed V2 server container")
	}
}

func TestMCPAuthStaysWithOriginalDestination(t *testing.T) {
	client := mcpClient{ID: "gemini", Field: "mcpServers"}
	raw := []byte(`{"mcpServers":{"demo":{"httpUrl":"https://old.example/mcp","headers":{"Authorization":"secret"}}}}`)
	edit := MCPChange{Action: "edit", Name: "demo", Transport: "http", Endpoint: "https://different.example/mcp"}
	if _, err := mutateMCPJSON(client, raw, edit); err == nil || err.Error() != "credential-destination" {
		t.Fatalf("new origin accepted: %v", err)
	}
	edit.Endpoint = "https://old.example:443/updated"
	if next, err := mutateMCPJSON(client, raw, edit); err != nil || !bytes.Contains(next, []byte("secret")) {
		t.Fatalf("same-origin edit = %s, %v", next, err)
	}
}

func TestMCPRestoreKeepsExternalNonMCPSettings(t *testing.T) {
	for _, test := range []struct {
		client                 mcpClient
		live, backup, expected string
	}{
		{mcpClient{ID: "codex", Field: "mcp_servers"}, "model = 'newer'\n[mcp_servers.demo]\ncommand = 'new'\n", "model = 'old'\n[mcp_servers.demo]\ncommand = 'before'\n", "model = 'newer'"},
		{mcpClient{ID: "claude", Field: "mcpServers"}, `{"counter":"newer","mcpServers":{"demo":{"command":"new"}}}`, `{"counter":"old","mcpServers":{"demo":{"command":"before"}}}`, `"counter": "newer"`},
	} {
		next, err := restoreMCPEntries(test.client, []byte(test.live), []byte(test.backup))
		if err != nil || !bytes.Contains(next, []byte(test.expected)) || !bytes.Contains(next, []byte("before")) {
			t.Fatalf("%s restore = %s, %v", test.client.ID, next, err)
		}
	}
}

func TestMCPRejectsDuplicateJSONKeys(t *testing.T) {
	client := mcpClient{ID: "claude", Field: "mcpServers"}
	raw := []byte(`{"mcpServers":{"demo":{"command":"first"},"d\u0065mo":{"command":"second"}}}`)
	if _, err := mutateMCPJSON(client, raw, MCPChange{Action: "remove", Name: "demo"}); err == nil {
		t.Fatal("accepted duplicate server keys")
	}
}
