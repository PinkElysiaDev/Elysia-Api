package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicMetadataPreservesOwnerAndDeclaredMeaning(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	for _, context := range []string{`null`, `{}`, `{"applied_edits":[]}`, `{"applied_edits":[{"type":"clear"}]}`} {
		body := strings.Replace(auditResponses[Anthropic], `{`, `{"context_management":`+context+`,"stop_details":null,`, 1)
		response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		wantMeaning := "empty"
		if strings.Contains(context, `"clear"`) {
			wantMeaning = "state"
		}
		found := false
		for _, field := range response.Metadata {
			if field.Name == "context_management" {
				found = field.Path == "/context_management" && field.Meaning == wantMeaning
			}
		}
		if !found {
			t.Fatal("source metadata ownership or meaning changed", response.Metadata)
		}
		response.Native = nil
		wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"context_management":` + context, `"stop_details":null`, `"citations":[]`, `"service_tier":"standard"`} {
			if !strings.Contains(string(wire), field) {
				t.Fatal("rebuild lost source metadata", field, string(wire))
			}
		}
	}
}

func TestAnthropicMetadataSpecRejectsInvalidTypes(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	for _, body := range []string{
		`{"context_management":false}`,
		`{"stop_details":[]}`,
		`{"usage":{"service_tier":42}}`,
		`{"usage":{"server_tool_use":false}}`,
		`{"content":[{"type":"text","text":"OK","citations":{}}]}`,
	} {
		if _, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{})); err == nil {
			t.Fatal("source Spec accepted malformed metadata", body)
		}
		options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
		frame := `{"type":"message_start","message":` + body + `}`
		_, err := compiled.DecodeFrame(t.Context(), testValue(t, frame), options)
		options.State.Release()
		if err == nil {
			t.Fatal("source stream Spec accepted malformed metadata", frame)
		}
	}
}

func anthropicMetadataFrames() []string {
	return []string{
		`{"type":"message_start","message":{"id":"r","type":"message","model":"m","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"context_management":{"applied_edits":[]},"usage":{"input_tokens":3,"output_tokens":0,"service_tier":"standard"}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","citations":[]}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	}
}

func TestAnthropicMetadataStreamRebuild(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	frames, _ := decodePackageStream(t, compiled, anthropicMetadataFrames(), p.Scope{})
	wire := encodePackageStream(t, compiled, frames, p.Scope{}, false)
	joined := strings.Join(wire, "\n")
	for _, field := range []string{`"citations":[]`, `"context_management":{"applied_edits":[]}`, `"service_tier":"standard"`, `"stop_details":null`} {
		if !strings.Contains(joined, field) {
			t.Fatal("rebuilt stream lost source-owned metadata", field, joined)
		}
	}
}

func TestAnthropicLateContentMetadataCannotDisappear(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	frames, _ := decodePackageStream(t, compiled, anthropicMetadataFrames(), p.Scope{})
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	defer options.State.Release()
	options, consume := bindFixtureOutput(t, compiled, options)
	for index, source := range frames {
		if index == 2 {
			event := source.Events[0]
			event.Metadata = []p.ResponseMetadata{{Codec: "anthropic", SourceCodec: "anthropic", Location: "content", Name: "citations", Path: "/late/citations", Meaning: "empty", Value: testValue(t, `[]`)}}
			if _, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{event}}, options); err == nil || !strings.Contains(err.Error(), "block-start frame") {
				t.Fatal("late content metadata was silently omitted", err)
			}
		}
		output, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: source.Events}, options)
		if err != nil {
			t.Fatal("rejected metadata changed the stream transaction", err)
		}
		consume(output)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
}

func TestAnthropicStopSequenceRebuild(t *testing.T) {
	compiled := compileBuiltinPackage(t, Anthropic)
	const stop = "<end>"
	responseBody := strings.Replace(auditResponses[Anthropic], `"stop_reason":"end_turn","stop_sequence":null`, `"stop_reason":"stop_sequence","stop_sequence":"`+stop+`"`, 1)
	response, err := compiled.DecodeResponse(t.Context(), []byte(responseBody), fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if !response.Attributes["stopSequence"].IsString(stop) {
		t.Fatal("source did not expose the matched stop string", response.Attributes)
	}
	response.Native = nil
	wire, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateWireOutput(p.EncodeResponse, testValue(t, string(wire))); err != nil {
		t.Fatal(err)
	}
	fields, err := testValue(t, string(wire)).ReadObject()
	if err != nil || !fields["stop_reason"].IsString("stop_sequence") || !fields["stop_sequence"].IsString(stop) {
		t.Fatal("JSON rebuild lost the matched stop string", string(wire), err)
	}
	roundTrip, err := compiled.DecodeResponse(t.Context(), wire, fixtureOptions(p.EvaluationContext{}))
	if err != nil || !roundTrip.Attributes["stopSequence"].IsString(stop) {
		t.Fatal("JSON round trip changed the matched stop string", roundTrip, err)
	}

	raws := anthropicMetadataFrames()
	raws[4] = strings.Replace(raws[4], `"stop_reason":"end_turn","stop_sequence":null`, `"stop_reason":"stop_sequence","stop_sequence":"`+stop+`"`, 1)
	frames, collected := decodePackageStream(t, compiled, raws, p.Scope{})
	if !collected.Attributes["stopSequence"].IsString(stop) {
		t.Fatal("stream source lost the matched stop string", collected.Attributes)
	}
	rebuilt := encodePackageStream(t, compiled, frames, p.Scope{}, false)
	_, collected = decodePackageStream(t, compiled, rebuilt, p.Scope{})
	if !collected.Attributes["stopSequence"].IsString(stop) || !strings.Contains(strings.Join(rebuilt, "\n"), `"stop_reason":"stop_sequence"`) {
		t.Fatal("stream rebuild changed the matched stop string", rebuilt)
	}

	for _, name := range []string{Chat, Responses, Gemini} {
		t.Run(name, func(t *testing.T) {
			target := compileBuiltinPackage(t, name)
			conversion, err := p.ResolveConversion(p.DefaultConversionPolicy(target, compiled))
			if err != nil {
				t.Fatal(err)
			}
			route := fixtureConversionContext(t, conversion, p.ConversionResponse, compiled.Identity())
			route.Transport = p.HTTPJSON
			projected, err := conversion.Response(t.Context(), response, route, nil)
			if err == nil {
				_, err = target.EncodeResponse(t.Context(), projected, fixtureOptions(p.EvaluationContext{}))
			}
			if err == nil || !strings.Contains(err.Error(), "/attributes") {
				t.Fatal("foreign target silently lost the matched stop string", err)
			}
		})
	}
}
