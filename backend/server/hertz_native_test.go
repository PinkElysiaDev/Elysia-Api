package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/elysia-api/backend/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHertzRejectsUploadBeforeReadingBody(t *testing.T) {
	s := &Server{config: &config.Config{MaxBodyBytes: 32 << 20}}
	e := newTestEngine(t)
	e.POST("/upload", s.authMiddleware(), func(context.Context, *app.RequestContext) { t.Error("unauthorized upload reached handler") })
	web := startTestServer(e)
	defer web.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	// Only a two-byte prefix is provided; a framework-level full body read or drain
	// would wait for the remaining 32 MiB instead of rejecting the request.
	fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\n\r\nxx", 32<<20)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 401 || !response.Close || !strings.Contains(string(body), "API key") {
		t.Fatal(response.StatusCode, response.Close, string(body), err)
	}
}

func TestHertzDisconnectCancelsBeforeFirstResponse(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	e := newTestEngine(t)
	e.GET("/wait", func(ctx context.Context, c *app.RequestContext) { close(started); <-ctx.Done(); close(canceled) })
	web := startTestServer(e)
	defer web.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /wait HTTP/1.1\r\nHost: localhost\r\n\r\n")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	conn.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel a handler waiting for its first response")
	}
}

func TestHertzUncommittedStreamCanReturnHTTPError(t *testing.T) {
	e := newTestEngine(t)
	e.GET("/stream", func(ctx context.Context, c *app.RequestContext) {
		c.Header("Trailer", gatewayStreamErrorTrailer)
		startStream(c)
		discardUncommittedStream(c)
		c.JSON(502, map[string]string{"error": "before first frame"})
	})
	web := startTestServer(e)
	defer web.Close()
	response, err := web.Client().Get(web.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 502 || !strings.Contains(string(body), "before first frame") || response.Header.Get("Trailer") != "" {
		t.Fatal(response.StatusCode, string(body), err)
	}
}

// Recovery must preserve the on-wire commit boundary, not just the buffered
// Response.StatusCode. Exercise the real HTTP parser on both sides.
func TestHertzStreamPanicCommitBoundary(t *testing.T) {
	e := newTestEngine(t)
	s := &Server{}
	e.Use(s.requestLogging())
	e.GET("/:stage", func(ctx context.Context, c *app.RequestContext) {
		c.Header("Content-Type", "text/event-stream")
		w := startStream(c)
		switch c.Param("stage") {
		case "body":
			_, _ = w.Write([]byte("data: partial\n\n"))
			_ = w.Flush()
		case "headers":
			_ = w.Flush()
		}
		panic("test stream panic")
	})
	web := startTestServer(e)
	defer web.Close()
	for _, stage := range []string{"uncommitted", "headers", "body"} {
		t.Run(stage, func(t *testing.T) {
			response, err := web.Client().Get(web.URL + "/" + stage)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if stage == "uncommitted" {
				if response.StatusCode != 500 || err != nil || string(body) != "internal server error" {
					t.Fatal(response.StatusCode, string(body), err)
				}
				return
			}
			if response.StatusCode != 200 || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("committed panic looked successful", response.StatusCode, string(body), err)
			}
			if stage == "body" && string(body) != "data: partial\n\n" {
				t.Fatal(string(body))
			}
		})
	}
}

func TestHertzChunkedBodyKeepsNextRequestIntact(t *testing.T) {
	e := newTestEngine(t)
	e.POST("/body", func(ctx context.Context, c *app.RequestContext) {
		body, err := io.ReadAll(requestBody(c))
		if err != nil {
			c.String(400, "%s", err)
			return
		}
		c.Data(200, "text/plain", body)
	})
	e.GET("/next", func(ctx context.Context, c *app.RequestContext) { c.String(200, "next") })
	web := startTestServer(e)
	defer web.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(web.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(conn, "POST /body HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\nGET /next HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	reader := bufio.NewReader(conn)
	for _, want := range []string{"abc", "next"} {
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != want {
			t.Fatal(response.StatusCode, string(body), err)
		}
	}
}

func TestHertzEmbeddedUI(t *testing.T) {
	e := newTestEngine(t)
	files := fstest.MapFS{"index.html": {Data: []byte("console")}, "assets/movie.mp4": {Data: []byte("0123456789")}, "assets/empty": {}}
	handler := serveEmbeddedUI(files)
	e.GET("/ui/*filepath", handler)
	e.HEAD("/ui/*filepath", handler)
	web := startTestServer(e)
	defer web.Close()
	for _, tc := range []struct {
		method, path, rangeHeader string
		status                    int
		body                      string
	}{
		{"GET", "/ui/", "", 200, "console"}, {"GET", "/ui/assets/", "", 404, ""},
		{"GET", "/ui/assets/movie.mp4", "bytes=2-5", 206, "2345"}, {"HEAD", "/ui/assets/movie.mp4", "", 200, ""},
		{"GET", "/ui/assets/movie.mp4", "bytes=50-60", 416, ""},
		{"GET", "/ui/assets/movie.mp4", "bytes=-3", 206, "789"},
		{"GET", "/ui/assets/movie.mp4", "bytes=-0", 416, ""},
		{"GET", "/ui/assets/empty", "bytes=-3", 416, ""},
	} {
		t.Run(tc.method+tc.path+tc.rangeHeader, func(t *testing.T) {
			request, _ := http.NewRequest(tc.method, web.URL+tc.path, nil)
			request.Header.Set("Range", tc.rangeHeader)
			response, err := web.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != tc.status || string(body) != tc.body {
				t.Fatal(response.StatusCode, string(body), err)
			}
		})
	}
}

func TestMCPNativeMirrorHeaders(t *testing.T) {
	s := newAgentIntegrationServer(t)
	body := mcpRequest(1, "tools/call", map[string]any{"name": agentToolCLI, "arguments": map[string]any{"command": "elysia help"}})
	for _, headers := range []map[string]string{{"Mcp-Method": ""}, {"Mcp-Method": "tools/list"}, {"Mcp-Name": ""}, {"Mcp-Name": "other"}} {
		c, rec := remoteContext("POST", "/mcp", body, mcpHeaders(body, headers))
		s.handleMCP(contextForTest(c), c)
		flushTestResponse(c)
		var response struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 400 || response.Error.Code != mcp.CodeHeaderMismatch {
			t.Fatal(headers, rec.Code, rec.Body.String(), err)
		}
	}
}
