package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

func TestWireContractEscapedRoles(t *testing.T) {
	cases := []struct {
		name, protocol, body string
		direction            p.Direction
		wantError            bool
	}{
		{"anthropic_normal", Anthropic, "{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}", p.EncodeRequest, false},
		{"anthropic_equivalent", Anthropic, "{\"model\":\"m\",\"messages\":[{\"role\":\"\\u0075ser\",\"content\":\"hello\"}]}", p.EncodeRequest, false},
		{"responses_invalid_role", Responses, "{\"model\":\"m\",\"input\":[{\"type\":\"message\",\"role\":\"invalid\",\"content\":\"hello\"}]}", p.EncodeRequest, true},
		{"responses_escaped_type_bypass", Responses, "{\"model\":\"m\",\"input\":[{\"type\":\"\\u006dessage\",\"role\":\"invalid\",\"content\":\"hello\"}]}", p.EncodeRequest, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			value, err := p.ParseValue([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			err = (module{name: tt.protocol}).ValidateWireOutput(tt.direction, value)
			if (err != nil) != tt.wantError {
				t.Fatalf("err=%v", err)
			}
			t.Logf("error: %v", err)
		})
	}
}

func TestEscapedResponsesRefusalStream(t *testing.T) {
	compiled := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	collector, err := p.NewResponseCollector(p.Target{Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
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
	value := testValue(t, `{"cyber":"\u0073tandard"}`)
	if err := p.ValidateMetadataValue(Responses, "response", "access_programs", value, "/access_programs"); err != nil {
		t.Fatal(err)
	}
}
