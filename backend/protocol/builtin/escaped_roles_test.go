package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestWireContractEscapedRoles(t *testing.T) {
	for _, fixture := range []struct {
		name, protocol, body string
		shouldReject         bool
	}{
		{"anthropic-normal", Anthropic, `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`, false},
		{"anthropic-equivalent", Anthropic, `{"model":"m","max_tokens":8,"messages":[{"role":"\u0075ser","content":"hello"}]}`, false},
		{"responses-invalid-role", Responses, `{"model":"m","input":[{"type":"message","role":"invalid","content":"hello"}]}`, true},
		{"responses-escaped-type-bypass", Responses, `{"model":"m","input":[{"type":"\u006dessage","role":"invalid","content":"hello"}]}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			err := testCompiled(t, fixture.protocol).ValidateWireOutput(p.EncodeRequest, testValue(t, fixture.body))
			if (err != nil) != fixture.shouldReject {
				t.Fatal("escaped role changed the independent final wire contract", err)
			}
		})
	}
}

func TestEscapedResponsesRefusalStream(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"object":"response","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"\u006dessage","role":"assistant","status":"in_progress","content":[],"phase":"final_answer"}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"\u0072efusal","refusal":""}}`,
		`{"type":"response.refusal.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"no"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"\u0072efusal","refusal":"no"}}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","created_at":1,"object":"response","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"refusal","refusal":"no"}]}]}}`,
	} {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 || len(response.Content[0].Children) != 1 || response.Content[0].Children[0].Kind != p.RefusalNode || !response.Content[0].Children[0].Payload.IsString("no") {
		t.Fatalf("refusal lost: %+v", response)
	}
}

func TestEscapedAccessProgram(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	response, err := compiled.DecodeResponse(t.Context(), []byte(`{"id":"r","output":[],"access_programs":{"cyber":"\u0073tandard"}}`), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Metadata) != 1 || response.Metadata[0].Name != "access_programs" {
		t.Fatal("escaped wire enum was not mapped to its owner", response.Metadata)
	}
	fields, err := response.Metadata[0].Value.ReadObject()
	if err != nil || !fields["cyber"].IsString("standard") {
		t.Fatal(fields, err)
	}
}
