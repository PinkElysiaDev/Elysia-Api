package builtin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicThinkingDisplayUpdatesPreserved(t *testing.T) {
	// Claude Code 2.1.296 sends the official BetaThinkingConfig display enum.
	for _, thinking := range []struct{ mode, model string }{
		{`"type":"adaptive"`, "claude-opus-4-6"},
		{`"type":"enabled","budget_tokens":2048`, "claude-haiku-4-5"},
	} {
		for _, policyMode := range []string{"compatible", "strict"} {
			t.Run(thinking.model+"/"+policyMode, func(t *testing.T) {
				raw := fmt.Sprintf(`{"model":%q,"max_tokens":8192,"thinking":{%s,"display":"updates"},"messages":[{"role":"user","content":"hi"},{"role":"system","content":"be concise","output_config":{"effort":"high"}}]}`, thinking.model, thinking.mode)
				_, wire, issues, err := convertThinking(t, Anthropic, Anthropic, raw, thinking.model, policyMode, nil)
				if err != nil {
					t.Fatal(err)
				}
				sameJSON(t, wire, raw)
				if len(issues) != 0 {
					t.Fatal("native display changed or falsely degraded", issues)
				}
			})
		}
	}
}

func TestAnthropicThinkingDisplayUpdatesCrossProtocol(t *testing.T) {
	raw := `{"model":"alias","max_tokens":8192,"thinking":{"type":"adaptive","display":"updates"},"messages":[{"role":"user","content":"hi"}]}`
	for target, model := range map[string]string{Chat: "gpt-5.4", Responses: "gpt-5.4", Gemini: "gemini-3.5-flash-lite"} {
		t.Run(target, func(t *testing.T) {
			_, wire, issues, err := convertThinking(t, Anthropic, target, raw, model, "compatible", nil)
			if err != nil {
				t.Fatal(err)
			}
			hasDisplayLoss := false
			for _, issue := range issues {
				if issue.Path == "/reasoning/summary" && issue.RuleID == "reasoning-controls" && issue.Fidelity == "lossy_compatible" {
					hasDisplayLoss = true
				}
			}
			if !hasDisplayLoss || strings.Contains(string(wire), `"updates"`) {
				t.Fatalf("display projection must be explicit: %s %+v", wire, issues)
			}
			if _, _, _, err := convertThinking(t, Anthropic, target, raw, model, "strict", nil); err == nil {
				t.Fatal("strict conversion silently changed thinking controls")
			}
		})
	}
}

func TestAnthropicThinkingDisplayInvalidInput(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Anthropic)
	for _, display := range []string{`"future"`, `""`, `42`, `true`, `{}`, `[]`} {
		t.Run(display, func(t *testing.T) {
			_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":8192,"thinking":{"type":"adaptive","display":`+display+`},"messages":[{"role":"user","content":"hi"}]}`), p.EvaluationContext{})
			var failure *p.ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Path != "/thinking/display" || failure.Issues[0].Code != p.InvalidInput {
				t.Fatal("missing field-level display validation", err)
			}
			issue := failure.Issues[0]
			if issue.Direction != p.DecodeRequest || issue.Stage != "request.validation" || issue.Protocol != compiled.Identity() || strings.Contains(issue.Suggestion, "upstream stream") {
				t.Fatal("request error misclassified", issue)
			}
		})
	}
}
