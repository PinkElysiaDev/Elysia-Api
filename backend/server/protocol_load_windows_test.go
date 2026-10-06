package server

import (
	"syscall"
	"time"
	"unsafe"
)

func loadCPUTime() (time.Duration, error) {
	var created, exited, kernel, user syscall.Filetime
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	getTimes := syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessTimes")
	result, _, err := getTimes.Call(uintptr(process), uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if result == 0 {
		return 0, err
	}
	return time.Duration((uint64(kernel.HighDateTime)<<32|uint64(kernel.LowDateTime))+(uint64(user.HighDateTime)<<32|uint64(user.LowDateTime))) * 100, nil
}
