package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesNativeVisibleReasoningPreservesEdits(t *testing.T) {
	compiled := testCompiled(t, Responses)
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[{"type":"reasoning_text","text":"original","vendor":9007199254740993}]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	request.Content[0].Payload = p.StringValue("edited")
	body, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{})
	if err != nil || !strings.Contains(string(body), `"text":"edited"`) || !strings.Contains(string(body), `9007199254740993`) || strings.Contains(string(body), "original") {
		t.Fatal(string(body), err)
	}
	request.Content[0].Native.Source.Protocol.Family = "foreign"
	if _, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil {
		t.Fatal("foreign thinking masqueraded as a native Responses extension")
	}
}

func TestResponsesReasoningKeepsOrderedSummariesAndEncryptedPayload(t *testing.T) {
	compiled := testCompiled(t, Responses)
	scope := p.Scope{Provider: "p", Account: "a", Model: "m"}
	options := p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}
	collector, err := p.NewResponseCollector(p.Target{Protocol: compiled.Identity(), Direction: p.EncodeEvent, Scope: scope, Capabilities: compiled.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	frames := []string{
		`{"type":"response.created","response":{"id":"r","model":"m","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs","type":"reasoning","status":"in_progress","summary":[]}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"first"}`,
		`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"first"}}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":1,"part":{"type":"summary_text","text":"second"}}`,
		`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":1,"part":{"type":"summary_text","text":"second"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"first"},{"type":"summary_text","text":"second"}],"encrypted_content":"sealed"}}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"id":"rs","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"first"},{"type":"summary_text","text":"second"}],"encrypted_content":"sealed"}]}}`,
	}
	var events []p.Event
	for _, body := range frames {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 {
		t.Fatal(response.Content)
	}
	node := response.Content[0]
	if node.ReasoningForm != "summary" || !node.Payload.IsZero() || len(node.Children) != 2 || len(node.Resources) != 1 {
		t.Fatal(node)
	}
	sameJSON(t, node.Children[0].Payload.Bytes(), `"first"`)
	sameJSON(t, node.Children[1].Payload.Bytes(), `"second"`)
	sameJSON(t, node.Resources[0].ID.Bytes(), `"sealed"`)
	encoderOptions := p.EvaluationContext{State: p.NewEvaluationState(), Scope: scope}
	var encoded []p.Value
	for _, event := range events {
		frames, err := compiled.EncodeFrames(t.Context(), event, encoderOptions)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, frames...)
	}
	tail, err := compiled.FinishEvents(t.Context(), encoderOptions)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, tail...)
	terminal, err := encoded[len(encoded)-1].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	final, err := terminal["response"].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	output, err := readArray(final["output"])
	if err != nil || len(output) != 1 {
		t.Fatal(output, err)
	}
	sameJSON(t, output[0].Bytes(), `{"id":"rs","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"first"},{"type":"summary_text","text":"second"}],"encrypted_content":"sealed"}`)
	foreign := testCompiled(t, Anthropic)
	if _, err := foreign.EncodeResponse(t.Context(), response, p.EvaluationContext{Scope: scope}); err == nil {
		t.Fatal("summary/encryption silently became visible thinking")
	}
}

func TestResponsesSummaryRejectsRewriteAndOutOfOrderParts(t *testing.T) {
	for _, bad := range []string{
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":2,"part":{"type":"summary_text","text":"wrong index"}}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"replacement"}`,
	} {
		compiled := testCompiled(t, Responses)
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		for _, body := range []string{`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","summary":[]}}`, `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"prefix"}}`} {
			if _, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := compiled.DecodeFrame(t.Context(), testValue(t, bad), options); err == nil {
			t.Fatal("invalid summary accepted", bad)
		}
	}
}
