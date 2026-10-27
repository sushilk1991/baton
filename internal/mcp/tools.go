package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sushilk1991/baton/internal/config"
	"github.com/sushilk1991/baton/internal/gc"
	"github.com/sushilk1991/baton/internal/reap"
	"github.com/sushilk1991/baton/internal/runner"
)

func tools() []map[string]any {
	definitions := []struct {
		name, description string
		properties        map[string]any
		required          []string
	}{
		{"status", "Read running jobs, lane capacity, queue positions, RAM, swap, and pressure.", nil, nil},
		{"history", "Read retained completed jobs, optionally filtered by lane and duration.", map[string]any{"lane": map[string]any{"type": "string"}, "since": map[string]any{"type": "string", "description": "Nonnegative Go duration, e.g. 24h"}}, nil},
		{"reap_candidates", "List aged orphan heavy processes. Never sends a signal.", nil, nil},
		{"gc_candidates", "List aged regenerable files under configured roots. Never deletes.", nil, nil},
		{"explain_lane", "Explain the auto lane and admission policy for command argv without executing it.", map[string]any{"argv": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1}}, []string{"argv"}},
	}
	result := []map[string]any{}
	for _, d := range definitions {
		schema := map[string]any{"type": "object", "additionalProperties": false}
		if d.properties != nil {
			schema["properties"] = d.properties
		}
		if d.required != nil {
			schema["required"] = d.required
		}
		result = append(result, map[string]any{"name": d.name, "description": d.description, "inputSchema": schema, "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}})
	}
	return result
}

func call(req request) *response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta,omitempty"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return failure(req.ID, invalidParams, "invalid tools/call parameters")
	}
	var out, diagnostics bytes.Buffer
	var err error
	code := 0
	switch params.Name {
	case "history":
		var args struct {
			Lane  string `json:"lane"`
			Since string `json:"since"`
		}
		if err := decodeParams(params.Arguments, &args); err != nil {
			return failure(req.ID, invalidParams, "history accepts only lane and since strings")
		}
		argv := []string{"--json"}
		if args.Lane != "" {
			argv = append(argv, "--lane="+args.Lane)
		}
		if args.Since != "" {
			duration, parseErr := time.ParseDuration(args.Since)
			if parseErr != nil || duration < 0 {
				return failure(req.ID, invalidParams, "since requires a nonnegative duration")
			}
			argv = append(argv, "--since="+args.Since)
		}
		code = runner.History(argv, &out, &diagnostics)
		if code == 0 && out.Len() == 0 {
			out.WriteString("[]\n")
		}
	case "explain_lane":
		var args struct {
			Args []string `json:"argv"`
		}
		if err := decodeParams(params.Arguments, &args); err != nil || len(args.Args) == 0 || args.Args[0] == "" {
			return failure(req.ID, invalidParams, "argv requires a nonempty array of command strings")
		}
		err = explain(args.Args, &out)
	case "status", "reap_candidates", "gc_candidates":
		var args struct{}
		if err := decodeParams(params.Arguments, &args); err != nil {
			return failure(req.ID, invalidParams, "tool accepts no arguments")
		}
		// Only these fixed read paths cross into domain packages. Client text
		// never becomes a CLI option, process command, deletion, or signal.
		switch params.Name {
		case "status":
			code = runner.Status([]string{"--json"}, &out, &diagnostics)
		case "reap_candidates":
			err = reap.Inspect(&out)
		case "gc_candidates":
			err = gc.Inspect(&out)
		}
	default:
		return failure(req.ID, invalidParams, "unknown tool")
	}
	if code != 0 {
		err = errors.New(diagnostics.String())
	}
	failed := err != nil
	text := out.String()
	if failed {
		text = err.Error()
	}
	return success(req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": failed})
}

func explain(argv []string, out *bytes.Buffer) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	name := settings.AutoLane(argv)
	result := map[string]any{"lane": name, "queued": name != ""}
	if name != "" {
		policy := settings.Lane(name)
		result["slots"] = policy.Slots
		result["max_weight"] = policy.MaxWeight
		result["memory_gate"] = policy.MemoryGate
		result["max_wait"] = policy.MaxWait.String()
		result["safety_margin_bytes"] = policy.MarginBytes
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
