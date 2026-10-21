package server

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hserver "github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/network"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/cloudwego/hertz/pkg/route/param"
)

// Fixtures exercise native Hertz contexts and real Hertz listeners. net/http
// requests/recorders are only test data and client-side assertion utilities.
func newTestEngine(t testing.TB) *route.Engine {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	e := newHTTPEngine(nil, hserver.WithListener(listener))
	return e
}

func addTestParam(c *app.RequestContext, key, value string) {
	c.Params = append(c.Params, param.Param{Key: key, Value: value})
}

type hertzTestServer struct {
	URL    string
	engine *route.Engine
	client *http.Client
	done   chan error
	once   sync.Once
}

func startTestServer(engine *route.Engine) *hertzTestServer {
	s := &hertzTestServer{URL: "http://" + engine.GetOptions().Listener.Addr().String(), engine: engine, client: &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}, done: make(chan error, 1)}
	go func() { s.done <- engine.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for !engine.IsRunning() {
		select {
		case err := <-s.done:
			panic(err)
		default:
		}
		if time.Now().After(deadline) {
			panic("Hertz test listener did not start")
		}
		time.Sleep(time.Millisecond)
	}
	return s
}
func (s *hertzTestServer) Client() *http.Client { return s.client }
func (s *hertzTestServer) Close() {
	s.once.Do(func() { s.client.CloseIdleConnections(); _ = s.engine.Close(); <-s.done })
}

type testConnection struct {
	network.Conn
	writer network.Writer
	wire   bytes.Buffer
	remote net.Addr
	flush  func()
}

func (c *testConnection) Malloc(n int) ([]byte, error)      { return c.writer.Malloc(n) }
func (c *testConnection) WriteBinary(b []byte) (int, error) { return c.writer.WriteBinary(b) }
func (c *testConnection) Flush() error {
	err := c.writer.Flush()
	if c.flush != nil {
		c.flush()
	}
	return err
}
func (c *testConnection) RemoteAddr() net.Addr            { return c.remote }
func (c *testConnection) LocalAddr() net.Addr             { return nil }
func (c *testConnection) SetReadDeadline(time.Time) error { return nil }

func newTestContext(rec *httptest.ResponseRecorder) *app.RequestContext {
	c := app.NewContext(0)
	conn := &testConnection{remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234}}
	conn.writer = network.NewWriter(&conn.wire)
	c.SetConn(conn)
	c.Set("testRecorder", rec)
	c.Set("testContext", context.Background())
	conn.flush = func() { rec.Flushed = true }
	return c
}
func contextForTest(c *app.RequestContext) context.Context {
	if ctx, ok := c.Get("testContext"); ok {
		return ctx.(context.Context)
	}
	return context.Background()
}
func setTestRequest(c *app.RequestContext, r *http.Request) {
	c.Request.Reset()
	c.Request.SetMethod(r.Method)
	c.Request.SetRequestURI(r.URL.String())
	c.Request.SetHost(r.Host)
	for k, values := range r.Header {
		for _, v := range values {
			c.Request.Header.Add(k, v)
		}
	}
	if r.Body != nil {
		c.Request.SetBodyStream(r.Body, int(r.ContentLength))
	}
	c.Set("testContext", r.Context())
	setTestRemoteAddr(c, r.RemoteAddr)
}
func setTestRemoteAddr(c *app.RequestContext, address string) {
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		addr = &net.TCPAddr{IP: net.ParseIP(address)}
	}
	c.GetConn().(*testConnection).remote = addr
}

// Only cancellation-at-flush tests need a snapshot before handler completion.
// Ordinary fixtures decode once, avoiding quadratic copying for long streams.
func observeTestFlush(c *app.RequestContext) {
	conn := c.GetConn().(*testConnection)
	value, _ := c.Get("testRecorder")
	conn.flush = func() { copyTestStream(conn, value.(*httptest.ResponseRecorder)) }
}

func copyTestStream(conn *testConnection, rec *httptest.ResponseRecorder) {
	r, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(conn.wire.Bytes())), nil)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(r.Body)
	rec.Code = r.StatusCode
	rec.HeaderMap = r.Header.Clone()
	rec.Body.Reset()
	rec.Body.Write(body)
	rec.Flushed = true
	for k, values := range r.Trailer {
		rec.Header().Add("Trailer", k)
		for _, v := range values {
			rec.Header().Set(k, v)
		}
	}
}
func flushTestResponse(c *app.RequestContext) {
	value, ok := c.Get("testRecorder")
	if !ok {
		return
	}
	rec := value.(*httptest.ResponseRecorder)
	if w := c.Response.GetHijackWriter(); w != nil {
		_ = w.Finalize()
		copyTestStream(c.GetConn().(*testConnection), rec)
		return
	}
	rec.Code = c.Response.StatusCode()
	rec.HeaderMap = make(http.Header)
	c.Response.Header.VisitAll(func(k, v []byte) { rec.Header().Add(string(k), string(v)) })
	rec.Body.Reset()
	rec.Body.Write(c.Response.Body())
}
func serveTestHTTP(engine *route.Engine, rec *httptest.ResponseRecorder, r *http.Request) {
	c := newTestContext(rec)
	defer finishHTTPRequest(c)
	setTestRequest(c, r)
	engine.ServeHTTP(r.Context(), c)
	flushTestResponse(c)
}
