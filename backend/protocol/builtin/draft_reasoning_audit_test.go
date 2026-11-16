package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestDraftSourceReasoningBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		codec, name, wire         string
		isInterleaved, isRejected bool
	}{
		{Anthropic, "enabled-with-unknowns", `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":4096,"future_thinking":true},"output_config":{"effort":"high","future_config":true},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "disabled-generation-effort", `{"model":"m","max_tokens":8192,"thinking":{"type":"disabled"},"output_config":{"effort":"high","future_config":true},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "adaptive-display", `{"model":"m","max_tokens":8192,"thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "between-tools", `{"model":"m","max_tokens":8192,"thinking":{"type":"between_tools"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "null-display", `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":1024,"display":null},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "empty-controls", `{"model":"m","max_tokens":8192,"thinking":{},"output_config":{},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "null-controls", `{"model":"m","max_tokens":8192,"thinking":null,"output_config":null,"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "unknown-only-output", `{"model":"m","max_tokens":8192,"output_config":{"future_config":true},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "effort-null", `{"model":"m","max_tokens":8192,"output_config":{"effort":null},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "empty-thinking-with-effort", `{"model":"m","max_tokens":8192,"thinking":{},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "null-thinking-with-effort", `{"model":"m","max_tokens":8192,"thinking":null,"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "empty-output-with-mode", `{"model":"m","max_tokens":8192,"thinking":{"type":"disabled"},"output_config":{},"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "null-output-with-mode", `{"model":"m","max_tokens":8192,"thinking":{"type":"disabled"},"output_config":null,"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "interleaved", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`, true, false},
		{Anthropic, "interleaved-without-tools", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`, true, false},
		{Anthropic, "ordinary-context-cannot-enable", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`, false, false},
		{Anthropic, "missing-mode", `{"model":"m","max_tokens":8192,"thinking":{"future":true},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "missing-budget", `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "null-budget", `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":null},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "small-budget", `{"model":"m","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":1023},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "adaptive-null-budget", `{"model":"m","max_tokens":8192,"thinking":{"type":"adaptive","budget_tokens":null},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "unknown-display", `{"model":"m","max_tokens":8192,"thinking":{"type":"adaptive","display":"future"},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "array-display", `{"model":"m","max_tokens":8192,"thinking":{"type":"adaptive","display":[]},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "effort-none", `{"model":"m","max_tokens":8192,"output_config":{"effort":"none"},"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Anthropic, "scalar-output-config", `{"model":"m","max_tokens":8192,"output_config":false,"messages":[{"role":"user","content":"hi"}]}`, false, true},
		{Gemini, "level-with-remainder", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"medium","includeThoughts":true,"future":{"n":9007199254740993}}}}`, false, false},
		{Gemini, "automatic-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":-1,"includeThoughts":false}}}`, false, false},
		{Gemini, "disabled-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`, false, false},
		{Gemini, "manual-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":4096}}}`, false, false},
		{Gemini, "include-only", `{"contents":[],"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`, false, false},
		{Gemini, "empty", `{"contents":[],"generationConfig":{"thinkingConfig":{}}}`, false, false},
		{Gemini, "null", `{"contents":[],"generationConfig":{"thinkingConfig":null}}`, false, false},
		{Gemini, "unknown-only", `{"contents":[],"generationConfig":{"thinkingConfig":{"future":null}}}`, false, false},
		{Gemini, "null-level", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":null}}}`, false, false},
		{Gemini, "null-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":null}}}`, false, false},
		{Gemini, "null-level-manual-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":null,"thinkingBudget":2048}}}`, false, false},
		{Gemini, "scalar", `{"contents":[],"generationConfig":{"thinkingConfig":false}}`, false, true},
		{Gemini, "level-and-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","thinkingBudget":1024}}}`, false, true},
		{Gemini, "null-include", `{"contents":[],"generationConfig":{"thinkingConfig":{"includeThoughts":null}}}`, false, true},
		{Gemini, "unsupported-level", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"xhigh"}}}`, false, true},
		{Gemini, "negative-budget", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingBudget":-2}}}`, false, true},
	} {
		t.Run(fixture.codec+"/"+fixture.name, func(t *testing.T) {
			compiled := testCompiled(t, fixture.codec)
			options := newDraftAuditOptions()
			options.InterleavedThinking = fixture.isInterleaved
			spoof, _ := p.EncodeValue(!fixture.isInterleaved)
			options.Values = p.Object{"interleavedThinking": spoof}
			request, err := compiled.DecodeRequest(t.Context(), []byte(fixture.wire), options)
			if fixture.isRejected {
				requireDraftRejection(t, err, "")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			wire, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			expected := fixture.wire
			if fixture.codec == Anthropic {
				expected = strings.Replace(expected, `"content":"hi"`, `"content":[{"type":"text","text":"hi"}]`, 1)
			}
			sameJSON(t, wire.Bytes(), expected)
		})
	}
}
func TestDraftGeminiReasoningEdits(t *testing.T) {
	const original = `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","future":{"n":9007199254740993}}}}`
	compiled := testCompiled(t, Gemini)
	for _, shouldRemove := range []bool{false, true} {
		t.Run(fmt.Sprint(shouldRemove), func(t *testing.T) {
			request, err := compiled.DecodeRequest(t.Context(), []byte(original), newDraftAuditOptions())
			if err != nil {
				t.Fatal(err)
			}
			request.Native = nil
			expected := `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"low","future":{"n":9007199254740993}}}}`
			if shouldRemove {
				request.Reasoning = &p.ReasoningIntent{}
				expected = `{"contents":[],"generationConfig":{"thinkingConfig":{"future":{"n":9007199254740993}}}}`
			} else {
				request.Reasoning.Effort = p.StringValue("low")
			}
			wire := encodeDraftRequest(t, compiled, request)
			sameJSON(t, wire.Bytes(), expected)
		})
	}
}

func TestDraftGeminiConflictingReasoning(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		reasoning  *p.ReasoningIntent
		parameters p.Object
	}{
		{"canonical-conflict", &p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue("high"), EffortScope: "reasoning"}, p.Object{"gemini_thinking": testValue(t, `{"thinkingLevel":"low"}`)}},
		{"canonical-level-and-budget", &p.ReasoningIntent{Mode: "enabled", Effort: p.StringValue("high"), EffortScope: "reasoning", Budget: testValue(t, `1024`)}, nil},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("m"), Content: []p.Node{}, Reasoning: fixture.reasoning, Parameters: fixture.parameters}
			before, _ := p.EncodeValue(request)
			_, err := testCompiled(t, Gemini).EncodeRequestValue(t.Context(), request, newDraftAuditOptions())
			requireDraftRejection(t, err, "")
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("conflicting reasoning modified the caller")
			}
		})
	}
}
