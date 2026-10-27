package process

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("reap process inspection is unavailable on Windows")

// List fails explicitly rather than guessing which Windows processes are safe.
func List(context.Context) ([]Info, error) {
	return nil, errUnsupported
}

func read(context.Context, int) (Info, error) {
	return Info{}, errUnsupported
}

// Send never terminates Windows processes without a supported identity driver.
func Send(context.Context, Info, Signal) error {
	return errUnsupported
}
