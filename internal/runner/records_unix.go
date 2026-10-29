//go:build darwin || linux

package runner

import "os"

// Unix rename replaces a record atomically while readers keep the old
// inode, so record operations need no retry here; see records_windows.go.
func readRecord(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func replaceRecord(from, to string) error {
	return os.Rename(from, to)
}

func removeRecord(path string) error {
	return os.Remove(path)
}
