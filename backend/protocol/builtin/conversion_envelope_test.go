package builtin

import (
	"strings"
	"testing"

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
					if err == nil || !strings.Contains(err.Error(), "/store") {
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
				if req.ClientOutput.ResponsesStorage.Effective || req.ClientOutput.ResponsesStorage.Requested != (value != "false") {
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
		req, err = c.Request(t.Context(), req, p.ConversionContext{}, nil)
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
			if policy.Rules[i].Action == "responses_storage" {
				if reject {
					policy.Rules[i].Value = testValue(t, `{"targetCodec":"gemini","onUnsupported":"reject"}`)
				} else {
					policy.Rules[i].Enabled = false
				}
			}
		}
		c, _ := p.ResolveConversion(policy)
		req, _ := from.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hello","store":true}`), p.EvaluationContext{})
		req, err := c.Request(t.Context(), req, p.ConversionContext{}, nil)
		if err == nil {
			_, err = to.EncodeRequest(t.Context(), req, p.EvaluationContext{})
		}
		if err == nil {
			t.Fatal("storage override ignored")
		}
	}
}

func TestEnvelopeDefaultsUseInstalledMappings(t *testing.T) {
	ingress, upstream := testCompiled(t, Anthropic), testCompiled(t, Gemini)
	d := upstream.Definition()
	d.ID, d.Family, d.WireVersion = "old-mixed-copy", ingress.Identity().Family, ingress.Identity().WireVersion
	d.Requires = nil
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	raw, _ := p.EncodeValue(d)
	mixed, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(ingress, mixed))
	route := p.ConversionContext{Source: mixed.Identity(), Target: ingress.Identity()}
	if !c.HasAnthropicEnvelope(p.ConversionEvent, route) {
		t.Fatal("family label suppressed installed codec projection")
	}
	native, _ := p.ResolveConversion(p.DefaultConversionPolicy(ingress, ingress))
	if native.HasAnthropicEnvelope(p.ConversionEvent, route) {
		t.Fatal("native replay rewritten")
	}
	policy := p.DefaultConversionPolicy(ingress, mixed)
	for i := range policy.Rules {
		if policy.Rules[i].Action == "anthropic_usage_envelope" {
			policy.Rules[i].Enabled = false
		}
	}
	c, _ = p.ResolveConversion(policy)
	out, err := c.Response(t.Context(), &p.Response{SchemaVersion: 1, ID: p.StringValue("r"), Model: p.StringValue("m"), Content: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}}, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ingress.EncodeResponse(t.Context(), out, p.EvaluationContext{})
	if err == nil {
		err = ingress.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire)))
	}
	if err == nil {
		t.Fatal("disabled envelope was silently repaired")
	}
}

func TestStrictEnvelopeEvidenceRequiresPositiveFixtures(t *testing.T) {
	ingress, upstream := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	policy := p.DefaultConversionPolicy(ingress, upstream)
	policy.Mode = "strict"
	c, _ := p.ResolveConversion(policy)
	contract := p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}
	report := p.VerifyBindingCombination(t.Context(), ingress, upstream, contract, c)
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	negative := false
	for _, check := range report.Checks {
		if check.SampleID == "stream-text-decode" {
			negative = check.Rejected && !check.Passed && len(check.Capabilities) == 0
		}
	}
	if !negative {
		t.Fatal("conditional rejection counted as positive evidence", report.Checks)
	}
	d := upstream.Definition()
	for i, sample := range d.Samples {
		if sample.ID == "stream-text-initial-usage-decode" {
			d.Samples = append(d.Samples[:i], d.Samples[i+1:]...)
			break
		}
	}
	raw, _ := p.EncodeValue(d)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	upstream, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if report = p.VerifyBindingCombination(t.Context(), ingress, upstream, contract, c); report.Passed {
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
				response := &p.Response{SchemaVersion: 1, Content: []p.Node{{Kind: p.TextNode, Payload: p.StringValue("hello")}}, Usage: usage, Attributes: p.Object{"finishReason": p.StringValue("stop")}}
				result, err := c.Response(t.Context(), response, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: "m"}, sink)
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
				wire, err := to.EncodeResponse(t.Context(), result, p.EvaluationContext{})
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
	response := &p.Response{SchemaVersion: 1, Usage: &p.Usage{Input: &p.Counter{Origin: p.PlaceholderCount}}}
	issues := p.CheckModelResponse(response, upstream, p.Binding{Capabilities: upstream.Definition().Capabilities}, p.Scope{})
	if err := p.IssuesError(issues); err == nil || !strings.Contains(err.Error(), "upstream_contract_violation at /usage/input") {
		t.Fatal(issues)
	}
}
