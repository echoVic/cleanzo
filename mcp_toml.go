package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type mcpTOMLSpan struct {
	Start, End  int
	Replacement []byte
}

func mcpPathPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

func mcpTOMLLineStart(raw []byte, offset int) int {
	return bytes.LastIndexByte(raw[:offset], '\n') + 1
}
func mcpTOMLLineEnd(raw []byte, offset int) int {
	if next := bytes.IndexByte(raw[offset:], '\n'); next >= 0 {
		return offset + next + 1
	}
	return len(raw)
}

func mutateMCPTOML(raw []byte, change MCPChange) ([]byte, error) {
	client := mcpClient{ID: "codex", Field: "mcp_servers"}
	entries, err := parseMCPConfig(client, raw)
	if err != nil {
		return nil, err
	}
	fields, err := mcpChangedFields(client, entries, change)
	if err != nil {
		return nil, err
	}
	target := []string{client.Field, change.Name}
	var parser unstable.Parser
	parser.Reset(raw)
	var table []string
	var changes []mcpTOMLSpan
	headerEnd := -1
	found := false
	for parser.NextExpression() {
		node := parser.Expression()
		keys := node.Key()
		first := -1
		var path []string
		for keys.Next() {
			key := keys.Node()
			if first < 0 {
				first = int(key.Raw.Offset)
			}
			path = append(path, string(key.Data))
		}
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			table = path
			if !mcpPathPrefix(table, target) {
				continue
			}
			found = true
			if len(table) == len(target) && node.Kind == unstable.Table {
				headerEnd = mcpTOMLLineEnd(raw, first)
			}
			if change.Action == "remove" {
				changes = append(changes, mcpTOMLSpan{mcpTOMLLineStart(raw, first), mcpTOMLLineEnd(raw, first), nil})
			}
			continue
		}
		path = append(slices.Clone(table), path...)
		if !mcpPathPrefix(path, target) || len(path) == len(target) {
			continue
		}
		found = true
		start, end := int(node.Raw.Offset), int(node.Raw.Offset+node.Raw.Length)
		if change.Action == "remove" {
			changes = append(changes, mcpTOMLSpan{mcpTOMLLineStart(raw, start), mcpTOMLLineEnd(raw, end), nil})
			continue
		}
		if len(path) != 3 {
			continue
		}
		field := path[2]
		value, ok := fields[field]
		if !ok {
			continue
		}
		statement, err := toml.Marshal(map[string]any{field: value})
		if err != nil {
			return nil, errors.New("invalid-value")
		}
		// Preserve dotted key spelling, indentation and comments outside the value.
		ending := bytes.IndexByte(statement, '=')
		replacement := bytes.TrimSpace(statement[ending+1:])
		startValue := int(node.Value().Raw.Offset)
		valueEnd := int(node.Value().Raw.Offset + node.Value().Raw.Length)
		if node.Value().Raw.Length == 0 {
			// Arrays do not have a Raw range in the AST. KeyValue.Raw contains the
			// complete statement and excludes the comment at the end of the line.
			original := raw[start:end]
			equal := bytes.IndexByte(original, '=')
			if equal < 0 {
				return nil, errors.New("unsupported-layout")
			}
			startValue = start + equal + 1
			for startValue < end && (raw[startValue] == ' ' || raw[startValue] == '\t') {
				startValue++
			}
			valueEnd = end
		}
		changes = append(changes, mcpTOMLSpan{startValue, valueEnd, replacement})
		delete(fields, field)
	}
	if parser.Error() != nil {
		return nil, errors.New("invalid-config")
	}
	if change.Action != "add" && !found {
		return nil, errors.New("unsupported-layout")
	}
	if len(fields) > 0 {
		joined, err := toml.Marshal(fields)
		if err != nil {
			return nil, errors.New("invalid-value")
		}
		if headerEnd < 0 {
			if !mcpNames.MatchString(change.Name) {
				return nil, errors.New("unsupported-layout")
			}
			headerEnd = len(raw)
			joined = append([]byte(fmt.Sprintf("\n[mcp_servers.%q]\n", change.Name)), joined...)
		} else if headerEnd == len(raw) && !strings.HasSuffix(string(raw), "\n") {
			joined = append([]byte{'\n'}, joined...)
		}
		changes = append(changes, mcpTOMLSpan{headerEnd, headerEnd, joined})
	}
	return applyMCPTOMLSpans(raw, changes)
}

func restoreMCPTOML(raw []byte, entries map[string]any) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(raw)
	var changes []mcpTOMLSpan
	inMCPTable, inTable := false, false
	for parser.NextExpression() {
		node := parser.Expression()
		key := node.Key()
		if !key.Next() {
			continue
		}
		isMCP := string(key.Node().Data) == "mcp_servers"
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			inMCPTable, inTable = isMCP, true
			if inMCPTable {
				start := int(key.Node().Raw.Offset)
				changes = append(changes, mcpTOMLSpan{mcpTOMLLineStart(raw, start), mcpTOMLLineEnd(raw, start), nil})
			}
			continue
		}
		if inMCPTable || isMCP && !inTable {
			start, end := int(node.Raw.Offset), int(node.Raw.Offset+node.Raw.Length)
			changes = append(changes, mcpTOMLSpan{mcpTOMLLineStart(raw, start), mcpTOMLLineEnd(raw, end), nil})
		}
	}
	if parser.Error() != nil {
		return nil, errors.New("invalid-config")
	}
	if len(entries) > 0 {
		encoded, err := toml.Marshal(map[string]any{"mcp_servers": entries})
		if err != nil {
			return nil, errors.New("backup-unavailable")
		}
		changes = append(changes, mcpTOMLSpan{len(raw), len(raw), append([]byte{'\n'}, encoded...)})
	}
	return applyMCPTOMLSpans(raw, changes)
}

func applyMCPTOMLSpans(raw []byte, changes []mcpTOMLSpan) ([]byte, error) {
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Start < changes[j].Start })
	var out bytes.Buffer
	cursor := 0
	for _, change := range changes {
		if change.Start < cursor || change.End < change.Start {
			return nil, errors.New("unsupported-layout")
		}
		out.Write(raw[cursor:change.Start])
		out.Write(change.Replacement)
		cursor = change.End
	}
	out.Write(raw[cursor:])
	if _, err := parseMCPConfig(mcpClient{ID: "codex", Field: "mcp_servers"}, out.Bytes()); err != nil {
		return nil, errors.New("unsupported-layout")
	}
	return out.Bytes(), nil
}
