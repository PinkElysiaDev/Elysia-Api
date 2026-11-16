package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestEmptyVisibleReasoningRequiresDeclaredNormalization(t *testing.T) {
	for _, target := range []string{Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		from := shippedProjectionProtocol(t, Responses)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(target+"/"+mode, func(t *testing.T) {
				policy := p.DefaultConversionPolicy(to, from)
				if mode == "strict" {
					policy.Mode = mode
				}
				for index := range policy.Rules {
					if policy.Rules[index].ID == "response-shape" {
						policy.Rules[index].Enabled = mode != "disabled"
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, Source: from.Identity(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("completed"),
					Content: []p.Node{{Kind: p.MessageNode, Role: p.StringValue("assistant"), Children: []p.Node{{Kind: p.ReasoningNode}, {Kind: p.TextNode, Payload: p.StringValue("OK")}}}},
					Usage:   &p.Usage{Input: &p.Counter{Count: 1, Origin: p.ObservedCount}, Output: &p.Counter{Count: 1, Origin: p.ObservedCount}}}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Response(t.Context(), response, fixtureConversionContext(t, conversion, p.ConversionResponse, from.Identity()), sink)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
				if err == nil {
					err = to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire)))
				}
				if (err == nil) != (mode != "disabled") {
					t.Fatal("target bypassed its normalization rule", string(wire), err)
				}
				if !response.Content[0].Children[0].Payload.IsZero() {
					t.Fatal("source reasoning changed")
				}
				hasNormalization := false
				for _, issue := range sink.Issues() {
					if issue.Path == "/content/0/children/0/payload" {
						hasNormalization = issue.Code == p.ConversionNormalized && issue.RuleID == "response-shape" && issue.PolicyHash == conversion.Hash
					}
				}
				if hasNormalization != (mode != "disabled") {
					t.Fatal("missing or unexpected normalization diagnostic", sink.Issues())
				}
			})
		}
	}
}

func TestEmptyResponsesVisibleThoughtGetsGeminiDataMember(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Gemini)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"type":"reasoning","summary":[],"content":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"text":"","thought":true`) {
		t.Fatal(string(wire))
	}
	if !r.Content[0].Payload.IsZero() {
		t.Fatal("source thought modified")
	}
	found := false
	for _, i := range sink.Issues() {
		if strings.HasSuffix(i.Path, "/payload") {
			found = true
			if i.Code != p.ConversionNormalized || i.Fidelity != "preserved" || i.RuleID == "" {
				t.Fatal(i)
			}
		}
	}
	if !found {
		t.Fatal("empty thought normalization not diagnosed")
	}
}

func TestGeminiFinalContractRejectsEmptyPartAfterMapping(t *testing.T) {
	to := shippedProjectionProtocol(t, Gemini)
	for _, part := range []string{`{}`, `{"thought":true}`, `{"text":null}`} {
		for _, direction := range []p.Direction{p.EncodeResponse, p.EncodeEvent} {
			err := to.ValidateWireOutput(direction, testValue(t, `{"candidates":[{"content":{"role":"model","parts":[`+part+`]}}]}`))
			if err == nil || !strings.Contains(err.Error(), "/candidates/0/content/parts/0") {
				t.Fatal(part, err)
			}
		}
	}
	for _, part := range []string{`{"text":"","thought":true}`, `{"text":"OK","vendor":{"extension":true}}`, `{"thoughtSignature":"signature","thought":true}`} {
		if err := to.ValidateWireOutput(p.EncodeEvent, testValue(t, `{"candidates":[{"content":{"role":"model","parts":[`+part+`]}}]}`)); err != nil {
			t.Fatal(part, err)
		}
	}
}

func TestEmptyResponsesThoughtHasAnthropicRequiredThinking(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"type":"reasoning","summary":[],"content":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"thinking":""`) || !strings.Contains(string(wire), `"signature":""`) {
		t.Fatal(string(wire))
	}
	if err := to.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	if !r.Content[0].Payload.IsZero() {
		t.Fatal("source thought modified")
	}
	found := false
	for _, issue := range sink.Issues() {
		if strings.HasSuffix(issue.Path, "/payload") {
			found = issue.Code == p.ConversionNormalized && issue.Fidelity == "preserved" && issue.RuleID != ""
		}
	}
	if !found {
		t.Fatal("normalization was not diagnosed")
	}
}
