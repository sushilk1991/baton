package runner

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	fields := append([]string{"S", "1"}, strings.Fields(strings.Repeat("0 ", 19))...)
	fields = append(fields, "10")
	data := fmt.Sprintf("12 (comm with ) spaces) %s", strings.Join(fields, " "))
	row, ok := parseProcStat(data, 12, 4096)
	if !ok || row.parent != 1 || row.rss != 40960 {
		t.Fatalf("parsed %+v valid %t", row, ok)
	}
	for _, input := range []string{"bad", "12 (bad) S", strings.Replace(data, "S 1", "S no", 1), strings.Replace(data, "10", "-1", 1)} {
		if _, ok := parseProcStat(input, 12, 4096); ok {
			t.Fatalf("accepted malformed stat: %q", input)
		}
	}
}
