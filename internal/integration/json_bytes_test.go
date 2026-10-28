package integration

import (
	"testing"
)

func TestHookRemovalBytes(t *testing.T) {
	// Semantic ownership survives a user moving or reformatting the owned
	// entry. Neighboring hooks retain their exact spelling and separators.
	const user = `{"command":"echo && > [ } \\\"", "timeout":9007199254740993}`
	const owned = `{"command":"baton init _hook"}`
	before := []byte(`{"hooks":{"PreToolUse":[` + user + `]}}`)
	after := []byte(`{"hooks":{"PreToolUse":[` + user + `,` + owned + `]}}`)
	for _, tc := range []struct{ current, want string }{
		{`{ "z":1e2,"hooks" : { "PreToolUse" : [ {"command" : "baton init _hook"}, ` + user + ` ] }, "a":"&& >" }`, `{ "z":1e2,"hooks" : { "PreToolUse" : [ ` + user + ` ] }, "a":"&& >" }`},
		{`{"hooks":{"PreToolUse":[` + user + `, {"command" : "baton init _hook"}, {"command":"later && >"}]}}`, `{"hooks":{"PreToolUse":[` + user + `, {"command":"later && >"}]}}`},
		{`{"hooks":{"PreToolUse":[` + user + `, {"command":"later && >"}, {"command" : "baton init _hook"} ]}}`, `{"hooks":{"PreToolUse":[` + user + `, {"command":"later && >"} ]}}`},
	} {
		got, err := undoJSON(before, after, []byte(tc.current))
		if err != nil || string(got) != tc.want {
			t.Fatalf("got %q (%v), want %q", got, err, tc.want)
		}
	}

	// A user event added under a baton-created hooks object survives removal
	// of PreToolUse; only now-empty parents belong to the installation.
	current := []byte(`{ "z":0, "hooks" : {"Stop" : [{"command":"echo && >"}], "PreToolUse":[` + owned + `] }, "a":1 }`)
	want := `{ "z":0, "hooks" : {"Stop" : [{"command":"echo && >"}] }, "a":1 }`
	got, err := undoJSON([]byte(`{"z":0,"a":1}`), []byte(`{"z":0,"hooks":{"PreToolUse":[`+owned+`]},"a":1}`), current)
	if err != nil || string(got) != want {
		t.Fatalf("created parent: got %q (%v), want %q", got, err, want)
	}
}
