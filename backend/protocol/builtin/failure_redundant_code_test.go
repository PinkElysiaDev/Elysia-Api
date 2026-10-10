package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestRepeatedProviderErrorClassification(t *testing.T) {
	for _, source := range []string{Chat, Responses, Anthropic} {
		t.Run(source, func(t *testing.T) {
			from := shippedProjectionProtocol(t, source)
			for _, typ := range []string{"invalid_request_error", "rate_limit_error", "api_error"} {
				body := `{"error":{"type":"` + typ + `","message":"synthetic rejection","param":null,"code":"` + typ + `"}}`
				r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
					wire, err := shippedProjectionProtocol(t, target).EncodeResponse(t.Context(), r, p.EvaluationContext{})
					if err != nil || !strings.Contains(string(wire), "synthetic rejection") {
						t.Fatal(target, string(wire), err)
					}
				}
				native, err := from.EncodeResponse(t.Context(), r, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, native, body)
				frameBody := body
				if source != Chat {
					frameBody = `{"type":"error",` + strings.TrimPrefix(body, "{")
				}
				frame, err := from.DecodeFrame(t.Context(), testValue(t, frameBody), p.EvaluationContext{State: p.NewEvaluationState()})
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
					frames, err := shippedProjectionProtocol(t, target).EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, p.EvaluationContext{State: p.NewEvaluationState()})
					if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), "synthetic rejection") {
						t.Fatal(target, frames, err)
					}
				}
			}
		})
	}
}

func TestUnrecognizedProviderErrorDetailsRemainProtected(t *testing.T) {
	for _, extra := range []string{`"code":"vendor_detail"`, `"param":"tool_choice"`, `"vendor":null`, `"code":42`} {
		from, to := shippedProjectionProtocol(t, Chat), shippedProjectionProtocol(t, Anthropic)
		r, err := from.DecodeResponse(t.Context(), []byte(`{"error":{"type":"invalid_request_error","message":"rejected",`+extra+`}}`), p.EvaluationContext{})
		if err != nil {
			continue
		}
		if _, err := to.EncodeResponse(t.Context(), r, p.EvaluationContext{}); err == nil {
			t.Fatal("provider detail disappeared", extra)
		}
	}
}
