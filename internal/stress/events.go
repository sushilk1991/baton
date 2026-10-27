package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxLogLine = 1 << 20

func readLog(data []byte) (uint64, error) {
	var events uint64
	finished := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxLogLine)
	for scanner.Scan() {
		var row struct {
			Message  *string `json:"eventMessage"`
			Count    *uint64 `json:"count"`
			Finished *int    `json:"finished"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return 0, err
		}
		if finished {
			return 0, errors.New("log output after completion marker")
		}
		if row.Message != nil && strings.TrimSpace(*row.Message) != "" {
			events++
			continue
		}
		if row.Count == nil || row.Finished == nil || *row.Finished != 1 || *row.Count != events {
			return 0, errors.New("incomplete or inconsistent log result")
		}
		finished = true
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if !finished {
		return 0, errors.New("missing log completion marker")
	}
	return events, nil
}

func readCounter(data []byte) (uint64, error) {
	var value uint64
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "oom_kill" {
			continue
		}
		if found || len(fields) != 2 {
			return 0, errors.New("duplicate or malformed oom_kill counter")
		}
		var err error
		value, err = strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		found = true
	}
	if !found {
		return 0, fmt.Errorf("missing oom_kill counter")
	}
	return value, nil
}
