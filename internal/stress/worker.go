package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"runtime"
	"time"
)

const (
	wordBytes     = 8
	bitsPerByte   = 8
	fillChunk     = 1 << 20
	workerPoll    = 10 * time.Millisecond
	retouchPeriod = 100 * time.Millisecond
	xorshiftSeed  = uint64(0x2545f4914f6cdd1d)
	xorshiftLeft  = 13
	xorshiftRight = 7
	xorshiftFinal = 17
)

type jobRecord struct {
	PID       int       `json:"pid"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended"`
	Bytes     uint64    `json:"allocated_bytes"`
	Touches   uint64    `json:"page_touches"`
	Completed bool      `json:"completed"`
}

func workerMain(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	var bytes uint64
	var hold time.Duration
	var record, gate string
	flags.Uint64Var(&bytes, "bytes", 0, "allocation bytes")
	flags.DurationVar(&hold, "hold", 0, "hold duration")
	flags.StringVar(&record, "record", "", "worker record")
	flags.StringVar(&gate, "gate", "", "launch barrier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if bytes == 0 || bytes >= uint64(^uint(0)>>1) || hold <= 0 || record == "" || gate == "" || flags.NArg() != 0 {
		return errors.New("invalid worker arguments")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	// The parent owns the sole write end. EOF also handles a killed harness,
	// including when baton has put the worker in a separate process group.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	return work(ctx, bytes, hold, record, gate)
}

func work(ctx context.Context, bytes uint64, hold time.Duration, record, gate string) (err error) {
	ticker := time.NewTicker(workerPoll)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	job := jobRecord{PID: os.Getpid(), Started: time.Now()}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeJSON(record, job); err != nil {
		return err
	}
	defer func() {
		job.Ended, job.Completed = time.Now(), err == nil
		err = errors.Join(err, writeJSON(record, job))
	}()

	// Pseudorandom full-page contents resist zero-page and repeated-byte
	// compression. Revisit each real OS page while retaining the whole slice.
	data := make([]byte, int(bytes))
	value := xorshiftSeed ^ uint64(os.Getpid())
	for i := 0; i < len(data); i++ {
		if i%wordBytes == 0 {
			value ^= value << xorshiftLeft
			value ^= value >> xorshiftRight
			value ^= value << xorshiftFinal
		}
		data[i] = byte(value >> (uint(i%wordBytes) * bitsPerByte))
		if i%fillChunk == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	job.Bytes = bytes
	job.Touches = (bytes + uint64(os.Getpagesize()) - 1) / uint64(os.Getpagesize())
	retouch := time.NewTicker(retouchPeriod)
	defer retouch.Stop()
	deadline := time.NewTimer(hold)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			runtime.KeepAlive(data)
			return nil
		case <-retouch.C:
			for i := 0; i < len(data); i += os.Getpagesize() {
				data[i]++
				job.Touches++
			}
		}
	}
}
