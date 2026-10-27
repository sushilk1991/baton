package config

import (
	"testing"
	"time"
)

// Reap overrides must reach consumers, including an empty opt-out matcher list.
func TestReapPolicy(t *testing.T) {
	settings, err := decode("[reap]\nolder_than='3h'\ngrace_period='20ms'\nmatchers=['^ffmpeg ']\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	older, grace := settings.ReapTiming()
	if older != 3*time.Hour || grace != 20*time.Millisecond || !settings.ReapMatch([]string{"ffmpeg", "-i"}) || settings.ReapMatch([]string{"chromium", "--headless"}) {
		t.Fatalf("reap policy: age=%s grace=%s", older, grace)
	}
	empty, err := decode("[reap]\nmatchers=[]\n", defaults(1, gib, Paths{CacheDir: t.TempDir()}))
	if err != nil || empty.ReapMatch([]string{"ffmpeg"}) {
		t.Fatalf("empty matcher list: %v", err)
	}
}
