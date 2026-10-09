package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestActualPresetsKeepSystemAndReminderBoundaries(t *testing.T) {
	anthropic := shippedProjectionProtocol(t, Anthropic)
	body := `{"model":"m","max_tokens":64,"system":[{"type":"text","text":"top","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"<system-reminder>ordinary text</system-reminder>"}]}`
	r, err := anthropic.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(anthropic, anthropic))
	sink := &p.DiagnosticSink{}
	r, err = c.Request(t.Context(), r, p.ConversionContext{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := anthropic.EncodeRequest(t.Context(), r, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, body)
	for _, i := range sink.Issues() {
		if i.RuleID == "system-instruction-hoist" {
			t.Fatal("native top-level system falsely degraded", i)
		}
	}
	for _, source := range []string{Chat, Anthropic} {
		from := shippedProjectionProtocol(t, source)
		for _, target := range []string{Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				body := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"system","content":"middle"},{"role":"user","content":"<system-reminder>ordinary</system-reminder>"}]}`
				r, err := from.DecodeRequest(t.Context(), []byte(body), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				policy := p.DefaultConversionPolicy(from, to)
				if mode == "disabled" {
					for i := range policy.Rules {
						if policy.Rules[i].ID == "system-instruction-hoist" {
							policy.Rules[i].Enabled = false
						}
					}
				} else {
					policy.Mode = mode
				}
				c, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				sink := &p.DiagnosticSink{}
				projected, err := c.Request(t.Context(), r, p.ConversionContext{}, sink)
				var wire []byte
				if err == nil {
					wire, err = to.EncodeRequest(t.Context(), projected, p.EvaluationContext{})
				}
				wantOK := mode == "compatible"
				if (err == nil) != wantOK {
					t.Fatal(source, target, mode, err)
				}
				if wantOK {
					if !strings.Contains(string(wire), "middle") || !strings.Contains(string(wire), "system-reminder") || !sinkHas(sink, "/content/1") {
						t.Fatal("scope change or ordinary text lost", string(wire), sink.Issues())
					}
				}
			}
		}
	}
}
