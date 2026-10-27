package gc

import (
	"errors"
	"io"
	"testing"
)

func TestGlobs(t *testing.T) {
	for _, test := range []struct {
		pattern, name string
		want          bool
	}{
		{"**/out/slice-*.mp4", "out/slice-1.mp4", true},
		{"**/out/slice-*.mp4", "a/b/out/slice-1.mp4", true},
		{"**/out/slice-*.mp4", "a/out/deep/slice-1.mp4", false},
		{"**/out/slice-*.mp4", "a/notout/slice-1.mp4", false},
		{"**/out/slice-*.mp4", "out/slice-1.mp4.bak", false},
		{"**/.remotion/**", ".remotion/frame.png", true},
		{"**/.remotion/**", "a/.remotion/deep/frame.png", true},
		{"frames/*.png", "frames/deep/frame.png", false},
		{"frames/[0-9]?.png", "frames/12.png", true},
		{"frames/[0-9]?.png", "frames/aa.png", false},
		{"**/**/a/**/b", "a/b", true},
		{"**/**/a/**/b", "x/a/y/z/b", true},
		{"**/**/a/**/b", "x/a/y/z/c", false},
		{"[", "file", false},
	} {
		if got := matches([]string{test.pattern}, test.name); got != test.want {
			t.Errorf("%q vs %q: %v, want %v", test.pattern, test.name, got, test.want)
		}
	}
	if matches(nil, "anything") {
		t.Fatal("empty patterns selected a file")
	}
}

type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) {
	return 0, errors.New("output closed")
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) {
	return 0, nil
}

func TestOutputErrors(t *testing.T) {
	if err := printRows(closedWriter{}, nil); err == nil {
		t.Fatal("closed output succeeded")
	}
	if err := printRows(shortWriter{}, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short output: %v", err)
	}
}
