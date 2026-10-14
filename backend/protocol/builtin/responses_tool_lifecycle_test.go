package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesToolInputCannotCompleteMessageItem(t *testing.T) {
	for _, kind := range []string{"response.function_call_arguments.done", "response.custom_tool_call_input.done"} {
		t.Run(kind, func(t *testing.T) {
			compiled := shippedProjectionProtocol(t, Responses)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			for _, raw := range []string{
				`{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"object":"response","status":"in_progress","output":[]}}`,
				`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
			} {
				if _, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options); err != nil {
					t.Fatal(err)
				}
			}
			_, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"`+kind+`","output_index":0,"item_id":"msg","arguments":"{}","input":"text"}`), options)
			if err == nil || !strings.Contains(err.Error(), "/item_id") {
				t.Fatalf("invalid tool association accepted: %v", err)
			}
		})
	}
}
