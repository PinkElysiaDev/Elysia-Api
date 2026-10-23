//go:build !windows

package server

import (
	"errors"
	"syscall"
)

func newPlatformDisconnectMonitor() *httpDisconnectMonitor { return nil }

func isPeerReset(err error) bool { return errors.Is(err, syscall.ECONNRESET) }
