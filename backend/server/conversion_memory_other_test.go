//go:build !windows

package server

// Working-set sampling in this Windows investigation is unavailable here;
// zero is explicitly an unavailable metric, never a measured zero RSS.
func conversionWorkingSetBytes() uint64 { return 0 }
