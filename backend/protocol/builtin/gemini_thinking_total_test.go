package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiThinkingTotalInfersOnlyUnambiguousOutput(t *testing.T) {
	adapter := module{name: Gemini}
	for _, tc := range []struct {
		raw  string
		want *p.Counter
	}{
		{`{"promptTokenCount":17,"thoughtsTokenCount":202,"totalTokenCount":219}`, &p.Counter{Count: 202, Origin: p.InferredCount}},
		{`{"promptTokenCount":17,"thoughtsTokenCount":0,"totalTokenCount":17}`, &p.Counter{Count: 0, Origin: p.InferredCount}},
		{`{"promptTokenCount":17,"thoughtsTokenCount":202,"totalTokenCount":220}`, nil},
		{`{"promptTokenCount":17,"thoughtsTokenCount":202}`, nil},
		{`{"thoughtsTokenCount":202,"totalTokenCount":219}`, nil},
		{`{"promptTokenCount":17,"totalTokenCount":219}`, nil},
		{`{"promptTokenCount":17,"thoughtsTokenCount":202,"toolUsePromptTokenCount":1,"totalTokenCount":220}`, nil},
		{`{"promptTokenCount":17,"candidatesTokenCount":0,"thoughtsTokenCount":202,"totalTokenCount":219}`, &p.Counter{Count: 202, Origin: p.ObservedCount}},
	} {
		u, err := adapter.decodeUsage(testValue(t, tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		if (u.Output == nil) != (tc.want == nil) || (u.Output != nil && *u.Output != *tc.want) {
			t.Fatalf("%s: got %+v want %+v", tc.raw, u.Output, tc.want)
		}
	}
	from := shippedProjectionProtocol(t, Gemini)
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"thoughtsTokenCount":202,"totalTokenCount":219}}`
	r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, body)
}

func TestGeminiThinkingTotalLateSnapshotRemainsInferred(t *testing.T) {
	stream := &streamModule{module: module{name: Gemini}, limits: p.DefaultLimits()}
	var usage *p.Usage
	for _, raw := range []string{`{"thoughtsTokenCount":202}`, `{"promptTokenCount":17}`, `{"totalTokenCount":219}`, `{"totalTokenCount":219}`} {
		update, err := stream.decodeUsageUpdate(testValue(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		usage = p.MergeUsage(usage, update)
	}
	if usage.Output == nil || usage.Output.Count != 202 || usage.Output.Origin != p.InferredCount {
		t.Fatal(usage)
	}
	update, err := stream.decodeUsageUpdate(testValue(t, `{"candidatesTokenCount":2,"totalTokenCount":221}`))
	if err != nil {
		t.Fatal(err)
	}
	usage = p.MergeUsage(usage, update)
	if usage.Output.Count != 204 || usage.Output.Origin != p.ObservedCount {
		t.Fatal(usage)
	}
}
