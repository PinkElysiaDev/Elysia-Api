package builtin

import (
	"bytes"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDeferredRequestOutputParity(t *testing.T) {
	const textBytes, imageBytes = 64 << 10, 1 << 20
	compiled := testCompiled(t, Anthropic)
	for _, sample := range []struct{ name, raw, payload string }{
		{"text", `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("A", textBytes) + `"}]}]}`, strings.Repeat("A", textBytes)},
		{"tool-image", `{"model":"m","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"lookup","input":{"n":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", imageBytes) + `"}}]}]}]}`, strings.Repeat("A", imageBytes)},
	} {
		t.Run(sample.name, func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(sample.raw), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			for _, hasExtension := range []bool{false, true} {
				candidate := request.Clone()
				if hasExtension {
					candidate.Content[0].Attributes = p.Object{"wire:claude": testValue(t, `{"vendor":9007199254740993}`)}
				}
				wire, err := compiled.EncodeRequest(t.Context(), candidate, p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				value, err := compiled.EncodeRequestValue(t.Context(), candidate, p.EvaluationContext{})
				if err != nil || !bytes.Equal(wire, value.Bytes()) {
					t.Fatal("public request encoders disagree", err)
				}
				if !bytes.Contains(wire, []byte(sample.payload)) || hasExtension != bytes.Contains(wire, []byte(`"vendor":9007199254740993`)) {
					t.Fatal("request encoding lost its large payload or message extension")
				}
				if sample.name == "tool-image" && !bytes.Contains(wire, []byte(`"n":9007199254740993`)) {
					t.Fatal("request encoding rounded the tool argument")
				}
				decoded, err := compiled.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
				if err != nil || len(decoded.Content) != len(candidate.Content) {
					t.Fatal("request output changed history shape", err)
				}
			}
		})
	}
}
