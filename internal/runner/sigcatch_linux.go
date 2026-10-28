package runner

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const sigCaughtField = "SigCgt:"

// catchesSignal reports whether pid has a handler installed for sig.
// /proc/<pid>/status lists caught signals as a hex mask, bit n-1 for signal n:
// "SigCgt:\t0000000000080000" means SIGTSTP (20) is caught.
func catchesSignal(pid int, sig syscall.Signal) (bool, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, sigCaughtField) {
			continue
		}
		mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, sigCaughtField)), 16, 64)
		if err != nil {
			return false, err
		}
		return mask&(1<<(uint(sig)-1)) != 0, nil
	}
	return false, scanner.Err()
}
