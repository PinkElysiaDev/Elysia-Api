package builtin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesStorageConversion(t *testing.T) {
	from, to := testCompiled(t, Responses), testCompiled(t, Gemini)
	for _, mode := range []string{"compatible", "strict"} {
		for _, value := range []string{"", "null", "false", "true", "42", `"false"`, `{}`, `[]`} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				policy := p.DefaultConversionPolicy(from, to)
				policy.Mode = mode
				c, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				raw := `{"model":"m","input":"hello"`
				if value != "" {
					raw += `,"store":` + value
				}
				raw += `}`
				req, err := from.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
				sink := &p.DiagnosticSink{}
				if err == nil {
					req, err = c.Request(t.Context(), req, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, sink)
				}
				valid := value == "" || value == "null" || value == "false" || value == "true"
				if !valid {
					if err == nil || !strings.Contains(err.Error(), "invalid_input at /store") {
						t.Fatal(err)
					}
					return
				}
				if mode == "strict" && value != "false" {
					if err == nil || !strings.Contains(err.Error(), "/clientOutput/responsesStorage") {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeRequest(t.Context(), req, p.EvaluationContext{})
				if err != nil || strings.Contains(string(wire), "store") {
					t.Fatal(string(wire), err)
				}
				if req.ClientOutput.ResponsesStorage.Effective || req.ClientOutput.ResponsesStorage.Requested != (value == "" || value == "true") {
					t.Fatal(req.ClientOutput)
				}
				if (len(sink.Issues()) > 0) != (value != "false") {
					t.Fatal(sink.Issues())
				}
			})
		}
	}
	for _, value := range []string{"null", "false", "true"} {
		req, err := from.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hello","store":`+value+`}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		c, _ := p.ResolveConversion(p.DefaultConversionPolicy(from, from))
		req, err = c.Request(t.Context(), req, fixtureConversionContext(t, c, p.ConversionRequest, req.Source), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Native = nil
		wire, err := from.EncodeRequest(t.Context(), req, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		obj, _ := testValue(t, string(wire)).ReadObject()
		sameJSON(t, obj["store"].Bytes(), value)
	}
	// Disable/reject overrides are effective; a decoder's boolean validation cannot be bypassed.
	for _, reject := range []bool{false, true} {
		policy := p.DefaultConversionPolicy(from, to)
		for i := range policy.Rules {
			if policy.Rules[i].ID == "responses-storage" {
				if reject {
					policy.Rules[i] = p.ConversionRule{ID: "responses-storage", Enabled: true, Phase: p.ConversionRequest, Action: "reject", Path: "/clientOutput/responsesStorage", Reason: "fixture rejects response retrieval storage"}
				} else {
					policy.Rules[i].Enabled = false
				}
			}
		}
		c, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := from.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hello","store":true}`), p.EvaluationContext{})
		req, err = c.Request(t.Context(), req, fixtureConversionContext(t, c, p.ConversionRequest, req.Source), nil)
		if err == nil {
			_, err = to.EncodeRequest(t.Context(), req, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("storage override ignored")
		}
	}
}

func TestPackageStrictShapeRequiresPositiveEvidence(t *testing.T) {
	ingress, upstream := compileBuiltinPackage(t, "openai-chat"), compileBuiltinPackage(t, "openai-responses")
	policy := p.DefaultConversionPolicy(ingress, upstream)
	policy.Mode = "strict"
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	report := p.VerifyBindingCombination(t.Context(), ingress, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, conversion)
	hasRejection := false
	for _, check := range report.Checks {
		if check.Rejected {
			hasRejection = true
			if check.Passed || len(check.Capabilities) != 0 {
				t.Fatal("declared refusal supplied positive evidence", check)
			}
		}
	}
	if !hasRejection || report.Passed {
		t.Fatal("strict message-identity refusal did not require independent positive coverage", report)
	}
}

func TestEnvelopeDefaultsUseInstalledMappings(t *testing.T) {
	ingress, upstream := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	document, _ := upstream.Package()
	document.Spec.ID, document.Spec.Family, document.Spec.WireVersion = "mixed-copy", ingress.Identity().Family, ingress.Identity().WireVersion
	mixed, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, isEnabled := range []bool{true, false} {
		policy := declaredRulePolicy(t, ingress, mixed, "response-anthropic-usage-envelope", "compatible")
		policy.Rules[0].Enabled = isEnabled
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		response := usageProjectionResponse(nil)
		response.Source = mixed.Identity()
		result, err := conversion.Response(t.Context(), response, p.ConversionContext{Source: mixed.Identity(), Target: ingress.Identity()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := ingress.EncodeResponse(t.Context(), result, fixtureOptions(p.EvaluationContext{}))
		if err == nil {
			err = ingress.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire)))
		}
		if (err == nil) != isEnabled {
			t.Fatal("family label or encoder bypassed the declared envelope", isEnabled, err)
		}
		if isEnabled && (result.Usage.Input.Origin != p.PlaceholderCount || result.Usage.Output.Origin != p.PlaceholderCount) {
			t.Fatal("placeholder lost its declared origin", result.Usage)
		}
	}
}

func TestStrictEnvelopeEvidenceRequiresPositiveFixtures(t *testing.T) {
	ingress, upstream := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	policy := p.DefaultConversionPolicy(ingress, upstream)
	policy.Mode = "strict"
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}
	report := p.VerifyBindingCombination(t.Context(), ingress, upstream, contract, conversion)
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	hasRejection := false
	for _, check := range report.Checks {
		if check.SampleID == "declared-stream-text-decode" {
			hasRejection = check.Rejected && !check.Passed && len(check.Capabilities) == 0
		}
	}
	if !hasRejection {
		t.Fatal("conditional rejection counted as positive evidence", report.Checks)
	}
	document, _ := upstream.Package()
	remaining := document.Mapping.Samples[:0]
	removed := 0
	for _, sample := range document.Mapping.Samples {
		if sample.ID == "declared-stream-metered-text-decode" {
			removed++
			continue
		}
		remaining = append(remaining, sample)
	}
	if removed != 1 {
		t.Fatal("positive stream fixture missing")
	}
	document.Mapping.Samples = remaining
	withoutPositive, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	conversion, err = p.ResolveConversion(p.DefaultConversionPolicy(ingress, withoutPositive), p.ConversionPolicy{SchemaVersion: p.ConversionPolicySchemaVersion, ID: "strict-proof", Mode: "strict"})
	if err != nil {
		t.Fatal(err)
	}
	if report := p.VerifyBindingCombination(t.Context(), ingress, withoutPositive, contract, conversion); report.Passed {
		t.Fatal("negative evidence replaced positive stream coverage")
	}
}

func TestAnthropicUsageEnvelopeProjection(t *testing.T) {
	from, to := testCompiled(t, Gemini), testCompiled(t, Anthropic)
	for _, mode := range []string{"compatible", "strict"} {
		for _, raw := range []string{`null`, `{}`, `{"input":{"count":0,"origin":"observed"}}`, `{"input":{"count":3,"origin":"observed"},"output":{"count":5,"origin":"observed"}}`} {
			t.Run(mode+raw, func(t *testing.T) {
				var usage *p.Usage
				if err := testValue(t, raw).Decode(&usage); err != nil {
					t.Fatal(err)
				}
				original, _ := p.EncodeValue(usage)
				policy := p.DefaultConversionPolicy(to, from)
				policy.Mode = mode
				c, _ := p.ResolveConversion(policy)
				sink := &p.DiagnosticSink{}
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}, Usage: usage, Attributes: p.Object{"finishReason": p.StringValue("stop")}}
				route := fixtureConversionContext(t, c, p.ConversionResponse, from.Identity())
				result, err := c.Response(t.Context(), response, route, sink)
				complete := usage != nil && usage.Input != nil && usage.Output != nil
				if mode == "strict" && !complete {
					if err == nil {
						t.Fatal("missing usage accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeResponse(t.Context(), result, fixtureOptions(p.EvaluationContext{}))
				if err != nil {
					t.Fatal(err)
				}
				if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
					t.Fatal(err)
				}
				after, _ := p.EncodeValue(usage)
				sameJSON(t, after.Bytes(), string(original.Bytes()))
				if !complete && len(sink.Issues()) == 0 {
					t.Fatal("placeholder not diagnosed")
				}
				if usage == nil && result.Usage.Input.Origin != p.PlaceholderCount {
					t.Fatal("placeholder mislabeled", result.Usage)
				}
			})
		}
	}
}

func TestAnthropicWireRequiredFields(t *testing.T) {
	adapter := testCompiled(t, Anthropic)
	for _, raw := range []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[]}}`,
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
	} {
		if err := adapter.ValidateWireOutput(p.EncodeEvent, testValue(t, raw)); err == nil {
			t.Fatal("accepted invalid client frame", raw)
		}
	}
}

func TestFinalWirePolicyCannotEraseAnthropicUsage(t *testing.T) {
	ingress, upstream := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	policy := p.DefaultConversionPolicy(ingress, upstream)
	policy.Rules = append(policy.Rules, p.ConversionRule{ID: "bad-final-wire", Order: 1000, Enabled: true, Phase: p.ConversionWire, Action: "remove", Path: "/usage"})
	c, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	report := p.VerifyBindingCombination(t.Context(), ingress, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, c)
	if report.Passed {
		t.Fatal("wire rule erased mandatory usage without failing verification")
	}
	for _, issue := range report.Issues {
		if issue.Stage == "wire.output" && issue.Path == "/usage" {
			return
		}
	}
	t.Fatal(report.Issues)
}

func TestProviderCannotMintPlaceholderAccounting(t *testing.T) {
	upstream := testCompiled(t, Gemini)
	response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Usage: &p.Usage{Input: &p.Counter{Origin: p.PlaceholderCount}}}
	issues := p.CheckModelResponse(response, upstream, p.Binding{Capabilities: packageMapping(upstream).Capabilities}, p.Scope{})
	if err := p.IssuesError(issues); err == nil || !strings.Contains(err.Error(), "upstream_contract_violation at /usage/input") {
		t.Fatal(issues)
	}
}

func TestStoreDecisionTable(t *testing.T) {
	for _, source := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
			for _, raw := range []string{"", "null", "false", "true"} {
				if (source == Anthropic || source == Gemini) && raw != "" {
					continue
				}
				t.Run(source+"/"+target+"/"+raw, func(t *testing.T) {
					from, to := shippedProjectionProtocol(t, source), shippedProjectionProtocol(t, target)
					input := map[string]any{"model": "alias", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
					if source == Responses {
						input = map[string]any{"model": "alias", "input": "hi"}
					}
					if source == Anthropic {
						input["max_tokens"] = 8192
					}
					if source == Gemini {
						input = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}}
					}
					if raw != "" {
						input["store"] = json.RawMessage(raw)
					}
					body, _ := sonic.ConfigStd.Marshal(input)
					request, err := from.DecodeRequest(t.Context(), body, p.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					setRequestParameter(request, "max_output_tokens", testValue(t, "8192"))
					unknown := (source == Chat || source == Responses) && raw == "null"
					on := raw == "true" || source == Responses && raw == ""
					lossy := source != target && (unknown || on)
					for _, mode := range []string{"compatible", "strict"} {
						policy := p.DefaultConversionPolicy(from, to)
						policy.Mode = mode
						c, _ := p.ResolveConversion(policy)
						sink := &p.DiagnosticSink{}
						converted, err := c.Request(t.Context(), request, fixtureConversionContext(t, c, p.ConversionRequest, request.Source), sink)
						if mode == "strict" && lossy {
							var failure *p.ConversionError
							if !errors.As(err, &failure) || !failure.CandidateOnly {
								t.Fatalf("wanted candidate rejection: %v", err)
							}
							continue
						}
						if err != nil {
							t.Fatal(err)
						}
						wire, err := to.EncodeRequest(t.Context(), converted, p.EvaluationContext{})
						if err != nil {
							t.Fatal(err)
						}
						var out map[string]json.RawMessage
						sonic.ConfigStd.Unmarshal(wire, &out)
						want := ""
						if source == target {
							want = raw
						} else if target == Chat || target == Responses {
							want = "false"
							if on {
								want = "true"
							}
						}
						if string(out["store"]) != want {
							t.Fatalf("%s want store=%s", wire, want)
						}
						intent := converted.ClientOutput.ResponsesStorage
						if intent.RequestedUnknown != unknown || intent.EffectiveUnknown != (unknown && source == target) {
							t.Fatal(intent)
						}
						warnings := false
						for _, issue := range sink.Issues() {
							expectedPath := "/clientOutput/responsesStorage"
							if target == Chat || target == Responses {
								expectedPath = "/store"
							}
							warnings = warnings || issue.RuleID == "responses-storage" && issue.Path == expectedPath
						}
						if warnings != lossy {
							t.Fatal(sink.Issues())
						}
					}
				})
			}
		}
	}
}
