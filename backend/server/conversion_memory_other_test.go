//go:build !windows

package server

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Linux exposes current resident pages, not the process high-water mark.
// Zero denotes unavailable sampling on other platforms or a procfs read error.
func conversionWorkingSetBytes() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}
