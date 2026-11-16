package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesMessagePhaseJSONAndHistory(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, value := range []string{`null`, `"commentary"`, `"final_answer"`} {
		for _, content := range []string{`[]`, `[{"type":"output_text","text":"OK","annotations":[]}]`} {
			message := `{"id":"msg","type":"message","role":"assistant","status":"completed","phase":` + value + `,"content":` + content + `}`
			r, err := c.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[`+message+`]}`), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Content) != 1 || len(r.Content[0].Metadata) != 1 {
				t.Fatal("phase owner missing", r.Content)
			}
			// Force reconstruction, not original-body replay. The same item
			// must survive both client delivery and next-turn history.
			r.Native, r.Content[0].Native = nil, nil
			wire, err := c.EncodeResponse(t.Context(), r, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			obj, _ := testValue(t, string(wire)).ReadObject()
			items, _ := readArray(obj["output"])
			if len(items) != 1 {
				t.Fatal("message dropped", string(wire))
			}
			sameJSON(t, items[0].Bytes(), message)
			request, err := c.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[`+message+`]}`), fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			request.Native, request.Content[0].Native = nil, nil
			wire, err = c.EncodeRequest(t.Context(), request, fixtureOptions(p.EvaluationContext{}))
			if err != nil {
				t.Fatal(err)
			}
			obj, _ = testValue(t, string(wire)).ReadObject()
			items, _ = readArray(obj["input"])
			if len(items) != 1 {
				t.Fatal("history message dropped", string(wire))
			}
			sameJSON(t, items[0].Bytes(), message)
		}
	}
	for _, value := range []string{`42`, `true`, `[]`, `{}`, `"unknown"`, `""`} {
		for _, direction := range []p.Direction{p.DecodeRequest, p.DecodeResponse} {
			body := `{"model":"m","input":[{"role":"assistant","phase":` + value + `,"content":[]}]}`
			var err error
			if direction == p.DecodeRequest {
				_, err = c.DecodeRequest(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
			} else {
				_, err = c.DecodeResponse(t.Context(), []byte(strings.Replace(body, `"input"`, `"output"`, 1)), fixtureOptions(p.EvaluationContext{}))
			}
			if err == nil || !strings.Contains(err.Error(), "invalid_input") || !strings.Contains(err.Error(), "/0/phase") {
				t.Fatalf("bad phase %s: %v", value, err)
			}
		}
	}
}

func TestResponsesMessagePhaseUsesNamedProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, codec := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, codec)
		for _, mode := range []string{"compatible", "strict"} {
			for _, value := range []string{`null`, `"commentary"`, `"final_answer"`} {
				t.Run(codec+mode+value, func(t *testing.T) {
					r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[{"role":"assistant","phase":`+value+`,"content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`), fixtureOptions(p.EvaluationContext{}))
					if err != nil {
						t.Fatal(err)
					}
					policy := p.DefaultConversionPolicy(to, from)
					policy.Mode = mode
					conversion, _ := p.ResolveConversion(policy)
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Response(t.Context(), r, fixtureConversionContext(t, conversion, p.ConversionResponse, r.Source), sink)
					if mode == "strict" && value != `null` {
						if err == nil || !strings.Contains(err.Error(), "/phase") {
							t.Fatal("phase loss accepted in strict mode", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{})); err != nil {
						t.Fatal(err)
					}
					if !sinkHas(sink, "/phase") {
						t.Fatal("phase omission has no diagnostic")
					}
					for i := range policy.Rules {
						if (policy.Rules[i].ID == "response-metadata" || policy.Rules[i].ID == "event-metadata" || policy.Rules[i].ID == "history-response-metadata") {
							policy.Rules[i].Enabled = false
						}
					}
					conversion, _ = p.ResolveConversion(policy)
					projected, err = conversion.Response(t.Context(), r, fixtureConversionContext(t, conversion, p.ConversionResponse, r.Source), nil)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := to.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{})); err == nil {
						t.Fatal("encoder silently omitted phase with rule disabled")
					}
				})
			}
		}
	}
}
