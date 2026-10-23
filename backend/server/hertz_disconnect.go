package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/network/standard"
)

const (
	httpDisconnectInterval       = 100 * time.Millisecond
	standardUnlimitedIdleTimeout = -time.Nanosecond
)

type httpDisconnectWatch struct {
	raw        syscall.RawConn
	cancel     context.CancelCauseFunc
	isDisabled bool // protected by the monitor lock
}

// The monitor owns only connection observations and cancellation functions. It
// never reads HTTP data or retains a pooled Hertz RequestContext.
type httpDisconnectMonitor struct {
	mu          sync.Mutex
	watches     map[*httpDisconnectWatch]struct{}
	isRunning   bool
	isClosed    bool
	probeFailed bool
	wake        chan struct{}
	stop        chan struct{}
	workers     sync.WaitGroup
	probe       func(syscall.RawConn) (bool, error)
}

func newHTTPDisconnectMonitor(probe func(syscall.RawConn) (bool, error)) *httpDisconnectMonitor {
	return &httpDisconnectMonitor{
		watches: make(map[*httpDisconnectWatch]struct{}),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		probe:   probe,
	}
}

func (monitor *httpDisconnectMonitor) middleware() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		conn, ok := ctx.Value(httpSocketKey{}).(net.Conn)
		if !ok {
			// In-memory handler calls have no transport to observe.
			request.Next(ctx)
			return
		}
		ctx, finish := monitor.watch(ctx, conn)
		onHTTPComplete(request, finish)
		request.Next(ctx)
	}
}

func (monitor *httpDisconnectMonitor) watch(ctx context.Context, conn net.Conn) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	watch := &httpDisconnectWatch{cancel: cancel}
	var err error
	if socket, ok := conn.(syscall.Conn); ok {
		watch.raw, err = socket.SyscallConn()
	} else {
		err = fmt.Errorf("accepted connection %T does not expose a native socket", conn)
	}
	if err != nil {
		watch.isDisabled = true
	}
	monitor.mu.Lock()
	if monitor.isClosed {
		monitor.mu.Unlock()
		cancel(errHTTPServerShutdown)
		return ctx, func() {}
	}
	monitor.watches[watch] = struct{}{}
	if !monitor.isRunning {
		monitor.isRunning = true
		monitor.workers.Add(1)
		go monitor.run()
	}
	monitor.mu.Unlock()
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			cancel(&downstreamObservationError{cause: err})
		} else {
			monitor.reportProbeFailure(err)
		}
	}
	return ctx, func() {
		cancel(nil)
		monitor.mu.Lock()
		delete(monitor.watches, watch)
		isIdle := len(monitor.watches) == 0
		monitor.mu.Unlock()
		if isIdle {
			select {
			case monitor.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (monitor *httpDisconnectMonitor) run() {
	defer monitor.workers.Done()
	ticker := time.NewTicker(httpDisconnectInterval)
	defer ticker.Stop()
	for {
		monitor.mu.Lock()
		if monitor.isClosed || len(monitor.watches) == 0 {
			// Registration and idle exit share the lock: a new request either
			// belongs to this loop or starts its replacement.
			monitor.isRunning = false
			monitor.mu.Unlock()
			return
		}
		watches := make([]*httpDisconnectWatch, 0, len(monitor.watches))
		for watch := range monitor.watches {
			if !watch.isDisabled {
				watches = append(watches, watch)
			}
		}
		monitor.mu.Unlock()
		for _, watch := range watches {
			isDisconnected, err := monitor.probe(watch.raw)
			if !isDisconnected && err == nil {
				continue
			}
			monitor.mu.Lock()
			_, isActive := monitor.watches[watch]
			if isActive {
				watch.isDisabled = true
			}
			monitor.mu.Unlock()
			if !isActive {
				continue
			}
			if isDisconnected {
				cause := err
				if cause == nil {
					cause = errDownstreamDisconnected
				}
				watch.cancel(cause)
			} else {
				monitor.reportProbeFailure(err)
			}
		}
		select {
		case <-ticker.C:
		case <-monitor.wake:
		case <-monitor.stop:
		}
	}
}

func (monitor *httpDisconnectMonitor) reportProbeFailure(err error) {
	monitor.mu.Lock()
	shouldReport := !monitor.probeFailed
	monitor.probeFailed = true
	monitor.mu.Unlock()
	if shouldReport {
		hlog.SystemLogger().Warnf("Windows client disconnect observation is unavailable: %v; waiting requests may continue after the client disconnects", err)
	}
}

func (monitor *httpDisconnectMonitor) close() {
	monitor.mu.Lock()
	if !monitor.isClosed {
		monitor.isClosed = true
		close(monitor.stop)
	}
	cancels := make([]context.CancelCauseFunc, 0, len(monitor.watches))
	for watch := range monitor.watches {
		cancels = append(cancels, watch.cancel)
	}
	clear(monitor.watches)
	monitor.mu.Unlock()
	for _, cancel := range cancels {
		cancel(errHTTPServerShutdown)
	}
	monitor.workers.Wait()
}

type httpDisconnectTransport struct {
	network.Transporter
	monitor           *httpDisconnectMonitor
	mu                sync.Mutex
	isGracefulClosing bool
}

func (monitor *httpDisconnectMonitor) transport(options *hconfig.Options) network.Transporter {
	// Hertz applies this sentinel by the concrete transport's package name.
	// The wrapper must preserve standard's unlimited keep-alive behavior.
	if options.IdleTimeout == 0 {
		options.IdleTimeout = standardUnlimitedIdleTimeout
	}
	return &httpDisconnectTransport{Transporter: standard.NewTransporter(options), monitor: monitor}
}

func (transport *httpDisconnectTransport) ListenAndServe(onData network.OnData) error {
	defer func() {
		transport.mu.Lock()
		isGracefulClosing := transport.isGracefulClosing
		transport.mu.Unlock()
		if !isGracefulClosing && transport.monitor != nil {
			transport.monitor.close()
		}
	}()
	return transport.Transporter.ListenAndServe(func(ctx context.Context, conn interface{}) error {
		return onData(ctx, observeHTTPConnection(conn))
	})
}

func (transport *httpDisconnectTransport) Shutdown(ctx context.Context) error {
	transport.mu.Lock()
	transport.isGracefulClosing = true
	transport.mu.Unlock()
	// Closing the listener makes ListenAndServe return before active handlers
	// finish. Keep observing them until the graceful shutdown budget ends.
	if transport.monitor != nil {
		defer transport.monitor.close()
	}
	return transport.Transporter.Shutdown(ctx)
}

func (transport *httpDisconnectTransport) Close() error {
	// Hertz's standard Close closes its listener, not all accepted sockets.
	// Cancel handlers before delegating, including engines used without Server.
	if transport.monitor != nil {
		transport.monitor.close()
	}
	return transport.Transporter.Close()
}
