package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/lockfile"
)

const (
	historyLimit = 10 << 20
	storePoll    = 10 * time.Millisecond
	ewmaDivisor  = 4
)

type jobRecord struct {
	ID            string    `json:"id"`
	Signature     string    `json:"signature"`
	Lane          string    `json:"lane"`
	Label         string    `json:"label"`
	Agent         string    `json:"agent"`
	Cwd           string    `json:"cwd"`
	Repo          string    `json:"repo"`
	User          string    `json:"user"`
	OwnerPID      int       `json:"owner_pid"`
	PID           int       `json:"pid"`
	ProcessStart  string    `json:"process_start,omitempty"`
	Weight        int       `json:"weight"`
	QueueSequence uint64    `json:"queue_sequence,omitempty"`
	Priority      string    `json:"priority"`
	Phase         string    `json:"phase"`
	Queued        time.Time `json:"queued"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	WaitNanos     int64     `json:"wait_ns"`
	ExitCode      int       `json:"exit_code"`
	MemoryBytes   uint64    `json:"memory_bytes"`
	RSSBytes      uint64    `json:"rss_bytes"`
	PeakRSSBytes  uint64    `json:"peak_rss_bytes"`
	CacheHit      bool      `json:"cache_hit"`
	// Shell identity is distinct from the holder PID: baton did not start it.
	ShellPID   int    `json:"shell_pid,omitempty"`
	ShellStart string `json:"shell_start,omitempty"`
	Nested     bool   `json:"nested"`
}

type store struct{ root string }

func newStore(root string) (*store, error) {
	if err := os.MkdirAll(filepath.Join(root, "jobs"), directoryMode); err != nil {
		return nil, err
	}
	return &store{root: root}, nil
}

func newJob(opts options) (jobRecord, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return jobRecord{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return jobRecord{}, err
	}
	// JSON preserves argv boundaries; cwd separates unrelated project builds.
	// No plaintext argv is retained: command arguments can contain credentials.
	payload, err := json.Marshal(struct {
		Args []string
		Cwd  string
	}{opts.args, cwd})
	if err != nil {
		return jobRecord{}, err
	}
	signature := sha256.Sum256(payload)
	label := opts.label
	if label == "" {
		label = filepath.Base(opts.args[0])
	}
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	return jobRecord{
		ID: hex.EncodeToString(id[:]), Signature: hex.EncodeToString(signature[:]),
		Lane: opts.lane, Label: label, Agent: detectAgent(opts.agent, os.Environ()),
		Cwd: cwd, Repo: repoRoot(cwd), User: user, OwnerPID: os.Getpid(),
		Weight: opts.weight, Priority: string(opts.priority), Phase: "queued", Queued: time.Now().UTC(),
	}, nil
}

func detectAgent(override string, env []string) string {
	if override != "" {
		return override
	}
	values := make(map[string]string)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if value != "" {
			values[key] = value
		}
	}
	if values["CLAUDECODE"] != "" {
		return "claude-code"
	}
	// Only trailing-underscore markers match prefixes; GEMINI_CLI excludes
	// credentials and configuration such as GEMINI_API_KEY or GEMINI_CLI_HOME.
	for _, marker := range []struct{ key, agent string }{{"CODEX_", "codex"}, {"CURSOR_", "cursor"}, {"GEMINI_CLI", "gemini"}, {"AIDER_", "aider"}, {"OPENCODE_", "opencode"}, {"GOOSE_", "goose"}} {
		for key := range values {
			if key == marker.key || strings.HasSuffix(marker.key, "_") && strings.HasPrefix(key, marker.key) {
				return marker.agent
			}
		}
	}
	if values["TERM_PROGRAM"] != "" {
		return values["TERM_PROGRAM"]
	}
	return "terminal"
}

func repoRoot(cwd string) string {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func (store *store) publish(job jobRecord) error {
	return writeJSON(filepath.Join(store.root, "jobs", job.ID+".json"), job)
}

func (store *store) remove(id string) {
	_ = os.Remove(filepath.Join(store.root, "jobs", id+".json"))
	_ = os.Remove(filepath.Join(store.root, "jobs", id+".lock"))
}

func (store *store) guard(ctx context.Context) (*lockfile.Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		guard, err := lockfile.Try(filepath.Join(store.root, "history.lock"))
		if !errors.Is(err, lockfile.ErrBusy) {
			return guard, err
		}
		timer := time.NewTimer(storePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (store *store) predict(ctx context.Context, signature string) (uint64, error) {
	guard, err := store.guard(ctx)
	if err != nil {
		return 0, err
	}
	defer guard.Close()
	learned, err := store.readLearned()
	return learned[signature], err
}

func (store *store) readLearned() (map[string]uint64, error) {
	values := make(map[string]uint64)
	data, err := os.ReadFile(filepath.Join(store.root, "learned.json"))
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return values, err
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, errors.New("learned memory must be a JSON object")
	}
	return values, nil
}

func (store *store) finish(ctx context.Context, job jobRecord) error {
	guard, err := store.guard(ctx)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err := store.appendHistory(job); err != nil {
		return err
	}
	if job.PeakRSSBytes == 0 {
		return nil
	}
	learned, err := store.readLearned()
	if err != nil {
		return err
	}
	previous := learned[job.Signature]
	value := job.PeakRSSBytes
	if previous != 0 {
		// alpha=1/4, written as a difference to avoid uint64 overflow.
		if value >= previous {
			value = previous + (value-previous)/ewmaDivisor
		} else {
			value = previous - (previous-value)/ewmaDivisor
		}
	}
	learned[job.Signature] = value
	return writeJSON(filepath.Join(store.root, "learned.json"), learned)
}

func (store *store) appendHistory(job jobRecord) error {
	path := filepath.Join(store.root, "history.jsonl")
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && info.Size() >= historyLimit {
		// Keep one previous segment. Rotation and append share the same guard,
		// so simultaneous finishes cannot interleave or overwrite a segment.
		if err := os.Remove(path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(path, path+".1"); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(file).Encode(job), file.Close())
}

func writeJSON(path string, value any) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := errors.Join(json.NewEncoder(file).Encode(value), file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Rename(file.Name(), path)
}
