package integration

import (
	"bytes"
	"encoding/json"
	"reflect"
)

func (i installer) replayRemoved(e change, data []byte) ([]byte, error) {
	// Re-plan an absent edit against current bytes, retaining user whitespace
	// and the recorded hook opt-in. Partial or edited content stays a conflict.
	// current (before) -> journal -> re-init (after), or uninstall (no write)
	var planned journal
	if !json.Valid(e.After) && bytes.Contains(e.After, []byte(startMarker)) {
		if bytes.Contains(data, []byte(startMarker)) || bytes.Contains(data, []byte(endMarker)) {
			return nil, nil
		}
		if err := i.addSnippet(&planned, e.Path); err != nil {
			return nil, err
		}
		return planned.Entries[len(planned.Entries)-1].After, nil
	}
	if !hookRemoved(e, data) {
		return nil, nil
	}
	policy := withClaudeHook
	if bytes.Contains(e.After, []byte("init _hook-block")) {
		policy = withClaudeBlock
	}
	if err := i.addHook(&planned, e.Path, policy); err != nil {
		return nil, err
	}
	return planned.Entries[len(planned.Entries)-1].After, nil
}

func hookRemoved(e change, data []byte) bool {
	// A missing key or the unchanged original array proves removal. An array
	// containing different entries could be an edited hook, so preserve it.
	original, _, validBefore := preHooks(e.Before)
	installed, _, validAfter := preHooks(e.After)
	current, present, validCurrent := preHooks(data)
	if !validBefore || !validAfter || !validCurrent || len(installed) != len(original)+1 {
		return false
	}
	if !present || (len(original) == 0 && len(current) == 0) {
		return true
	}
	return reflect.DeepEqual(original, current)
}

func preHooks(data []byte) ([]any, bool, bool) {
	// Decode only the hook path; unrelated settings do not establish ownership.
	if len(data) == 0 {
		data = []byte("{}")
	}
	var settings map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !json.Valid(data) || decoder.Decode(&settings) != nil || settings == nil {
		return nil, false, false
	}
	value, present := settings["hooks"]
	if !present {
		return nil, false, true
	}
	hooks, ok := value.(map[string]any)
	if !ok {
		return nil, false, false
	}
	value, present = hooks["PreToolUse"]
	if !present {
		return nil, false, true
	}
	entries, ok := value.([]any)
	return entries, present, ok
}
