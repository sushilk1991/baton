package reap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushilk1991/baton/internal/process"
)

func readOrigins(ctx context.Context, d driver, root string) (map[int]string, error) {
	origins := make(map[int]string)
	entries, err := os.ReadDir(filepath.Join(root, "jobs"))
	if os.IsNotExist(err) {
		return origins, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, "jobs", entry.Name())
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("job record is not a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var record struct {
			PID   int    `json:"pid"`
			Owner int    `json:"owner_pid"`
			Start string `json:"process_start"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("job record %s: %w", path, err)
		}
		// Old records without a kernel birth token cannot prove ownership;
		// they still qualify through direct reparenting to init/launchd.
		if record.PID <= initPID || record.Owner <= initPID || record.Start == "" {
			continue
		}
		owner, err := d.Read(ctx, record.Owner)
		if errors.Is(err, process.ErrGone) || (err == nil && owner.Dead) {
			origins[record.PID] = record.Start
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("recorded owner %d: %w", record.Owner, err)
		}
	}
	return origins, nil
}
