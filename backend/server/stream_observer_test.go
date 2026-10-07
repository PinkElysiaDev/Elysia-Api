package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// 回归：流事件须保留「最后」N 条（终态事件在流尾部），且非法 JSON 不得
// 混入（会让整个事件数组的序列化永远失败）。物化推迟到 recordUsage 一次完成。
func TestStreamEventsKeepTailAndMaterialize(t *testing.T) {
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: usageBodyMaxBytes}}
	for index := 0; index < 120; index++ {
		record.appendStreamEvent(fmt.Sprintf(`{"i":%d}`, index))
	}
	record.appendStreamEvent("invalid JSON")
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

func TestStreamBodyCaptureDisabledStillCountsTokens(t *testing.T) {
	record := &usageRecord{bodyOpts: usageBodyOptions{maxBytes: 0}}
	compiled := compileFixtureDefinition(t, presetDefinition(t, "openai-chat-completions"))
	state := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	frame, err := compiled.DecodeFrame(t.Context(), mustProtocolValue(t, `{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`), state)
	if err != nil {
		t.Fatal(err)
	}
	record.appendStreamEvent(`{"private":"response"}`)
	for _, event := range frame.Events {
		updateRecordProtocolUsage(record, event.Usage)
	}
	if len(record.pendingStreamEvents) != 0 {
		t.Fatal("disabled body capture must not retain stream events")
	}
	record.materializeStreamEvents()
	if record.ProviderResponse.Content != "" || derefInt(record.Usage.TotalTokens) != 5 {
		t.Fatalf("body=%q, usage=%+v", record.ProviderResponse.Content, record.Usage)
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
