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

func TestPackageThinkingProjectionMatrix(t *testing.T) {
	sources, models := thinkingMatrixFixtures()
	legacy, declared := map[string]*p.Compiled{}, map[string]*p.Compiled{}
	for codec, name := range map[string]string{Chat: "openai-chat", Responses: "openai-responses", Anthropic: "anthropic", Gemini: "google-generate-content"} {
		legacy[codec], declared[codec] = shippedProjectionProtocol(t, codec), compileBuiltinPackage(t, name)
	}
	for source, raw := range sources {
		request, err := legacy[source].DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		// Isolate model controls from the other default conversion actions.
		request.Native = nil
		request.Parameters = p.Object{"max_output_tokens": request.Parameters["max_output_tokens"]}
		for target, model := range models {
			for _, mode := range []string{"compatible", "strict"} {
				t.Run(source+"/"+target+"/"+mode, func(t *testing.T) {
					var results [2]*p.Request
					var diagnostics [2][]p.ConversionIssue
					var failures [2]error
					for index, versions := range []map[string]*p.Compiled{legacy, declared} {
						from, to := versions[source], versions[target]
						conversion := compileModelMigrationRule(t, from, to, mode)
						input := request.Clone()
						input.SchemaVersion = to.CoreVersion()
						sink := &p.DiagnosticSink{}
						results[index], failures[index] = conversion.Request(t.Context(), input, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: model}, sink)
						diagnostics[index] = sink.Issues()
					}
					compareModelMigrationResult(t, results[0], results[1], diagnostics[0], diagnostics[1], failures[0], failures[1])
					if failures[0] == nil {
						results[0].Model, results[1].Model = p.StringValue(model), p.StringValue(model)
						oldWire, oldErr := legacy[target].EncodeRequest(t.Context(), results[0], p.EvaluationContext{})
						newWire, newErr := declared[target].EncodeRequest(t.Context(), results[1], p.EvaluationContext{})
						if oldErr != nil || newErr != nil || string(oldWire) != string(newWire) {
							t.Fatal("projected controls changed target rendering", string(oldWire), string(newWire), oldErr, newErr)
						}
					}
				})
			}
		}
	}
}

func compileModelMigrationRule(t *testing.T, source, target *p.Compiled, mode string) *p.CompiledConversion {
	t.Helper()
	conversion, err := p.ResolveConversion(modelMigrationPolicy(t, source, target, mode))
	if err != nil {
		t.Fatal(err)
	}
	return conversion
}

func modelMigrationPolicy(t *testing.T, source, target *p.Compiled, mode string) p.ConversionPolicy {
	t.Helper()
	policy := p.DefaultConversionPolicy(source, target)
	for _, rule := range policy.Rules {
		if rule.ID == "reasoning-controls" {
			if rule.Order != 175 {
				t.Fatal("model rule changed order", rule)
			}
			policy.Rules, policy.Usage, policy.Mode = []p.ConversionRule{rule}, nil, mode
			return policy
		}
	}
	t.Fatal("default model rule missing")
	return p.ConversionPolicy{}
}

func compareModelMigrationResult(t *testing.T, want, got *p.Request, wantIssues, gotIssues []p.ConversionIssue, wantErr, gotErr error) {
	t.Helper()
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatal("model control acceptance changed", wantErr, gotErr)
	}
	if want != nil {
		oldVersion := want.SchemaVersion
		want.SchemaVersion = got.SchemaVersion
		isEqual := equalAgentMigrationValues(t, want, got)
		want.SchemaVersion = oldVersion
		if !isEqual {
			t.Fatal("model controls changed the core result", want, got)
		}
	}
	if wantErr != nil {
		var oldFailure, newFailure *p.ConversionError
		if errors.As(wantErr, &oldFailure) != errors.As(gotErr, &newFailure) {
			t.Fatal("model error classification changed", wantErr, gotErr)
		}
		if oldFailure != nil {
			if oldFailure.CandidateOnly != newFailure.CandidateOnly {
				t.Fatal("model candidate classification changed", oldFailure, newFailure)
			}
			wantIssues = append(wantIssues, oldFailure.Issues...)
			gotIssues = append(gotIssues, newFailure.Issues...)
		} else if wantErr.Error() != gotErr.Error() {
			t.Fatal("model error changed", wantErr, gotErr)
		}
	}
	if len(wantIssues) != len(gotIssues) {
		t.Fatal("model diagnostic count changed", wantIssues, gotIssues)
	}
	for index, want := range wantIssues {
		got := gotIssues[index]
		if want.Code != got.Code || want.Severity != got.Severity || want.Direction != got.Direction || want.Stage != got.Stage || want.Path != got.Path || want.Reason != got.Reason || want.Suggestion != got.Suggestion || want.RuleID != got.RuleID || want.Fidelity != got.Fidelity {
			t.Fatal("model diagnostic changed", want, got)
		}
	}
}

func TestPackageModelLateRulesMatchExistingPolicy(t *testing.T) {
	for _, target := range []struct {
		name, codec, model, reasoning, legacyBucket, legacyControls string
	}{
		{"anthropic", Anthropic, "claude-sonnet-4", `{"mode":"enabled","budget":2048}`, "anthropic_thinking", `{"type":"enabled"}`},
		{"google-generate-content", Gemini, "gemini-2.5-pro", `{"mode":"enabled","budget":2048}`, "gemini_thinking", `{"thinkingBudget":2048}`},
		{"openai-chat", Chat, "gpt-5.4", `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, "reasoning_effort", `"high"`},
		{"openai-responses", Responses, "gpt-5.4", `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, "responses_reasoning", `{"effort":"high"}`},
	} {
		legacy, declared := shippedProjectionProtocol(t, target.codec), compileBuiltinPackage(t, target.name)
		for _, sample := range []struct{ name, path, value string }{
			{"string-budget", "/reasoning/budget", `"2048"`},
			{"object-budget", "/reasoning/budget", `{}`},
			{"null-budget", "/reasoning/budget", `null`},
			{"legacy-conflict", "/parameters/" + target.legacyBucket, target.legacyControls},
			{"invalid-legacy-bucket", "/parameters/" + target.legacyBucket, `false`},
			{"interleaved-operation", "/reasoning/interleaved", `true`},
			{"unresolved-history", "/reasoning/historyRetention", `"all"`},
		} {
			t.Run(target.name+"/"+sample.name, func(t *testing.T) {
				execute := func(protocol *p.Compiled) (*p.Request, []p.ConversionIssue, error) {
					t.Helper()
					policy := modelMigrationPolicy(t, protocol, protocol, "compatible")
					policy.Rules = append(policy.Rules, p.ConversionRule{ID: "authored-late-control", Order: 250, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: sample.path, Value: testValue(t, sample.value)})
					conversion, err := p.ResolveConversion(policy)
					if err != nil {
						t.Fatal(err)
					}
					request := &p.Request{SchemaVersion: protocol.CoreVersion(), Model: p.StringValue(target.model), Parameters: p.Object{"max_output_tokens": testValue(t, "8192")}}
					if err := testValue(t, target.reasoning).Decode(&request.Reasoning); err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					result, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: protocol.Identity(), Target: protocol.Identity(), Model: target.model}, sink)
					return result, sink.Issues(), err
				}
				want, wantIssues, wantErr := execute(legacy)
				got, gotIssues, gotErr := execute(declared)
				compareModelMigrationResult(t, want, got, wantIssues, gotIssues, wantErr, gotErr)
			})
		}
	}
}

func TestPackageModelProjectionEdgesMatchExistingPolicy(t *testing.T) {
	legacy, declared := map[string]*p.Compiled{}, map[string]*p.Compiled{}
	for codec, name := range map[string]string{Chat: "openai-chat", Responses: "openai-responses", Anthropic: "anthropic", Gemini: "google-generate-content"} {
		legacy[codec], declared[codec] = shippedProjectionProtocol(t, codec), compileBuiltinPackage(t, name)
	}
	for _, sample := range []struct {
		name, source, target, model, reasoning, limit string
		isDefault, isAgent, isInterleaved             bool
		capability                                    *p.ReasoningCapabilities
	}{
		{name: "manual-percent", source: Chat, target: Anthropic, model: "claude-opus-4-5", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, limit: "8192"},
		{name: "manual-explicit-minimum", source: Chat, target: Anthropic, model: "claude-opus-4-5", reasoning: `{"mode":"enabled","effort":"low","effortScope":"reasoning"}`, limit: "1024"},
		{name: "manual-bound-expansion", source: Anthropic, target: Anthropic, model: "custom", reasoning: `{"mode":"enabled","budget":8192}`, limit: "1024", isDefault: true, capability: &p.ReasoningCapabilities{Kind: "budget", Manual: true, MinBudget: 1024, MaxBudget: 32768, MaxOutputTokens: 16384}},
		{name: "manual-interleaved", source: Anthropic, target: Anthropic, model: "claude-sonnet-4-5", reasoning: `{"mode":"enabled","budget":16384,"interleaved":true}`, limit: "8192", isInterleaved: true},
		{name: "manual-without-operation", source: Anthropic, target: Anthropic, model: "claude-sonnet-4-5", reasoning: `{"mode":"enabled","budget":16384,"interleaved":true}`, limit: "8192"},
		{name: "dynamic-budget", source: Gemini, target: Chat, model: "gpt-5.4", reasoning: `{"mode":"auto","budget":-1}`},
		{name: "budget-bound", source: Anthropic, target: Gemini, model: "gemini-2.5-flash", reasoning: `{"mode":"enabled","budget":30000}`},
		{name: "disabled-no-thinking", source: Gemini, target: Chat, model: "gpt-4o", reasoning: `{"mode":"disabled","budget":0,"summary":"omitted"}`},
		{name: "between-tools", source: Anthropic, target: Responses, model: "gpt-5.4", reasoning: `{"mode":"between_tools","effort":"high","effortScope":"generation"}`},
		{name: "summary-projection", source: Responses, target: Anthropic, model: "claude-opus-4-6", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning","summary":"detailed"}`},
		{name: "unknown-native", source: Responses, target: Responses, model: "unknown", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`},
		{name: "unknown-agent", source: Responses, target: Responses, model: "unknown", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, isAgent: true},
	} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(sample.name+"/"+mode, func(t *testing.T) {
				execute := func(versions map[string]*p.Compiled) (*p.Request, []p.ConversionIssue, error) {
					from, to := versions[sample.source], versions[sample.target]
					conversion := compileModelMigrationRule(t, from, to, mode)
					request := &p.Request{SchemaVersion: to.CoreVersion(), Model: p.StringValue(sample.model), Parameters: p.Object{}, OutputLimitDefault: sample.isDefault}
					if err := testValue(t, sample.reasoning).Decode(&request.Reasoning); err != nil {
						t.Fatal(err)
					}
					if sample.limit != "" {
						request.Parameters["max_output_tokens"] = testValue(t, sample.limit)
					}
					if sample.isAgent {
						request.Source = p.AgentIdentity()
					}
					sink := &p.DiagnosticSink{}
					result, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: sample.model, Reasoning: sample.capability, InterleavedThinking: sample.isInterleaved}, sink)
					return result, sink.Issues(), err
				}
				want, wantIssues, wantErr := execute(legacy)
				got, gotIssues, gotErr := execute(declared)
				compareModelMigrationResult(t, want, got, wantIssues, gotIssues, wantErr, gotErr)
			})
		}
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
					policy := modelMigrationPolicy(t, protocol, protocol, "compatible")
					overrides := p.ConversionPolicy{SchemaVersion: 1, ID: "user-model-controls", Mode: "compatible"}
					if sample.isDisabled {
						modelRule := policy.Rules[0]
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
				compareModelMigrationResult(t, want, got, wantIssues, gotIssues, wantErr, gotErr)
			})
		}
	}
}
