package config

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// GC consumers receive the configured policy without access to mutable DTOs.
func TestGCPolicy(t *testing.T) {
	root := t.TempDir()
	settings, err := decode(fmt.Sprintf("[gc]\nroots=[%q]\npatterns=['frames/**']\nolder_than='72h'\n", root), defaults(1, gib, Paths{CacheDir: root}))
	if err != nil {
		t.Fatal(err)
	}
	roots, patterns, age := settings.GCPolicy()
	if len(roots) != 1 || roots[0] != root || len(patterns) != 1 || patterns[0] != "frames/**" || age != 72*time.Hour {
		t.Fatalf("policy: roots=%v patterns=%v age=%v", roots, patterns, age)
	}
	roots[0] = filepath.Join(root, "changed")
	patterns[0] = "**"
	roots, patterns, _ = settings.GCPolicy()
	if roots[0] != root || patterns[0] != "frames/**" {
		t.Fatal("consumer changed stored gc policy")
	}
	empty, err := decode("[gc]\nroots=[]\npatterns=[]\n", defaults(1, gib, Paths{CacheDir: root}))
	if err != nil {
		t.Fatal(err)
	}
	roots, patterns, _ = empty.GCPolicy()
	if len(roots) != 0 || len(patterns) != 0 {
		t.Fatalf("empty policy ignored: %v %v", roots, patterns)
	}
}
