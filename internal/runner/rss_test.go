package runner

import (
	"fmt"
	"math"
	"testing"
)

func TestSumTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []processRow
		root int
		want uint64
	}{
		{"empty", nil, 1, 0},
		{"missing root", []processRow{{2, 1, 20}}, 1, 20},
		{"descendants only", []processRow{{1, 0, 10}, {2, 1, 20}, {3, 2, 30}, {4, 0, 40}}, 2, 50},
		{"cycle", []processRow{{1, 2, 10}, {2, 1, 20}}, 1, 30},
		{"self parent", []processRow{{1, 1, 10}}, 1, 10},
		{"duplicate pid", []processRow{{1, 0, 10}, {2, 1, 20}, {2, 1, 99}}, 1, 30},
		{"saturation", []processRow{{1, 0, math.MaxUint64}, {2, 1, 1}}, 1, math.MaxUint64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sumTree(tc.rows, tc.root); got != tc.want {
				t.Fatalf("tree RSS = %d, want %d", got, tc.want)
			}
		})
	}
}

func BenchmarkSumTree(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			rows := make([]processRow, size)
			for n := range rows {
				rows[n] = processRow{pid: n + 1, parent: n, rss: 1}
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if got := sumTree(rows, 1); got != uint64(size) {
					b.Fatalf("tree RSS = %d, want %d", got, size)
				}
			}
		})
	}
}
