package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesErrorEnvelopeProjection(t *testing.T) {
	for _, body := range []string{
		`{"type":"error","sequence_number":51,"error":{"type":"api_error","message":"provider failed","code":null,"param":null}}`,
		`{"type":"error","sequence_number":51,"message":"provider failed","code":null,"param":null}`,
	} {
		from := shippedProjectionProtocol(t, Responses)
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		if len(frame.Events) != 1 || frame.Events[0].Type != p.OperationFailed || frame.Events[0].Unmapped != nil {
			t.Fatalf("error envelope was treated as an extension: %+v", frame.Events)
		}
		failure, _ := frame.Events[0].Error.ReadObject()
		if failure["message"] != p.StringValue("provider failed") || len(failure) > 2 {
			t.Fatalf("framing leaked into error semantics: %s", frame.Events[0].Error.Bytes())
		}
		for _, name := range []string{Chat, Responses, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, name)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			// Semantic projection must work even when native replay is unavailable.
			frames, err := to.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options)
			if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), "provider failed") {
				t.Fatal(name, frames, err)
			}
			if err := to.ValidateWireOutput(p.EncodeEvent, frames[0]); err != nil {
				t.Fatal(err)
			}
		}
		native, err := from.EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil || len(native) != 1 || string(native[0].Bytes()) != body {
			t.Fatal(native, err)
		}
	}
}

func TestResponsesErrorEnvelopeValidation(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, test := range []struct{ body, path string }{
		{`{"type":"error","sequence_number":1,"error":{"type":"api_error"}}`, "/error/message"},
		{`{"type":"error","sequence_number":1,"error":{"type":"api_error","message":42}}`, "/error/message"},
		{`{"type":"error","sequence_number":1,"error":null}`, "/error"},
		{`{"type":"error","sequence_number":1,"message":null}`, "/message"},
		{`{"type":"error","sequence_number":1,"message":"bad","code":42}`, "/code"},
		{`{"type":"error","sequence_number":1,"message":"bad","param":[]}`, "/param"},
		{`{"type":"error","sequence_number":1,"message":"outer","error":{"type":"api_error","message":"inner"}}`, "/message"},
	} {
		v := testValue(t, test.body)
		if _, err := c.DecodeFrame(t.Context(), v, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil || !strings.Contains(err.Error(), test.path) {
			t.Fatal(test.body, "decode", err)
		}
		if err := c.ValidateWireOutput(p.EncodeEvent, v); err == nil || !strings.Contains(err.Error(), test.path) {
			t.Fatal(test.body, "wire", err)
		}
	}
	for _, extra := range []string{
		`{"type":"error","sequence_number":1,"message":"bad","vendor":false}`,
		`{"type":"error","sequence_number":1,"error":{"type":"api_error","message":"bad","billing":null}}`,
	} {
		frame, err := c.DecodeFrame(t.Context(), testValue(t, extra), p.EvaluationContext{State: p.NewEvaluationState()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := shippedProjectionProtocol(t, Anthropic).EncodeFrame(t.Context(), frame, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil {
			t.Fatal("unknown error extension disappeared", extra)
		}
	}
}
