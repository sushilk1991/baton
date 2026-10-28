package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sushilk1991/baton/internal/config"
)

const (
	hookLimit = 1 << 20
)

// holdSupported is false on Windows, where baton hold always exits 1. The
// hook then leaves commands alone, and init refuses to install it.
var holdSupported = runtime.GOOS != "windows"

func hook(in io.Reader, out, diagnostics io.Writer, policy hookPolicy) int {
	if os.Getenv("BATON") == batonDisabled {
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
	if !holdSupported {
		return nil
	}
	var command string
	if err := json.Unmarshal(request.Input["command"], &command); err != nil {
		return err
	}
	settings, err := config.Load()
	if err != nil {
		return err
	}
	// Only executable heads reach matchers; arguments such as grep ffmpeg do not.
	// segments -> unwrap -> executable/verb -> first lane -> whole shell job
	lane := hookLane(command, settings)
	if lane == "" {
		return nil
	}
	binary, err := invokedBinary()
	if err != nil {
		return err
	}
	// Keep shell syntax at top level for Claude's shell and permission checks.
	// shell -> hold (waits for capacity) -> original (same shell snapshot)
	wrapped := shellQuote(binary) + " hold --lane " + shellQuote(lane) + " --pid $$ && " + command
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
	binary, _ := invokedBinary()
	if len(words) < 2 || words[1] != "run" {
		return false
	}
	base := filepath.Base(words[0])
	return words[0] == binary || base == "baton" || base == "baton.exe"
}

func hookLane(command string, settings *config.Config) string {
	// Recognize our exact prefix on repeated PreToolUse evaluation. The
	// original remains separate shell syntax; no permission is granted here.
	segments := shellSegments(command)
	if len(segments) > 0 && holdPrefix(segments[0]) {
		return ""
	}
	// An explicit reservation protects its segment, not later bare commands.
	for _, segment := range segments {
		words := unwrapCommand(segment)
		if queuedCommand(words) {
			continue
		}
		if lane := settings.AutoLane(commandHead(words)); lane != "" {
			return lane
		}
	}
	return ""
}

func holdPrefix(words []string) bool {
	if len(words) != 6 || words[1] != "hold" || words[2] != "--lane" || words[4] != "--pid" || words[5] != "$$" {
		return false
	}
	binary, _ := invokedBinary()
	return words[0] == binary || filepath.Base(words[0]) == "baton"
}
