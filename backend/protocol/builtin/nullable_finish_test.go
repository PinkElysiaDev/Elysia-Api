package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiNullFinishReasonKeepsStreamOpen(t *testing.T) {
	compiled := testCompiled(t, Gemini)
	replay, err := p.NewEventReplay(p.Target{Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	for index, raw := range []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":null,"index":0,"safetyRatings":[]}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":0,"cachedContentTokenCount":0}}`,
		`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0,"safetyRatings":[]}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"cachedContentTokenCount":70}}`,
	} {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, err := replay.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
		if index == 0 && replay.Finish() == nil {
			t.Fatal("null finish reason terminated generation")
		}
		frames, err := compiled.EncodeFrame(t.Context(), frame, options)
		if err != nil || len(frames) != 1 {
			t.Fatal("native frame replay", err)
		}
		sameJSON(t, frames[0].Bytes(), raw)
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	if replay.Usage().Input.Count != 100 || replay.Usage().CacheRead.Count != 70 {
		t.Fatal("late usage lost")
	}
}
