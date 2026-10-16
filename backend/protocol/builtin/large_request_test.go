package builtin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Synthetic media and tool history exercise every native snapshot without
// storing real conversation data or allocating binary image fixtures.
func largeAnthropicRequest(imageBytes, textBlocks int) []byte {
	blocks := []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": strings.Repeat("A", imageBytes)}}}
	for range textBlocks {
		blocks = append(blocks, map[string]any{"type": "text", "text": "tool output"})
	}
	body, _ := json.Marshal(map[string]any{"model": "m", "max_tokens": 64, "messages": []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "call_1", "name": "lookup", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": blocks}}},
	}})
	return body
}

func TestLargeRequestSnapshotBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bytes, blocks int
	}{{"bytes", 3 << 20, 0}, {"nodes", 4, 1800}} {
		t.Run(tc.name, func(t *testing.T) {
			compiled := shippedProjectionProtocol(t, Anthropic)
			body := largeAnthropicRequest(tc.bytes, tc.blocks)
			request, err := compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
			if err != nil {
				t.Fatal("decode:", err)
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			request, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
			if err != nil {
				t.Fatal("conversion:", err)
			}
			out, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
			if err != nil {
				t.Fatal("encode:", err)
			}
			if !json.Valid(out) {
				t.Fatal("invalid output")
			}
			var before, after any
			_ = json.Unmarshal(body, &before)
			_ = json.Unmarshal(out, &after)
			b, _ := json.Marshal(before)
			a, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("native image/tool history changed")
			}
		})
	}
}

func TestNativeRequestCanShrinkToTargetLimit(t *testing.T) {
	source := compileAgentDefinition(t, func(*p.Definition) {})
	target := compileAgentDefinition(t, func(definition *p.Definition) {
		limits := p.DefaultLimits()
		limits.BufferBytes = 1024
		definition.Limits = &limits
	})
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("a", 2048) + `"}],"extra":9007199254740993}`)
	request, err := source.DecodeRequest(t.Context(), body, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Content[0].Children[0].Payload = p.StringValue("short")
	output, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > 1024 || !strings.Contains(string(output), "short") || !strings.Contains(string(output), "9007199254740993") {
		t.Fatal("shrink lost native data")
	}
	request.Content[0].Children[0].Payload = p.StringValue(strings.Repeat("b", 2048))
	_, err = target.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	var resource *p.ResourceLimitError
	if !errors.As(err, &resource) {
		t.Fatalf("target cap not enforced: %v", err)
	}
}

func TestGenericLargeRequestAndAfterBudget(t *testing.T) {
	compiled := compileAgentDefinition(t, func(definition *p.Definition) {
		mapping := definition.Directions[p.DecodeRequest]
		mapping.After = &p.Expression{Op: "read", Path: ""}
		definition.Directions[p.DecodeRequest] = mapping
		mapping = definition.Directions[p.EncodeRequest]
		mapping.After = &p.Expression{Op: "read", Path: ""}
		definition.Directions[p.EncodeRequest] = mapping
	})
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("a", 9<<20) + `"}]}`)
	request, err := compiled.DecodeRequest(t.Context(), body, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), strings.Repeat("a", 9<<20)) {
		t.Fatal("text lost")
	}
}
