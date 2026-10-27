package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A real child exchanges newline-delimited MCP messages, never terminal prose.
func TestMCPStdio(t *testing.T) {
	isolateHome(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":"list","method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"explain_lane","arguments":{"argv":["ffmpeg","-i","a","b"]}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"history","arguments":{}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"gc_candidates","arguments":{}}}
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"status","arguments":{}}}
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"gc_candidates","arguments":{"yes":true}}}
{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"run","arguments":{}}}
{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"reap_candidates","arguments":{}}}
`
	command := cliCommand(t, "mcp")
	command.Stdin = strings.NewReader(input)
	var out, diag bytes.Buffer
	command.Stdout, command.Stderr = &out, &diag
	if err := command.Run(); err != nil {
		t.Fatalf("MCP: %v %s", err, &diag)
	}
	scanner := bufio.NewScanner(&out)
	rows := []map[string]any{}
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("stdout is not JSON-RPC: %q %v", scanner.Text(), err)
		}
		if row["jsonrpc"] != "2.0" {
			t.Fatalf("not JSON-RPC: %v", row)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 {
		t.Fatalf("responses=%d %s", len(rows), &out)
	}
	init := rows[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-11-25" {
		t.Fatalf("negotiation: %v", init)
	}
	list := rows[1]["result"].(map[string]any)["tools"].([]any)
	if len(list) != 5 {
		t.Fatalf("tools=%v", list)
	}
	expected := map[string]bool{"status": true, "history": true, "gc_candidates": true, "reap_candidates": true, "explain_lane": true}
	for _, raw := range list {
		tool := raw.(map[string]any)
		if !expected[tool["name"].(string)] || tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatalf("unsafe tool: %v", tool)
		}
	}
	for n := 2; n < 6; n++ {
		result, ok := rows[n]["result"].(map[string]any)
		if !ok || result["isError"] == true || len(result["content"].([]any)) == 0 {
			t.Fatalf("tool %d: %v", n, rows[n])
		}
	}
	text := rows[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"lane":"encode"`) {
		t.Fatalf("lane: %s", text)
	}
	for n := 6; n < 8; n++ {
		if _, ok := rows[n]["error"]; !ok {
			t.Fatalf("accepted unsafe call: %v", rows[n])
		}
	}
	if result, ok := rows[8]["result"].(map[string]any); !ok || result["isError"] != (runtime.GOOS == "windows") {
		t.Fatalf("reap candidates: %v", rows[8])
	}
	// Even tools/list and status must leave an unused state directory absent.
	state := filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("MCP wrote scheduler state: %v", err)
	}
}

// Candidate inspection must list a matching file without deleting it.
func TestMCPCandidatesPreserve(t *testing.T) {
	isolateHome(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "slice-1.mp4")
	if err := os.WriteFile(file, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(configRoot, 0700); err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(root)
	policy := "[gc]\nroots = [" + string(quoted) + "]\npatterns = [\"slice-*.mp4\"]\nolder_than = \"1h\"\n"
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gc_candidates","arguments":{}}}
`
	command := cliCommand(t, "mcp")
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	valid := bytes.Contains(output, []byte("slice-1.mp4")) && !bytes.Contains(output, []byte(`"isError":true`))
	if runtime.GOOS == "windows" {
		valid = bytes.Contains(output, []byte(`"isError":true`)) && bytes.Contains(output, []byte("not supported on Windows"))
	}
	if err != nil || !valid {
		t.Fatalf("candidates: %s %v", output, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "preserve" {
		t.Fatalf("candidate was changed: %q %v", data, err)
	}
}
