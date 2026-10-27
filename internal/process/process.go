// Package process inspects kernel process identities without cgo.
package process

import (
	"context"
	"errors"
	"reflect"
	"time"
)

// ErrGone means a process exited or its PID now names another process.
var ErrGone = errors.New("process is gone or changed")

// Info is an observed process, including a kernel birth identity.
type Info struct {
	// PID identifies this observation, not a durable process handle.
	PID int
	// Parent is the current parent PID.
	Parent int
	// UID is the effective user that owns the process.
	UID int
	// Start is the kernel birth token; compare it before acting on a PID.
	Start string
	// Started is the wall-clock approximation used for age filtering.
	Started time.Time
	// Args preserves argument boundaries and is never persisted by this driver.
	Args []string
	// Dead excludes zombies and processes already exiting.
	Dead bool
}

// Signal selects graceful termination or forced termination.
type Signal uint8

const (
	// Terminate requests normal process cleanup.
	Terminate Signal = iota
	// Kill forces exit after the grace period.
	Kill
)

// Same checks birth, owner, and argv so an exec or PID reuse fails closed.
func Same(left, right Info) bool {
	return left.PID == right.PID && left.Start != "" && left.Start == right.Start && left.UID == right.UID && reflect.DeepEqual(left.Args, right.Args) && !right.Dead
}

// Read inspects one PID; unreadable processes return an error, never a guess.
func Read(ctx context.Context, pid int) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if pid <= 0 {
		return Info{}, ErrGone
	}
	return read(ctx, pid)
}
