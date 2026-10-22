//go:build windows

package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	windowsTCPInfoControl = 0xd8000027 // _WSAIORW(IOC_VENDOR, 39), SIO_TCP_INFO
	windowsTCPInfoSize    = 88         // TCP_INFO_v0, including ABI padding
	tcpStateClosed        = 0
	tcpStateCloseWait     = 7
	tcpStateClosing       = 8
	tcpStateLastAck       = 9
	tcpStateTimeWait      = 10
	tcpStateMax           = 11
)

func newPlatformDisconnectMonitor() *httpDisconnectMonitor {
	return newHTTPDisconnectMonitor(probeWindowsTCPDisconnect)
}

// Query the accepted TCP socket without reading data or changing its deadlines.
// Version 0 is available on Windows 10 1703 / Server 2016 and later. Byte buffers
// avoid relying on Go struct alignment for the Windows TCP_INFO_v0 ABI.
func probeWindowsTCPDisconnect(raw syscall.RawConn) (bool, error) {
	var version [4]byte
	var info [windowsTCPInfoSize]byte
	var returned uint32
	var queryErr error
	err := raw.Control(func(handle uintptr) {
		queryErr = windows.WSAIoctl(windows.Handle(handle), windowsTCPInfoControl,
			&version[0], uint32(len(version)), &info[0], uint32(len(info)), &returned, nil, 0)
	})
	if err == nil {
		err = queryErr
	}
	if err != nil {
		if isClosedWindowsSocket(err) {
			return true, nil
		}
		return false, err
	}
	if returned != windowsTCPInfoSize {
		return false, fmt.Errorf("SIO_TCP_INFO returned %d bytes, expected %d", returned, windowsTCPInfoSize)
	}
	return isClosedTCPState(binary.LittleEndian.Uint32(info[:4]))
}

func isClosedWindowsSocket(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, windows.WSAECONNRESET) ||
		errors.Is(err, windows.WSAECONNABORTED) || errors.Is(err, windows.WSAENOTCONN)
}

func isClosedTCPState(state uint32) (bool, error) {
	switch state {
	case tcpStateClosed, tcpStateCloseWait, tcpStateClosing, tcpStateLastAck, tcpStateTimeWait:
		return true, nil
	default:
		if state >= tcpStateMax {
			return false, fmt.Errorf("SIO_TCP_INFO returned invalid TCP state %d", state)
		}
		// FIN_WAIT_1/2 only establish that this endpoint sent FIN. They do not
		// prove a peer disconnect; neither do listen/handshake states.
		return false, nil
	}
}
