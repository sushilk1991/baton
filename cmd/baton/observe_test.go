package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObserveEmpty(t *testing.T) {
	isolateHome(t)
	for _, command := range []string{"status", "history"} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{command, "--json"}, &out, &diagnostics); code != exitOK {
			t.Fatalf("%s: exit=%d stdout=%q stderr=%q", command, code, &out, &diagnostics)
		}
		if command == "status" {
			var value struct {
				Lanes []struct {
					Name        string
					Used, Total int
				}
				Running, Queue []any
			}
			if err := json.Unmarshal(out.Bytes(), &value); err != nil || len(value.Lanes) != 5 || value.Running == nil || value.Queue == nil {
				t.Fatalf("status=%s err=%v", &out, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "baton")); !os.IsNotExist(err) {
		t.Fatalf("read commands created state: %v", err)
	}
	var out bytes.Buffer
	if code := run([]string{"status", "--line"}, &out, &bytes.Buffer{}); code != exitOK || !strings.Contains(out.String(), "0 waiting") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("line: exit=%d out=%q", code, &out)
	}
}

func TestObserveUsageAndOutput(t *testing.T) {
	isolateHome(t)
	for _, args := range [][]string{
		{"status", "--json", "--line"}, {"status", "extra"}, {"history", "extra"},
		{"history", "--since", "-1s"}, {"history", "--since", "nonsense"}, {"status", "--unknown"},
	} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("%v: exit=%d want %d", args, code, exitUsage)
		}
	}
	for _, command := range []string{"status", "history"} {
		if code := run([]string{command}, failedWriter{}, &bytes.Buffer{}); code != exitFailure {
			t.Fatalf("%s output failure: exit=%d", command, code)
		}
	}
}

func TestObserveLiveCLI(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	t.Setenv("BATON_OBSERVE_JOB", "1")
	configPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "baton", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[lanes.x]\nslots=2\nmemory_gate=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var children []*exec.Cmd
	var diagnostics []*bytes.Buffer
	// Release only these test-owned jobs, including on assertion failure.
	defer func() {
		_ = os.WriteFile(filepath.Join(root, "release"), nil, 0600)
		for i, child := range children {
			if err := child.Wait(); err != nil {
				t.Errorf("owned CLI exit: %v: %s", err, diagnostics[i])
			}
		}
	}()
	launch := func(label, priority, weight string) {
		command := cliCommand(t, "run", "--lane", "x", "--weight", weight, "--priority", priority, "--label", label, "--agent", "test-agent", "--quiet", "--", executable, "-test.run=^TestObserveJob$", "--", root, label)
		command.Dir = root
		buffer := &bytes.Buffer{}
		command.Stderr = buffer
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, command)
		diagnostics = append(diagnostics, buffer)
	}
	snapshot := func() struct {
		Running, Queue []struct {
			Label, Agent, Lane string
			Position           int
			QueueSequence      uint64 `json:"queue_sequence"`
			RSSBytes           uint64 `json:"rss_bytes"`
		}
		Lanes []struct {
			Name        string
			Used, Total int
		}
	} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{"status", "--json"}, &out, &diagnostics); code != exitOK {
			t.Fatalf("status exit=%d stderr=%s", code, &diagnostics)
		}
		var value struct {
			Running, Queue []struct {
				Label, Agent, Lane string
				Position           int
				QueueSequence      uint64 `json:"queue_sequence"`
				RSSBytes           uint64 `json:"rss_bytes"`
			}
			Lanes []struct {
				Name        string
				Used, Total int
			}
		}
		if err := json.Unmarshal(out.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	launch("holder", "normal", "2")
	deadline := time.Now().Add(10 * time.Second)
	for {
		view := snapshot()
		if len(view.Running) == 1 && view.Running[0].RSSBytes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("holder never appeared: %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
	launch("normal", "normal", "1")
	launch("high", "high", "1")
	for {
		view := snapshot()
		if len(view.Queue) == 2 {
			if view.Queue[0].Label != "high" || view.Queue[1].Label != "normal" || view.Queue[0].Position != 1 || view.Queue[0].QueueSequence == 0 || view.Running[0].Agent != "test-agent" {
				t.Fatalf("wrong identity/order: %+v", view)
			}
			found := false
			for _, lane := range view.Lanes {
				if lane.Name == "x" {
					found = true
					if lane.Used != 2 || lane.Total != 2 {
						t.Fatalf("slots=%+v", lane)
					}
				}
			}
			if !found {
				t.Fatal("configured lane missing")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters never appeared: %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for i, child := range children {
		if err := child.Wait(); err != nil {
			t.Fatalf("run exit: %v %s", err, diagnostics[i])
		}
	}
	children = nil
	view := snapshot()
	if len(view.Running) != 0 || len(view.Queue) != 0 {
		t.Fatalf("finished jobs still active: %+v", view)
	}
	var out, errors bytes.Buffer
	if code := run([]string{"history", "--json", "--since", "1h", "--lane", "x"}, &out, &errors); code != exitOK {
		t.Fatalf("history exit=%d: %s", code, &errors)
	}
	records := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(records) != 3 {
		t.Fatalf("history has %d records: %s", len(records), &out)
	}
	for _, record := range records {
		var job struct {
			Agent, Lane string
			ExitCode    int `json:"exit_code"`
			Start, End  time.Time
		}
		if err := json.Unmarshal(record, &job); err != nil || job.Agent != "test-agent" || job.Lane != "x" || job.ExitCode != 0 || job.Start.IsZero() || job.End.Before(job.Start) {
			t.Fatalf("history=%s err=%v", record, err)
		}
	}
}

func TestObserveJob(t *testing.T) {
	if os.Getenv("BATON_OBSERVE_JOB") != "1" {
		return
	}
	root := os.Args[len(os.Args)-2]
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("test-owned job timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
