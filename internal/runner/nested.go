package runner

import (
	"context"
	"encoding/json"
	"os"
)

const lanesEnv = "_BATON_LANES"

func inheritedLanes(ctx context.Context, store *store) (map[string]string, error) {
	lanes := make(map[string]string)
	var inherited map[string]string
	// A stale or malformed marker grants no reservation. Matching live owner
	// locks also scope inheritance to the current state directory.
	if json.Unmarshal([]byte(os.Getenv(lanesEnv)), &inherited) != nil || len(inherited) == 0 {
		return lanes, nil
	}
	jobs, err := activeJobs(ctx, store.root)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		// A newly started child can arrive before its parent's RSS monitor
		// replaces queued metadata. Only admitted children receive this ID.
		if job.Lane == "" || job.Nested || inherited[job.Lane] != job.ID {
			continue
		}
		lanes[job.Lane] = job.ID
	}
	return lanes, nil
}

func childEnvironment(lanes map[string]string, job jobRecord) ([]string, error) {
	// runner -> child -> runner (same lane: reuse; new lane: acquire)
	// Retain every live ancestor lane for deeper calls, without changing the
	// runner's own environment or claiming another slot for a nested job.
	if job.Lane != "" && !job.Nested {
		lanes[job.Lane] = job.ID
	}
	data, err := json.Marshal(lanes)
	if err != nil {
		return nil, err
	}
	return append(os.Environ(), lanesEnv+"="+string(data)), nil
}
