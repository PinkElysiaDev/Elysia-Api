package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestEmptyResponsesVisibleThoughtGetsGeminiDataMember(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Gemini)
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"type":"reasoning","summary":[],"content":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	projected, err := c.Response(t.Context(), r, p.ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
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
	r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"type":"reasoning","summary":[],"content":[]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	sink := &p.DiagnosticSink{}
	projected, err := c.Response(t.Context(), r, p.ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
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
