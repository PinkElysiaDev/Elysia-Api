package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatSingleChoiceExtensionKeepsNativeBoundary(t *testing.T) {
	source := shippedProjectionProtocol(t, Chat)
	// A real channel triggered a nil-map panic with native_finish_reason.
	// Use an unknown vendor key here to retain the extension boundary itself.
	wire := `{"id":"r","model":"m","created":1,"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop","vendor_finish":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	response, err := source.DecodeResponse(t.Context(), []byte(wire), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := source.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(encoded), `"vendor_finish":"stop"`) {
		t.Fatalf("native extension lost: %s %v", encoded, err)
	}
	target := shippedProjectionProtocol(t, Anthropic)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := conversion.Response(t.Context(), response, p.ConversionContext{}, nil)
	if err == nil {
		_, err = target.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
	}
	if err == nil {
		t.Fatal("unknown choice extension silently crossed protocol boundary")
	}
}
