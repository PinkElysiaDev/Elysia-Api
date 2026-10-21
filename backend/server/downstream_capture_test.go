package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/network"
)

// usageBodyMaxBytes 是测试用的请求体捕获上限。生产路径的上限来自
// usageLogConfig().BodyMaxBytes，测试需要固定值来断言截断行为。
const usageBodyMaxBytes = 1 * 1024 * 1024

func newTestStreamCapture(limit int) (*downstreamCapture, network.ExtWriter, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	observeTestFlush(c)
	capture := installDownstreamCapture(c, &usageRecord{}, limit)
	return capture, startStream(c), rec
}

// 实际写出给客户端的内容必须完整（capture 不应吞字节）。
func TestDownstreamCaptureRecordsWrittenBytes(t *testing.T) {
	capture, writer, rec := newTestStreamCapture(usageBodyMaxBytes)
	if _, err := writer.Write([]byte(`{"hello":`)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if _, err := io.WriteString(writer, `"world"}`); err != nil {
		t.Fatalf("unexpected writestring error: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	body := capture.downstreamBody()
	if body.Content != `{"hello":"world"}` {
		t.Fatalf("captured content mismatch, got %q", body.Content)
	}
	if body.Truncated {
		t.Fatal("did not expect truncation for small body")
	}
	if rec.Body.String() != `{"hello":"world"}` {
		t.Fatalf("downstream client body mismatch, got %q", rec.Body.String())
	}
}

// 截断只影响捕获的副本，客户端仍收到完整字节。
func TestDownstreamCaptureTruncatesAtLimit(t *testing.T) {
	capture, writer, rec := newTestStreamCapture(usageBodyMaxBytes)
	big := strings.Repeat("a", usageBodyMaxBytes+512)
	if _, err := writer.Write([]byte(big)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	body := capture.downstreamBody()
	if !body.Truncated {
		t.Fatal("expected truncation flag for oversized body")
	}
	if len(body.Content) != usageBodyMaxBytes {
		t.Fatalf("expected captured content capped at %d, got %d", usageBodyMaxBytes, len(body.Content))
	}
	if rec.Body.Len() != len(big) {
		t.Fatalf("downstream client must receive full body, got %d want %d", rec.Body.Len(), len(big))
	}
}

func TestBufferedDownstreamCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	record := &usageRecord{}
	installDownstreamCapture(c, record, usageBodyMaxBytes)
	if _, err := c.Write([]byte("downstream-bytes")); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if got := record.downstream.downstreamBody().Content; got != "downstream-bytes" {
		t.Fatalf("captured content mismatch via record, got %q", got)
	}
}
