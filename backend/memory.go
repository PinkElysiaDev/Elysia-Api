package main

import (
	"os"
	"runtime/debug"
)

const defaultMemoryLimit = 256 << 20

// The process entry point owns the global runtime policy, not server.New (which
// is also used by embedded hosts and tests). An explicit config value wins;
// otherwise preserve the runtime's interpretation of GOMEMLIMIT, including off.
func configureMemoryLimit(configured int64) int64 {
	limit := configured
	if limit == 0 {
		if os.Getenv("GOMEMLIMIT") != "" {
			limit = debug.SetMemoryLimit(-1)
		} else {
			limit = defaultMemoryLimit
		}
	}
	debug.SetMemoryLimit(limit)
	return limit
}
