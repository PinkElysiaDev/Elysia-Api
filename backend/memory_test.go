package main

import (
	"runtime/debug"
	"testing"
)

func TestConfigureMemoryLimit(t *testing.T) {
	previous := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })
	for _, test := range []struct {
		name, environment         string
		configured, current, want int64
	}{
		{"unconfigured", "", 0, 1 << 30, 1 << 30},
		{"environment", "768MiB", 0, 768 << 20, 768 << 20},
		{"environment-off", "off", 0, 1<<63 - 1, 1<<63 - 1},
		{"explicit-wins", "1GiB", 192 << 20, 1 << 30, 192 << 20},
		{"explicit-not-clamped", "", 123456789, 1 << 30, 123456789},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GOMEMLIMIT", test.environment)
			debug.SetMemoryLimit(test.current)
			if got := configureMemoryLimit(test.configured); got != test.want || debug.SetMemoryLimit(-1) != test.want {
				t.Fatalf("limit = %d, runtime = %d; want %d", got, debug.SetMemoryLimit(-1), test.want)
			}
		})
	}
}
