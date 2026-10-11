package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatThinkingControlsReachGemini(t *testing.T) {
	from, to := testCompiled(t, Chat), testCompiled(t, Gemini)
	for _, model := range []string{"gemini-3.5-flash-lite", "gemini-3.8-flash", "gemini-2.5-flash"} {
		t.Run(model, func(t *testing.T) {
			req, err := from.DecodeRequest(t.Context(), []byte(`{"model":"group","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high","store":false,"stream":true,"stream_options":{"include_usage":true},"max_completion_tokens":8192}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			req, err = conversion.Request(t.Context(), req, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: model, Transport: p.SSE}, sink)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := to.EncodeRequest(t.Context(), req, p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				GenerationConfig struct {
					ThinkingConfig map[string]any `json:"thinkingConfig"`
				} `json:"generationConfig"`
			}
			if err := json.Unmarshal(wire, &body); err != nil {
				t.Fatal(err)
			}
			thinking := body.GenerationConfig.ThinkingConfig
			if model == "gemini-2.5-flash" {
				if thinking["thinkingBudget"] != float64(24576) || thinking["thinkingLevel"] != nil {
					t.Fatalf("wrong budget dialect: %s", wire)
				}
			} else if thinking["thinkingLevel"] != "high" || thinking["thinkingBudget"] != nil {
				t.Fatalf("reasoning was lost or changed: %s", wire)
			}
		})
	}
}

func convertThinking(t *testing.T, source, target, raw, model, mode string, capabilities *p.ReasoningCapabilities) (*p.Request, []byte, []p.ConversionIssue, error) {
	t.Helper()
	from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target)
	request, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{Values: p.Object{"model": p.StringValue("group")}})
	if err != nil {
		return nil, nil, nil, err
	}
	policy := p.DefaultConversionPolicy(from, to)
	policy.Mode = mode
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: model, Reasoning: capabilities}
	sink := &p.DiagnosticSink{}
	request, err = conversion.Request(t.Context(), request, route, sink)
	if err != nil {
		return request, nil, sink.Issues(), err
	}
	request.Model = p.StringValue(model)
	wire, err := to.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	return request, wire, sink.Issues(), err
}

func TestThinkingFourByFour(t *testing.T) {
	sources := map[string]string{
		Chat:      `{"model":"alias","store":false,"max_completion_tokens":8192,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`,
		Responses: `{"model":"alias","store":false,"max_output_tokens":8192,"reasoning":{"effort":"high"},"input":"hi"}`,
		Anthropic: `{"model":"alias","max_tokens":8192,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`,
		Gemini:    `{"generationConfig":{"maxOutputTokens":8192,"thinkingConfig":{"thinkingLevel":"high"}},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
	}
	models := map[string]string{Chat: "gpt-5.4", Responses: "gpt-5.4", Anthropic: "claude-opus-4-6", Gemini: "gemini-3.5-flash-lite"}
	for source, raw := range sources {
		for target, model := range models {
			t.Run(source+"/"+target, func(t *testing.T) {
				_, wire, _, err := convertThinking(t, source, target, raw, model, "compatible", nil)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(wire, &body); err != nil {
					t.Fatal(err)
				}
				var effort any
				switch target {
				case Chat:
					effort = body["reasoning_effort"]
				case Responses:
					effort = body["reasoning"].(map[string]any)["effort"]
				case Anthropic:
					effort = body["output_config"].(map[string]any)["effort"]
					if body["thinking"].(map[string]any)["type"] != "adaptive" {
						t.Fatal(string(wire))
					}
				case Gemini:
					effort = body["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)["thinkingLevel"]
				}
				if effort != "high" {
					t.Fatalf("lost effort: %s", wire)
				}
			})
		}
	}
}

func TestAnthropicIndependentControlsAndNativeExtensions(t *testing.T) {
	for _, thinking := range []string{`{"type":"enabled","budget_tokens":4096}`, `{"type":"disabled"}`} {
		raw := `{"model":"claude-opus-4-5","max_tokens":8192,"thinking":` + thinking + `,"output_config":{"effort":"high","future_config":true},"messages":[{"role":"user","content":"hi"}]}`
		_, wire, issues, err := convertThinking(t, Anthropic, Anthropic, raw, "claude-opus-4-5", "strict", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 {
			t.Fatal(issues)
		}
		sameJSON(t, wire, raw)
	}
}

func TestThinkingSentinelsAndNulls(t *testing.T) {
	for _, value := range []string{"0", "-1", "null"} {
		from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Gemini)
		raw := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":` + value + `}}}`
		req, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{Values: p.Object{"model": p.StringValue("gemini-2.5-flash")}})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, to))
		req, err = c.Request(t.Context(), req, p.ConversionContext{Model: "gemini-2.5-flash"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeRequest(t.Context(), req, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		sameJSON(t, wire, raw)
	}
	for source, raw := range map[string]string{
		Chat:      `{"model":"alias","store":false,"reasoning_effort":null,"messages":[{"role":"user","content":"hi"}]}`,
		Responses: `{"model":"alias","store":false,"reasoning":null,"input":"hi"}`,
		Anthropic: `{"model":"alias","max_tokens":8192,"thinking":null,"messages":[{"role":"user","content":"hi"}]}`,
	} {
		_, wire, _, err := convertThinking(t, source, Gemini, raw, "gemini-3.5-flash-lite", "strict", nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), "thinkingConfig") {
			t.Fatal(string(wire))
		}
	}
}

func TestThinkingInvalidSourcesRejectGlobally(t *testing.T) {
	for _, raw := range []string{
		`{"thinkingLevel":"high","thinkingBudget":1024}`,
		`{"thinkingBudget":-2}`, `{"thinkingBudget":9223372036854775808}`, `{"thinkingBudget":"1024"}`, `{"includeThoughts":null}`, `{"thinkingLevel":false}`,
	} {
		from := shippedProjectionProtocol(t, Gemini)
		_, err := from.DecodeRequest(t.Context(), []byte(`{"contents":[],"generationConfig":{"thinkingConfig":`+raw+`}}`), p.EvaluationContext{})
		var failure *p.ConversionError
		if !errors.As(err, &failure) || failure.CandidateOnly {
			t.Fatalf("expected global source validation failure: %s %v", raw, err)
		}
	}
}

func TestThinkingStrictLossAndNativeCannotRestore(t *testing.T) {
	raw := `{"model":"alias","store":false,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`
	_, _, _, err := convertThinking(t, Chat, Gemini, raw, "gemini-2.5-flash", "strict", nil)
	var failure *p.ConversionError
	if !errors.As(err, &failure) || !failure.CandidateOnly {
		t.Fatalf("not candidate-local: %v", err)
	}
	raw = strings.Replace(raw, `"high"`, `"xhigh"`, 1)
	_, wire, issues, err := convertThinking(t, Chat, Chat, raw, "gpt-5.1", "compatible", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"reasoning_effort":"high"`) || len(issues) == 0 {
		t.Fatalf("native restored changed effort: %s %+v", wire, issues)
	}
	_, wire, issues, err = convertThinking(t, Chat, Gemini, raw, "unknown-model", "compatible", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "thinkingConfig") || len(issues) == 0 {
		t.Fatal(string(wire), issues)
	}
}

func TestThinkingDynamicBudgetConversion(t *testing.T) {
	raw := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`
	for _, mode := range []string{"compatible", "strict"} {
		for _, target := range []string{Chat, Responses, Gemini} {
			t.Run(mode+"/"+target, func(t *testing.T) {
				model := "gpt-5.4"
				if target == Gemini {
					model = "gemini-2.5-flash"
				}
				_, wire, issues, err := convertThinking(t, Gemini, target, raw, model, mode, nil)
				if target == Gemini {
					if err != nil || len(issues) != 0 {
						t.Fatal(err, issues)
					}
					sameJSON(t, wire, raw)
					return
				}
				if mode == "strict" {
					var failure *p.ConversionError
					if !errors.As(err, &failure) || !failure.CandidateOnly {
						t.Fatal("automatic budget approximation must reject this candidate", err)
					}
					issues = failure.Issues
				} else if err != nil || !strings.Contains(string(wire), `"medium"`) {
					t.Fatal(string(wire), err)
				}
				found := false
				for _, issue := range issues {
					found = found || issue.Path == "/reasoning/mode"
				}
				if !found {
					t.Fatal("missing automatic-budget loss diagnostic", issues)
				}
			})
		}
	}
}

func TestThinkingOutputLimitOriginAndBounds(t *testing.T) {
	caps := p.ResolveReasoningCapabilities(Anthropic, "claude-opus-4-5")
	caps.MaxOutputTokens = 16384
	for _, tc := range []struct {
		name       string
		limit      int
		hard       int64
		wantLimit  int
		wantBudget int
	}{
		{"explicit-fits", 8192, 16384, 8192, 6553}, {"system-default", 0, 16384, 8192, 6553},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := caps
			local.MaxOutputTokens = tc.hard
			raw := `{"model":"alias","store":false,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]`
			if tc.limit > 0 {
				raw += fmt.Sprintf(`,"max_completion_tokens":%d`, tc.limit)
			}
			raw += `}`
			req, wire, issues, err := convertThinking(t, Chat, Anthropic, raw, "claude-opus-4-5", "compatible", &local)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Max      int `json:"max_tokens"`
				Thinking struct {
					Budget int `json:"budget_tokens"`
				} `json:"thinking"`
			}
			json.Unmarshal(wire, &body)
			if body.Max != tc.wantLimit || body.Thinking.Budget != tc.wantBudget || len(issues) == 0 || req.OutputLimitDefault != (tc.limit == 0) {
				t.Fatalf("%s %+v", wire, issues)
			}
		})
	}
}

func TestThinkingInterleavedSourceAliasDefersModelConstraint(t *testing.T) {
	from := shippedProjectionProtocol(t, Anthropic)
	raw := []byte(`{"model":"group","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`)
	for _, interleaved := range []bool{false, true} {
		request, err := from.DecodeRequest(t.Context(), raw, p.EvaluationContext{InterleavedThinking: interleaved})
		if err != nil || request.Reasoning == nil || request.Reasoning.Interleaved != interleaved {
			t.Fatal("output limit constraints belong to the target candidate", request, err)
		}
	}
}

func TestThinkingSourceProtocolEnumsRejectBeforeConversion(t *testing.T) {
	for _, tc := range []struct{ codec, raw string }{
		{Anthropic, `{"model":"alias","max_tokens":8192,"output_config":{"effort":"none"},"messages":[{"role":"user","content":"hi"}]}`},
		{Responses, `{"model":"alias","reasoning":{"summary":"omitted"},"input":"hi"}`},
		{Gemini, `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"xhigh"}}}`},
	} {
		_, err := shippedProjectionProtocol(t, tc.codec).DecodeRequest(t.Context(), []byte(tc.raw), p.EvaluationContext{})
		var failure *p.ConversionError
		if !errors.As(err, &failure) || failure.CandidateOnly {
			t.Fatal(tc.codec, err)
		}
	}
}

func TestThinkingNativeReplayUsesSourceContext(t *testing.T) {
	for _, tc := range []struct {
		codec, sourceModel, targetModel, raw, want, absent string
		interleaved                                        bool
	}{
		{Chat, "gpt-5.4", "gpt-5", `{"model":"gpt-5.4","store":false,"reasoning_effort":"minimal","messages":[{"role":"user","content":"hi"}],"future_field":true}`, `"reasoning_effort":"minimal"`, `"reasoning_effort":"medium"`, false},
		{Gemini, "gemini-2.5-flash", "gemini-3.8-flash", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":4096}},"cachedContent":"cachedContents/test","future_field":true}`, `"thinkingLevel":"medium"`, "thinkingBudget", false},
		{Anthropic, "claude-opus-4-5", "claude-opus-4-5", `{"model":"claude-opus-4-5","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"future_field":true}`, `"budget_tokens":2047`, `"budget_tokens":4096`, true},
	} {
		t.Run(tc.codec, func(t *testing.T) {
			compiled := shippedProjectionProtocol(t, tc.codec)
			options := p.EvaluationContext{Values: p.Object{"model": p.StringValue(tc.sourceModel)}, InterleavedThinking: tc.interleaved}
			scope := p.Scope{Provider: "provider", Account: "account", Model: tc.targetModel}
			authorizations := 0
			options.ResolveRequestScope = func(*p.Request) (p.Scope, error) {
				authorizations++
				return scope, nil
			}
			request, err := compiled.DecodeRequest(t.Context(), []byte(tc.raw), options)
			if err != nil {
				t.Fatal(err)
			}
			// Reusing the caller's options and serializing the request must not change its replay context.
			options.Values["model"] = p.StringValue(tc.targetModel)
			encoded, _ := json.Marshal(request)
			if err := json.Unmarshal(encoded, &request); err != nil {
				t.Fatal(err)
			}
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(compiled, compiled))
			if err != nil {
				t.Fatal(err)
			}
			converted, err := conversion.Request(t.Context(), request, p.ConversionContext{Model: tc.targetModel}, nil)
			if err != nil {
				t.Fatal(err)
			}
			converted.Model = p.StringValue(tc.targetModel)
			wire, err := compiled.EncodeRequest(t.Context(), converted, p.EvaluationContext{Scope: scope, Values: p.Object{"model": p.StringValue(tc.targetModel)}})
			if err != nil {
				t.Fatal("native replay rejected a converted request", err)
			}
			if !strings.Contains(string(wire), tc.want) || strings.Contains(string(wire), tc.absent) || !strings.Contains(string(wire), `"future_field":true`) {
				t.Fatal("native replay lost extensions or restored converted controls", string(wire))
			}
			if authorizations != 1 || (tc.codec == Gemini && !strings.Contains(string(wire), `"cachedContent":"cachedContents/test"`)) {
				t.Fatal("native replay lost authorized cache provenance or repeated authorization", authorizations, string(wire))
			}
		})
	}
}
