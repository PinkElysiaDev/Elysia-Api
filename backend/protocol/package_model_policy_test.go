package protocol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPackageModelCapabilityBindingAndIsolation(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ModelPolicy = &ProtocolModelPolicy{Reasoning: literalExpression(ReasoningCapabilities{Kind: "level", Disable: true, Levels: []string{"low", "high"}, DefaultEffort: "high"})}
	compiled := compilePackageForTest(t, document)
	first, err := compiled.ResolveModelReasoning(t.Context(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Levels[0] = "edited"
	bound := ReasoningCapabilities{Kind: "level", Levels: []string{"medium"}, DefaultMode: "enabled", Source: "explicit-binding"}
	got, err := compiled.ResolveModelReasoning(t.Context(), "model", &bound)
	if err != nil || !reflect.DeepEqual(got, bound) {
		t.Fatal("binding was merged with inferred capability", got, err)
	}
	got.Levels[0] = "edited"
	if bound.Levels[0] != "medium" {
		t.Fatal("result mutated the binding")
	}
	fresh, err := compiled.ResolveModelReasoning(t.Context(), "model", nil)
	if err != nil || fresh.Levels[0] != "low" {
		t.Fatal("result mutated the compiled profile", fresh, err)
	}
	bound.Kind = "invalid"
	if _, err := compiled.ResolveModelReasoning(t.Context(), "model", &bound); err == nil {
		t.Fatal("invalid dynamic binding was accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.ResolveModelReasoning(ctx, "model", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("capability lookup ignored cancellation", err)
	}
}

func TestPackageSourceReasoningNormalization(t *testing.T) {
	for _, source := range []struct {
		name, codec, parameters string
	}{
		{"openai-chat", "openai-chat", `{"reasoning_effort":"high"}`},
		{"openai-responses", "responses", `{"responses_reasoning":{"effort":"high","summary":"auto","future":9007199254740993}}`},
		{"anthropic", "anthropic", `{"anthropic_thinking":{"type":"enabled","budget_tokens":2048,"display":"summarized","future":9007199254740993},"anthropic_output_config":{"effort":"high","future":null}}`},
		{"google-generate-content", "gemini", `{"gemini_thinking":{"thinkingBudget":4096,"includeThoughts":true,"future":9007199254740993}}`},
	} {
		raw, err := os.ReadFile(filepath.Join("builtin", "packages", source.name, "mapping.json"))
		if err != nil {
			t.Fatal(err)
		}
		var mapping ProtocolMapping
		if err := decodeContract(raw, &mapping); err != nil {
			t.Fatal(err)
		}
		compiler := &expressionCompiler{limits: DefaultLimits(), references: mapping.Expressions, resolving: map[string]bool{}, used: map[string]bool{}}
		normalization := modelNormalizationOptions{Steps: mapping.ModelPolicy.Normalize, Checks: mapping.ModelPolicy.NormalizeChecks}
		if err := normalization.compile(compiler); err != nil {
			t.Fatal(err)
		}
		var parameters Object
		if err := numericTestInput(t, source.parameters).Decode(&parameters); err != nil {
			t.Fatal(err)
		}
		type normalizationFixture struct {
			name   string
			change func(*Request)
		}
		fixtures := []normalizationFixture{
			{"legacy", func(*Request) {}},
			{"history-and-legacy", func(request *Request) { request.Reasoning = &ReasoningIntent{HistoryRetention: StringValue("all")} }},
			{"empty-canonical-conflict", func(request *Request) {
				request.Reasoning = &ReasoningIntent{}
				delete(request.Parameters, "anthropic_output_config")
			}},
			{"empty", func(request *Request) { request.Parameters = nil }},
			{"empty-buckets", func(request *Request) {
				for name := range request.Parameters {
					request.Parameters[name] = numericTestInput(t, `{}`)
				}
				if source.codec == "openai-chat" {
					request.Parameters["reasoning_effort"] = Value{}
					delete(request.Parameters, "reasoning_effort")
				}
			}},
			{"null-buckets", func(request *Request) {
				for name := range request.Parameters {
					request.Parameters[name] = numericTestInput(t, `null`)
				}
			}},
			{"canonical-conflict", func(request *Request) {
				request.Reasoning = &ReasoningIntent{Mode: "enabled", Effort: StringValue("high"), EffortScope: "reasoning"}
				delete(request.Parameters, "anthropic_output_config")
			}},
			{"invalid-bucket", func(request *Request) {
				for name := range request.Parameters {
					request.Parameters[name] = numericTestInput(t, `false`)
				}
			}},
		}
		if source.codec == "anthropic" {
			fixtures = append(fixtures, normalizationFixture{"thinking-before-output-config", func(request *Request) {
				request.Parameters["anthropic_thinking"] = numericTestInput(t, `{"type":"unknown"}`)
				request.Parameters["anthropic_output_config"] = numericTestInput(t, `false`)
			}})
		}
		for _, fixture := range fixtures {
			t.Run(source.name+"/"+fixture.name, func(t *testing.T) {
				request := &Request{SchemaVersion: ProtocolCoreVersion, Parameters: parameters}
				request = request.Clone()
				fixture.change(request)
				want, got := request.Clone(), request.Clone()
				wantErr := NormalizeReasoningParameters(want, source.codec, false)
				execution := &modelControlExecution{limits: DefaultLimits(), bindings: make([]JSONOutput, normalization.bindingSlots)}
				gotErr := execution.apply(t.Context(), &CompiledConversion{}, got, ConversionContext{}, nil, normalization.steps, false)
				if gotErr == nil {
					gotErr = ValidateReasoningIntent(got.Reasoning)
				}
				if gotErr == nil && execution.changedReasoning {
					gotErr = execution.apply(t.Context(), &CompiledConversion{}, got, ConversionContext{}, nil, normalization.checks, false)
				}
				if (wantErr == nil) != (gotErr == nil) {
					t.Fatal("source normalization acceptance changed", wantErr, gotErr)
				}
				if wantErr == nil {
					before, _ := EncodeValue(want)
					after, _ := EncodeValue(got)
					if before != after {
						t.Fatal("source normalization changed semantic fields", before.JSONString(), after.JSONString())
					}
					return
				}
				var wantFailure, gotFailure *ConversionError
				if errors.As(wantErr, &wantFailure) != errors.As(gotErr, &gotFailure) {
					t.Fatal("source normalization changed error classification", wantErr, gotErr)
				}
				if wantFailure != nil {
					if !reflect.DeepEqual(wantFailure.Issues, gotFailure.Issues) || wantFailure.CandidateOnly != gotFailure.CandidateOnly {
						t.Fatal("source normalization diagnostic changed", wantFailure, gotFailure)
					}
				} else if wantErr.Error() != gotErr.Error() {
					t.Fatal("source normalization error changed", wantErr, gotErr)
				}
			})
		}
	}
}

func TestPackageModelCapabilitiesValidateProfilesAtCompileTime(t *testing.T) {
	valid := literalExpression(ReasoningCapabilities{Kind: "unknown"})
	for _, expression := range []Expression{
		literalExpression(ReasoningCapabilities{Kind: "budget", MinBudget: 100, MaxBudget: 50}),
		literalExpression(ReasoningCapabilities{Kind: "level"}),
		literalExpression(ReasoningCapabilities{Kind: "unknown", Levels: []string{"unrecognized"}}),
		{Op: "if", When: expressionPointer(literalExpression(true)), Then: &valid},
		readExpression("input", ""),
	} {
		document := textProtocolPackage(t)
		document.Mapping.ModelPolicy = &ProtocolModelPolicy{Reasoning: expression}
		compiler, err := NewCompiler(DefaultLimits(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
		if compiled != nil || IssuesError(issues) == nil {
			t.Fatal("invalid static model policy compiled", expression, issues)
		}
	}
	document := textProtocolPackage(t)
	document.Spec.Family = "openai-chat"
	compiled := compilePackageForTest(t, document)
	capability, err := compiled.ResolveModelReasoning(t.Context(), "gpt-6.1-sol", nil)
	if err != nil || capability.Kind != "unknown" || len(capability.Levels) != 0 {
		t.Fatal("wire family selected an undeclared model policy", capability, err)
	}
}
