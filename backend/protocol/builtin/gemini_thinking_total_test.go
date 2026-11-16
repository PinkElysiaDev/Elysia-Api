package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiThinkingTotalInfersOnlyUnambiguousOutput(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	for _, sample := range []struct {
		usage string
		want  *p.Counter
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
		t.Run(sample.usage, func(t *testing.T) {
			body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":` + sample.usage + `}`
			response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			usage := response.Usage
			if usage == nil || (usage.Output == nil) != (sample.want == nil) || usage.Output != nil && *usage.Output != *sample.want {
				t.Fatal("missing output acquired an unproven count", usage, sample.want)
			}
			wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, wire, body)
		})
	}
}

func TestGeminiThinkingTotalLateSnapshotRemainsInferred(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	var usage *p.Usage
	consume := func(body string) {
		t.Helper()
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			usage = p.MergeUsage(usage, event.Usage)
		}
	}
	consume(`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]}}]}`)
	for _, raw := range []string{`{"thoughtsTokenCount":202}`, `{"promptTokenCount":17}`, `{"totalTokenCount":219}`, `{"totalTokenCount":219}`} {
		consume(`{"usageMetadata":` + raw + `}`)
	}
	if usage == nil || usage.Output == nil || usage.Output.Count != 202 || usage.Output.Origin != p.InferredCount {
		t.Fatal("late partial usage lost inferred output", usage)
	}
	consume(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"candidatesTokenCount":2,"totalTokenCount":221}}`)
	if usage.Output.Count != 204 || usage.Output.Origin != p.ObservedCount {
		t.Fatal("observed output did not replace inference", usage)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
}
