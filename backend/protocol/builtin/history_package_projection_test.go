package builtin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// The source remains the existing finite-value prototype. This fixture proves
// target policy and rendering, not migration of the full Anthropic decoder.
func TestPackageDraftHistoryTargets(t *testing.T) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	load := func(path string) *p.Compiled {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.CompilePackage(raw)
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		return compiled
	}
	source := load(filepath.Join("..", "testdata", "history", "anthropic.json"))
	options := p.EvaluationContext{Scope: p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}}
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`), options)
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.EncodeValue(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name, file string
		canDefault bool
	}{
		{"gemini", "google-generate-content.draft.json", true},
		{"chat", "openai-chat.draft.json", false},
		{"responses", "openai-responses.draft.json", false},
	} {
		target := load(filepath.Join("packages", fixture.file))
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				policy := p.DefaultConversionPolicy(source, target)
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				route := p.ConversionContext{Source: source.Identity(), Target: target.Identity(), Scope: options.Scope}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Request(t.Context(), request, route, sink)
				if !fixture.canDefault {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || len(failure.Issues) != 1 {
						t.Fatal("target history rejection missing", err)
					}
					issue := failure.Issues[0]
					if issue.Code != p.ConversionRejected || issue.Path != "/reasoning/historyRetention" || issue.RuleID != "request-projection" || issue.PolicyHash != conversion.Hash {
						t.Fatal("target history rejection lost policy identity", issue)
					}
				} else {
					if err != nil || projected.Reasoning != nil {
						t.Fatal("history-only default did not consume its intent", err, projected)
					}
					wire, err := target.EncodeRequest(t.Context(), projected, options)
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, wire, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64}}`)
					issues := sink.Issues()
					if len(issues) != 1 || issues[0].Code != p.ConversionNormalized || issues[0].Fidelity != "preserved" || issues[0].Path != "/reasoning/historyRetention" || issues[0].RuleID != "request-projection" || issues[0].PolicyHash != conversion.Hash {
						t.Fatal("default history intent was not a declared normalization", issues)
					}
					withGeneration := request.Clone()
					withGeneration.Reasoning.Mode = "enabled"
					withGeneration.Reasoning.Effort = p.StringValue("high")
					withGeneration.Reasoning.EffortScope = "reasoning"
					projected, err = conversion.Request(t.Context(), withGeneration, route, nil)
					if err != nil || projected.Reasoning == nil || !projected.Reasoning.HistoryRetention.IsZero() || projected.Reasoning.Mode != "enabled" || !projected.Reasoning.Effort.IsString("high") || projected.Reasoning.EffortScope != "reasoning" {
						t.Fatal("history disposition changed generation controls", err, projected)
					}
					wire, err = target.EncodeRequest(t.Context(), projected, options)
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, wire, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64,"thinkingConfig":{"thinkingLevel":"high"}}}`)
				}
				for index := range policy.Rules {
					if policy.Rules[index].ID == "request-projection" {
						policy.Rules[index].Enabled = false
					}
				}
				disabled, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				retained, err := disabled.Request(t.Context(), request, route, nil)
				if err != nil || retained.Reasoning == nil || !retained.Reasoning.HistoryRetention.IsString("all") {
					t.Fatal("disabled target disposition still ran", err, retained)
				}
				if _, err := target.EncodeRequest(t.Context(), retained, options); err == nil {
					t.Fatal("encoder silently repeated disabled history disposition")
				}
				after, err := p.EncodeValue(request)
				if err != nil || before != after {
					t.Fatal("projection mutated the source history", err)
				}
			})
		}
	}
}
