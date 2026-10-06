//go:build linux || darwin

package server

import (
	"syscall"
	"time"
)

func loadCPUTime() (time.Duration, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), nil
}
