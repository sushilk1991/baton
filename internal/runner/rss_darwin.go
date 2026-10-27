package runner

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const rssKiB = 1024

func treeRSS(pid int) (uint64, error) {
	// ps exposes all parent/RSS pairs without cgo. Fix locale and deadline;
	// its own process is a child of baton, not of the measured command.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,rss=")
	command.Env = append(os.Environ(), "LC_ALL=C")
	data, err := command.Output()
	if err != nil {
		return 0, err
	}
	return sumTree(parsePS(string(data)), pid), nil
}

func parsePS(data string) []processRow {
	var rows []processRow
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, firstErr := strconv.Atoi(fields[0])
		parent, secondErr := strconv.Atoi(fields[1])
		rss, thirdErr := strconv.ParseUint(fields[2], 10, 64)
		if firstErr != nil || secondErr != nil || thirdErr != nil || rss > ^uint64(0)/rssKiB {
			continue
		}
		rows = append(rows, processRow{pid: pid, parent: parent, rss: rss * rssKiB})
	}
	return rows
}
