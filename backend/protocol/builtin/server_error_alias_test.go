package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestOpenAIServerErrorAliasConvertsWithoutMaskingFailure(t *testing.T) {
	for _, source := range []string{Chat, Responses} {
		from := shippedProjectionProtocol(t, source)
		const body = `{"error":{"type":"server_error","message":"upstream overloaded"}}`
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			r, err = c.Response(t.Context(), r, p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeResponse(t.Context(), r, p.EvaluationContext{})
			if err != nil || !strings.Contains(string(wire), "upstream overloaded") {
				t.Fatalf("%s -> %s: %s %v", source, target, wire, err)
			}
			if source == target && !strings.Contains(string(wire), `"type":"server_error"`) {
				t.Fatal("native error spelling changed", string(wire))
			}
			frameBody := body
			if source == Responses {
				frameBody = `{"type":"error","error":{"type":"server_error","message":"upstream overloaded"}}`
			}
			frame, err := from.DecodeFrame(t.Context(), testValue(t, frameBody), p.EvaluationContext{State: p.NewEvaluationState()})
			if err != nil {
				t.Fatal(err)
			}
			if len(frame.Events) != 1 || frame.Events[0].Type != p.OperationFailed {
				t.Fatal(frame)
			}
			event, err := c.Event(t.Context(), frame.Events[0], p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			frames, err := to.EncodeFrames(t.Context(), event, p.EvaluationContext{State: p.NewEvaluationState()})
			if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), "upstream overloaded") {
				t.Fatal(frames, err)
			}
		}
	}
}

func TestServerErrorAliasDoesNotConsumeUnknownErrorExtensions(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"error":{"type":"server_error","message":"failed","vendor":42}}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = to.EncodeResponse(t.Context(), r, p.EvaluationContext{}); err == nil {
		t.Fatal("unknown error extension silently consumed")
	}
}
