package main

import (
	"runtime/debug"
)

// The process entry point owns the global runtime policy, not server.New (which
// is also used by embedded hosts and tests). An explicit config value wins;
// otherwise the runtime's own interpretation of GOMEMLIMIT stands, including
// off. With neither configured, no limit is imposed: a guessed cap paces the
// collector without reclaiming memory, and reclaiming is the idle worker's job.
func configureMemoryLimit(configured int64) int64 {
	if configured == 0 {
		return debug.SetMemoryLimit(-1)
	}
	debug.SetMemoryLimit(configured)
	return configured
}
