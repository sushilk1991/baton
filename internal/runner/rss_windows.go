package runner

import (
	"syscall"
	"unsafe"
)

const (
	processSnapshot = 0x00000002
	processQuery    = 0x0400
	processRead     = 0x0010
	maxPathChars    = 260
)

var (
	kernelDLL      = syscall.NewLazyDLL("kernel32.dll")
	psapiDLL       = syscall.NewLazyDLL("psapi.dll")
	createSnapshot = kernelDLL.NewProc("CreateToolhelp32Snapshot")
	processFirst   = kernelDLL.NewProc("Process32FirstW")
	processNext    = kernelDLL.NewProc("Process32NextW")
	getMemory      = psapiDLL.NewProc("GetProcessMemoryInfo")
)

type processEntry struct {
	size       uint32
	usage      uint32
	pid        uint32
	heap       uintptr
	module     uint32
	threads    uint32
	parent     uint32
	priority   int32
	flags      uint32
	executable [maxPathChars]uint16
}

type memoryCounters struct {
	size         uint32
	faults       uint32
	peakWorking  uintptr
	working      uintptr
	peakPaged    uintptr
	paged        uintptr
	peakNonpaged uintptr
	nonpaged     uintptr
	pagefile     uintptr
	peakPagefile uintptr
}

func treeRSS(pid int) (uint64, error) {
	handle, _, callErr := createSnapshot.Call(processSnapshot, 0)
	if handle == ^uintptr(0) {
		return 0, callErr
	}
	defer syscall.CloseHandle(syscall.Handle(handle))
	entry := processEntry{size: uint32(unsafe.Sizeof(processEntry{}))}
	ok, _, _ := processFirst.Call(handle, uintptr(unsafe.Pointer(&entry)))
	var rows []processRow
	for ok != 0 {
		rows = append(rows, processRow{pid: int(entry.pid), parent: int(entry.parent), rss: workingSet(entry.pid)})
		ok, _, _ = processNext.Call(handle, uintptr(unsafe.Pointer(&entry)))
	}
	return sumTree(rows, pid), nil
}

func workingSet(pid uint32) uint64 {
	handle, err := syscall.OpenProcess(processQuery|processRead, false, pid)
	if err != nil {
		return 0
	}
	defer syscall.CloseHandle(handle)
	counters := memoryCounters{size: uint32(unsafe.Sizeof(memoryCounters{}))}
	ok, _, _ := getMemory.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.size))
	if ok == 0 {
		return 0
	}
	return uint64(counters.working)
}
