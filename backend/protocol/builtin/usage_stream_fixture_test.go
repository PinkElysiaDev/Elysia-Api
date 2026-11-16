package builtin

import (
	"fmt"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func decodeUsageSnapshots(t *testing.T, name string, updates []string) []*p.Usage {
	t.Helper()
	compiled := testCompiled(t, name)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	defer validation.Release()
	var usage *p.Usage
	consume := func(raw string) {
		t.Helper()
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(raw, err)
		}
		for _, event := range frame.Events {
			usage = p.MergeUsage(usage, event.Usage)
		}
	}
	starts := map[string]string{
		Chat:      `{"id":"r","model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		Responses: progressStart,
		Anthropic: `{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{}}}`,
		Gemini:    `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}]}`,
	}
	consume(starts[name])
	var snapshots []*p.Usage
	for index, update := range updates {
		var raw string
		switch name {
		case Chat:
			raw = `{"choices":[],"usage":` + update + `}`
		case Responses:
			raw = fmt.Sprintf(`{"type":"response.in_progress","sequence_number":%d,"response":{"id":"r","usage":%s}}`, index+1, update)
		case Anthropic:
			raw = `{"type":"message_delta","usage":` + update + `}`
		case Gemini:
			raw = `{"usageMetadata":` + update + `}`
		}
		consume(raw)
		snapshots = append(snapshots, usage)
	}
	ends := map[string][]string{
		Chat:      {`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
		Responses: {fmt.Sprintf(`{"type":"response.completed","sequence_number":%d,"response":{"id":"r","model":"m","status":"completed","output":[]}}`, len(updates)+1)},
		Anthropic: {`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`},
		Gemini:    {`{"candidates":[{"finishReason":"STOP"}]}`},
	}
	for _, raw := range ends[name] {
		consume(raw)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	return snapshots
}
