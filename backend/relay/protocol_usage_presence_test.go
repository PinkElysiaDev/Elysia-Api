package relay

import (
	"github.com/elysia-api/backend/protocol"
	"testing"
)

func TestCompiledWireAdaptersNeverInventMissingUsage(t *testing.T) {
	fixtures := map[string]string{
		"openai-chat": `{"id":"r","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`,
		"responses":   `{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`,
		"anthropic":   `{"id":"r","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`,
		"gemini":      `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`,
	}
	for source, body := range fixtures {
		for target := range fixtures {
			t.Run(source+"/"+target, func(t *testing.T) {
				response, err := compileBuiltinModule(t, source).DecodeResponse(t.Context(), []byte(body), protocol.EvaluationContext{})
				if err != nil || response.Usage != nil {
					t.Fatalf("decode fabricated usage: %v %+v", err, response)
				}
				encoder := compileBuiltinModule(t, target)
				wire, err := encoder.EncodeResponse(t.Context(), response, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				value, _ := protocol.ParseValue(wire)
				fields, _ := value.ReadObject()
				if !fields["usage"].IsZero() || !fields["usageMetadata"].IsZero() {
					t.Fatalf("encode fabricated usage: %s", wire)
				}
			})
		}
	}
}
