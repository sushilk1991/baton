package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// jsonPart locates a member and its value without decoding its spelling.
type jsonPart struct {
	key               string
	start, value, end int
}

func jsonParts(data []byte) ([]jsonPart, error) {
	// The standard decoder handles quoted delimiters and nested values. Its
	// offsets let edits retain key order, whitespace, and literal && or >.
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || (delim != '{' && delim != '[') {
		return nil, errors.New("expected JSON container")
	}
	var parts []jsonPart
	keys := map[string]bool{}
	for decoder.More() {
		part := jsonPart{start: int(decoder.InputOffset())}
		for bytes.ContainsRune([]byte(" \t\r\n,"), rune(data[part.start])) {
			part.start++
		}
		if delim == '{' {
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			part.key, _ = token.(string)
			if keys[part.key] {
				return nil, errors.New("duplicate JSON key")
			}
			keys[part.key] = true
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		part.end = int(decoder.InputOffset())
		part.value = part.end - len(raw)
		parts = append(parts, part)
	}
	return parts, nil
}

func spliceJSON(data []byte, start, end int, value []byte) []byte {
	// Copy both sides so recursive edits cannot overwrite a parent slice.
	result := append([]byte{}, data[:start]...)
	result = append(result, value...)
	return append(result, data[end:]...)
}

func appendJSON(data []byte, path []string, entry []byte) ([]byte, error) {
	// Descend through existing containers; create only missing hook parents.
	// settings -> hooks -> PreToolUse -> append one owned entry
	parts, err := jsonParts(data)
	if err != nil {
		return nil, err
	}
	if len(path) > 0 {
		for _, part := range parts {
			if part.key != path[0] {
				continue
			}
			value, err := appendJSON(data[part.value:part.end], path[1:], entry)
			if err != nil {
				return nil, err
			}
			return spliceJSON(data, part.value, part.end, value), nil
		}
		entry = append([]byte{'['}, append(entry, ']')...)
		for n := len(path) - 1; n > 0; n-- {
			key, _ := json.Marshal(path[n])
			entry = append(append(append([]byte{'{'}, key...), ':'), append(entry, '}')...)
		}
		key, _ := json.Marshal(path[0])
		entry = append(append(key, ':'), entry...)
	}
	// Append beside the last value, leaving every existing byte in place.
	at := bytes.IndexAny(data, "{[") + 1
	if len(parts) > 0 {
		at = parts[len(parts)-1].end
		entry = append([]byte{','}, entry...)
	}
	return spliceJSON(data, at, at, entry), nil
}

func removeJSON(data []byte, parts []jsonPart, index int) []byte {
	// Remove the owned member and one separator. A last member owns its
	// preceding comma; other positions own the following comma instead.
	start, end := parts[index].start, parts[index].end
	if index+1 < len(parts) {
		end = parts[index+1].start
	} else if index > 0 {
		start = parts[index-1].end
	}
	return spliceJSON(data, start, end, nil)
}

func restoreJSON(data []byte, current, restored any) ([]byte, error) {
	if reflect.DeepEqual(current, restored) {
		return data, nil
	}
	parts, err := jsonParts(data)
	if err != nil {
		return nil, err
	}
	if target, ok := restored.(map[string]any); ok {
		present, _ := current.(map[string]any)
		// Work backward so splicing a child cannot shift earlier offsets.
		for n := len(parts) - 1; n >= 0; n-- {
			part := parts[n]
			value, exists := target[part.key]
			if !exists {
				data = removeJSON(data, parts, n)
				continue
			}
			raw, err := restoreJSON(data[part.value:part.end], present[part.key], value)
			if err != nil {
				return nil, err
			}
			data = spliceJSON(data, part.value, part.end, raw)
		}
		return data, nil
	}
	if target, ok := restored.([]any); ok {
		present, _ := current.([]any)
		if len(present) != len(target)+1 {
			return nil, errors.New("expected one removed JSON entry")
		}
		index := 0
		for index < len(target) && reflect.DeepEqual(present[index], target[index]) {
			index++
		}
		return removeJSON(data, parts, index), nil
	}
	// Aider can restore a scalar read path from its installed array form.
	return json.Marshal(restored)
}
