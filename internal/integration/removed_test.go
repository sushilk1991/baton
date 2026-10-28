package integration

import "testing"

func TestRemovedPartialMarkers(t *testing.T) {
	isolate(t)
	t.Setenv("TMPDIR", t.TempDir())
	for _, data := range []string{startMarker, endMarker} {
		// A remaining marker is an edited block, never proof of removal.
		e := change{After: []byte(instructions)}
		installer := installer{}
		replayed, err := installer.replayRemoved(e, []byte(data))
		if err != nil || replayed != nil {
			t.Fatalf("replayed partial marker %q: %q (%v)", data, replayed, err)
		}
		if _, err := undoSnippet(e, []byte(data)); err == nil {
			t.Fatalf("accepted partial marker %q", data)
		}
	}
}

func TestRemovedHookNumbers(t *testing.T) {
	isolate(t)
	t.Setenv("TMPDIR", t.TempDir())
	// Different user hooks cannot prove deletion. Preserve integer precision
	// above 2^53, where float64 would make these two settings compare equal.
	e := change{
		Before: []byte(`{"hooks":{"PreToolUse":[{"timeout":9007199254740992}]}}`),
		After:  []byte(`{"hooks":{"PreToolUse":[{"timeout":9007199254740992},{"command":"baton init _hook"}]}}`),
	}
	data := []byte(`{"hooks":{"PreToolUse":[{"timeout":9007199254740993}]}}`)
	if hookRemoved(e, data) {
		t.Fatal("treated a changed user hook as proof of removal")
	}
}
