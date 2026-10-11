package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestGeminiUsageDisclosuresAreMapped(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	for _, extra := range []string{
		`"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}]`,
		`"serviceTier":"standard"`,
		`"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}],"serviceTier":"standard"`,
	} {
		raw := `{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4,` + extra + `}}`
		frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range frame.Events {
			if e.Unmapped != nil {
				t.Errorf("recognized usage became opaque: %s", e.Unmapped.Value.Bytes())
			}
		}
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			response, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			projected, err := c.Response(t.Context(), response, p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(target, err)
			}
			wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatal(target, err)
			}
			if (target == Chat || target == Responses) && strings.Contains(extra, "promptTokensDetails") && !strings.Contains(string(wire), `"text_tokens":3`) {
				t.Fatal("input modality count lost", string(wire))
			}
			frames := auditConvertFrames(t, Gemini, target, []string{raw})
			if target == Gemini && strings.Contains(extra, "serviceTier") {
				found := false
				for _, frame := range frames {
					if strings.Contains(string(frame.Bytes()), `"serviceTier":"standard"`) {
						found = true
					}
				}
				if !found {
					t.Fatal("native stream service tier lost")
				}
			}
		}
	}
}

func TestGeminiModalityUsageProjectionModes(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	for _, bucket := range geminiModalityBuckets {
		for _, modality := range []string{"TEXT", "IMAGE", "AUDIO", "VIDEO", "DOCUMENT"} {
			for _, count := range []string{"0", "9007199254740993"} {
				value := `[{"modality":"` + modality + `","tokenCount":` + count + `}]`
				raw := `{"usageMetadata":{"` + bucket.field + `":` + value + `}}`
				response, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				before, _ := p.EncodeValue(response)
				for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
					to := shippedProjectionProtocol(t, target)
					for _, mode := range []string{"compatible", "strict"} {
						policy := p.DefaultConversionPolicy(to, from)
						policy.Mode = mode
						c, _ := p.ResolveConversion(policy)
						sink := &p.DiagnosticSink{}
						projected, err := c.Response(t.Context(), response, p.ConversionContext{}, sink)
						supported := target == Gemini || (target == Chat || target == Responses) &&
							(bucket.prefix == "input" || bucket.prefix == "output") && (modality == "TEXT" || modality == "IMAGE" || modality == "AUDIO")
						if mode == "strict" && !supported {
							if err == nil {
								t.Fatal("strict modality loss accepted", target, bucket.field, modality)
							}
							continue
						}
						if err != nil {
							t.Fatal(target, mode, bucket.field, modality, err)
						}
						wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						back, err := to.DecodeResponse(t.Context(), wire, p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						name := bucket.prefix + "." + strings.ToLower(modality) + "_tokens"
						if supported {
							if back.Usage == nil || back.Usage.Details[name] != response.Usage.Details[name] {
								t.Fatal("modality count changed", target, name, string(wire))
							}
						} else {
							found := false
							for _, issue := range sink.Issues() {
								if issue.Path == "/usage/details/"+name && issue.Code == p.ConversionDegraded && issue.Fidelity == "lossy_compatible" {
									found = true
								}
							}
							if !found || len(projected.Usage.Details) != 0 {
								t.Fatal("unmapped count omitted without diagnostic", name, sink.Issues())
							}
						}
					}
				}
				after, _ := p.EncodeValue(response)
				if !equivalentJSON(before, after) {
					t.Fatal("projection mutated original accounting")
				}
			}
		}
	}
}

func TestGeminiModalityUsageValidationAndExtensions(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, value := range []string{
		`null`, `{}`, `42`, `[null]`, `[{}]`, `[{"modality":1,"tokenCount":0}]`,
		`[{"modality":"TEXT"}]`, `[{"modality":"TEXT","tokenCount":null}]`,
		`[{"modality":"TEXT","tokenCount":-1}]`, `[{"modality":"TEXT","tokenCount":"1"}]`,
		`[{"modality":"TEXT","tokenCount":1.5}]`, `[{"modality":"TEXT","tokenCount":9223372036854775808}]`,
		`[{"modality":"TEXT","tokenCount":0},{"modality":"TEXT","tokenCount":1}]`,
		`[{"modality":"TEXT","tokenCount":-1,"vendor":true}]`,
	} {
		for _, bucket := range geminiModalityBuckets {
			raw := `{"usageMetadata":{"` + bucket.field + `":` + value + `}}`
			if _, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{}); err == nil {
				t.Fatal("invalid JSON usage accepted", raw)
			}
			if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
				t.Fatal("invalid stream usage accepted", raw)
			}
		}
	}
	for _, value := range []string{
		`[{"modality":"FUTURE","tokenCount":1}]`,
		`[{"modality":"TEXT","tokenCount":1,"vendor":true}]`,
		`[{"modality":"TEXT","tokenCount":1},{"modality":"FUTURE","tokenCount":2}]`,
	} {
		raw := `{"usageMetadata":{"promptTokensDetails":` + value + `}}`
		response, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		response.Native = nil
		wire, err := from.EncodeResponse(t.Context(), response, p.EvaluationContext{})
		if err != nil || !strings.Contains(string(wire), `"promptTokensDetails":`+value) {
			t.Fatal("native usage extension lost", string(wire), err)
		}
		projected, err := c.Response(t.Context(), response, p.ConversionContext{}, nil)
		if err == nil {
			_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("unknown modality crossed protocols", raw)
		}
		frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil || len(frame.Events) == 0 || frame.Events[0].Unmapped == nil {
			t.Fatal("unknown stream usage lost", err)
		}
	}
	for _, value := range []string{`false`, `1`, `{}`, `[]`} {
		raw := `{"usageMetadata":{"serviceTier":` + value + `}}`
		if _, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{}); err == nil {
			t.Fatal("invalid service tier accepted", raw)
		}
		if _, err := from.DecodeFrame(t.Context(), testValue(t, raw), p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
			t.Fatal("invalid stream service tier accepted", raw)
		}
	}
}

func TestGeminiModalityNativeEditsAndUsageTails(t *testing.T) {
	from := shippedProjectionProtocol(t, Gemini)
	raw := `{"usageMetadata":{"promptTokenCount":3,"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}],"serviceTier":"standard"}}`
	response, err := from.DecodeResponse(t.Context(), []byte(raw), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := from.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, raw)
	delete(response.Usage.Details, "input.text_tokens")
	response.Metadata = nil
	wire, err = from.EncodeResponse(t.Context(), response, p.EvaluationContext{})
	if err != nil || strings.Contains(string(wire), "promptTokensDetails") || strings.Contains(string(wire), "serviceTier") {
		t.Fatal("native snapshot restored deleted usage", string(wire), err)
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	var usage *p.Usage
	for _, raw := range []string{
		`{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`,
		`{"usageMetadata":{"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}]}}`,
		`{"usageMetadata":{"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}]}}`,
	} {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if event.Unmapped != nil {
				t.Fatal("usage tail became opaque")
			}
			usage = p.MergeUsage(usage, event.Usage)
		}
	}
	if usage.Input.Count != 3 || usage.Output.Count != 1 || usage.Total.Count != 4 || usage.Details["input.text_tokens"].Count != 3 {
		t.Fatal("modality totals were counted twice", usage)
	}
}

func TestModalityUsageCannotHideInvalidCounts(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Anthropic)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, raw := range []string{
		`{"promptTokenCount":3,"promptTokensDetails":[{"modality":"TEXT","tokenCount":4}]}`,
		`{"candidatesTokenCount":3,"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":2},{"modality":"IMAGE","tokenCount":2}]}`,
		`{"cachedContentTokenCount":0,"cacheTokensDetails":[{"modality":"AUDIO","tokenCount":1}]}`,
		`{"toolUsePromptTokenCount":1,"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":2}]}`,
		`{"promptTokensDetails":[{"modality":"TEXT","tokenCount":9223372036854775807},{"modality":"IMAGE","tokenCount":1}]}`,
	} {
		response, err := from.DecodeResponse(t.Context(), []byte(`{"usageMetadata":`+raw+`}`), p.EvaluationContext{})
		if err == nil {
			_, err = c.Response(t.Context(), response, p.ConversionContext{}, nil)
		}
		if err == nil {
			t.Fatal("invalid counts hidden by projection", raw)
		}
	}
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	if _, err := from.DecodeFrame(t.Context(), testValue(t, `{"usageMetadata":{"promptTokenCount":3}}`), options); err != nil {
		t.Fatal(err)
	}
	if _, err := from.DecodeFrame(t.Context(), testValue(t, `{"usageMetadata":{"promptTokensDetails":[{"modality":"TEXT","tokenCount":4}]}}`), options); err == nil {
		t.Fatal("late breakdown exceeded retained input")
	}
}

func TestGeminiServiceTierProjectionModes(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Gemini), shippedProjectionProtocol(t, Responses)
	for _, raw := range []string{`null`, `"standard"`} {
		for _, mode := range []string{"compatible", "strict"} {
			response, err := from.DecodeResponse(t.Context(), []byte(`{"usageMetadata":{"serviceTier":`+raw+`}}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			policy := p.DefaultConversionPolicy(to, from)
			policy.Mode = mode
			c, _ := p.ResolveConversion(policy)
			sink := &p.DiagnosticSink{}
			_, err = c.Response(t.Context(), response, p.ConversionContext{}, sink)
			if mode == "strict" && raw != "null" {
				if err == nil || !strings.Contains(err.Error(), "serviceTier") {
					t.Fatal("strict service tier loss accepted", err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			issues := sink.Issues()
			if len(issues) != 1 || issues[0].Path != "/usageMetadata/serviceTier" || raw != "null" && issues[0].Fidelity != "lossy_compatible" || raw == "null" && issues[0].Code != p.ConversionNormalized {
				t.Fatal("missing tier projection diagnostic", issues)
			}
		}
	}
}
