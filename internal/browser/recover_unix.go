//go:build darwin || linux

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushilk1991/baton/internal/process"
)

type owner struct {
	PID     int    `json:"pid"`
	Start   string `json:"start"`
	Profile string `json:"profile"`
}

func saveOwner(root, profile string, c *child) error {
	info, err := process.Read(context.Background(), c.command.Process.Pid)
	if err != nil {
		return err
	}
	data, err := json.Marshal(owner{info.PID, info.Start, profile})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "owner.json"), data, privateFile)
}

func orphan(root string) (process.Info, error) {
	data, err := os.ReadFile(filepath.Join(root, "owner.json"))
	if os.IsNotExist(err) {
		return process.Info{}, process.ErrGone
	}
	if err != nil {
		return process.Info{}, err
	}
	var saved owner
	if err := json.Unmarshal(data, &saved); err != nil {
		return process.Info{}, fmt.Errorf("browser ownership metadata: %w", err)
	}
	if filepath.Dir(saved.Profile) != root || !strings.HasPrefix(filepath.Base(saved.Profile), "profile-") {
		return process.Info{}, errors.New("browser profile outside private state")
	}
	info, err := process.Read(context.Background(), saved.PID)
	if err != nil {
		return process.Info{}, err
	}
	// Birth identity prevents PID reuse; exact private-profile argv prevents
	// signalling a browser that exec'd into another command after the crash.
	if info.Dead || info.Start != saved.Start || info.UID != os.Geteuid() {
		return process.Info{}, process.ErrGone
	}
	for _, arg := range info.Args {
		if arg == "--user-data-dir="+saved.Profile {
			return info, nil
		}
	}
	return process.Info{}, errors.New("browser process changed; refusing recovery")
}

func orphaned(root string) error {
	_, err := orphan(root)
	if errors.Is(err, process.ErrGone) || os.IsNotExist(err) {
		return errStopped
	}
	if err != nil {
		return err
	}
	return errOrphan
}

func recoverChild(root string) error {
	info, err := orphan(root)
	if errors.Is(err, process.ErrGone) || os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := process.Send(context.Background(), info, process.Terminate); err != nil && !errors.Is(err, process.ErrGone) {
		return err
	}
	deadline := time.Now().Add(shutdownGrace)
	for time.Now().Before(deadline) {
		if _, err := orphan(root); errors.Is(err, process.ErrGone) || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(pollTime)
	}
	if err := process.Send(context.Background(), info, process.Kill); err != nil && !errors.Is(err, process.ErrGone) {
		return err
	}
	// Wait for observed death before a replacement may launch. Stale private
	// profiles remain after SIGKILL; no arbitrary filesystem tree is removed.
	deadline = time.Now().Add(requestTimeout)
	for time.Now().Before(deadline) {
		if _, err := orphan(root); errors.Is(err, process.ErrGone) || os.IsNotExist(err) {
			return nil
		}
		time.Sleep(pollTime)
	}
	return errors.New("orphaned browser did not exit")
}
