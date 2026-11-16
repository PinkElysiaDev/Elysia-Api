package builtin

import (
	"errors"
	"os"
	"reflect"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Static expectations preserve the pre-cutover contract without executing a retired resolver.
func TestPackageModelCapabilitiesMatchContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/model-reasoning.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]map[string]p.ReasoningCapabilities
	value, err := p.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := value.Decode(&expected); err != nil {
		t.Fatal(err)
	}
	for codec, models := range expected {
		t.Run(codec, func(t *testing.T) {
			compiled := compileBuiltinPackage(t, packageName(codec))
			for model, want := range models {
				got, err := compiled.ResolveModelReasoning(t.Context(), model, nil)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("model %s: actual=%+v expected=%+v error=%v", model, got, want, err)
				}
			}
		})
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

func TestPackageThinkingProjectionMatrix(t *testing.T) {
	sources, models := thinkingMatrixFixtures()
	for source, raw := range sources {
		from := testCompiled(t, source)
		request, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		request.Native = nil
		request.Parameters = p.Object{"max_output_tokens": request.Parameters["max_output_tokens"]}
		request.ClientOutput = nil
		for target, model := range models {
			to := testCompiled(t, target)
			for _, mode := range []string{"compatible", "strict"} {
				t.Run(source+"/"+target+"/"+mode, func(t *testing.T) {
					conversion := compileModelMigrationRule(t, from, to, mode)
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: model}, sink)
					hasScopeLoss := (source == Anthropic) != (target == Anthropic)
					if mode == "strict" && hasScopeLoss {
						var failure *p.ConversionError
						if !errors.As(err, &failure) || !failure.CandidateOnly {
							t.Fatal("strict scope change did not reject its target", err)
						}
						return
					}
					if err != nil || projected.Reasoning == nil || !projected.Reasoning.Effort.IsString("high") || projected.Parameters["max_output_tokens"].JSONString() != "8192" {
						t.Fatal("model projection changed effort or output limit", projected, err)
					}
					projected.Model = p.StringValue(model)
					wire, err := to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					decoded, err := to.DecodeRequest(t.Context(), wire, p.EvaluationContext{})
					if err != nil || !equalAgentMigrationValues(t, projected.Reasoning, decoded.Reasoning) {
						t.Fatal("target wire changed projected thinking", projected.Reasoning, decoded, err)
					}
					for _, issue := range sink.Issues() {
						if issue.PolicyHash != conversion.Hash || issue.RuleID != "reasoning-controls" {
							t.Fatal("model diagnostic lost its policy identity", issue)
						}
					}
				})
			}
		}
	}
}

func TestPackageModelOutputControlsMatchExistingPolicy(t *testing.T) {
	for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
		compiled := testCompiled(t, name)
		for _, sample := range []struct {
			name, limit, lateLimit string
			maximum                int64
			isDefault, isDisabled  bool
			isRejected, isGlobal   bool
		}{
			{name: "absent"}, {name: "explicit-null", limit: "null"}, {name: "explicit-zero", limit: "0"}, {name: "negative-zero", limit: "-0"},
			{name: "explicit", limit: "128"}, {name: "bound-default", maximum: 4096},
			{name: "bound-system-limit", limit: "8192", isDefault: true, maximum: 4096},
			{name: "explicit-hard-limit", limit: "8192", maximum: 4096, isRejected: true},
			{name: "string-limit", limit: `"128"`, isRejected: true, isGlobal: true},
			{name: "negative-limit", limit: "-1", isRejected: true, isGlobal: true},
			{name: "later-hard-limit", limit: "128", maximum: 4096, lateLimit: "8192", isRejected: true},
			{name: "later-invalid-limit", limit: "128", lateLimit: `"bad"`, isRejected: true},
			{name: "disabled-default", isDisabled: true},
		} {
			t.Run(name+"/"+sample.name, func(t *testing.T) {
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue("model"), Parameters: p.Object{}, OutputLimitDefault: sample.isDefault}
				if sample.limit != "" {
					request.Parameters["max_output_tokens"] = testValue(t, sample.limit)
				}
				var bound *p.ReasoningCapabilities
				if sample.maximum != 0 {
					bound = &p.ReasoningCapabilities{Kind: "unknown", MaxOutputTokens: sample.maximum}
				}
				policy := modelMigrationPolicy(t, compiled, compiled, "compatible")
				policy.Rules[0].Enabled = !sample.isDisabled
				if sample.lateLimit != "" {
					policy.Rules = append(policy.Rules, p.ConversionRule{ID: "late-output-limit", Order: 250, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: "/parameters/max_output_tokens", Value: testValue(t, sample.lateLimit)})
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: "model", Reasoning: bound}, sink)
				if sample.isRejected {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || failure.CandidateOnly == sample.isGlobal || len(failure.Issues) == 0 || failure.Issues[0].Path != "/parameters/max_output_tokens" {
						t.Fatal("invalid output control escaped its expected rejection boundary", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want, isDefault := sample.limit, sample.isDefault
				if sample.name == "bound-system-limit" {
					want = "4096"
				}
				if name == Anthropic && !sample.isDisabled && (want == "" || want == "null") {
					want, isDefault = "8192", true
					if sample.maximum != 0 {
						want = "4096"
					}
				}
				if want == "-0" {
					want = "0"
				}
				if projected.Parameters["max_output_tokens"].JSONString() != want || projected.OutputLimitDefault != isDefault {
					t.Fatal("output limit lost its explicit/default distinction", projected.Parameters, projected.OutputLimitDefault, want, isDefault)
				}
				for _, issue := range sink.Issues() {
					if issue.PolicyHash != conversion.Hash || issue.RuleID != "reasoning-controls" {
						t.Fatal("output diagnostic lost its policy identity", issue)
					}
				}
			})
		}
	}
}

func TestPackageModelLateRulesMatchExistingPolicy(t *testing.T) {
	for _, target := range []struct{ name, model, reasoning, legacyBucket, legacyControls string }{
		{Anthropic, "claude-sonnet-4", `{"mode":"enabled","budget":2048}`, "anthropic_thinking", `{"type":"enabled"}`},
		{Gemini, "gemini-2.5-pro", `{"mode":"enabled","budget":2048}`, "gemini_thinking", `{"thinkingBudget":2048}`},
		{Chat, "gpt-5.4", `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, "reasoning_effort", `"high"`},
		{Responses, "gpt-5.4", `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, "responses_reasoning", `{"effort":"high"}`},
	} {
		compiled := testCompiled(t, target.name)
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
				policy := modelMigrationPolicy(t, compiled, compiled, "compatible")
				policy.Rules = append(policy.Rules, p.ConversionRule{ID: "authored-late-control", Order: 250, Enabled: true, Phase: p.ConversionRequest, Action: "set", Path: sample.path, Value: testValue(t, sample.value)})
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue(target.model), Content: []p.Node{}, Parameters: p.Object{"max_output_tokens": testValue(t, "8192")}}
				if err := testValue(t, target.reasoning).Decode(&request.Reasoning); err != nil {
					t.Fatal(err)
				}
				before, err := p.EncodeValue(request)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Model: target.model}, nil)
				if err == nil {
					_, err = compiled.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
				}
				canEncode := sample.name == "unresolved-history" && target.name == Anthropic || sample.name == "null-budget" && target.name == Gemini
				if (err == nil) != canEncode {
					t.Fatal("late authored control escaped target validation", err)
				}
				after, encodeErr := p.EncodeValue(request)
				if encodeErr != nil || before != after {
					t.Fatal("late control changed its source request", encodeErr)
				}
			})
		}
	}
}

func TestPackageModelProjectionEdgesMatchExistingPolicy(t *testing.T) {
	for _, sample := range []struct {
		name, source, target, model, reasoning, limit string
		wantBudget, wantLimit, wantEffort             string
		isDefault, isAgent, isInterleaved             bool
		isRejected, isLossy, isEmpty                  bool
		capability                                    *p.ReasoningCapabilities
	}{
		{name: "manual-percent", source: Chat, target: Anthropic, model: "claude-opus-4-5", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, limit: "8192", wantBudget: "6553", wantLimit: "8192", wantEffort: "high", isLossy: true},
		{name: "manual-explicit-minimum", source: Chat, target: Anthropic, model: "claude-opus-4-5", reasoning: `{"mode":"enabled","effort":"low","effortScope":"reasoning"}`, limit: "1024", isRejected: true},
		{name: "manual-bound-expansion", source: Anthropic, target: Anthropic, model: "custom", reasoning: `{"mode":"enabled","budget":8192}`, limit: "1024", isDefault: true, capability: &p.ReasoningCapabilities{Kind: "budget", Manual: true, MinBudget: 1024, MaxBudget: 32768, MaxOutputTokens: 16384}, wantBudget: "8192", wantLimit: "8193"},
		{name: "manual-interleaved", source: Anthropic, target: Anthropic, model: "claude-sonnet-4-5", reasoning: `{"mode":"enabled","budget":16384,"interleaved":true}`, limit: "8192", isInterleaved: true, isRejected: true},
		{name: "manual-without-operation", source: Anthropic, target: Anthropic, model: "claude-sonnet-4-5", reasoning: `{"mode":"enabled","budget":16384,"interleaved":true}`, limit: "8192", isRejected: true},
		{name: "dynamic-budget", source: Gemini, target: Chat, model: "gpt-5.4", reasoning: `{"mode":"auto","budget":-1}`, wantEffort: "medium", isLossy: true},
		{name: "budget-bound", source: Anthropic, target: Gemini, model: "gemini-2.5-flash", reasoning: `{"mode":"enabled","budget":30000}`, wantBudget: "24576", isLossy: true},
		{name: "disabled-no-thinking", source: Gemini, target: Chat, model: "gpt-4o", reasoning: `{"mode":"disabled","budget":0,"summary":"omitted"}`, isEmpty: true},
		{name: "between-tools", source: Anthropic, target: Responses, model: "gpt-5.4", reasoning: `{"mode":"between_tools","effort":"high","effortScope":"generation"}`, wantEffort: "high", isLossy: true},
		{name: "summary-projection", source: Responses, target: Anthropic, model: "claude-opus-4-6", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning","summary":"detailed"}`, wantLimit: "8192", wantEffort: "high", isLossy: true},
		{name: "unknown-native", source: Responses, target: Responses, model: "unknown", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, wantEffort: "high"},
		{name: "unknown-agent", source: Responses, target: Responses, model: "unknown", reasoning: `{"mode":"enabled","effort":"high","effortScope":"reasoning"}`, isAgent: true, isEmpty: true, isLossy: true},
	} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(sample.name+"/"+mode, func(t *testing.T) {
				from, to := testCompiled(t, sample.source), testCompiled(t, sample.target)
				conversion := compileModelMigrationRule(t, from, to, mode)
				request := &p.Request{SchemaVersion: p.ProtocolCoreVersion, Model: p.StringValue(sample.model), Parameters: p.Object{}, OutputLimitDefault: sample.isDefault}
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
				projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: sample.model, Reasoning: sample.capability, InterleavedThinking: sample.isInterleaved}, sink)
				if sample.isRejected || mode == "strict" && sample.isLossy {
					if err == nil {
						t.Fatal("model constraint or strict information loss was accepted")
					}
					return
				}
				if err != nil || (projected.Reasoning == nil) != sample.isEmpty || projected.Parameters["max_output_tokens"].JSONString() != sample.wantLimit {
					t.Fatal("model projection changed output or disabled intent", projected, err)
				}
				if !sample.isEmpty && (projected.Reasoning.Budget.JSONString() != sample.wantBudget || projected.Reasoning.Effort.JSONString() != quotedControl(sample.wantEffort)) {
					t.Fatal("model projection changed the selected effort/budget", projected.Reasoning)
				}
				if sample.isLossy && len(sink.Issues()) == 0 {
					t.Fatal("approximated model controls lost their diagnostic")
				}
			})
		}
	}
}

func quotedControl(value string) string {
	if value == "" {
		return ""
	}
	return p.StringValue(value).JSONString()
}
