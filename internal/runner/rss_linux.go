package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const statRSSIndex = 21

func treeRSS(pid int) (uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	var rows []processRow
	for _, entry := range entries {
		id, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			// Processes may exit or deny inspection between enumeration and read.
			continue
		}
		row, ok := parseProcStat(string(data), id, uint64(os.Getpagesize()))
		if ok {
			rows = append(rows, row)
		}
	}
	return sumTree(rows, pid), nil
}

func parseProcStat(data string, pid int, page uint64) (processRow, bool) {
	// comm is parenthesized and can itself contain spaces and ')'. Fields
	// after its final ')' start at state (field 3); RSS is field 24.
	end := strings.LastIndexByte(data, ')')
	if end < 0 {
		return processRow{}, false
	}
	fields := strings.Fields(data[end+1:])
	if len(fields) <= statRSSIndex {
		return processRow{}, false
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return processRow{}, false
	}
	pages, err := strconv.ParseUint(fields[statRSSIndex], 10, 64)
	if err != nil || page == 0 || pages > ^uint64(0)/page {
		return processRow{}, false
	}
	return processRow{pid: pid, parent: parent, rss: pages * page}, true
}
