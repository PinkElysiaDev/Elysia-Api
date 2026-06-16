package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newTestGinWriter 借助 gin.CreateTestContext 拿到一个真实的 gin.ResponseWriter
// （底层是 httptest recorder），避免手写实现整个接口。
func newTestGinWriter() (gin.ResponseWriter, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return c.Writer, rec
}

func TestDownstreamCaptureRecordsWrittenBytes(t *testing.T) {
	inner, rec := newTestGinWriter()
	capture := newDownstreamCaptureWriter(inner)

	if _, err := capture.Write([]byte(`{"hello":`)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if _, err := capture.WriteString(`"world"}`); err != nil {
		t.Fatalf("unexpected writestring error: %v", err)
	}

	body := capture.downstreamBody()
	if body.Content != `{"hello":"world"}` {
		t.Fatalf("captured content mismatch, got %q", body.Content)
	}
	if body.Truncated {
		t.Fatal("did not expect truncation for small body")
	}
	// 实际写出给客户端的内容必须完整（capture 不应吞字节）。
	if rec.Body.String() != `{"hello":"world"}` {
		t.Fatalf("downstream client body mismatch, got %q", rec.Body.String())
	}
}

func TestDownstreamCaptureTruncatesAtLimit(t *testing.T) {
	inner, rec := newTestGinWriter()
	capture := newDownstreamCaptureWriter(inner)

	big := strings.Repeat("a", UsageBodyMaxBytes+512)
	if _, err := capture.Write([]byte(big)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	body := capture.downstreamBody()
	if !body.Truncated {
		t.Fatal("expected truncation flag for oversized body")
	}
	if len(body.Content) != UsageBodyMaxBytes {
		t.Fatalf("expected captured content capped at %d, got %d", UsageBodyMaxBytes, len(body.Content))
	}
	// 截断只影响捕获的副本，客户端仍收到完整字节。
	if rec.Body.Len() != len(big) {
		t.Fatalf("downstream client must receive full body, got %d want %d", rec.Body.Len(), len(big))
	}
}

func TestInstallDownstreamCaptureWiresRecord(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	record := &usageRecord{}

	capture := installDownstreamCapture(c, record)
	if capture == nil {
		t.Fatal("expected capture writer")
	}
	if record.downstream != capture {
		t.Fatal("record.downstream not wired to capture writer")
	}
	// 幂等：重复安装应复用已有 capture，不再二次包裹。
	again := installDownstreamCapture(c, record)
	if again != capture {
		t.Fatal("expected installDownstreamCapture to be idempotent")
	}

	if _, err := c.Writer.Write([]byte("downstream-bytes")); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if got := record.downstream.downstreamBody().Content; got != "downstream-bytes" {
		t.Fatalf("captured content mismatch via record, got %q", got)
	}
}
