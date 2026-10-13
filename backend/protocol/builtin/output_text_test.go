package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesOutputTextProjection(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		response, err := from.DecodeResponse(t.Context(), liveAuditFixture(t, "output_text").Bytes(), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
		sink := &p.DiagnosticSink{}
		projected, err := conversion.Response(t.Context(), response, conversion.VerificationRoute(from.Identity(), to.Identity(), p.HTTPJSON), sink)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
		if err != nil {
			t.Fatal(target, err)
		}
		if !strings.Contains(string(wire), "AUDIT_OK") || (strings.Contains(string(wire), `"output_text":"AUDIT_OK"`) != (target == Responses)) {
			t.Fatal(string(wire))
		}
		found := false
		for _, issue := range sink.Issues() {
			if issue.Path == "/output_text" {
				found = issue.Code == p.ConversionNormalized && issue.PolicyHash == conversion.Hash
			}
		}
		if found != (target != Responses) {
			t.Fatal(sink.Issues())
		}
		frames := responsesPaddedTextFrames()
		frames[len(frames)-1] = strings.Replace(frames[len(frames)-1], `"status":"completed","output"`, `"status":"completed","output_text":"OK","output"`, 1)
		auditConvertFrames(t, Responses, target, frames)
	}
}

func TestResponsesOutputTextBoundaries(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, raw := range []string{`null`, `42`, `[]`, `{}`, `"different"`} {
		fields, _ := liveAuditFixture(t, "output_text").ReadObject()
		fields["output_text"] = testValue(t, raw)
		_, err := from.DecodeResponse(t.Context(), object(fields).Bytes(), p.EvaluationContext{})
		if err == nil || !strings.Contains(err.Error(), "/output_text") {
			t.Fatal(raw, err)
		}
	}
	for _, output := range []string{`null`, `"text"`} {
		_, err := from.DecodeResponse(t.Context(), []byte(`{"output_text":"","output":`+output+`}`), p.EvaluationContext{})
		if err == nil {
			t.Fatal(output)
		}
	}
	for _, body := range []string{
		`{"output_text":"","output":[]}`,
		`{"output_text":"A&B","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A\u0026"}]},{"type":"function_call","id":"t","call_id":"c","name":"audit_echo","arguments":"{\"n\":9007199254740993}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"B"}]}]}`,
	} {
		r, err := from.DecodeResponse(t.Context(), []byte(body), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		if err = p.ValidateResponseOutputText(r); err != nil {
			t.Fatal(err)
		}
		for i := range r.Metadata {
			if r.Metadata[i].Name == "output_text" {
				r.Metadata[i].Value = p.StringValue("changed")
			}
		}
		conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(shippedProjectionProtocol(t, Chat), from))
		if _, err = conversion.Response(t.Context(), r, p.ConversionContext{}, nil); err == nil {
			t.Fatal("mapped mismatch accepted")
		}
	}
}
