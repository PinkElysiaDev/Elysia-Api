//go:build !windows

package server

func newPlatformDisconnectMonitor() *httpDisconnectMonitor { return nil }
