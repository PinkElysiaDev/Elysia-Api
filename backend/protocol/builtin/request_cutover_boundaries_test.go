package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestRequestItemIdentityRequiresDeclaredProjection(t *testing.T) {
	source := testCompiled(t, Responses)
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[{"type":"message","role":"user","id":"message-1","status":"completed","content":"hello"}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Chat, Anthropic, Gemini} {
		target := testCompiled(t, name)
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				policy := p.DefaultConversionPolicy(source, target)
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for index := range policy.Rules {
						if policy.Rules[index].ID == "request-shape" {
							policy.Rules[index].Enabled = false
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				output, err := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, request.Source), sink)
				if err == nil {
					_, err = target.EncodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{}))
				}
				if (err == nil) != (mode == "compatible") {
					t.Fatal("item identity disappeared without its declared disposition", err)
				}
				if mode == "compatible" {
					for _, path := range []string{"id", "status"} {
						if !sinkHas(sink, "/content/0/"+path) {
							t.Fatal("identity projection lost its diagnostic", sink.Issues())
						}
					}
				}
				if request.Content[0].ID != p.StringValue("message-1") || request.Content[0].Status != p.StringValue("completed") {
					t.Fatal("target preparation changed the source request")
				}
			})
		}
	}
}

func TestEmptyToolResultStatusCannotBypassDisabledDisposition(t *testing.T) {
	source := testCompiled(t, Anthropic)
	request, err := source.DecodeRequest(t.Context(), []byte(`{"model":"m","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[],"is_error":true}]}]}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Chat, Responses} {
		target := testCompiled(t, name)
		policy := p.DefaultConversionPolicy(source, target)
		for index := range policy.Rules {
			if policy.Rules[index].ID == "tool-results" {
				policy.Rules[index].Enabled = false
			}
		}
		conversion, err := p.ResolveConversion(policy)
		if err != nil {
			t.Fatal(err)
		}
		output, err := conversion.Request(t.Context(), request, fixtureConversionContext(t, conversion, p.ConversionRequest, request.Source), nil)
		if err == nil {
			_, err = target.EncodeRequest(t.Context(), output, fixtureOptions(p.EvaluationContext{}))
		}
		if err == nil {
			t.Fatal("empty result erased an unresolved failure status", name)
		}
	}
}
