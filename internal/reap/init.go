package reap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	noContainer     = "none"
	noContainerExit = 1
)

func (systemDriver) hostInit(ctx context.Context) bool {
	// A normal user's process snapshot omits root's argv. Inspect init
	// separately; unknown or unreadable environments require job records.
	switch runtime.GOOS {
	case "darwin":
		data, err := exec.CommandContext(ctx, "/bin/ps", "-p", "1", "-o", "comm=").Output()
		return err == nil && strings.TrimSpace(string(data)) == "/sbin/launchd"
	case "linux":
		name, err := os.ReadFile("/proc/1/comm")
		if err != nil || !linuxInit(string(name)) {
			return false
		}
		// systemd can itself be container PID 1. Require an explicit "none"
		// result, not merely a failed or missing container detector.
		data, err := exec.CommandContext(ctx, "/usr/bin/systemd-detect-virt", "--container").Output()
		return outsideContainer(data, err)
	default:
		return false
	}
}

func linuxInit(name string) bool {
	name = strings.TrimSpace(name)
	return name == "init" || name == "systemd"
}

func outsideContainer(data []byte, err error) bool {
	var exit *exec.ExitError
	return strings.TrimSpace(string(data)) == noContainer && errors.As(err, &exit) && exit.ExitCode() == noContainerExit
}
