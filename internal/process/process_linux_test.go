package process

import (
	"reflect"
	"strings"
	"testing"
)

// Parent and start fields follow the last ')' even when comm contains ')'.
func TestParseStat(t *testing.T) {
	fields := append([]string{"S", "17"}, strings.Fields(strings.Repeat("0 ", 17))...)
	fields = append(fields, "9876")
	row, ticks, err := parseStat([]byte("42 (name ) with spaces) "+strings.Join(fields, " ")), 42)
	if err != nil || row.Parent != 17 || row.Start != "9876" || ticks != 9876 {
		t.Fatalf("stat: %+v ticks=%d err=%v", row, ticks, err)
	}
	for _, bad := range []string{"bad", "42 (short) S 1", "42 (bad parent) S x " + strings.Repeat("0 ", 18)} {
		if _, _, err := parseStat([]byte(bad), 42); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestParseCmdline(t *testing.T) {
	if args := parseCmdline([]byte("chrome\x00two words\x00\x00--headless\x00")); !reflect.DeepEqual(args, []string{"chrome", "two words", "", "--headless"}) {
		t.Fatalf("argv: %q", args)
	}
	if args := parseCmdline([]byte("truncated")); args != nil {
		t.Fatalf("accepted %q", args)
	}
	if uid, err := parseUID([]byte("Name:\ttest\nUid:\t100\t101\t100\t100\n")); err != nil || uid != 101 {
		t.Fatalf("effective uid %d err=%v", uid, err)
	}
	if _, err := parseUID([]byte("Uid: invalid\n")); err == nil {
		t.Fatal("accepted invalid uid")
	}
}
