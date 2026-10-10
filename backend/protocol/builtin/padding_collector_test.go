package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestResponsesPaddingDoesNotBecomeCollectedContent(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	collector, err := p.NewResponseCollector(p.Target{Protocol: from.Identity(), Direction: p.EncodeEvent, Capabilities: from.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
	for _, raw := range responsesPaddedTextFrames() {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 || len(r.Content[0].Children) != 1 || len(r.Content[0].Children[0].Metadata) > 1 {
		t.Fatal(r.Content)
	}
}
