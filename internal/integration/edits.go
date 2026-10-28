package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
)

func (i installer) reconcile(j *journal) error {
	for n, e := range j.Entries {
		// Resolve ancestors before reading edited files, retaining the existing
		// refusal to follow a replaced agent directory outside its recorded path.
		parent, err := resolvePath(filepath.Dir(e.Path))
		if err != nil {
			return err
		}
		if parent != filepath.Dir(e.Path) {
			return fmt.Errorf("%s: parent changed; preserved", e.Path)
		}
		before, after, err := state(e)
		if err != nil {
			return err
		}
		if before || after || e.Kind != "file" {
			continue
		}
		data, mode, err := readFile(e.Path)
		if err != nil {
			return err
		}
		replayed, err := i.replayRemoved(e, data)
		if err != nil {
			return fmt.Errorf("%s: modified integration; preserved: %w", e.Path, err)
		}
		if replayed != nil {
			// Gone content is already uninstalled. Re-init can replay the new
			// snapshots without resurrecting user bytes from the old manifest.
			e.Before, e.After = data, replayed
			e.Existed = true
			e.BeforeMode, e.Mode = mode, mode
			j.Entries[n] = e
			continue
		}
		restored, err := i.restoreOwned(e, data)
		if err != nil {
			return fmt.Errorf("%s: modified integration; preserved: %w", e.Path, err)
		}
		// Rebase both snapshots on the user's current surroundings. The existing
		// write-ahead journal then recovers either side of a partial uninstall.
		// current -> remove owned edit -> before; current -> after -> no rewrite
		e.Before, e.After = restored, data
		e.Existed = e.Existed || len(restored) > 0
		e.BeforeMode, e.Mode = mode, mode
		j.Entries[n] = e
	}
	return nil
}

func (i installer) restoreOwned(e change, data []byte) ([]byte, error) {
	// Infer the edit from recorded contents, including version-1 manifests and
	// symlink targets whose basename differs from settings.json or AGENTS.md.
	if bytes.HasPrefix(bytes.TrimSpace(e.After), []byte("{")) && json.Valid(e.After) {
		return undoJSON(e.Before, e.After, data)
	}
	// Marker counts reject edited or duplicated blocks before inverse editing.
	if bytes.Contains(e.After, []byte(startMarker)) {
		return undoSnippet(e, data)
	}
	instruction := filepath.Join(i.home, ".agents", "AGENTS.md")
	if !bytes.Contains(e.After, []byte(instruction)) {
		return nil, errors.New("owned file changed")
	}
	restored, err := undoText(e.Before, e.After, data)
	if err != nil {
		return nil, err
	}
	// Reapplying the supported Aider edit must reproduce the current document;
	// a matching path in some other YAML field is not proof of ownership.
	replayed, err := aiderRead(restored, instruction)
	if err != nil || !bytes.Equal(replayed, data) {
		return nil, errors.New("owned Aider read field changed")
	}
	return restored, nil
}

func undoSnippet(e change, data []byte) ([]byte, error) {
	if bytes.Count(data, []byte(startMarker)) != 1 || bytes.Count(data, []byte(endMarker)) != 1 {
		return nil, errors.New("owned instruction markers changed")
	}
	start := bytes.Index(e.After, []byte(startMarker))
	end := bytes.Index(e.After[start:], []byte(endMarker))
	if end < 0 {
		return nil, errors.New("recorded instruction block is invalid")
	}
	end += start + len(endMarker)
	block := e.After[start:end]
	if !bytes.Contains(data, block) {
		return nil, errors.New("owned instruction block changed")
	}
	// Restore unchanged separators when possible. Text inserted immediately
	// above the marker can separate them; then remove only the recorded block.
	if restored, err := undoText(e.Before, e.After, data); err == nil {
		return restored, nil
	}
	if end < len(e.After) && e.After[end] == '\n' && bytes.Contains(data, append(append([]byte{}, block...), '\n')) {
		block = append(append([]byte{}, block...), '\n')
	}
	return bytes.Replace(data, block, nil, 1), nil
}

func undoText(before, after, data []byte) ([]byte, error) {
	// Isolate the recorded insertion/replacement, including its separators.
	// Preserve all current bytes outside that unique unchanged span.
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	oldEnd, newEnd := len(before), len(after)
	for oldEnd > start && newEnd > start && before[oldEnd-1] == after[newEnd-1] {
		oldEnd--
		newEnd--
	}
	owned := after[start:newEnd]
	if len(owned) == 0 || bytes.Count(data, owned) != 1 {
		return nil, errors.New("owned text changed or duplicated")
	}
	return bytes.Replace(data, owned, before[start:oldEnd], 1), nil
}

func undoJSON(before, after, data []byte) ([]byte, error) {
	values := make([]any, 3)
	for n, raw := range [][]byte{before, after, data} {
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		// Preserve integer precision in unrelated settings during reserialization.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if !json.Valid(raw) || decoder.Decode(&values[n]) != nil {
			return nil, errors.New("expected JSON settings object")
		}
		if _, ok := values[n].(map[string]any); !ok {
			return nil, errors.New("expected JSON settings object")
		}
	}
	restored, ok := undoValue(values[0], values[1], values[2])
	if !ok {
		return nil, errors.New("owned JSON entry changed or duplicated")
	}
	body, err := json.MarshalIndent(restored, "", "  ")
	return append(body, '\n'), err
}

func undoValue(before, after, current any) (any, bool) {
	if reflect.DeepEqual(before, after) {
		return current, true
	}
	if installed, ok := after.(map[string]any); ok {
		original, _ := before.(map[string]any)
		settings, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		// Descend only through keys baton changed; retain unrelated current keys.
		for key, value := range installed {
			old, existed := original[key]
			if existed && reflect.DeepEqual(old, value) {
				continue
			}
			present, exists := settings[key]
			if !exists {
				return nil, false
			}
			restored, ok := undoValue(old, value, present)
			if !ok {
				return nil, false
			}
			settings[key] = restored
			if !existed && emptyValue(restored) {
				delete(settings, key)
			}
		}
		return settings, true
	}
	return undoEntry(before, after, current)
}

func undoEntry(before, after, current any) (any, bool) {
	installed, ok := after.([]any)
	if !ok || len(installed) == 0 {
		return nil, false
	}
	original, _ := before.([]any)
	scalar, wasScalar := before.(string)
	if wasScalar {
		original = []any{scalar}
	}
	original = append([]any{}, original...)
	entries, ok := current.([]any)
	if !ok || len(installed) != len(original)+1 {
		return nil, false
	}
	added := 0
	for added < len(original) && reflect.DeepEqual(original[added], installed[added]) {
		added++
	}
	if !reflect.DeepEqual(original[added:], installed[added+1:]) {
		return nil, false
	}
	// Match the entire appended entry, independent of array position or JSON
	// formatting. Multiple matches are ambiguous and must remain untouched.
	owned := installed[added]
	index := -1
	for n, entry := range entries {
		if !reflect.DeepEqual(entry, owned) {
			continue
		}
		if index >= 0 {
			return nil, false
		}
		index = n
	}
	if index < 0 {
		return nil, false
	}
	entries = append(entries[:index], entries[index+1:]...)
	if wasScalar && reflect.DeepEqual(entries, original) {
		return scalar, true
	}
	return entries, true
}

func emptyValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return value == nil
}
