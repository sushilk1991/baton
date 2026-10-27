package gc

import "errors"

type fileRoot struct{}

// Fail closed until Windows has a handle-relative, reparse-safe driver.
func openRoot(string) (*fileRoot, error) {
	return nil, errors.New("gc with configured roots is not supported on Windows")
}

func (*fileRoot) close() {}

func (*fileRoot) list(string) ([]entry, error) {
	return nil, errors.New("gc discovery is not supported on Windows")
}

func (*fileRoot) remove(entry) error {
	return errors.New("gc deletion is not supported on Windows")
}
