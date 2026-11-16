package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesIncludeProjection(t *testing.T) {
	t.Parallel()
	responses, gemini := testCompiled(t, Responses), testCompiled(t, Gemini)
	for _, tc := range []struct{ name, field, errorPath string }{
		{"absent", "", ""}, {"null", `,"include":null`, ""}, {"empty", `,"include":[]`, ""},
		{"encrypted", `,"include":["reasoning.encrypted_content"]`, ""},
		{"unknown", `,"include":["future"]`, "/parameters"}, {"mixed", `,"include":["reasoning.encrypted_content","future"]`, "/parameters"},
		{"number", `,"include":42`, "/include"}, {"element", `,"include":[null]`, "/include/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := responses.DecodeRequest(t.Context(), []byte(`{"model":"m","store":false,"input":"hi"`+tc.field+`}`), fixtureOptions(p.EvaluationContext{}))
			original, _ := p.EncodeValue(request)
			var converted *p.Request
			sink := &p.DiagnosticSink{}
			if err == nil {
				conversion, e := p.ResolveConversion(p.DefaultConversionPolicy(responses, gemini))
				if e != nil {
					t.Fatal(e)
				}
				converted, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: responses.Identity(), Target: gemini.Identity()}, sink)
			}
			var wire []byte
			if err == nil {
				wire, err = gemini.EncodeRequest(t.Context(), converted, fixtureOptions(p.EvaluationContext{}))
			}
			if tc.errorPath != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorPath) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err != nil || strings.Contains(string(wire), "include") {
				t.Fatal(string(wire), err)
			}
			if tc.name == "encrypted" && !converted.ClientOutput.RequireContinuationState {
				t.Fatal("client selection lost")
			}
			current, _ := p.EncodeValue(request)
			if original != current {
				t.Fatal("original request mutated")
			}
			found := false
			for _, issue := range sink.Issues() {
				if issue.Code == p.ConversionNormalized && issue.RuleID == "reasoning-controls" {
					if issue.Severity != p.SeverityInfo || issue.Fidelity != "preserved" || issue.PolicyHash == "" || issue.PolicyRevision == "" || issue.Path != "/parameters/wire:openai_responses/include" {
						t.Fatal(issue)
					}
					found = true
				}
			}
			if found != (tc.field != "" && tc.name != "encrypted") {
				t.Fatal("normalization diagnostic", tc.name, sink.Issues())
			}
		})
	}
	for _, value := range []string{`null`, `[]`, `["future","reasoning.encrypted_content"]`} {
		raw := `{"model":"m","input":"hi","include":` + value + `}`
		req, err := responses.DecodeRequest(t.Context(), []byte(raw), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(responses, responses))
		sink := &p.DiagnosticSink{}
		req, err = conversion.Request(t.Context(), req, fixtureConversionContext(t, conversion, p.ConversionRequest, req.Source), sink)
		if err != nil {
			t.Fatal(err)
		}
		if len(sink.Issues()) != 0 {
			t.Fatal("unchanged native include diagnosed", sink.Issues())
		}
		req.Native = nil
		wire, err := responses.EncodeRequest(t.Context(), req, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = sonic.ConfigStd.Unmarshal(wire, &fields)
		sameJSON(t, fields["include"], value)
	}
}

func shippedProjectionProtocol(t *testing.T, name string) *p.Compiled {
	t.Helper()
	compiled, err := shippedTestProtocols()
	if err != nil {
		t.Fatal(err)
	}
	if protocol := compiled[name]; protocol != nil {
		return protocol
	}
	t.Fatal("missing shipped module", name)
	return nil
}

func TestCustomModuleProjectionAndUnknownBoundaries(t *testing.T) {
	document, _ := testCompiled(t, Responses).Package()
	document.Spec.ID = "custom-projection-source"
	source, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	target := testCompiled(t, Gemini)
	conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(source, target))
	if err != nil {
		t.Fatal(err)
	}
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi","include":["reasoning.encrypted_content"]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	projected, err := conversion.Request(t.Context(), request, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.EncodeRequest(t.Context(), projected, fixtureOptions(p.EvaluationContext{})); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"responses_text", "parallel_tool_calls"} {
		candidate := request.Clone()
		setRequestParameter(candidate, name, p.StringValue("must-not-disappear"))
		before, _ := p.EncodeValue(candidate)
		output, err := conversion.Request(t.Context(), candidate, route, nil)
		if err == nil {
			if output.Parameters[name].IsZero() {
				t.Fatal("unsupported parameter disappeared before target validation", name)
			}
			_, err = target.EncodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("invalid parameter crossed the conversion boundary", name)
		}
		after, _ := p.EncodeValue(candidate)
		if before != after {
			t.Fatal("rejected conversion mutated source parameters")
		}
	}
}

func TestProjectionEvidenceAndDisabledRules(t *testing.T) {
	upstream := testCompiled(t, Gemini)
	document, _ := upstream.Package()
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{
		ID: "reasoning-usage-regression", Direction: p.DecodeResponse, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")},
		Input:    testValue(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":8,"toolUsePromptTokenCount":1}}`),
		Expected: testValue(t, `{"schemaVersion":2,"status":"completed","content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"hello"}]}],"attributes":{"finishReason":"stop"},"usage":{"input":{"count":3,"origin":"observed"},"output":{"count":5,"origin":"observed"},"total":{"count":8,"origin":"observed"},"details":{"output.reasoning_tokens":{"count":3,"origin":"observed"},"toolUsePromptTokenCount":{"count":1,"origin":"observed"}}}}`),
	})
	upstream, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), upstream); !report.Passed {
		t.Fatal("independent usage expectation failed", report.Issues)
	}
	ingress := testCompiled(t, Anthropic)
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		policy := p.DefaultConversionPolicy(ingress, upstream)
		if mode == "strict" {
			policy.Mode = mode
		}
		for index := range policy.Rules {
			if mode == "disabled" && (policy.Rules[index].ID == "response-usage-projection" || policy.Rules[index].ID == "event-usage-projection") {
				policy.Rules[index].Enabled = false
			}
		}
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		conversion = conversion.WithVerificationContext(p.ConversionContext{Model: "m"})
		report := p.VerifyBindingCombination(t.Context(), ingress, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, conversion)
		if mode == "compatible" && (!report.Passed || report.Fidelity != "lossy_compatible") || mode == "disabled" && report.Passed {
			t.Fatal("usage projection lost its evidence or policy boundary", mode, report.Issues)
		}
		hasUsage := false
		for _, check := range report.Checks {
			if check.SampleID != "reasoning-usage-regression" {
				continue
			}
			hasUsage = true
			if check.Skipped || check.Passed != (mode == "compatible") || mode == "strict" && (!check.Rejected || len(check.Capabilities) != 0) {
				t.Fatal("rejected usage sample counted as positive evidence", mode, check)
			}
		}
		if !hasUsage {
			t.Fatal("original usage regression was not executed", mode)
		}
	}
	checkIncludeCombinationEvidence(t)
}

func checkIncludeCombinationEvidence(t *testing.T) {
	t.Helper()
	source := testCompiled(t, Responses)
	document, _ := source.Package()
	document.Mapping.Samples = append(document.Mapping.Samples, p.Sample{
		ID: "include-regression", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"},
		Input:    testValue(t, `{"model":"m","input":"hello","include":["reasoning.encrypted_content"]}`),
		Expected: testValue(t, `{"schemaVersion":2,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"clientOutput":{"requireContinuationState":true,"responsesStorage":{"sourceCodec":"responses","requested":true,"effective":false,"effectiveUnknown":true}}}`),
	})
	source, issues := compilePackageDocument(t, document)
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report := p.Verify(t.Context(), source); !report.Passed {
		t.Fatal("independent include expectation failed", report.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), source, testCompiled(t, Gemini), p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	hasInclude := false
	for _, check := range report.Checks {
		if check.SampleID == "include-regression" && check.Passed && !check.Skipped {
			hasInclude = true
		}
	}
	if !hasInclude {
		t.Fatal("include normalization did not enter request roundtrip evidence")
	}
}

func usageProjectionPolicy(t *testing.T, source, target *p.Compiled, mode string) *p.CompiledConversion {
	t.Helper()
	policy := p.DefaultConversionPolicy(target, source)
	policy.Mode = mode
	rules := policy.Rules[:0]
	for _, rule := range policy.Rules {
		if rule.ID == "response-usage-projection" || rule.ID == "event-usage-projection" {
			rules = append(rules, rule)
		}
	}
	policy.Rules = rules
	if len(rules) != 2 {
		t.Fatal("missing declared response/event usage projections")
	}
	conversion, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	return conversion
}

func usageProjectionResponse(usage *p.Usage) *p.Response {
	return &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"), Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}}, Attributes: p.Object{"finishReason": p.StringValue("stop")}, Usage: usage}
}

func TestUsageProjectionIsolationAndModes(t *testing.T) {
	source := testCompiled(t, Gemini)
	for _, targetName := range []string{Chat, Responses, Anthropic, Gemini} {
		target := testCompiled(t, targetName)
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(targetName+"/"+mode, func(t *testing.T) {
				conversion := usageProjectionPolicy(t, source, target, mode)
				route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
				usage := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, Output: &p.Counter{Count: 5, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{
					"output.reasoning_tokens": {Count: 0, Origin: p.ObservedCount}, "toolUsePromptTokenCount": {Count: 3, Origin: p.ObservedCount}, "ephemeral_1h_input_tokens": {Count: 2, Origin: p.ObservedCount}, "uncached_input_tokens": {Count: 8, Origin: p.ObservedCount},
				}}
				original, _ := p.EncodeValue(usage)
				sink := &p.DiagnosticSink{}
				response, err := conversion.Response(t.Context(), usageProjectionResponse(usage), route, sink)
				if mode == "strict" {
					if err == nil {
						t.Fatal("strict loss passed")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if response.Usage.Output.Count != 5 {
					t.Fatal("reasoning counted twice")
				}
				if _, err := target.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{})); err != nil {
					t.Fatal(err)
				}
				if len(sink.Issues()) == 0 || sink.Issues()[0].PolicyHash != conversion.Hash || sink.Issues()[0].RuleID != "response-usage-projection" {
					t.Fatal(sink.Issues())
				}
				event, err := conversion.Event(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.UsageUpdated, Usage: usage, Response: usageProjectionResponse(usage)}, route, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, projected := range []*p.Usage{event.Usage, event.Response.Usage} {
					if _, err := target.EncodeResponse(t.Context(), usageProjectionResponse(projected), fixtureOptions(p.EvaluationContext{})); err != nil {
						t.Fatal(err)
					}
				}
				after, _ := p.EncodeValue(usage)
				if original != after {
					t.Fatal("accounting mutated")
				}
			})
		}
	}
}

func TestUsageProjectionNeverHidesInvalidOrUnknownDetails(t *testing.T) {
	source, target := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	conversion := usageProjectionPolicy(t, source, target, "compatible")
	route := p.ConversionContext{Source: source.Identity(), Target: target.Identity()}
	usage := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{"uncached_input_tokens": {Count: 9, Origin: p.ObservedCount}}}
	if _, err := conversion.Response(t.Context(), usageProjectionResponse(usage), route, nil); err == nil {
		t.Fatal("projection concealed invalid subtotal")
	}
	usage.Details = map[string]p.Counter{"vendor.unknown": {Count: 0, Origin: p.ObservedCount}}
	output, err := conversion.Response(t.Context(), usageProjectionResponse(usage), route, nil)
	if err != nil {
		t.Fatal(err)
	}
	if detail, exists := output.Usage.Details["vendor.unknown"]; !exists || detail.Count != 0 || detail.Origin != p.ObservedCount {
		t.Fatal("unknown detail changed before encoding", output.Usage)
	}
	if _, err := target.EncodeResponse(t.Context(), output, fixtureOptions(p.EvaluationContext{})); err == nil || !strings.Contains(err.Error(), "/usage/details") {
		t.Fatal("unknown detail disappeared", err)
	}
	usage.Details = map[string]p.Counter{"ephemeral_5m_input_tokens": {Count: -1, Origin: p.ObservedCount}}
	if _, err := conversion.Response(t.Context(), usageProjectionResponse(usage), route, nil); err == nil {
		t.Fatal("projection hid a negative counter")
	}
}
