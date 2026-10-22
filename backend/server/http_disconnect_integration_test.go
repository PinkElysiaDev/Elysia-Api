//go:build windows

package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hserver "github.com/cloudwego/hertz/pkg/app/server"
	hconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/route"
)

const disconnectIntegrationTimeout = 3 * time.Second

func awaitDisconnectResult(t *testing.T, results <-chan error, stage string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(disconnectIntegrationTimeout):
		t.Fatalf("%s did not finish within %s", stage, disconnectIntegrationTimeout)
		return nil
	}
}

// Capture the real transporter during engine construction. No observer state
// is added to production request contexts for these assertions.
func newDisconnectTestEngine(t *testing.T, listener net.Listener, extra ...hconfig.Option) (*route.Engine, *httpDisconnectMonitor) {
	t.Helper()
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = listener.Close() })
	var monitor *httpDisconnectMonitor
	options := append([]hconfig.Option{hserver.WithListener(listener)}, extra...)
	options = append(options, hconfig.Option{F: func(options *hconfig.Options) {
		factory := options.TransporterNewer
		options.TransporterNewer = func(options *hconfig.Options) network.Transporter {
			transporter := factory(options)
			if monitored, ok := transporter.(*httpDisconnectTransport); ok {
				monitor = monitored.monitor
			}
			return transporter
		}
	}})
	engine := newHTTPEngine(nil, options...)
	if monitor == nil {
		t.Fatal("Windows transport has no disconnect monitor")
	}
	return engine, monitor
}

func awaitDisconnectState(t *testing.T, monitor *httpDisconnectMonitor, active int, closed bool) {
	t.Helper()
	deadline := time.Now().Add(disconnectIntegrationTimeout)
	for {
		state := monitor.snapshot()
		if state.ProbeFailed {
			t.Fatal("Windows TCP observation failed", state)
		}
		if state.Active == active && state.Closed == closed && (active > 0 || !state.Running) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitor did not settle: %+v; want active=%d closed=%t", state, active, closed)
		}
		time.Sleep(time.Millisecond)
	}
}

// Each batch keeps four real upstream requests waiting. Closing the clients must
// release both HTTP owners without completing or submitting another generation.
func TestWindowsDisconnectPropagatesToUpstream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			const concurrency, batches = 4, 5
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			started := make(chan error, concurrency)
			upstreamDone := make(chan error, concurrency)
			handlerDone := make(chan error, concurrency)

			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				started <- nil
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"text\":\"started\"}\n\n")
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
				upstreamDone <- r.Context().Err()
			}))
			defer upstream.Close()
			e, monitor := newDisconnectTestEngine(t, nil)
			e.GET("/forward", func(ctx context.Context, c *app.RequestContext) {

				defer func() { handlerDone <- ctx.Err() }()
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
				if err != nil {
					t.Error(err)
					return
				}
				response, err := upstream.Client().Do(request)
				if err != nil {
					return
				}
				defer response.Body.Close()
				reader := bufio.NewReader(response.Body)
				if streaming {
					writer := startStream(c)
					c.Header("Content-Type", "text/event-stream")
					for range 2 {
						line, readErr := reader.ReadString('\n')
						if readErr != nil {
							return
						}
						if _, err := io.WriteString(writer, line); err != nil {
							return
						}
					}
					if err := writer.Flush(); err != nil {
						return
					}
				}
				_, _ = io.Copy(io.Discard, reader)
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			for batch := range batches {
				clients := make([]net.Conn, 0, concurrency)
				for range concurrency {
					conn, err := net.DialTimeout("tcp", strings.TrimPrefix(web.URL, "http://"), disconnectIntegrationTimeout)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = conn.Close() })
					clients = append(clients, conn)
					_ = conn.SetDeadline(time.Now().Add(disconnectIntegrationTimeout))
					if _, err := fmt.Fprint(conn, "GET /forward HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
						t.Fatal(err)
					}
				}
				for _, conn := range clients {
					if err := awaitDisconnectResult(t, started, "upstream start"); err != nil {
						t.Fatal(err)
					}
					if streaming {
						response, err := http.ReadResponse(bufio.NewReader(conn), nil)
						if err != nil {
							t.Fatal(err)
						}
						frame := make([]byte, len("data: {\"text\":\"started\"}\n\n"))
						if _, err := io.ReadFull(response.Body, frame); err != nil || string(frame) != "data: {\"text\":\"started\"}\n\n" {
							t.Fatalf("first complete SSE frame: %q %v", frame, err)
						}
					}
				}
				awaitDisconnectState(t, monitor, concurrency, false)
				for _, conn := range clients {
					_ = conn.Close()
				}
				for range concurrency {
					if err := awaitDisconnectResult(t, upstreamDone, "upstream cancellation"); !errors.Is(err, context.Canceled) {
						t.Fatalf("upstream ended without cancellation: %v", err)
					}
					if err := awaitDisconnectResult(t, handlerDone, "handler cancellation"); !errors.Is(err, context.Canceled) {
						t.Fatalf("handler ended without cancellation: %v", err)
					}
				}
				awaitDisconnectState(t, monitor, 0, false)
				if calls.Load() != int64((batch+1)*concurrency) {
					t.Fatal("disconnect submitted duplicate upstream requests", calls.Load())
				}
			}
		})
	}
}

func TestWindowsDisconnectDuringUpload(t *testing.T) {
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })

	done := make(chan error, 1)
	e, monitor := newDisconnectTestEngine(t, nil)
	e.POST("/upload", func(ctx context.Context, c *app.RequestContext) {

		_, readErr := io.Copy(io.Discard, requestBody(c))
		if readErr == nil {
			t.Error("truncated upload unexpectedly completed")
		}
		select {
		case <-ctx.Done():
		case <-release:
		}
		done <- ctx.Err()
	})
	web := startTestServer(e)
	defer web.Close()
	defer unblock()
	conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1048576\r\n\r\nxx")

	awaitDisconnectState(t, monitor, 1, false)
	_ = conn.Close()
	if err := awaitDisconnectResult(t, done, "upload cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatal("upload context was not canceled", err)
	}
	awaitDisconnectState(t, monitor, 0, false)
}

func TestWindowsDisconnectNetworkVariants(t *testing.T) {
	certificate := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfig := certificate.TLS.Clone()
	certificate.Close()
	for _, variant := range []string{"fin", "rst", "tls-tcp-close", "ipv6"} {
		t.Run(variant, func(t *testing.T) {
			address := "127.0.0.1:0"
			if variant == "ipv6" {
				address = "[::1]:0"
			}
			listener, err := net.Listen("tcp", address)
			if err != nil {
				if variant == "ipv6" {
					t.Skipf("IPv6 loopback listener unavailable: %v", err)
				}
				t.Fatal(err)
			}
			defer listener.Close()
			var connectionTLS *tls.Config
			if variant == "tls-tcp-close" {
				connectionTLS = tlsConfig
			}
			e, monitor := newDisconnectTestEngine(t, listener, hserver.WithTLS(connectionTLS))
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })

			done := make(chan error, 1)
			e.GET("/wait", func(ctx context.Context, _ *app.RequestContext) {

				select {
				case <-ctx.Done():
				case <-release:
				}
				done <- ctx.Err()
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			var conn net.Conn
			if variant == "tls-tcp-close" {
				conn, err = tls.DialWithDialer(&net.Dialer{Timeout: disconnectIntegrationTimeout}, "tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true}) // Loopback certificate only.
			} else {
				conn, err = net.DialTimeout("tcp", listener.Addr().String(), disconnectIntegrationTimeout)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, _ = fmt.Fprint(conn, "GET /wait HTTP/1.1\r\nHost: localhost\r\n\r\n")

			awaitDisconnectState(t, monitor, 1, false)
			switch variant {
			case "fin":
				err = conn.(*net.TCPConn).CloseWrite()
			case "rst":
				if err = conn.(*net.TCPConn).SetLinger(0); err == nil {
					err = conn.Close()
				}
			case "tls-tcp-close":
				err = conn.(*tls.Conn).NetConn().Close()
			default:
				err = conn.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := awaitDisconnectResult(t, done, "network disconnect"); !errors.Is(err, context.Canceled) {
				t.Fatal("network disconnect did not cancel request", err)
			}
			awaitDisconnectState(t, monitor, 0, false)
		})
	}
}

func TestWindowsDisconnectKeepAliveRequestOwnership(t *testing.T) {
	for _, idleTimeout := range []time.Duration{0, time.Minute} {
		t.Run(fmt.Sprint(idleTimeout), func(t *testing.T) {
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })

			done := make(chan error, 1)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			e, monitor := newDisconnectTestEngine(t, listener, hserver.WithIdleTimeout(idleTimeout))
			e.GET("/:stage", func(ctx context.Context, c *app.RequestContext) {

				if c.Param("stage") == "first" {
					c.String(200, "complete")
					return
				}
				select {
				case <-ctx.Done():
				case <-release:
				}
				done <- ctx.Err()
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(disconnectIntegrationTimeout))
			_, _ = fmt.Fprint(conn, "GET /first HTTP/1.1\r\nHost: localhost\r\n\r\n")

			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "complete" {
				t.Fatalf("first response: %q %v", body, err)
			}
			awaitDisconnectState(t, monitor, 0, false)
			_, _ = fmt.Fprint(conn, "GET /second HTTP/1.1\r\nHost: localhost\r\n\r\n")
			awaitDisconnectState(t, monitor, 1, false)
			select {
			case err := <-done:
				t.Fatal("prior completion canceled the next request", err)
			case <-time.After(3 * httpDisconnectInterval):
			}
			_ = conn.Close()
			if err := awaitDisconnectResult(t, done, "second request cancellation"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			awaitDisconnectState(t, monitor, 0, false)
		})
	}
}

func TestWindowsDisconnectMonitorStopsWithTransport(t *testing.T) {
	for _, operation := range []string{"close", "shutdown", "shutdown-deadline", "listener"} {
		t.Run(operation, func(t *testing.T) {
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })

			done := make(chan error, 1)
			e, monitor := newDisconnectTestEngine(t, nil)
			e.GET("/wait", func(ctx context.Context, _ *app.RequestContext) {

				select {
				case <-ctx.Done():
				case <-release:
				}
				done <- ctx.Err()
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, _ = fmt.Fprint(conn, "GET /wait HTTP/1.1\r\nHost: localhost\r\n\r\n")

			awaitDisconnectState(t, monitor, 1, false)
			closed := make(chan error, 1)
			go func() {
				switch operation {
				case "close":
					closed <- e.Close()
				case "shutdown", "shutdown-deadline":
					grace := disconnectIntegrationTimeout
					if operation == "shutdown-deadline" {
						grace = 200 * time.Millisecond
					}
					ctx, cancel := context.WithTimeout(context.Background(), grace)
					defer cancel()
					closed <- e.Shutdown(ctx)
				default:
					closed <- e.GetOptions().Listener.Close()
				}
			}()
			if operation == "shutdown" {
				deadline := time.Now().Add(disconnectIntegrationTimeout)
				for e.IsRunning() && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if e.IsRunning() {
					t.Fatal("graceful shutdown did not start")
				}
				awaitDisconnectState(t, monitor, 1, false)
				_ = conn.Close()
			}
			if operation == "shutdown-deadline" {
				select {
				case err := <-done:
					t.Fatal("graceful shutdown canceled its handler before the grace period", err)
				case <-time.After(50 * time.Millisecond):
				}
			}
			if err := awaitDisconnectResult(t, done, "transport cancellation"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			// Immediate/expired close may report the standard transport deadline.
			// Other shutdown errors must not disappear behind the cleanup checks.
			err = awaitDisconnectResult(t, closed, "transport stop")
			if err != nil && (!(operation == "close" || operation == "shutdown-deadline") || !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatal("transport stop failed", err)
			}
			awaitDisconnectState(t, monitor, 0, true)
		})
	}
}

type disconnectResponseBody struct {
	ctx     context.Context
	started chan error
	release <-chan struct{}
	once    sync.Once
}

func (body *disconnectResponseBody) Read([]byte) (int, error) {
	body.once.Do(func() { body.started <- nil })
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.release:
		return 0, io.EOF
	}
}

func TestWindowsDisconnectSurvivesHandlerReturn(t *testing.T) {
	for _, kind := range []string{"body", "hijack"} {
		t.Run(kind, func(t *testing.T) {
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })

			started := make(chan error, 1)
			done := make(chan error, 1)
			e, monitor := newDisconnectTestEngine(t, nil)
			e.GET("/hold", func(ctx context.Context, c *app.RequestContext) {

				onHTTPComplete(c, func() { done <- ctx.Err() })
				if kind == "body" {
					c.SetBodyStream(&disconnectResponseBody{ctx: ctx, started: started, release: release}, -1)
					return
				}
				c.Hijack(func(network.Conn) {
					started <- nil
					select {
					case <-ctx.Done():
					case <-release:
					}
				})
			})
			web := startTestServer(e)
			defer web.Close()
			defer unblock()
			conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, _ = fmt.Fprint(conn, "GET /hold HTTP/1.1\r\nHost: localhost\r\n\r\n")

			awaitDisconnectState(t, monitor, 1, false)
			if err := awaitDisconnectResult(t, started, "deferred response start"); err != nil {
				t.Fatal(err)
			}
			awaitDisconnectState(t, monitor, 1, false)
			select {
			case err := <-done:
				t.Fatal("response completion ran before deferred body/hijack ended", err)
			default:
			}
			_ = conn.Close()
			if err := awaitDisconnectResult(t, done, "deferred response cancellation"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			awaitDisconnectState(t, monitor, 0, false)
		})
	}
}
