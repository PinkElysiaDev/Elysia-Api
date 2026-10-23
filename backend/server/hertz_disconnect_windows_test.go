//go:build windows

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
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
				var observation *downstreamObservationError
				if err != nil && (!isClosed || !errors.As(err, &observation)) {
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
			if isClosed, err := probeWindowsTCPDisconnect(raw); !isClosed || !errors.Is(err, net.ErrClosed) {
				t.Fatalf("closed Go connection: closed=%v err=%v", isClosed, err)
			}
		})
	}
}

type failedSocketObservation struct{ cause error }

func (raw failedSocketObservation) Control(func(uintptr)) error    { return raw.cause }
func (raw failedSocketObservation) Read(func(uintptr) bool) error  { return raw.cause }
func (raw failedSocketObservation) Write(func(uintptr) bool) error { return raw.cause }

func TestWindowsAbortProbeAndWriteHaveSameUnknownOrigin(t *testing.T) {
	for _, socketErr := range []error{windows.WSAECONNABORTED, windows.WSAENOTCONN, net.ErrClosed} {
		closed, observedErr := probeWindowsTCPDisconnect(failedSocketObservation{socketErr})
		if !closed || !errors.Is(observedErr, socketErr) {
			t.Fatal("socket closure lost its evidence", closed, observedErr)
		}
		monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) { return closed, observedErr })
		ctx, finish := monitor.watch(t.Context(), disconnectTestConn{})
		awaitDisconnectSignal(t, ctx.Done())
		for _, requestCtx := range []context.Context{t.Context(), ctx} {
			record := &usageRecord{StatusCode: 200}
			setUsageError(record, requestCtx, wrapDownstreamError(socketErr, deliveryFlush))
			if record.StatusCode != 502 || record.ErrorKind != deliveryTransport {
				t.Fatal("polling order attributed an unknown abort to the client", record.StatusCode, record.ErrorKind)
			}
		}
		finish()
		monitor.close()
	}
}

func TestWindowsResetProbeAndWriteHaveSamePeerEvidence(t *testing.T) {
	closed, observedErr := probeWindowsTCPDisconnect(failedSocketObservation{windows.WSAECONNRESET})
	if !closed || observedErr != nil {
		t.Fatal("RESET probe did not establish peer evidence", closed, observedErr)
	}
	monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) { return closed, observedErr })
	t.Cleanup(monitor.close)
	observed, finish := monitor.watch(t.Context(), disconnectTestConn{})
	defer finish()
	awaitDisconnectSignal(t, observed.Done())
	unknown, cancelUnknown := context.WithCancelCause(t.Context())
	cancelUnknown(&downstreamObservationError{cause: windows.WSAECONNABORTED})
	for _, phase := range []string{deliveryWrite, deliveryFlush, deliveryFinalize} {
		t.Run(phase, func(t *testing.T) {
			for _, ctx := range []context.Context{t.Context(), observed, unknown} {
				cause := &net.OpError{Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "wsasend", Err: windows.WSAECONNRESET}}
				record := &usageRecord{StatusCode: 200}
				setUsageError(record, ctx, wrapDownstreamError(cause, phase))
				if record.StatusCode != 499 || record.ErrorKind != ErrorKindClientCanceled {
					t.Fatal("RESET attribution depended on probe timing", record.StatusCode, record.ErrorKind)
				}
			}
		})
	}
	upstream := &net.OpError{Op: "read", Net: "tcp", Err: windows.WSAECONNRESET}
	record := &usageRecord{StatusCode: 200}
	setUsageError(record, t.Context(), upstream)
	if record.StatusCode != 502 || record.ErrorKind != ErrorKindUpstream {
		t.Fatal("an untagged upstream RESET became a downstream failure", record.StatusCode, record.ErrorKind)
	}
	for _, tc := range []struct {
		cause  error
		status int
		kind   string
	}{
		{errHTTPServerShutdown, 503, deliveryShutdown},
		{context.DeadlineExceeded, 504, "request_timeout"},
	} {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(tc.cause)
		record := &usageRecord{StatusCode: 200}
		setUsageError(record, ctx, wrapDownstreamError(windows.WSAECONNRESET, deliveryFlush))
		if record.StatusCode != tc.status || record.ErrorKind != tc.kind {
			t.Fatal("RESET replaced the established local cause", record.StatusCode, record.ErrorKind)
		}
	}
}
