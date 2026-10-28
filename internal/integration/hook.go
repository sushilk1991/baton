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

	"github.com/sushilk1991/baton/internal/config"
)

const hookLimit = 1 << 20

func hook(in io.Reader, out, diagnostics io.Writer, policy hookPolicy) int {
	if os.Getenv("BATON") == "off" {
		return 0
	}
	if err := rewriteHook(in, out, policy); err != nil {
		var blocked blockedCommand
		if errors.As(err, &blocked) {
			fmt.Fprintln(diagnostics, "baton: use this queued command:\n"+string(blocked))
			return exitUsage
		}
		fmt.Fprintln(diagnostics, "baton hook:", err)
		return exitFailure
	}
	return 0
}

type blockedCommand string

// Error carries the exact command for the exit-2 hook fallback.
func (command blockedCommand) Error() string { return string(command) }

func rewriteHook(in io.Reader, out io.Writer, policy hookPolicy) error {
	data, err := io.ReadAll(io.LimitReader(in, hookLimit+1))
	if err != nil {
		return err
	}
	if len(data) > hookLimit {
		return errors.New("input exceeds 1 MiB")
	}
	var request struct {
		Event string                     `json:"hook_event_name"`
		Tool  string                     `json:"tool_name"`
		Input map[string]json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	if request.Event != "PreToolUse" || request.Tool != "Bash" {
		return nil
	}
	var command string
	if err := json.Unmarshal(request.Input["command"], &command); err != nil {
		return err
	}
	words := shellWords(command)
	if len(words) == 0 || queuedCommand(words) {
		return nil
	}
	settings, err := config.Load()
	if err != nil {
		return err
	}
	// Shell text is never executed here. Explicit lane selection lets the
	// runner preserve compound commands as one shell job, e.g. cd && npm ci.
	// Descendant shims inherit the admitted reservation instead of queuing twice.
	lane := settings.AutoLane(words)
	if lane == "" {
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	wrapped := shellQuote(binary) + " run --lane " + shellQuote(lane) + " -- bash -c " + shellQuote(command)
	if policy == withClaudeBlock {
		return blockedCommand(wrapped)
	}
	request.Input["command"], err = json.Marshal(wrapped)
	if err != nil {
		return err
	}
	// updatedInput alone retains Claude's normal permission evaluation. Never
	// emit "allow": queueing must not grant permission to execute a command.
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "updatedInput": request.Input}})
}

func queuedCommand(words []string) bool {
	binary, _ := os.Executable()
	// Skip commands that already include a reservation, including env prefixes
	// and cd && baton run. Nesting could wait on the caller's single slot.
	for index := 0; index+1 < len(words); index++ {
		if words[index+1] != "run" {
			continue
		}
		base := filepath.Base(words[index])
		if words[index] == binary || base == "baton" || base == "baton.exe" {
			return true
		}
	}
	return false
}

func shellWords(command string) []string {
	// Tokenize quotes only for matcher input and the outer-wrapper check.
	// No expansion or shell grammar interpretation occurs; baton is advisory.
	var word bytes.Buffer
	words := []string{}
	var quote rune
	escaped := false
	for _, char := range command {
		if escaped {
			word.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if strings.ContainsRune(" \t\r\n", char) {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
			continue
		}
		word.WriteRune(char)
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}
