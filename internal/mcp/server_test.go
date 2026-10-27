package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	keys := map[string]bool{"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true, "XDG_CONFIG_DIRS": true, "XDG_DATA_DIRS": true}
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "XDG_") {
			keys[key] = true
		}
	}
	for key := range keys {
		t.Setenv(key, t.TempDir())
	}
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("BATON", "")
}

func readyServer() *server { return &server{initialized: true, ready: true} }

func TestProtocolErrors(t *testing.T) {
	isolate(t)
	tests := []struct {
		input string
		code  int
	}{
		{`{`, parseError}, {`[]`, invalidRequest}, {`null`, invalidRequest},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, invalidRequest},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, invalidRequest},
		{`{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, invalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"unknown"}`, methodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"next"}}`, invalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{"watch":true}}}`, invalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"history","arguments":{"since":"-1s"}}}`, invalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"explain_lane","arguments":{"argv":[]}}}`, invalidParams},
	}
	for _, tc := range tests {
		reply := readyServer().message([]byte(tc.input))
		if reply == nil || reply.Error == nil || reply.Error.Code != tc.code {
			t.Fatalf("%s: %+v", tc.input, reply)
		}
	}
	if reply := (&server{}).message([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)); reply.Error == nil || reply.Error.Code != notReady {
		t.Fatalf("not ready: %+v", reply)
	}
}

func TestLifecycle(t *testing.T) {
	isolate(t)
	s := &server{}
	request := `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	reply := s.message([]byte(request))
	if reply.Error != nil || reply.Result.(map[string]any)["protocolVersion"] != "2024-11-05" {
		t.Fatalf("initialize: %+v", reply)
	}
	if reply := s.message([]byte(request)); reply.Error == nil {
		t.Fatal("reinitialization accepted")
	}
	if reply := s.message([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); reply != nil || !s.ready {
		t.Fatalf("notification: %+v", reply)
	}
	if reply := s.message([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"gc_candidates","arguments":{}}}`)); reply != nil {
		t.Fatal("notification got response")
	}
	if reply := s.message([]byte(`{"jsonrpc":"2.0","id":0,"method":"ping"}`)); reply.Error != nil {
		t.Fatalf("ping: %+v", reply)
	}
	// Unknown versions negotiate the supported version; clients may disconnect.
	s = &server{}
	request = strings.ReplaceAll(request, "2024-11-05", "future")
	reply = s.message([]byte(request))
	if reply.Result.(map[string]any)["protocolVersion"] != protocolVersion {
		t.Fatalf("fallback: %+v", reply)
	}
}

func TestToolFailure(t *testing.T) {
	isolate(t)
	root := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte("unknown=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reply := readyServer().message([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}`))
	if reply.Error != nil || reply.Result.(map[string]any)["isError"] != true {
		t.Fatalf("error hidden: %+v", reply)
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestTransportBounds(t *testing.T) {
	isolate(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	if err := serve(strings.NewReader(input), failedWriter{}); err == nil {
		t.Fatal("output failure hidden")
	}
	if err := serve(strings.NewReader(strings.Repeat("x", maxMessage+1)), &bytes.Buffer{}); err == nil {
		t.Fatal("oversized message accepted")
	}
	var out, diag bytes.Buffer
	if code := Run([]string{"extra"}, strings.NewReader(""), &out, &diag); code != 2 {
		t.Fatalf("args=%d", code)
	}
	if code := Run(nil, strings.NewReader(input), &out, &diag); code != 0 {
		t.Fatalf("stdio=%d %s", code, &diag)
	}
	var reply response
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
}

func TestArgumentTypes(t *testing.T) {
	isolate(t)
	for _, params := range []string{
		`{"name":"history","arguments":{"since":null}}`,
		`{"name":"history","arguments":{"lane":null}}`,
		`{"name":"explain_lane","arguments":{"argv":["echo",null]}}`,
	} {
		request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
		reply := readyServer().message([]byte(request))
		if reply.Error == nil || reply.Error.Code != invalidParams {
			t.Fatalf("accepted invalid argument type: %+v", reply)
		}
	}
}
