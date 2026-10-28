package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sushilk1991/baton/internal/lockfile"
)

// Inherited environment alone cannot bypass a lane after its owner exits.
func TestNestedInheritance(t *testing.T) {
	for _, test := range []struct {
		name, phase, marker string
		nested, released    bool
		want                map[string]string
	}{
		{"running", "running", `{"install":"parent"}`, false, false, map[string]string{"install": "parent"}},
		{"child startup", "queued", `{"install":"parent"}`, false, false, map[string]string{"install": "parent"}},
		{"nested", "running", `{"install":"parent"}`, true, false, map[string]string{}},
		{"released", "running", `{"install":"parent"}`, false, true, map[string]string{}},
		{"wrong lane", "running", `{"encode":"parent"}`, false, false, map[string]string{}},
		{"wrong state", "running", `{"install":"other"}`, false, false, map[string]string{}},
		{"malformed", "running", `{`, false, false, map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateHome(t)
			t.Setenv(lanesEnv, test.marker)
			store, err := newStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			owner, err := lockfile.Try(filepath.Join(store.root, "jobs", "parent.lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if err := store.publish(jobRecord{ID: "parent", Lane: "install", OwnerPID: os.Getpid(), Phase: test.phase, Nested: test.nested}); err != nil {
				t.Fatal(err)
			}
			if test.released {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			got, err := inheritedLanes(context.Background(), store)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("inherited %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestNestedChildEnvironment(t *testing.T) {
	isolateHome(t)
	t.Setenv(lanesEnv, "unchanged")
	lanes := map[string]string{"install": "parent"}
	for _, job := range []jobRecord{
		{ID: "nested", Lane: "install", Nested: true},
		{ID: "unmatched"},
		{ID: "encode", Lane: "encode"},
	} {
		env, err := childEnvironment(lanes, job)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		entry := env[len(env)-1]
		if err := json.Unmarshal([]byte(entry[len(lanesEnv)+1:]), &got); err != nil {
			t.Fatal(err)
		}
		if got["install"] != "parent" || got[""] != "" || (job.Lane == "encode" && got["encode"] != "encode") {
			t.Fatalf("lost ancestor or added an unheld lane: %v", got)
		}
		if os.Getenv(lanesEnv) != "unchanged" {
			t.Fatal("runner environment changed")
		}
	}
}
