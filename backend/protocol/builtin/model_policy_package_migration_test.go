package builtin

import (
	"errors"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPackageModelCapabilitiesMatchExistingResolver(t *testing.T) {
	models := []string{
		"alias", "MODELS/GEMINI-2.5-FLASH", "models/GPT-6.1-SOL", "CLAUDE-SONNET-4-6",
		"gemini-2.5-pro", "gemini-2.5-flash-lite-preview", "gemini-2.5-flash", "gemini-2.5-flash-image",
		"gemini-3-pro", "gemini-3.1-pro-preview", "gemini-3.7-flash", "gemini-3.8-flash", "gemini-3.2-flash-lite",
		"gemini-3.5-flash", "gemini-3.6-flash", "gemini-3.2-flash", "gemini-3.5-flash-live", "gemini-3.5-flash-tts", "gemini-3-audio",
		"claude-3-7-sonnet", "claude-sonnet-4", "claude-sonnet-4-20250514", "claude-opus-4-1", "claude-opus-4-5", "claude-haiku-4-5",
		"claude-opus-4-6", "claude-sonnet-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5",
		"claude-sonnet-5", "claude-sonnet-5-5", "claude-fable-5", "claude-mythos-5", "claude-haiku-5-5",
		"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20",
		"gpt-5", "gpt-5-preview", "gpt-5.1", "gpt-5.2", "gpt-5.4", "gpt-5.5", "gpt-5.6", "gpt-5.6-sol",
		"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol-pro", "gpt-5.2-codex", "gpt-6.1-pro",
		"o1-preview", "o3", "o4-mini",
	}
	for _, protocol := range []struct{ packageName, codec string }{
		{"anthropic", Anthropic}, {"google-generate-content", Gemini}, {"openai-chat", Chat}, {"openai-responses", Responses},
	} {
		t.Run(protocol.packageName, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, protocol.packageName)
			for _, model := range models {
				got, err := compiled.ResolveModelReasoning(t.Context(), model, nil)
				want := p.ResolveReasoningCapabilities(protocol.codec, model)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("model %s: declaration=%+v legacy=%+v error=%v", model, got, want, err)
				}
			}
		})
	}
}

func TestPackageModelOutputControlsMatchExistingPolicy(t *testing.T) {
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range definitions {
		var definition p.Definition
		if err := raw.Decode(&definition); err != nil {
			t.Fatal(err)
		}
		name := definition.ID
		if name == p.PresetChatCompletionsID {
			name = "openai-chat"
		} else if name == p.PresetAnthropicID {
			name = "anthropic"
		}
		legacy, issues := compiler.Compile(raw.Bytes())
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		declared := compileBuiltinPackage(t, name)
		for _, sample := range []struct {
			name, limit string
			isDefault   bool
			maximum     int64
			lateLimit   string
			isDisabled  bool
		}{
			{name: "absent"}, {name: "explicit-null", limit: "null"}, {name: "explicit-zero", limit: "0"}, {name: "negative-zero", limit: "-0"},
			{name: "explicit", limit: "128"}, {name: "bound-default", maximum: 4096},
			{name: "bound-system-limit", limit: "8192", isDefault: true, maximum: 4096},
			{name: "explicit-hard-limit", limit: "8192", maximum: 4096},
			{name: "string-limit", limit: `"128"`}, {name: "negative-limit", limit: "-1"},
			{name: "later-hard-limit", limit: "128", maximum: 4096, lateLimit: "8192"},
			{name: "later-invalid-limit", limit: "128", lateLimit: `"bad"`},
			{name: "disabled-default", isDisabled: true},
		} {
			t.Run(name+"/"+sample.name, func(t *testing.T) {
				request := &p.Request{Model: p.StringValue("model"), Parameters: p.Object{}, OutputLimitDefault: sample.isDefault}
				if sample.limit != "" {
					request.Parameters["max_output_tokens"] = testValue(t, sample.limit)
				}
				var bound *p.ReasoningCapabilities
				if sample.maximum != 0 {
					bound = &p.ReasoningCapabilities{Kind: "unknown", MaxOutputTokens: sample.maximum}
				}
				execute := func(protocol *p.Compiled) (*p.Request, []p.ConversionIssue, error) {
					t.Helper()
					policy := p.DefaultConversionPolicy(protocol, protocol)
					// Other default actions have separate migration coverage. This
					// slice compares the actual declared model rule and its order.
					var modelRule p.ConversionRule
					for _, rule := range policy.Rules {
						if rule.ID == "reasoning-controls" {
							modelRule = rule
						}
					}
					if modelRule.Order != 175 {
						t.Fatal("default model rule disappeared or moved", modelRule)
					}
					policy.Rules, policy.Usage = []p.ConversionRule{modelRule}, nil
					overrides := p.ConversionPolicy{SchemaVersion: 1, ID: "user-model-controls", Mode: "compatible"}
					if sample.isDisabled {
						modelRule.Enabled = false
						overrides.Rules = []p.ConversionRule{modelRule}
					}
					if sample.lateLimit != "" {
						policy.Rules = append(policy.Rules, p.ConversionRule{ID: "late-output-limit", Order: 250, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: "/parameters/max_output_tokens", Value: testValue(t, sample.lateLimit)})
					}
					conversion, err := p.ResolveConversion(policy, overrides)
					if err != nil {
						t.Fatal(err)
					}
					input := request.Clone()
					input.SchemaVersion = protocol.CoreVersion()
					sink := &p.DiagnosticSink{}
					result, err := conversion.Request(t.Context(), input, p.ConversionContext{Source: protocol.Identity(), Target: protocol.Identity(), Model: "model", Reasoning: bound}, sink)
					for _, issue := range sink.Issues() {
						if issue.PolicyHash != conversion.Hash {
							t.Fatal("diagnostic lost its effective policy identity", issue)
						}
					}
					return result, sink.Issues(), err
				}
				want, wantIssues, wantErr := execute(legacy)
				got, gotIssues, gotErr := execute(declared)
				if (wantErr == nil) != (gotErr == nil) {
					t.Fatal("output-limit acceptance changed", wantErr, gotErr)
				}
				if want != nil {
					want.SchemaVersion = got.SchemaVersion
					if !equalAgentMigrationValues(t, want, got) {
						t.Fatal("output controls changed the core result", want, got)
					}
				}
				if wantErr != nil {
					var oldFailure, newFailure *p.ConversionError
					if !errors.As(wantErr, &oldFailure) || !errors.As(gotErr, &newFailure) || oldFailure.CandidateOnly != newFailure.CandidateOnly {
						t.Fatal("control error classification changed", wantErr, gotErr)
					}
					wantIssues = append(wantIssues, oldFailure.Issues...)
					gotIssues = append(gotIssues, newFailure.Issues...)
				}
				if len(wantIssues) != len(gotIssues) {
					t.Fatal("control diagnostic count changed", wantIssues, gotIssues)
				}
				for index, want := range wantIssues {
					got := gotIssues[index]
					if want.Code != got.Code || want.Severity != got.Severity || want.Direction != got.Direction || want.Stage != got.Stage || want.Path != got.Path || want.Reason != got.Reason || want.Suggestion != got.Suggestion || want.RuleID != got.RuleID || want.Fidelity != got.Fidelity {
						t.Fatal("control diagnostic changed", want, got)
					}
				}
			})
		}
	}
}
