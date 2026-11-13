package runner

import "math"

type processRow struct {
	pid    int
	parent int
	rss    uint64
}

func sumTree(rows []processRow, root int) uint64 {
	children := make(map[int][]int, len(rows))
	rss := make(map[int]uint64, len(rows))
	for _, row := range rows {
		children[row.parent] = append(children[row.parent], row.pid)
		// Keep the first sample when a PID appears twice in a raced snapshot.
		if _, exists := rss[row.pid]; !exists {
			rss[row.pid] = row.rss
		}
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
		pending = append(pending, children[pid]...)
		if math.MaxUint64-total < rss[pid] {
			return math.MaxUint64
		}
		total += rss[pid]
	}
	return total
}
