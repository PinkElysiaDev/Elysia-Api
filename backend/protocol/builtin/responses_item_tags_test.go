package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"strings"
	"testing"
)

const itemTags = `"metadata":{"turn_id":"turn"},"internal_chat_message_metadata_passthrough":{"turn_id":"turn","create_time":1791613919.6719978},`

func TestResponsesToolAndThinkingTagsRetainTheirItems(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	items := []string{
		`{` + itemTags + `"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":"{}","status":"completed"}`,
		`{` + itemTags + `"type":"reasoning","id":"thinking","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"thought"}]}`,
	}
	for _, item := range items {
		for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
			to := shippedProjectionProtocol(t, target)
			c, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
			r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[`+item+`],"usage":{"input_tokens":1,"output_tokens":1}}`), p.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Content[0].Metadata) != 2 {
				t.Fatal("item annotations remained opaque", r.Content)
			}
			projected, err := c.Response(t.Context(), r, p.ConversionContext{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			projected.Native = nil
			for i := range projected.Content {
				projected.Content[i].Native = nil
			}
			wire, err := to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
			if err != nil {
				t.Fatalf("%s: %v", target, err)
			}
			if strings.Contains(string(wire), "create_time") != (target == Responses) {
				t.Fatal(string(wire))
			}
		}
	}
}

func TestResponsesToolTagsStreamAndCollector(t *testing.T) {
	start := `{` + itemTags + `"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":"","status":"in_progress"}`
	done := `{` + itemTags + `"type":"function_call","id":"fc","call_id":"call","name":"lookup","arguments":"{}","status":"completed"}`
	raw := []string{responsesPaddedTextFrames()[0],
		`{"type":"response.output_item.added","output_index":0,"item":` + start + `}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc","delta":"{}","obfuscation":"padding"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc","arguments":"{}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + done + `}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","created_at":1,"status":"completed","output":[` + done + `],"usage":{"input_tokens":3,"output_tokens":1}}}`}
	from := shippedProjectionProtocol(t, Responses)
	collector, err := p.NewResponseCollector(p.Target{Protocol: from.Identity(), Direction: p.EncodeEvent, Capabilities: from.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	opts := p.EvaluationContext{State: p.NewEvaluationState()}
	for _, body := range raw {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := collector.Finish()
	if err != nil || len(r.Content) != 1 || len(r.Content[0].Metadata) != 2 {
		t.Fatal(r, err)
	}
	for _, target := range []string{Responses, Chat, Anthropic, Gemini} {
		frames := auditConvertFrames(t, Responses, target, raw)
		found := false
		for _, v := range frames {
			if strings.Contains(string(v.Bytes()), "create_time") {
				found = true
			}
		}
		if found != (target == Responses) {
			t.Fatal("tool annotations disappeared", target)
		}
	}
	for _, raw := range []string{`{"create_time":null}`, `{"create_time":"now"}`, `{"create_time":-1}`} {
		if err := p.ValidateMetadataValue(Responses, "item", "internal_chat_message_metadata_passthrough", testValue(t, raw), "/item/internal_chat_message_metadata_passthrough"); err == nil {
			t.Fatal("invalid time accepted", raw)
		}
	}
}
