package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

func TestResponsesMessageTurnTagsJSONAndSSE(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	const tags = `"metadata":{"turn_id":"turn-one"},"internal_chat_message_metadata_passthrough":{"create_time":1791613919.6719978,"turn_id":"turn-two"},`
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, mode := range []string{"compatible", "strict"} {
			policy := p.DefaultConversionPolicy(to, from)
			policy.Mode = mode
			c, _ := p.ResolveConversion(policy)
			r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","created_at":1,"status":"completed","output":[{`+tags+`"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			sink := &p.DiagnosticSink{}
			projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), sink)
			if target != Responses && mode == "strict" {
				if err == nil || !strings.Contains(err.Error(), "metadata") {
					t.Fatal(err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			projected.Native = nil
			for i := range projected.Content {
				projected.Content[i].Native = nil
			}
			wire, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), "turn-one") != (target == Responses) || strings.Contains(string(wire), "turn-two") != (target == Responses) {
				t.Fatal(string(wire))
			}
			if target != Responses {
				n := 0
				for _, i := range sink.Issues() {
					if strings.Contains(i.Path, "metadata") {
						n++
						if i.RuleID == "" || i.PolicyHash != c.Hash || i.Fidelity != "lossy_compatible" {
							t.Fatal(i)
						}
					}
				}
				if n != 2 {
					t.Fatal(sink.Issues())
				}
			}
		}
		raw := responsesPaddedTextFrames()
		for i := range raw {
			raw[i] = strings.ReplaceAll(raw[i], `"type":"message",`, tags+`"type":"message",`)
		}
		frames := auditConvertFrames(t, Responses, target, raw)
		found := false
		for _, frame := range frames {
			if strings.Contains(string(frame.Bytes()), "turn-two") {
				found = true
			}
		}
		if found != (target == Responses) {
			t.Fatalf("target=%s message tags changed", target)
		}
	}
}

func TestResponsesTurnTagsUnknownAndInvalid(t *testing.T) {
	from, to := shippedProjectionProtocol(t, Responses), shippedProjectionProtocol(t, Anthropic)
	c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
	for _, name := range []string{"metadata", "internal_chat_message_metadata_passthrough"} {
		for _, value := range []string{`42`, `[]`, `{"turn_id":null}`, `{"turn_id":""}`, `{"turn_id":42}`} {
			body := `{"id":"r","model":"m","output":[{"type":"message","role":"assistant","` + name + `":` + value + `,"content":[{"type":"output_text","text":"OK"}]}]}`
			if _, err := from.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{})); err == nil || !strings.Contains(err.Error(), "/"+name) {
				t.Fatal(name, value)
			}
		}
		for _, value := range []string{`{"unknown":true}`, `{"turn_id":"t","unknown":true}`} {
			r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","output":[{"type":"message","role":"assistant","`+name+`":`+value+`,"content":[{"type":"output_text","text":"OK"}]}]}`), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			r.Native = nil
			for i := range r.Content {
				r.Content[i].Native = nil
			}
			wire, err := from.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
			if err != nil || !strings.Contains(string(wire), `"unknown":true`) {
				t.Fatal(string(wire), err)
			}
			projected, err := c.Response(t.Context(), r, fixtureConversionContext(t, c, p.ConversionResponse, r.Source), nil)
			if err == nil {
				_, err = to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			}
			if err == nil {
				t.Fatal("unknown vendor message annotation discarded")
			}
		}
	}
}
