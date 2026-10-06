package server

import (
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolUsageDetailsReachStatisticsAndKeepTailZeros(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "responses-api"))
	response, err := compiled.DecodeResponse(t.Context(), []byte(`{"status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":25,"text_tokens":80,"image_tokens":12,"audio_tokens":3},"output_tokens_details":{"reasoning_tokens":7,"text_tokens":30,"image_tokens":2,"audio_tokens":4}}}`), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	record := &usageRecord{}
	updateRecordProtocolUsage(record, response.Usage)
	for _, entry := range []struct {
		value *int
		want  int
	}{
		{record.Usage.TotalTokens, 150}, {record.UsageDetail.CachedInputTokens, 25},
		{record.UsageDetail.ReasoningTokens, 7}, {record.UsageDetail.TextInputTokens, 80},
		{record.UsageDetail.TextOutputTokens, 30}, {record.UsageDetail.ImageInputTokens, 12},
		{record.UsageDetail.ImageOutputTokens, 2}, {record.UsageDetail.AudioInputTokens, 3}, {record.UsageDetail.AudioOutputTokens, 4},
	} {
		if entry.value == nil || *entry.value != entry.want {
			t.Fatal("usage detail lost", record.UsageDetail)
		}
	}
	updateRecordProtocolUsage(record, &protocol.Usage{Details: map[string]protocol.Counter{"output.reasoning_tokens": {Count: 0, Origin: protocol.ObservedCount}}})
	if record.UsageDetail.ReasoningTokens == nil || *record.UsageDetail.ReasoningTokens != 0 || *record.UsageDetail.AudioOutputTokens != 4 || *record.Usage.TotalTokens != 150 {
		t.Fatal("tail cleared missing counters or lost explicit zero", record.UsageDetail)
	}
	updateRecordProtocolUsage(record, nil)
	if *record.Usage.TotalTokens != 150 {
		t.Fatal("absent usage cleared observed usage")
	}
}
