package runner

import "testing"

func TestParsePS(t *testing.T) {
	rows := parsePS(" 12 1 100\n13 12 200\n14 1 9000\nbad row\n15 12 -1\n16 12 18446744073709551615\n")
	if got := sumTree(rows, 12); got != 300*rssKiB {
		t.Fatalf("parsed tree RSS %d, want %d", got, 300*rssKiB)
	}
}
