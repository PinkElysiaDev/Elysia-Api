package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elysia-api/backend/relay"
)

// 回归：流事件须保留「最后」N 条（终态事件在流尾部），且非法 JSON 不得
// 混入（会让整个事件数组的序列化永远失败）。物化推迟到 recordUsage 一次完成。
func TestStreamEventsKeepTailAndMaterialize(t *testing.T) {
	record := &usageRecord{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(buildSSEStream(120)))}
	observeUpstreamUsage(resp, record, relay.PlatformOpenAI)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(record.pendingStreamEvents) != StreamEventsCacheMax {
		t.Fatalf("events kept = %d, want cap %d", len(record.pendingStreamEvents), StreamEventsCacheMax)
	}
	wantTail := fmt.Sprintf(`{"i":%d}`, 119)
	if string(record.pendingStreamEvents[StreamEventsCacheMax-1]) != wantTail {
		t.Fatalf("last event = %s, want %s (tail retention)", record.pendingStreamEvents[StreamEventsCacheMax-1], wantTail)
	}
	record.materializeStreamEvents()
	if !strings.Contains(record.ProviderResponse.Content, wantTail) {
		t.Fatalf("materialized ProviderResponse must contain the terminal event: %s", record.ProviderResponse.Content)
	}
	if strings.Contains(record.ProviderResponse.Content, `{"i":0}`) {
		t.Fatal("head events beyond the cap must be evicted")
	}
}

func buildSSEStream(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "data: {\"i\":%d}\n\n", i)
	}
	b.WriteString("data: [DONE]\n")
	return b.String()
}

// 回归：data: 载荷跨多次 Write 到达时，观察者必须缓冲到行完整才处理。
// 旧行为按单次 Write 切行，半截 JSON 混进事件数组后序列化永久失败。
func TestDownstreamObserverBuffersSplitLines(t *testing.T) {
	record := &usageRecord{}
	writer := &observingStreamWriter{inner: nopStreamWriter{}, record: record}
	// 同一 JSON 事件拆成三次写出。
	chunk1 := "data: {\"choices\":[{\"delta\":{\"con"
	chunk2 := "tent\":\"hello"
	chunk3 := "\"}}]}\n\n"
	for _, chunk := range []string{chunk1, chunk2, chunk3} {
		if _, err := writer.WriteString(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := writer.responseText.String(); got != "hello" {
		t.Fatalf("split-line payload must be reassembled before parsing, got %q", got)
	}
}

// 回归：重试事件超限后保尾淘汰（最后的错误最接近根因），首条被挤出。
func TestAppendRetryEventCapsAtLimit(t *testing.T) {
	record := &usageRecord{}
	s := &Server{}
	for i := 0; i < RetryEventsCacheMax+10; i++ {
		s.appendRetryEvent(record, i+1, "model", fmt.Sprintf("err-%d", i))
	}
	if len(record.RetryEvents) != RetryEventsCacheMax {
		t.Fatalf("retry events = %d, want cap %d", len(record.RetryEvents), RetryEventsCacheMax)
	}
	last := record.RetryEvents[RetryEventsCacheMax-1]
	if last.Error != fmt.Sprintf("err-%d", RetryEventsCacheMax+9) {
		t.Fatalf("tail must be retained, last = %+v", last)
	}
}
