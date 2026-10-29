// Package mcp serves the read-only baton tools over local stdio JSON-RPC.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sushilk1991/baton/internal/buildinfo"
)

const (
	rpcVersion      = "2.0"
	protocolVersion = "2025-11-25"
	maxMessage      = 1 << 20
	parseError      = -32700
	invalidRequest  = -32600
	methodNotFound  = -32601
	invalidParams   = -32602
	notReady        = -32000
)

type request struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type server struct{ initialized, ready bool }

// Run accepts only the stdio transport; stdout contains JSON-RPC alone.
func Run(args []string, in io.Reader, out, diagnostics io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "usage: baton mcp (newline-delimited JSON-RPC on stdio)")
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(diagnostics, "usage: baton mcp")
		return 2
	}
	if err := serve(in, out); err != nil {
		fmt.Fprintln(diagnostics, "baton mcp:", err)
		return 1
	}
	return 0
}

func serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxMessage)
	encoder := json.NewEncoder(out)
	s := server{}
	for scanner.Scan() {
		reply := s.message(scanner.Bytes())
		if reply == nil {
			continue
		}
		if err := encoder.Encode(reply); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *server) message(data []byte) *response {
	if !json.Valid(data) {
		return failure(nil, parseError, "invalid JSON")
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil || req.Version != rpcVersion || req.Method == "" || !validID(req.ID) {
		return failure(nil, invalidRequest, "invalid JSON-RPC request")
	}
	if len(req.ID) == 0 {
		if req.Method == "notifications/initialized" && s.initialized {
			s.ready = true
		}
		return nil
	}
	if req.Method == "ping" {
		return success(req.ID, map[string]any{})
	}
	if req.Method == "initialize" {
		return s.initialize(req)
	}
	if !s.ready {
		return failure(req.ID, notReady, "initialize and send notifications/initialized first")
	}
	switch req.Method {
	case "tools/list":
		var params struct {
			Cursor string          `json:"cursor"`
			Meta   json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeParams(req.Params, &params); err != nil || params.Cursor != "" {
			return failure(req.ID, invalidParams, "tools/list has no cursor")
		}
		return success(req.ID, map[string]any{"tools": tools()})
	case "tools/call":
		return call(req)
	default:
		return failure(req.ID, methodNotFound, "unknown method")
	}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var text string
	if id[0] == '"' && json.Unmarshal(id, &text) == nil {
		return true
	}
	// MCP request IDs are strings or integers, never null or fractions.
	var number json.Number
	if json.Unmarshal(id, &number) != nil || len(number) == 0 {
		return false
	}
	return !strings.ContainsAny(string(number), ".eE") && string(number) != "null"
}

func (s *server) initialize(req request) *response {
	if s.initialized {
		return failure(req.ID, invalidRequest, "already initialized")
	}
	var params struct {
		Version      string                     `json:"protocolVersion"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		Client       struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Version == "" || params.Capabilities == nil || params.Client.Name == "" || params.Client.Version == "" {
		return failure(req.ID, invalidParams, "initialize requires protocolVersion, capabilities, and clientInfo")
	}
	version := protocolVersion
	for _, known := range []string{protocolVersion, "2025-06-18", "2025-03-26", "2024-11-05"} {
		if params.Version == known {
			version = known
			break
		}
	}
	s.initialized = true
	return success(req.ID, map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "baton", "version": strings.TrimPrefix(buildinfo.Version(), "v")}})
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return errors.New("expected object")
	}
	var values any
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	if hasNull(values) {
		return errors.New("null is not an accepted argument type")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func success(id json.RawMessage, result any) *response {
	return &response{Version: rpcVersion, ID: id, Result: result}
}
func failure(id json.RawMessage, code int, message string) *response {
	return &response{Version: rpcVersion, ID: id, Error: &rpcError{code, message}}
}

func hasNull(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key != "_meta" && hasNull(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if hasNull(child) {
				return true
			}
		}
	}
	return false
}
