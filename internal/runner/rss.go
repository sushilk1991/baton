package runner

import "math"

type processRow struct {
	pid    int
	parent int
	rss    uint64
}

func sumTree(rows []processRow, root int) uint64 {
	children := make(map[int][]processRow)
	for _, row := range rows {
		children[row.parent] = append(children[row.parent], row)
	}
	pending := []int{root}
	seen := make(map[int]bool)
	var total uint64
	// root -> children -> grandchildren. A visited set handles a PID race or
	// malformed cyclic snapshot without double-counting or looping forever.
	for len(pending) > 0 {
		pid := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		for _, row := range children[pid] {
			pending = append(pending, row.pid)
		}
		for _, row := range rows {
			if row.pid != pid {
				continue
			}
			if math.MaxUint64-total < row.rss {
				return math.MaxUint64
			}
			total += row.rss
			break
		}
	}
	return total
}
