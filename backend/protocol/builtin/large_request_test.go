package builtin

import (
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/jsonutil"
	p "github.com/elysia-api/backend/protocol"
)

func TestNativeRequestCanShrinkToTargetLimit(t *testing.T) {
	const targetBytes, textBytes = 1024, 2048
	source := testCompiled(t, Chat)
	document := agentFixturePackage(t)
	limits := p.DefaultLimits()
	limits.BufferBytes = targetBytes
	document.Mapping.Limits = &limits
	target, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"`+strings.Repeat("a", textBytes)+`"}],"extra":9007199254740993}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Content[0].Children[0].Payload = p.StringValue("short")
	output, err := target.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > targetBytes || !strings.Contains(string(output), "short") || !strings.Contains(string(output), "9007199254740993") {
		t.Fatal("request shrinking lost native data")
	}
	request.Content[0].Children[0].Payload = p.StringValue(strings.Repeat("b", textBytes))
	_, err = target.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	var resource *p.ResourceLimitError
	if !errors.As(err, &resource) {
		t.Fatal("target output limit was not enforced", err)
	}
}

func TestGenericLargeRequestAndAfterBudget(t *testing.T) {
	const textBytes = 9 << 20
	compiled := testCompiled(t, Chat)
	text := strings.Repeat("a", textBytes)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","messages":[{"role":"user","content":"`+text+`"}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(output), text) {
		t.Fatal("declared request path lost large text", err)
	}
}

// Synthetic media and tool history exercise every native snapshot without
// storing real conversation data or allocating binary image fixtures.
func largeAnthropicRequest(imageBytes, textBlocks int) []byte {
	blocks := []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": strings.Repeat("A", imageBytes)}}}
	for range textBlocks {
		blocks = append(blocks, map[string]any{"type": "text", "text": "tool output"})
	}
	body, _ := sonic.ConfigStd.Marshal(map[string]any{"model": "m", "max_tokens": 64, "messages": []any{
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
			request, err := compiled.DecodeRequest(t.Context(), body, fixtureOptions(p.EvaluationContext{}))
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
			out, err := compiled.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal("encode:", err)
			}
			if !jsonutil.Valid(out) {
				t.Fatal("invalid output")
			}
			var before, after any
			_ = sonic.ConfigStd.Unmarshal(body, &before)
			_ = sonic.ConfigStd.Unmarshal(out, &after)
			b, _ := sonic.ConfigStd.Marshal(before)
			a, _ := sonic.ConfigStd.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("native image/tool history changed")
			}
		})
	}
}
