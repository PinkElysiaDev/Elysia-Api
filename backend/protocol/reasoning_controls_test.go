package protocol

import (
	"errors"
	"fmt"
	"testing"
)

func reasoningTestConversion(t *testing.T, source, target, mode string) *CompiledConversion {
	t.Helper()
	c, err := CompileConversion(ConversionPolicy{SchemaVersion: 1, ID: "reasoning-test", Mode: mode, Rules: []ConversionRule{{ID: "thinking", Enabled: true, Phase: ConversionRequest, Action: "reasoning_controls", Value: controlValue(reasoningOptions{SourceCodec: source, TargetCodec: target})}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReasoningSystemBudgetAndIdempotence(t *testing.T) {
	for _, hard := range []int64{0, 16384} {
		t.Run(controlValue(hard).raw, func(t *testing.T) {
			cap := ResolveReasoningCapabilities("anthropic", "claude-opus-4-5")
			cap.MaxOutputTokens = hard
			request := &Request{SchemaVersion: 1, Reasoning: &ReasoningIntent{Mode: "enabled", Budget: controlValue(12000)}}
			route := ConversionContext{Model: "claude-opus-4-5", Reasoning: &cap}
			c := reasoningTestConversion(t, "gemini", "anthropic", "compatible")
			converted, err := c.Request(t.Context(), request, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantLimit, wantBudget := int64(8192), int64(8191)
			if hard != 0 {
				wantLimit, wantBudget = 12001, 12000
			}
			if controlInt(converted.Parameters["max_output_tokens"]) != wantLimit || controlInt(converted.Reasoning.Budget) != wantBudget || !converted.OutputLimitDefault {
				t.Fatal(converted)
			}
			again, err := c.Request(t.Context(), converted, route, nil)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := EncodeValue(converted)
			b, _ := EncodeValue(again)
			if !equalValues(a, b) {
				t.Fatal("non-idempotent", a.raw, b.raw)
			}
			if request.Parameters != nil || controlInt(request.Reasoning.Budget) != 12000 {
				t.Fatal("candidate mutated source", request)
			}
		})
	}
}

func TestReasoningInterleavedRequiresModelEndpointAndTools(t *testing.T) {
	for _, model := range []string{"claude-opus-4-5", "claude-opus-4-6"} {
		for _, header := range []bool{false, true} {
			cap := ResolveReasoningCapabilities("anthropic", model)
			request := &Request{SchemaVersion: 1, Parameters: Object{"max_output_tokens": controlValue(2048)}, Reasoning: &ReasoningIntent{Mode: "enabled", Budget: controlValue(4096), Interleaved: true}, Tools: []Tool{{Kind: FunctionTool, Name: StringValue("lookup")}}}
			c := reasoningTestConversion(t, "anthropic", "anthropic", "compatible")
			converted, err := c.Request(t.Context(), request, ConversionContext{Model: model, Reasoning: &cap, InterleavedThinking: header}, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(2047)
			if header && model == "claude-opus-4-5" {
				want = 4096
			}
			if controlInt(converted.Reasoning.Budget) != want {
				t.Fatal(model, header, converted.Reasoning)
			}
		}
	}
}

func TestReasoningCapabilityEvidenceInvalidates(t *testing.T) {
	c := reasoningTestConversion(t, "openai-chat", "gemini", "strict")
	cap := ResolveReasoningCapabilities("gemini", "gemini-2.5-flash")
	route := ConversionContext{Model: "gemini-2.5-flash", Reasoning: &cap}
	snapshot := c.WithVerificationContext(route)
	first := snapshot.ContextHash()
	cap.MaxOutputTokens = 65536
	second := c.WithVerificationContext(route).ContextHash()
	if snapshot.ContextHash() != first {
		t.Fatal("caller mutation changed the evidence snapshot")
	}
	route.InterleavedThinking = true
	third := c.WithVerificationContext(route).ContextHash()
	if !c.ContextDependent() || first == second || second == third {
		t.Fatal("stale model/endpoint evidence")
	}
}

func TestReasoningCanonicalLegacyConflict(t *testing.T) {
	request := &Request{Reasoning: &ReasoningIntent{Mode: "enabled", Effort: StringValue("high"), EffortScope: "reasoning"}, Parameters: Object{"reasoning_effort": StringValue("low")}}
	err := NormalizeReasoningParameters(request, "openai-chat", false)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.CandidateOnly {
		t.Fatal(err)
	}
}

func TestReasoningModeEffortTargetConstraints(t *testing.T) {
	for _, tc := range []struct {
		model, mode, effort string
		valid               bool
	}{
		{"claude-opus-4-5", "disabled", "high", true}, {"claude-opus-5", "disabled", "xhigh", false},
		{"claude-sonnet-5-5", "between_tools", "high", true}, {"claude-sonnet-5-5", "between_tools", "max", false},
		{"claude-opus-4-5", "between_tools", "high", false}, {"claude-opus-5-5", "disabled", "low", false},
	} {
		request := &Request{Model: StringValue(tc.model), Parameters: Object{"anthropic_thinking": controlValue(Object{"type": StringValue(tc.mode)}), "anthropic_output_config": controlValue(Object{"effort": StringValue(tc.effort)})}}
		if err := NormalizeReasoningParameters(request, "anthropic", false); err != nil {
			t.Fatalf("model group name must not restrict source controls: %v", err)
		}
		_, err := reasoningTestConversion(t, "anthropic", "anthropic", "strict").Request(t.Context(), request, ConversionContext{Model: tc.model}, nil)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
		if err != nil {
			var failure *ConversionError
			if !errors.As(err, &failure) || !failure.CandidateOnly {
				t.Fatal("target constraint must reject only this candidate", err)
			}
		}
	}
}

func TestReasoningKnownNonThinkingAgentModels(t *testing.T) {
	for _, codec := range []string{"openai-chat", "responses"} {
		for _, model := range []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20", "gpt-4o-audio-preview", "gpt-4.1-private"} {
			t.Run(codec+"/"+model, func(t *testing.T) {
				req := &Request{SchemaVersion: 1, Source: AgentIdentity(), Reasoning: &ReasoningIntent{Mode: "disabled"}}
				sink := &DiagnosticSink{}
				out, err := reasoningTestConversion(t, codec, codec, "strict").Request(t.Context(), req, ConversionContext{Model: model}, sink)
				if model == "gpt-4o-audio-preview" || model == "gpt-4.1-private" {
					var failure *ConversionError
					if ResolveReasoningCapabilities(codec, model).Kind != "unknown" || !errors.As(err, &failure) || !failure.CandidateOnly {
						t.Fatal("unknown variant must still require confirmed capabilities", out, err)
					}
				} else if err != nil || out.Reasoning != nil || len(sink.Issues()) != 0 {
					t.Fatal("non-thinking model must accept disabled intent exactly", out, err, sink.Issues())
				}
			})
		}
	}
}

func TestReasoningValidationAfterAuthoredRules(t *testing.T) {
	c := reasoningTestConversion(t, "openai-chat", "gemini", "compatible")
	policy := c.Policy
	policy.Rules = append(policy.Rules, ConversionRule{ID: "late-effort", Order: 99, Enabled: true, Phase: ConversionRequest, Action: "set", Path: "/reasoning/effort", Value: StringValue("max")})
	c, err := CompileConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{SchemaVersion: 1, Reasoning: &ReasoningIntent{Mode: "enabled", Effort: StringValue("high"), EffortScope: "reasoning"}}
	_, err = c.Request(t.Context(), req, ConversionContext{Model: "gemini-3.8-flash"}, nil)
	var failure *ConversionError
	if !errors.As(err, &failure) || !failure.CandidateOnly {
		t.Fatal("late invalid control escaped target validation", err)
	}
	req.Reasoning = &ReasoningIntent{Mode: "enabled", Budget: controlValue(4096), Interleaved: true}
	_, err = reasoningTestConversion(t, "anthropic", "anthropic", "compatible").Request(t.Context(), req, ConversionContext{Model: "claude-sonnet-4-6", InterleavedThinking: true}, nil)
	if !errors.As(err, &failure) || failure.CandidateOnly {
		t.Fatal("invalid input must reject globally", err)
	}
}

func TestReasoningOutputLimitAfterAuthoredRules(t *testing.T) {
	for _, systemDefault := range []bool{false, true} {
		for _, tc := range []struct {
			value string
			valid bool
		}{
			{"128000", true}, {"128001", false}, {"0", true}, {"-1", false},
			{`"8192"`, false}, {"8192.5", false}, {"9223372036854775808", false},
		} {
			t.Run(fmt.Sprintf("%v/%s", systemDefault, tc.value), func(t *testing.T) {
				policy := reasoningTestConversion(t, "openai-chat", "openai-chat", "compatible").Policy
				value, err := ParseValue([]byte(tc.value))
				if err != nil {
					t.Fatal(err)
				}
				policy.Rules = append(policy.Rules, ConversionRule{ID: "late-output-limit", Order: 99, Enabled: true, Phase: ConversionRequest, Action: "set", Path: "/parameters/max_output_tokens", Value: value})
				conversion, err := CompileConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				request := &Request{SchemaVersion: 1, OutputLimitDefault: systemDefault, Parameters: Object{"max_output_tokens": controlValue(8192)}}
				converted, err := conversion.Request(t.Context(), request, ConversionContext{Model: "gpt-6.1-sol"}, nil)
				if tc.valid {
					if err != nil || converted.Parameters["max_output_tokens"] != value {
						t.Fatal(converted, err)
					}
				} else {
					var failure *ConversionError
					if !errors.As(err, &failure) || !failure.CandidateOnly {
						t.Fatal("late output limit escaped target validation", converted, err)
					}
				}
				if controlInt(request.Parameters["max_output_tokens"]) != 8192 {
					t.Fatal("candidate changed source output limit")
				}
			})
		}
	}
}

func TestReasoningZeroOutputAndSummaryOnly(t *testing.T) {
	c := reasoningTestConversion(t, "anthropic", "anthropic", "strict")
	req := &Request{SchemaVersion: 1, Parameters: Object{"max_output_tokens": controlValue(0)}}
	out, err := c.Request(t.Context(), req, ConversionContext{Model: "claude-haiku-4-5"}, nil)
	if err != nil || controlInt(out.Parameters["max_output_tokens"]) != 0 {
		t.Fatal(out, err)
	}
	req = &Request{SchemaVersion: 1, Reasoning: &ReasoningIntent{Summary: StringValue("auto")}}
	sink := &DiagnosticSink{}
	out, err = reasoningTestConversion(t, "responses", "anthropic", "compatible").Request(t.Context(), req, ConversionContext{Model: "claude-opus-4-6"}, sink)
	if err != nil || out.Reasoning != nil || len(sink.Issues()) == 0 {
		t.Fatal(out, err, sink.Issues())
	}
}

func TestReasoningExplicitRejectRemainsGlobal(t *testing.T) {
	policy := reasoningTestConversion(t, "openai-chat", "gemini", "compatible").Policy
	policy.Rules = append(policy.Rules, ConversionRule{ID: "global-reject", Order: 99, Enabled: true, Phase: ConversionRequest, Action: "reject", Reason: "operator policy"})
	c, err := CompileConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Request(t.Context(), &Request{SchemaVersion: 1}, ConversionContext{Model: "gemini-3.8-flash"}, nil)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.CandidateOnly {
		t.Fatal(err)
	}
}

func TestReasoningDisabledOnNonThinkingTargetIsExact(t *testing.T) {
	cap := &ReasoningCapabilities{Kind: "none"}
	req := &Request{SchemaVersion: 1, Reasoning: &ReasoningIntent{Mode: "disabled", Effort: StringValue("none"), EffortScope: "reasoning"}}
	out, err := reasoningTestConversion(t, "openai-chat", "gemini", "strict").Request(t.Context(), req, ConversionContext{Reasoning: cap}, nil)
	if err != nil || out.Reasoning != nil {
		t.Fatal(out, err)
	}
}

func TestReasoningUnknownModelPreservesWireButDiagnosesAgentIntent(t *testing.T) {
	for _, mode := range []string{"compatible", "strict"} {
		for _, thinking := range []string{"enabled", "disabled"} {
			req := &Request{SchemaVersion: 1, Source: AgentIdentity(), Reasoning: &ReasoningIntent{Mode: thinking}}
			if thinking == "enabled" {
				req.Reasoning.Effort = StringValue("high")
				req.Reasoning.EffortScope = "reasoning"
			}
			c := reasoningTestConversion(t, "gemini", "gemini", mode)
			sink := &DiagnosticSink{}
			result, err := c.Request(t.Context(), req, ConversionContext{Model: "private-alias"}, sink)
			if mode == "strict" || thinking == "disabled" {
				var failure *ConversionError
				if !errors.As(err, &failure) || !failure.CandidateOnly {
					t.Fatal(err)
				}
			} else if err != nil || result.Reasoning != nil || len(sink.Issues()) == 0 {
				t.Fatal(result, err, sink.Issues())
			}
		}
	}
}

func TestReasoningHardConstraintsRejectInBothModes(t *testing.T) {
	for _, mode := range []string{"compatible", "strict"} {
		for _, tc := range []struct {
			name, target, model string
			reasoning           *ReasoningIntent
			limit               Value
			cap                 *ReasoningCapabilities
			path                string
		}{
			{"cannot-disable-level", "gemini", "gemini-3.5-flash-lite", &ReasoningIntent{Mode: "disabled"}, Value{}, nil, "/reasoning/mode"},
			{"cannot-disable-budget", "gemini", "gemini-2.5-pro", &ReasoningIntent{Mode: "disabled"}, Value{}, nil, "/reasoning/mode"},
			{"cannot-disable-adaptive", "anthropic", "claude-opus-5-5", &ReasoningIntent{Mode: "disabled"}, Value{}, nil, "/reasoning/mode"},
			{"cannot-confirm-disable", "gemini", "private-alias", &ReasoningIntent{Mode: "disabled"}, Value{}, nil, "/reasoning/mode"},
			{"explicit-above-hard-limit", "openai-chat", "gpt-6.1-sol", nil, controlValue(128001), nil, "/parameters/max_output_tokens"},
			{"explicit-zero", "anthropic", "claude-opus-4-5", &ReasoningIntent{Mode: "enabled", Budget: controlValue(2048)}, controlValue(0), nil, "/parameters/max_output_tokens"},
			{"explicit-below-minimum", "anthropic", "claude-opus-4-5", &ReasoningIntent{Mode: "enabled", Budget: controlValue(2048)}, controlValue(512), &ReasoningCapabilities{Kind: "budget", Manual: true, Disable: true, MinBudget: 1024, MaxOutputTokens: 16384}, "/parameters/max_output_tokens"},
			{"explicit-equals-minimum", "anthropic", "claude-opus-4-5", &ReasoningIntent{Mode: "enabled", Budget: controlValue(2048)}, controlValue(1024), nil, "/parameters/max_output_tokens"},
			{"default-cannot-fit", "anthropic", "claude-opus-4-5", &ReasoningIntent{Mode: "enabled", Budget: controlValue(2048)}, Value{}, &ReasoningCapabilities{Kind: "budget", Manual: true, Disable: true, MinBudget: 1024, MaxOutputTokens: 512}, "/parameters/max_output_tokens"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				req := &Request{SchemaVersion: 1, Reasoning: tc.reasoning}
				if !tc.limit.IsZero() {
					req.Parameters = Object{"max_output_tokens": tc.limit}
				}
				before, _ := EncodeValue(req)
				c := reasoningTestConversion(t, "openai-chat", tc.target, mode)
				_, err := c.Request(t.Context(), req, ConversionContext{Model: tc.model, Reasoning: tc.cap}, nil)
				var failure *ConversionError
				if !errors.As(err, &failure) || !failure.CandidateOnly || len(failure.Issues) != 1 {
					t.Fatalf("expected candidate rejection: %v", err)
				}
				issue := failure.Issues[0]
				if issue.Code != ConversionRejected || issue.Fidelity != "rejected" || issue.RuleID != "thinking" || issue.PolicyHash != c.Hash || issue.Path != tc.path {
					t.Fatal("missing rejection evidence", issue)
				}
				after, _ := EncodeValue(req)
				if !equalValues(before, after) {
					t.Fatal("rejected candidate mutated original request", after.raw)
				}
			})
		}
	}
}

func TestReasoningExplicitLimitPreservedWhileBudgetFits(t *testing.T) {
	for _, mode := range []string{"compatible", "strict"} {
		for _, budget := range []int64{1024, 4096} {
			t.Run(fmt.Sprintf("%s/%d", mode, budget), func(t *testing.T) {
				req := &Request{SchemaVersion: 1, Parameters: Object{"max_output_tokens": controlValue(2048)}, Reasoning: &ReasoningIntent{Mode: "enabled", Budget: controlValue(budget)}}
				c := reasoningTestConversion(t, "gemini", "anthropic", mode)
				sink := &DiagnosticSink{}
				out, err := c.Request(t.Context(), req, ConversionContext{Model: "claude-opus-4-5"}, sink)
				if mode == "strict" && budget >= 2048 {
					var failure *ConversionError
					if !errors.As(err, &failure) || !failure.CandidateOnly {
						t.Fatal(err)
					}
					return
				}
				if err != nil || out.OutputLimitDefault || controlInt(out.Parameters["max_output_tokens"]) != 2048 || controlInt(out.Reasoning.Budget) != min(budget, 2047) {
					t.Fatal(out, err)
				}
				if (len(sink.Issues()) > 0) != (budget >= 2048) {
					t.Fatal(sink.Issues())
				}
			})
		}
	}
}

func TestReasoningUnknownNativeDisabledPreserved(t *testing.T) {
	for _, mode := range []string{"compatible", "strict"} {
		req := &Request{SchemaVersion: 1, Parameters: Object{"gemini_thinking": controlValue(Object{"thinkingBudget": controlValue(0)})}}
		out, err := reasoningTestConversion(t, "gemini", "gemini", mode).Request(t.Context(), req, ConversionContext{Model: "private-alias"}, nil)
		if err != nil || out.Reasoning == nil || out.Reasoning.Mode != "disabled" || controlInt(out.Reasoning.Budget) != 0 {
			t.Fatal(out, err)
		}
	}
}
