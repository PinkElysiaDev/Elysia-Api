//go:build windows

package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsDisconnectStateClassification(t *testing.T) {
	for state := uint32(0); state < tcpStateMax; state++ {
		isClosed, err := isClosedTCPState(state)
		wantClosed := state == 0 || state >= 7
		if err != nil || isClosed != wantClosed {
			t.Fatalf("TCP state %d: closed=%v err=%v", state, isClosed, err)
		}
	}
	for _, state := range []uint32{tcpStateMax, ^uint32(0)} {
		if isClosed, err := isClosedTCPState(state); isClosed || err == nil {
			t.Fatalf("invalid state %d was interpreted as a disconnect", state)
		}
	}
	for _, tc := range []struct {
		err      error
		isClosed bool
	}{
		{net.ErrClosed, true}, {windows.WSAECONNRESET, true}, {windows.WSAECONNABORTED, true}, {windows.WSAENOTCONN, true},
		{windows.WSAEOPNOTSUPP, false}, {windows.WSAEINVAL, false}, {windows.WSAEMSGSIZE, false},
		{windows.WSAESHUTDOWN, false}, {windows.WSAETIMEDOUT, false}, {windows.ERROR_IO_PENDING, false},
		{errors.New("unknown observation failure"), false},
	} {
		if got := isClosedWindowsSocket(fmt.Errorf("wrapped: %w", tc.err)); got != tc.isClosed {
			t.Fatalf("error %v: closed=%v", tc.err, got)
		}
	}
}

func TestWindowsDisconnectProbePreservesUnreadData(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "FIN", true: "RST"}[reset], func(t *testing.T) {
			listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			server, err := listener.AcceptTCP()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			raw, err := server.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			if isClosed, err := probeWindowsTCPDisconnect(raw); isClosed || err != nil {
				t.Fatalf("established socket query: closed=%v err=%v", isClosed, err)
			}
			const payload = "synthetic unread request bytes"
			if _, err := io.WriteString(client, payload); err != nil {
				t.Fatal(err)
			}
			if reset {
				if err := client.SetLinger(0); err != nil {
					t.Fatal(err)
				}
				err = client.Close()
			} else {
				err = client.CloseWrite()
			}
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				isClosed, err := probeWindowsTCPDisconnect(raw)
				if err != nil {
					t.Fatal("Windows disconnect capability unavailable", err)
				}
				if isClosed {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("peer close was not observed")
				}
				time.Sleep(time.Millisecond)
			}
			if !reset {
				_ = server.SetReadDeadline(deadline)
				body, err := io.ReadAll(server)
				if err != nil || string(body) != payload {
					t.Fatalf("observation consumed or changed unread data: %q %v", body, err)
				}
			}
			server.Close()
			if isClosed, err := probeWindowsTCPDisconnect(raw); !isClosed || err != nil {
				t.Fatalf("closed Go connection: closed=%v err=%v", isClosed, err)
			}
		})
	}
}
