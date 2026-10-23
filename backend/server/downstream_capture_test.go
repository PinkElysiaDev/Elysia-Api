package server

import (
	"bytes"
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

func TestBufferedDownstreamCaptureBoundariesAndIndependentSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     int
		content   string
		expected  string
		truncated bool
	}{
		{"disabled", 0, "synthetic", "", false},
		{"exact", 4, "abcd", "abcd", false},
		{"over", 4, "abcde", "abcd", true},
		{"empty", 4, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newTestContext(httptest.NewRecorder())
			request.Response.SetBodyString(tc.content)
			capture := installDownstreamCapture(request, &usageRecord{}, tc.limit)
			original := bytes.Clone(request.Response.Body())
			first := capture.downstreamBody()
			if first != (usageBody{Content: tc.expected, Truncated: tc.truncated}) || !bytes.Equal(original, request.Response.Body()) {
				t.Fatal("buffered capture changed its boundary or the real response", first)
			}
			if second := capture.downstreamBody(); second != first {
				t.Fatal("repeated buffered capture changed the snapshot", second, first)
			}
			if len(capture.buf) != 0 {
				t.Fatal("buffered capture retained an unnecessary second byte buffer")
			}
			for index := range request.Response.Body() {
				request.Response.Body()[index] = 'z'
			}
			if first.Content != tc.expected {
				t.Fatal("response buffer mutation changed an earlier capture")
			}
			request.Response.SetBodyString("next")
			next := capture.downstreamBody()
			wantNext := "next"
			if tc.limit == 0 {
				wantNext = ""
			}
			if next.Content != wantNext || next.Truncated {
				t.Fatal("a new response inherited capture bytes or truncation", next)
			}
		})
	}
}

func TestStreamDownstreamCaptureKeepsTeeAndSnapshotBoundaries(t *testing.T) {
	for _, limit := range []int{0, 4} {
		capture, writer, response := newTestStreamCapture(limit)
		for _, chunk := range []string{"ab", "cd"} {
			if _, err := io.WriteString(writer, chunk); err != nil {
				t.Fatal(err)
			}
		}
		exact := capture.downstreamBody()
		expected := "abcd"
		if limit == 0 {
			expected = ""
		}
		if exact.Content != expected || exact.Truncated {
			t.Fatal("exact stream boundary was truncated or changed", exact)
		}
		if _, err := io.WriteString(writer, "ef"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		final := capture.downstreamBody()
		if final.Content != expected || final.Truncated != (limit > 0) || response.Body.String() != "abcdef" {
			t.Fatal("capture changed tee delivery or its bounded copy", final, response.Body.String())
		}
		if exact.Content != expected || exact.Truncated {
			t.Fatal("later writes changed an earlier capture snapshot")
		}
		if repeated := capture.downstreamBody(); repeated != final {
			t.Fatal("repeated stream capture changed the recorded tail")
		}
	}
}
