package runner

import (
	"errors"
	"os"
	"syscall"
	"time"
)

const (
	sharingViolation = syscall.Errno(32)
	// A collision lasts as long as the other side's read or rename. A handle
	// held past this bound is a real failure and is returned unchanged.
	shareRetry = 2 * time.Second
	shareSleep = time.Millisecond
)

// Windows opens files without FILE_SHARE_DELETE, so concurrent jobs that
// read and replace the same record collide until the other side closes:
//
//	writer: rename(.record-*, job.json)  <->  reader: open(job.json)
//	        ERROR_ACCESS_DENIED                       ERROR_SHARING_VIOLATION
//
// Each record operation retries those two errors briefly.
func readRecord(path string) ([]byte, error) {
	var data []byte
	err := retryShared(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

func replaceRecord(from, to string) error {
	return retryShared(func() error { return os.Rename(from, to) })
}

func removeRecord(path string) error {
	return retryShared(func() error { return os.Remove(path) })
}

func retryShared(operation func() error) error {
	deadline := time.Now().Add(shareRetry)
	for {
		err := operation()
		if !sharedBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(shareSleep)
	}
}

func sharedBusy(err error) bool {
	return errors.Is(err, sharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
