package process

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// Environment strings and empty arguments must not alter command identity.
func TestParseArgs(t *testing.T) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, 4)
	data = append(data, []byte("/opt/Google Chrome\x00\x00/opt/Google Chrome\x00--headless\x00\x00two words\x00SECRET=hidden\x00")...)
	args, err := parseArgs(data)
	if err != nil || !reflect.DeepEqual(args, []string{"/opt/Google Chrome", "--headless", "", "two words"}) {
		t.Fatalf("args: %q, %v", args, err)
	}
	for _, bad := range [][]byte{nil, {1, 0, 0, 0}, data[:len(data)-40]} {
		if args, err := parseArgs(bad); err == nil {
			t.Fatalf("accepted malformed args: %q", args)
		}
	}
}
