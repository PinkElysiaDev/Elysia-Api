package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDeferredRequestOutputParity(t *testing.T) {
	adapter := module{name: Anthropic, family: "claude"}
	for _, raw := range []string{
		`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("A", 64<<10) + `"}]}]}`,
		`{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", 1<<20) + `"}}]}]}]}`,
	} {
		input := testValue(t, raw)
		request, err := adapter.decodeRequest(input, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if !adapter.canDeferRequest(request) {
			t.Fatal("large ordinary history did not select deferred construction")
		}
		before, beforeErr := adapter.encodeRequestOutput(t.Context(), request, p.EvaluationContext{}, false)
		after, afterErr := adapter.encodeRequest(t.Context(), request, p.EvaluationContext{})
		if fmt.Sprint(beforeErr) != fmt.Sprint(afterErr) || before != after {
			t.Fatalf("request output differs: %v / %v", beforeErr, afterErr)
		}
		// Message-level extensions must select the established renderer before
		// execution, never fail into a second encoding or silently lose fields.
		request.Content[0].Attributes = p.Object{"wire:claude": testValue(t, `{"vendor":9007199254740993}`)}
		if adapter.canDeferRequest(request) {
			t.Fatal("message extension bypassed the safe fallback")
		}
	}
}
