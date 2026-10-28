package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	startMarker  = "<!-- baton:start -->"
	endMarker    = "<!-- baton:end -->"
	instructions = startMarker + `
Queue heavy commands with baton run --lane auto -- <command> [args...].
Use baton status --json before launching concurrent renders, encodes, ML,
installs, or builds. Keep command argv intact; use --mem for a known budget.
Inspect baton reap and baton gc candidates; request approval before --yes.
BATON=off bypasses scheduling. Read ~/.agents/skills/baton/SKILL.md for recipes.
` + endMarker + "\n"
	skill = `---
name: baton
description: Queue heavy renders, video encodes, local ML, package installs, and builds across agents on one machine. Use before starting concurrent heavy work or inspecting memory pressure, queued jobs, orphan processes, or regenerable intermediates.
license: Apache-2.0
---

Run heavy work through baton, preserving argv:

` + "```sh\nbaton status --json\nbaton run --lane auto -- ffmpeg -i input.mp4 output.mp4\nbaton run --lane ml --mem 4G -- whisper audio.wav\nbaton run --lane install -- npm ci\nbaton run --lane build -- go build ./...\n```" + `

A slot queues work until capacity and memory permit it. Use --quiet to hide
wait notices, --label to name work, and --priority high|normal|low to order it.
The command's stdio and exit status pass through. BATON=off bypasses baton.

Use baton history --json for completed work. Inspect baton reap and baton gc
without --yes first. Ask before terminating processes or deleting files.
Never bypass the queue because a command waits. For shared Chrome, use baton
browser start, then baton browser endpoint to obtain the local CDP URL.
Read-only MCP tools are available by launching baton mcp over stdio.
`
)

type agent struct{ name, dir, document string }

func (i installer) plan(j journal, claude hookPolicy, out io.Writer) (journal, error) {
	agents := []agent{
		{"Claude Code", filepath.Join(i.home, ".claude"), "CLAUDE.md"},
		{"Codex", filepath.Join(i.home, ".codex"), "AGENTS.md"},
		{"Cursor", filepath.Join(i.home, ".cursor"), ""},
		{"Gemini CLI", filepath.Join(i.home, ".gemini"), "GEMINI.md"},
		{"OpenCode", filepath.Join(i.config, "opencode"), "AGENTS.md"},
		{"Goose", filepath.Join(i.config, "goose"), ".goosehints"},
	}
	canonical := filepath.Join(i.home, ".agents", "skills", "baton")
	if err := i.addFile(&j, filepath.Join(canonical, "SKILL.md"), []byte(skill)); err != nil {
		return j, err
	}
	if err := i.addSnippet(&j, filepath.Join(i.home, ".agents", "AGENTS.md")); err != nil {
		return j, err
	}
	foundClaude := false
	for _, a := range agents {
		info, err := os.Stat(a.dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return j, err
		}
		if !info.IsDir() {
			return j, fmt.Errorf("%s: expected agent directory", a.dir)
		}
		if _, err := fmt.Fprintf(out, "detected %s: %s\n", a.name, a.dir); err != nil {
			return j, err
		}
		if a.name == "Claude Code" {
			foundClaude = true
		}
		if err := i.addLink(&j, filepath.Join(a.dir, "skills", "baton"), canonical); err != nil {
			return j, err
		}
		if a.document == "" {
			continue
		}
		if err := i.addSnippet(&j, filepath.Join(a.dir, a.document)); err != nil {
			return j, err
		}
	}
	// Aider loads the shared instructions through its read-only config field.
	for _, name := range []string{".aider", ".aider.conf.yml"} {
		if _, err := os.Stat(filepath.Join(i.home, name)); err == nil {
			if _, err := fmt.Fprintln(out, "detected Aider: read-only conventions"); err != nil {
				return j, err
			}
			if err := i.addAider(&j); err != nil {
				return j, err
			}
			break
		} else if !os.IsNotExist(err) {
			return j, err
		}
	}

	if claude != withoutHook && !foundClaude {
		return j, errors.New("--claude-hook requires an existing ~/.claude directory")
	}
	if claude != withoutHook {
		if err := i.addHook(&j, filepath.Join(i.home, ".claude", "settings.json"), claude); err != nil {
			return j, err
		}
	}
	return j, nil
}

func (i installer) target(path string) (string, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return "", err
	}
	if !i.allowed(resolved) {
		return "", fmt.Errorf("%s: integration target outside home/config roots", path)
	}
	return resolved, nil
}

func recorded(j *journal, path string) bool {
	for _, e := range j.Entries {
		if e.Path == path {
			return true
		}
	}
	return false
}

func (i installer) addParents(j *journal, path string) error {
	dir := filepath.Dir(path)
	if dir == i.home || dir == i.config {
		return nil
	}
	if !i.allowed(dir) {
		return fmt.Errorf("%s: parent outside integration roots", dir)
	}
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s: expected directory", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if recorded(j, dir) {
		return nil
	}
	parent := filepath.Dir(dir)
	if parent != i.home && parent != i.config {
		if err := i.addParents(j, dir); err != nil {
			return err
		}
	}
	j.Entries = append(j.Entries, change{Path: dir, Kind: "directory"})
	return nil
}

func (i installer) addFile(j *journal, path string, body []byte) error {
	path, err := i.target(path)
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
	if exists && bytes.Equal(old, body) {
		return nil
	}
	if exists {
		return fmt.Errorf("%s: conflicting file; preserved", path)
	}
	if err := i.addParents(j, path); err != nil {
		return err
	}
	j.Entries = append(j.Entries, change{Path: path, Kind: "file", Existed: exists, Before: old, BeforeMode: mode, After: body, Mode: fileMode})
	return nil
}

func (i installer) addSnippet(j *journal, path string) error {
	path, err := i.target(path)
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
	if bytes.Contains(old, []byte(startMarker)) || bytes.Contains(old, []byte(endMarker)) {
		return fmt.Errorf("%s: unowned baton markers; preserved", path)
	}
	if !exists {
		mode = fileMode
	}
	body := append([]byte{}, old...)
	if len(body) > 0 {
		body = append(body, '\n', '\n')
	}
	body = append(body, []byte(instructions)...)
	if err := i.addParents(j, path); err != nil {
		return err
	}
	j.Entries = append(j.Entries, change{Path: path, Kind: "file", Existed: exists, Before: old, BeforeMode: mode, After: body, Mode: mode})
	return nil
}

func (i installer) addLink(j *journal, path, target string) error {
	// Resolve only parents: an existing link is observed, never followed and
	// replaced. A shared skills-directory alias needs no self-referential link.
	parent, err := i.target(filepath.Dir(path))
	if err != nil {
		return err
	}
	path = filepath.Join(parent, filepath.Base(path))
	if path == target || recorded(j, path) {
		return nil
	}
	info, err := os.Lstat(path)
	if err == nil {
		link, linkErr := os.Readlink(path)
		if info.Mode()&os.ModeSymlink != 0 && linkErr == nil {
			resolved, resolveErr := resolvePath(path)
			if resolveErr == nil && resolved == target && link != "" {
				return nil
			}
		}
		return fmt.Errorf("%s: conflicting skill; preserved", path)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := i.addParents(j, path); err != nil {
		return err
	}
	j.Entries = append(j.Entries, change{Path: path, Kind: "link", Target: target})
	return nil
}

func (i installer) addHook(j *journal, path string, policy hookPolicy) error {
	path, err := i.target(path)
	if err != nil {
		return err
	}
	if recorded(j, path) {
		for _, entry := range j.Entries {
			if entry.Path != path {
				continue
			}
			blocked := bytes.Contains(entry.After, []byte("init _hook-block"))
			if blocked != (policy == withClaudeBlock) {
				return fmt.Errorf("%s: uninstall before changing hook mode", path)
			}
		}
		return nil
	}
	old, mode, err := readFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	settings := map[string]json.RawMessage{}
	if exists {
		if err := json.Unmarshal(old, &settings); err != nil || settings == nil {
			return fmt.Errorf("%s: expected JSON settings object", path)
		}
	} else {
		mode = fileMode
	}
	hooks := map[string]json.RawMessage{}
	if raw, ok := settings["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return fmt.Errorf("%s: expected hooks object", path)
		}
	}
	entries := []json.RawMessage{}
	if raw, ok := hooks["PreToolUse"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
			return fmt.Errorf("%s: expected PreToolUse array", path)
		}
	}
	command := shellQuote(i.binary) + " init _hook"
	if policy == withClaudeBlock {
		command += "-block"
	}
	entry, err := json.Marshal(map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": command}}})
	if err != nil {
		return err
	}
	// Only the new entry is encoded. Existing settings remain byte-identical.
	base := old
	if !exists {
		base = []byte("{\n}\n")
	}
	body, err := appendJSON(base, []string{"hooks", "PreToolUse"}, entry)
	if err != nil {
		return err
	}
	if err := i.addParents(j, path); err != nil {
		return err
	}
	j.Entries = append(j.Entries, change{Path: path, Kind: "file", Existed: exists, Before: old, BeforeMode: mode, After: body, Mode: mode})
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
