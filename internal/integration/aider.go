package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (i installer) addAider(j *journal) error {
	path, err := i.target(filepath.Join(i.home, ".aider.conf.yml"))
	if err != nil {
		return err
	}
	if recorded(j, path) {
		return nil
	}
	old, mode, err := readFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !exists {
		mode = fileMode
	}
	instruction := filepath.Join(i.home, ".agents", "AGENTS.md")
	body, err := aiderRead(old, instruction)
	if err != nil {
		return fmt.Errorf("%s: %w; preserved", path, err)
	}
	if bytes.Equal(old, body) {
		return nil
	}
	j.Entries = append(j.Entries, change{Path: path, Kind: "file", Existed: exists, Before: old, BeforeMode: mode, After: body, Mode: mode})
	return nil
}

func aiderRead(old []byte, path string) ([]byte, error) {
	quoted, err := json.Marshal(path)
	if err != nil {
		return nil, err
	}
	// JSON is valid YAML. Preserve its other keys when the entire config uses
	// JSON syntax; ordinary YAML gets a narrow edit of only the read field.
	if bytes.HasPrefix(bytes.TrimSpace(old), []byte("{")) {
		return aiderJSON(old, path)
	}
	lines := strings.SplitAfter(string(old), "\n")
	index := -1
	value := ""
	for n, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "---" || trim == "..." || strings.HasPrefix(line, "<<:") {
			return nil, fmt.Errorf("Aider YAML documents/merge keys require manual read configuration")
		}
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(trim, "#") {
			continue
		}
		key, raw, found := strings.Cut(line, ":")
		if !found || (strings.TrimSpace(key) != "read" && strings.TrimSpace(key) != "'read'" && strings.TrimSpace(key) != `"read"`) {
			continue
		}
		if index >= 0 {
			return nil, fmt.Errorf("duplicate Aider read keys")
		}
		index = n
		value = strings.TrimSpace(raw)
	}
	if index < 0 {
		return append(append(append([]byte{}, old...), '\n'), []byte("read: ["+string(quoted)+"]\n")...), nil
	}
	end := index + 1
	for end < len(lines) {
		line := lines[end]
		trim := strings.TrimSpace(line)
		if trim != "" && !strings.HasPrefix(trim, "#") && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(line, "- ") {
			break
		}
		end++
	}
	// Only filename scalars, flow sequences, and ordinary block sequences are
	// edited. Anchors, aliases, tags, and multiline scalars fail explicitly.
	plain := strings.TrimSpace(strings.SplitN(value, " #", 2)[0])
	if strings.HasPrefix(plain, "&") || strings.HasPrefix(plain, "*") || strings.HasPrefix(plain, "!") || strings.HasPrefix(plain, "|") || strings.HasPrefix(plain, ">") || strings.HasPrefix(plain, "{") {
		return nil, fmt.Errorf("unsupported Aider read value; set a filename list")
	}
	if strings.HasPrefix(plain, "[") {
		close := flowEnd(value)
		if close < 0 {
			return nil, fmt.Errorf("multiline or malformed Aider read list")
		}
		prefix := value[:close]
		separator := ", "
		if strings.HasSuffix(strings.TrimSpace(prefix), ",") {
			separator = " "
		}
		if strings.TrimSpace(prefix) == "[" {
			separator = ""
		}
		lines[index] = "read: " + prefix + separator + string(quoted) + value[close:] + "\n"
		return []byte(strings.Join(lines, "")), nil
	}
	if plain != "" && plain != "null" && plain != "~" && !strings.HasPrefix(plain, "#") {
		if end > index+1 && strings.TrimSpace(strings.Join(lines[index+1:end], "")) != "" {
			return nil, fmt.Errorf("multiline Aider read scalar requires a filename list")
		}
		lines[index] = "read:\n  - " + value + "\n  - " + string(quoted) + "\n"
		return []byte(strings.Join(lines, "")), nil
	}
	indent := "  "
	for _, line := range lines[index+1 : end] {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if !strings.HasPrefix(trim, "- ") {
			return nil, fmt.Errorf("unsupported Aider read block")
		}
		indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	}
	prefix := strings.Join(lines[:end], "")
	if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return []byte(prefix + indent + "- " + string(quoted) + "\n" + strings.Join(lines[end:], "")), nil
}

func flowEnd(value string) int {
	var quote byte
	escaped := false
	for n := 1; n < len(value); n++ {
		char := value[n]
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ']' {
			return n
		}
		if char == '[' || char == '{' || char == '#' {
			return -1
		}
	}
	return -1
}

func aiderJSON(old []byte, path string) ([]byte, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(old, &settings); err != nil || settings == nil {
		return nil, fmt.Errorf("invalid JSON Aider config")
	}
	paths := []string{}
	if value, ok := settings["read"]; ok {
		if err := json.Unmarshal(value, &paths); err != nil {
			var single string
			if err := json.Unmarshal(value, &single); err != nil {
				return nil, fmt.Errorf("Aider read requires filenames")
			}
			paths = []string{single}
		}
	}
	paths = append(paths, path)
	raw, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	settings["read"] = raw
	data, err := json.MarshalIndent(settings, "", "  ")
	return append(data, '\n'), err
}
