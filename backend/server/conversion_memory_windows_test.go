package server

import (
	"syscall"
	"unsafe"
)

var conversionMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func conversionWorkingSetBytes() uint64 {
	var counters struct {
		Size, Faults uint32
		Memory       [8]uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0
	}
	result, _, _ := conversionMemoryInfo.Call(uintptr(process), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if result == 0 {
		return 0
	}
	return uint64(counters.Memory[1])
}
