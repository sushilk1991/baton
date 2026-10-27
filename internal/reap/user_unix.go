//go:build darwin || linux

package reap

import "os"

func currentUID() int { return os.Geteuid() }
