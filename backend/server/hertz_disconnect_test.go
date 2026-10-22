package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/network"
)

type httpDisconnectSnapshot struct {
	Active      int
	Running     bool
	Closed      bool
	ProbeFailed bool
}

func (monitor *httpDisconnectMonitor) snapshot() httpDisconnectSnapshot {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return httpDisconnectSnapshot{len(monitor.watches), monitor.isRunning, monitor.isClosed, monitor.probeFailed}
}

type disconnectTestRawConn struct{}

func (disconnectTestRawConn) Control(callback func(uintptr)) error { callback(1); return nil }
func (disconnectTestRawConn) Read(callback func(uintptr) bool) error {
	callback(1)
	return nil
}
func (disconnectTestRawConn) Write(callback func(uintptr) bool) error {
	callback(1)
	return nil
}

type disconnectTestConn struct{ net.Conn }

func (disconnectTestConn) SyscallConn() (syscall.RawConn, error) {
	return disconnectTestRawConn{}, nil
}

func awaitDisconnectSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect observation did not complete")
	}
}

func TestHTTPDisconnectMonitorCompletedRequestIsolation(t *testing.T) {
	started, release, nextProbe := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var probes atomic.Int32
	monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) {
		switch probes.Add(1) {
		case 1:
			close(started)
			<-release
			return true, nil // the old request completed while this probe ran
		case 2:
			close(nextProbe)
		}
		return false, nil
	})
	t.Cleanup(monitor.close)
	previous, finishPrevious := monitor.watch(t.Context(), disconnectTestConn{})
	awaitDisconnectSignal(t, started)
	finishPrevious()
	next, finishNext := monitor.watch(t.Context(), disconnectTestConn{})
	close(release)
	awaitDisconnectSignal(t, nextProbe)
	if previous.Err() == nil || next.Err() != nil || t.Context().Err() != nil {
		t.Fatal("a late observation escaped its request lifetime")
	}
	finishNext()
	monitor.workers.Wait()
	if snapshot := monitor.snapshot(); snapshot.Active != 0 || snapshot.Running || snapshot.ProbeFailed {
		t.Fatalf("completed requests retained observation state: %+v", snapshot)
	}
	// The same engine may serve another keep-alive request after an idle exit.
	current, finishCurrent := monitor.watch(t.Context(), disconnectTestConn{})
	if current.Err() != nil || monitor.snapshot().Active != 1 {
		t.Fatal("idle monitor did not accept a new request")
	}
	monitor.close()
	awaitDisconnectSignal(t, current.Done())
	finishCurrent()
	late, finishLate := monitor.watch(t.Context(), disconnectTestConn{})
	finishLate()
	if late.Err() == nil || !monitor.snapshot().Closed || monitor.snapshot().Running {
		t.Fatal("closed monitor admitted work")
	}
}

func TestHTTPDisconnectMonitorProbeErrorPreservesRequest(t *testing.T) {
	queried := make(chan struct{}, 2)
	monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) {
		queried <- struct{}{}
		return false, errors.New("synthetic unsupported observation")
	})
	t.Cleanup(monitor.close)
	ctx, finish := monitor.watch(t.Context(), disconnectTestConn{})
	defer finish()
	awaitDisconnectSignal(t, queried)
	deadline := time.Now().Add(time.Second)
	for !monitor.snapshot().ProbeFailed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil || !monitor.snapshot().ProbeFailed {
		t.Fatal("query failure was reported as a client disconnect or was hidden")
	}
	select {
	case <-queried:
		t.Fatal("unavailable observation retried the same socket")
	case <-time.After(2 * httpDisconnectInterval):
	}
	monitor.close()
	awaitDisconnectSignal(t, ctx.Done())
}

type disconnectTestTransport struct {
	listenDone      chan struct{}
	shutdownStarted chan struct{}
	finishShutdown  chan struct{}
	close           func() error
}

func (transport disconnectTestTransport) ListenAndServe(network.OnData) error {
	<-transport.listenDone
	return nil
}
func (transport disconnectTestTransport) Shutdown(context.Context) error {
	close(transport.listenDone)
	close(transport.shutdownStarted)
	<-transport.finishShutdown
	return nil
}
func (transport disconnectTestTransport) Close() error { return transport.close() }

func TestHTTPDisconnectTransportGracefulShutdown(t *testing.T) {
	monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) { return false, nil })
	t.Cleanup(monitor.close)
	ctx, finish := monitor.watch(t.Context(), disconnectTestConn{})
	defer finish()
	inner := disconnectTestTransport{listenDone: make(chan struct{}), shutdownStarted: make(chan struct{}), finishShutdown: make(chan struct{})}
	transport := &httpDisconnectTransport{Transporter: inner, monitor: monitor}
	listenReturned, shutdownReturned := make(chan struct{}), make(chan struct{})
	go func() { _ = transport.ListenAndServe(nil); close(listenReturned) }()
	go func() { _ = transport.Shutdown(t.Context()); close(shutdownReturned) }()
	awaitDisconnectSignal(t, inner.shutdownStarted)
	awaitDisconnectSignal(t, listenReturned)
	if ctx.Err() != nil || monitor.snapshot().Closed {
		t.Fatal("listener close prematurely canceled graceful handlers")
	}
	close(inner.finishShutdown)
	awaitDisconnectSignal(t, shutdownReturned)
	awaitDisconnectSignal(t, ctx.Done())
	if snapshot := monitor.snapshot(); !snapshot.Closed || snapshot.Active != 0 || snapshot.Running {
		t.Fatalf("shutdown retained monitor: %+v", snapshot)
	}
}

func TestHTTPDisconnectTransportForcedAndExternalClose(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "forced", true: "external-listener"}[external], func(t *testing.T) {
			monitor := newHTTPDisconnectMonitor(func(syscall.RawConn) (bool, error) { return false, nil })
			t.Cleanup(monitor.close)
			ctx, finish := monitor.watch(t.Context(), disconnectTestConn{})
			defer finish()
			inner := disconnectTestTransport{listenDone: make(chan struct{}), close: func() error {
				if ctx.Err() == nil {
					t.Error("forced close reached the transporter before handler cancellation")
				}
				return nil
			}}
			transport := &httpDisconnectTransport{Transporter: inner, monitor: monitor}
			if external {
				close(inner.listenDone)
				if err := transport.ListenAndServe(nil); err != nil {
					t.Fatal(err)
				}
			} else if err := transport.Close(); err != nil {
				t.Fatal(err)
			}
			awaitDisconnectSignal(t, ctx.Done())
			if !monitor.snapshot().Closed || monitor.snapshot().Running {
				t.Fatal("transport return retained the monitor")
			}
		})
	}
}
