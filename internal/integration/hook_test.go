package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHookExecutables(t *testing.T) {
	// Arguments and quoted separators are data; only executable heads route work.
	for _, tc := range []struct {
		command string
		lane    string
	}{
		{"grep -rn ffmpeg src/", ""},
		{"/tmp/chrome-tools/grep -rn ffmpeg src/", ""},
		{"cat docs/chrome-setup.md", ""},
		{"rg -l whisper .", ""},
		{"git commit -m 'Bump llama.cpp pin'", ""},
		{"echo 'ffmpeg; whisper'", ""},
		{"printf '%s' \"chrome && ffmpeg\"", ""},
		{"echo ignored # ffmpeg -i a b", ""},
		{"cat < ffmpeg > chrome", ""},
		{"echo hi &> log ffmpeg", ""},
		{"cat <<'EOF'\nffmpeg -i a b\nEOF", ""},
		{"npm info ffmpeg", ""},
		{"go version whisper", ""},
		{"sudo -u ffmpeg grep -rn whisper .", ""},
		{"env -u ffmpeg cat docs/chrome-setup.md", ""},
		{"nice --adjustment 5 grep ffmpeg .", ""},
		{"npx --package ffmpeg -- cat chrome.txt", ""},
		{"npx -c 'ffmpeg -i a b'", ""},
		{"time -o ffmpeg true", ""},
		{"ffmpeg -i a.mp4 b.mp4", "encode"},
		{"npx remotion render", "render"},
		{"FOO=1 ffmpeg -i a b", "encode"},
		{"2>log ffmpeg -i a b", "encode"},
		{"echo hi # comment\nffmpeg -i a b", "encode"},
		{"FOO='a b' BAR=2 /usr/bin/ffmpeg -i a b", "encode"},
		{"sudo -n -u root -- ffmpeg -i a b", "encode"},
		{"time -p ffmpeg -i a b", "encode"},
		{"nice -n 5 ffmpeg -i a b", "encode"},
		{"env -i -u FOO BAR=x ffmpeg -i a b", "encode"},
		{"env --chdir /tmp nice --adjustment=5 sudo --user=root time -p ffmpeg -i a b", "encode"},
		{"echo done;ffmpeg -i a b", "encode"},
		{"echo done\nffmpeg -i a b", "encode"},
		{"false||ffmpeg -i a b", "encode"},
		{"printf data|ffmpeg -i - b", "encode"},
		{"(ffmpeg -i a b)", "encode"},
		{"echo baton run; ffmpeg -i a b", "encode"},
		{"baton run --lane encode -- ffmpeg -i a b; whisper audio.wav", "ml"},
		{"env FOO=1 baton run -- ffmpeg -i a b", ""},
		{"grep ffmpeg .; cat chrome.txt", ""},
		{"echo 'a; b'&&npm ci", "install"},
		{"echo a\\;ffmpeg", ""},
		{"pnpm exec playwright test", "render"},
		{"npm exec -- remotion render", "render"},
		{"python3 -m mlx_whisper audio.wav", "ml"},
		{"sh -c 'ffmpeg -i a b'", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			isolate(t)
			t.Setenv("SHELL", "/bin/sh")
			var out, diag bytes.Buffer
			input, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": tc.command, "timeout": 123}})
			if err != nil {
				t.Fatal(err)
			}
			if code := Run([]string{"_hook"}, bytes.NewReader(input), &out, &diag); code != 0 {
				t.Fatalf("hook code=%d: %s", code, &diag)
			}
			if tc.lane == "" {
				if out.Len() != 0 {
					t.Fatalf("argument selected a lane: %s", &out)
				}
				return
			}
			var response struct {
				Output struct {
					Input struct {
						Command string `json:"command"`
						Timeout int    `json:"timeout"`
					} `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatalf("missing queued command: %s (%v)", &out, err)
			}
			if !strings.Contains(response.Output.Input.Command, " run --lane '"+tc.lane+"' -- ") || response.Output.Input.Timeout != 123 {
				t.Fatalf("wrong lane or changed input: %s", &out)
			}
			// The blocking fallback must print exactly the same runnable wrapper.
			var blocked, message bytes.Buffer
			if code := Run([]string{"_hook-block"}, bytes.NewReader(input), &blocked, &message); code != exitUsage || blocked.Len() != 0 || !strings.Contains(message.String(), response.Output.Input.Command) {
				t.Fatalf("block code=%d: %s %s", code, &blocked, &message)
			}
		})
	}
}
