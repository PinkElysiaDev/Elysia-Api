package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesIncludeProjection(t *testing.T) {
	responses, gemini := testCompiled(t, Responses), testCompiled(t, Gemini)
	for _, tc := range []struct{ name, field, errorPath string }{
		{"absent", "", ""}, {"null", `,"include":null`, ""}, {"empty", `,"include":[]`, ""},
		{"encrypted", `,"include":["reasoning.encrypted_content"]`, ""},
		{"unknown", `,"include":["future"]`, "/include/0"}, {"mixed", `,"include":["reasoning.encrypted_content","future"]`, "/include/1"},
		{"number", `,"include":42`, "/include"}, {"element", `,"include":[null]`, "/include/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := responses.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi"`+tc.field+`}`), p.EvaluationContext{})
			var converted *p.Request
			if err == nil {
				conversion, e := p.ResolveConversion(p.DefaultConversionPolicy(responses, gemini))
				if e != nil {
					t.Fatal(e)
				}
				converted, err = conversion.Request(t.Context(), request, p.ConversionContext{Source: responses.Identity(), Target: gemini.Identity()}, nil)
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
			wire, err := gemini.EncodeRequest(t.Context(), converted, p.EvaluationContext{})
			if err != nil || strings.Contains(string(wire), "include") {
				t.Fatal(string(wire), err)
			}
			if tc.name == "encrypted" && !converted.ClientOutput.RequestsEncryptedReasoning() {
				t.Fatal("client selection lost")
			}
			if tc.field != "" && request.Parameters["responses_include"].IsZero() {
				t.Fatal("original request mutated")
			}
		})
	}
	for _, value := range []string{`null`, `[]`, `["future","reasoning.encrypted_content"]`} {
		raw := `{"model":"m","input":"hi","include":` + value + `}`
		req, err := responses.DecodeRequest(t.Context(), []byte(raw), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(responses, responses))
		req, err = conversion.Request(t.Context(), req, p.ConversionContext{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Native = nil
		wire, err := responses.EncodeRequest(t.Context(), req, p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(wire, &fields)
		sameJSON(t, fields["include"], value)
	}
}

func TestUsageProjectionIsolationAndModes(t *testing.T) {
	for _, target := range []string{Chat, Responses, Anthropic, Gemini} {
		for _, strict := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/compatible", true: "/strict"}[strict], func(t *testing.T) {
				policy := p.DefaultConversionPolicy(testCompiled(t, target), testCompiled(t, Gemini))
				if strict {
					policy.Mode = "strict"
				}
				c, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				usage := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, Output: &p.Counter{Count: 5, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{
					"output.reasoning_tokens": {Count: 0, Origin: p.ObservedCount}, "toolUsePromptTokenCount": {Count: 3, Origin: p.ObservedCount}, "ephemeral_1h_input_tokens": {Count: 2, Origin: p.ObservedCount}, "uncached_input_tokens": {Count: 8, Origin: p.ObservedCount},
				}}
				original, _ := p.EncodeValue(usage)
				sink := &p.DiagnosticSink{}
				response, err := c.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, sink)
				if strict {
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
				if _, err := (module{name: target}).encodeUsage(response.Usage, p.EvaluationContext{}); err != nil {
					t.Fatal(err)
				}
				after, _ := p.EncodeValue(usage)
				if string(original.Bytes()) != string(after.Bytes()) {
					t.Fatal("accounting mutated")
				}
				if len(sink.Issues()) == 0 || sink.Issues()[0].PolicyHash == "" || sink.Issues()[0].RuleID == "" {
					t.Fatal(sink.Issues())
				}
				event, err := c.Event(t.Context(), p.Event{SchemaVersion: 1, Usage: usage, Response: &p.Response{SchemaVersion: 1, Usage: usage}}, p.ConversionContext{}, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, u := range []*p.Usage{event.Usage, event.Response.Usage} {
					if _, err := (module{name: target}).encodeUsage(u, p.EvaluationContext{}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestCustomModuleProjectionAndUnknownBoundaries(t *testing.T) {
	base := testCompiled(t, Responses).Definition()
	base.ID = "old-responses-copy"
	base.Requires = nil
	m := base.Directions[p.DecodeRequest]
	m.After = &p.Expression{Op: "read", Path: ""}
	base.Directions[p.DecodeRequest] = m
	value, _ := p.EncodeValue(base)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	custom, issues := compiler.Compile(value.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	gemini := testCompiled(t, Gemini)
	policy := p.DefaultConversionPolicy(custom, gemini)
	c, err := p.ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	req, err := custom.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi","include":["reasoning.encrypted_content"]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Request(t.Context(), req, p.ConversionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gemini.EncodeRequest(t.Context(), out, p.EvaluationContext{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"responses_text", "responses_truncation", "store", "parallel_tool_calls"} {
		r := req.Clone()
		r.Parameters[name] = p.StringValue("must-not-disappear")
		o, e := c.Request(t.Context(), r, p.ConversionContext{}, nil)
		if e != nil {
			t.Fatal(e)
		}
		if o.Parameters[name].IsZero() {
			t.Fatal(name, "lost")
		}
		if _, e = gemini.EncodeRequest(t.Context(), o, p.EvaluationContext{}); e == nil {
			t.Fatal(name, "unexpectedly encoded")
		}
	}
	base.Directions[p.EncodeRequest] = p.Mapping{Transform: &p.Expression{Op: "read", Path: ""}}
	value, _ = p.EncodeValue(base)
	handwritten, issues := compiler.Compile(value.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, r := range p.DefaultConversionPolicy(custom, handwritten).Rules {
		if r.Phase == p.ConversionRequest {
			t.Fatal("guessed handwritten codec", r)
		}
	}
}

func TestProjectionEvidenceAndDisabledRules(t *testing.T) {
	ingress, upstream := shippedProjectionProtocol(t, Anthropic), shippedProjectionProtocol(t, Gemini)
	d := upstream.Definition()
	d.Samples = append(d.Samples, p.Sample{ID: "reasoning-usage-regression", Direction: p.DecodeResponse, Expected: testValue(t, `{"schemaVersion":1,"source":{},"status":"completed","content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"hello"}]}],"attributes":{"finishReason":"stop"},"usage":{"input":{"count":3,"origin":"observed"},"output":{"count":5,"origin":"observed"},"total":{"count":8,"origin":"observed"},"details":{"output.reasoning_tokens":{"count":3,"origin":"observed"}}}}`), Input: testValue(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":8}}`)})
	raw, _ := p.EncodeValue(d)
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	upstream, issues := compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if r := p.Verify(t.Context(), upstream); !r.Passed {
		t.Fatal("invalid regression oracle", r.Issues)
	}
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		policy := p.DefaultConversionPolicy(ingress, upstream)
		if mode == "strict" {
			policy.Mode = "strict"
		}
		if mode == "disabled" {
			for i := range policy.Rules {
				if policy.Rules[i].Action == "usage_projection" {
					policy.Rules[i].Enabled = false
				}
			}
		}
		c, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		report := p.VerifyBindingCombination(t.Context(), ingress, upstream, p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true}, c)
		if report.Passed != (mode == "compatible") {
			t.Fatal(mode, report.Issues)
		}
		if mode == "compatible" && report.Fidelity != "lossy_compatible" {
			t.Fatal(report.Fidelity)
		}
	}
	// Include normalization participates in request roundtrip evidence.
	ingress = shippedProjectionProtocol(t, Responses)
	d = ingress.Definition()
	d.Samples = append(d.Samples, p.Sample{ID: "include-regression", Direction: p.DecodeRequest, Scope: p.Scope{Model: "m"}, Context: p.Object{"model": p.StringValue("m")}, Expected: testValue(t, `{"schemaVersion":1,"source":{},"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"parameters":{"responses_include":["reasoning.encrypted_content"]}}`), Input: testValue(t, `{"model":"m","input":"hello","include":["reasoning.encrypted_content"]}`)})
	raw, _ = p.EncodeValue(d)
	ingress, issues = compiler.Compile(raw.Bytes())
	if err := p.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if r := p.Verify(t.Context(), ingress); !r.Passed {
		t.Fatal("invalid include oracle", r.Issues)
	}
	report := p.VerifyBindingCombination(t.Context(), ingress, shippedProjectionProtocol(t, Gemini), p.CapabilitySet{p.TextCapability: true, p.UsageCapability: true})
	if !report.Passed {
		t.Fatal(report.Issues)
	}
}

func TestUsageProjectionNeverHidesInvalidOrUnknownDetails(t *testing.T) {
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(testCompiled(t, Gemini), testCompiled(t, Anthropic)))
	usage := &p.Usage{Input: &p.Counter{Count: 10, Origin: p.ObservedCount}, CacheCreation: &p.Counter{Count: 2, Origin: p.ObservedCount}, Details: map[string]p.Counter{"uncached_input_tokens": {Count: 9, Origin: p.ObservedCount}}}
	if _, err := c.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, nil); err == nil {
		t.Fatal("projection concealed invalid subtotal")
	}
	usage.Details = map[string]p.Counter{"vendor.unknown": {Count: 0, Origin: p.ObservedCount}}
	out, err := c.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (module{name: Gemini}).encodeUsage(out.Usage, p.EvaluationContext{}); err == nil {
		t.Fatal("unknown detail disappeared")
	}
	usage.Details = map[string]p.Counter{"ephemeral_5m_input_tokens": {Count: -1, Origin: p.ObservedCount}}
	if _, err := c.Response(t.Context(), &p.Response{SchemaVersion: 1, Usage: usage}, p.ConversionContext{}, nil); err == nil {
		t.Fatal("projection hid a negative counter")
	}
}

func shippedProjectionProtocol(t *testing.T, name string) *p.Compiled {
	t.Helper()
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	for _, value := range definitions {
		compiled, issues := compiler.Compile(value.Bytes())
		if err := p.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		if compiled.Codec(p.EncodeRequest) == name {
			return compiled
		}
	}
	t.Fatal("missing shipped module", name)
	return nil
}
