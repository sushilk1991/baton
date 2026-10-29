package pressure

import (
	"context"
	"testing"
)

// The syscall read must agree with the sysctl command that Sample parses.
func TestTotalBytesMatchesSample(t *testing.T) {
	isolateHome(t)
	sample, err := System().Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	total, err := TotalBytes(context.Background())
	if err != nil || total != sample.TotalBytes {
		t.Fatalf("TotalBytes %d (%v), Sample %d", total, err, sample.TotalBytes)
	}
}
