package builtin

import (
	"bytes"
	"errors"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func geminiPackageStreamFrames() []string {
	return []string{
		`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"think","thought":true},{"thoughtSignature":"sig-thought"},{"text":"hel"}]}}]}`,
		`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"lo"},{"functionCall":{"id":"call","name":"lookup","args":{"n":9007199254740993}}},{"thoughtSignature":"sig-tool"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":6}}`,
		`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":6}}`,
	}
}

func decodeGeminiPackageStream(t *testing.T, compiled *p.Compiled, raws []string, scope p.Scope) ([]*p.EventFrame, *p.Response) {
	t.Helper()
	options := p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}
	defer options.State.Release()
	validation, err := compiled.NewWireInputStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	target := compiled.Target(p.DecodeEvent, scope)
	target.Direction = p.EncodeEvent
	collector, err := p.NewResponseCollector(target, compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	var frames []*p.EventFrame
	for index, raw := range raws {
		value := testValue(t, raw)
		frame, err := validation.DecodeFrame(t.Context(), value, options)
		if err != nil {
			t.Fatal("validated source mapping", index, err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal("source semantic lifecycle", index, err)
			}
		}
		frames = append(frames, frame)
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return frames, response
}

func TestGeminiPackageStreamItemDisposition(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	ratings := testValue(t, `[{"category":"HARM","probability":"NEGLIGIBLE"}]`)
	start := p.Event{SchemaVersion: compiled.CoreVersion(), Type: p.ResponseStarted, ResponseID: p.StringValue("r"), Response: &p.Response{SchemaVersion: compiled.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("in_progress")}}
	frames := []*p.EventFrame{
		{Events: []p.Event{start}},
		{Events: []p.Event{{SchemaVersion: compiled.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("t"), Item: &p.Node{Kind: p.TextNode, Metadata: []p.ResponseMetadata{{Name: "safetyRatings", Location: "candidate", Codec: Gemini, SourceCodec: Gemini, Path: "/candidates/0/safetyRatings", Value: ratings}}}}}},
		{Events: []p.Event{{SchemaVersion: compiled.CoreVersion(), Type: p.ItemDelta, ItemID: p.StringValue("t"), Delta: p.StringValue("x")}}},
		{Events: []p.Event{{SchemaVersion: compiled.CoreVersion(), Type: p.ItemFinished, ItemID: p.StringValue("t")}}},
		{Events: []p.Event{{SchemaVersion: compiled.CoreVersion(), Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: compiled.CoreVersion(), Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}}}},
	}
	wire := encodeGeminiPackageStream(t, compiled, frames, p.Scope{}, false)
	observed := 0
	for _, raw := range wire {
		fields, err := testValue(t, raw).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		if fields["candidates"].IsZero() {
			continue
		}
		var candidates []p.Value
		if err := fields["candidates"].Decode(&candidates); err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			fields, err := candidate.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			if !fields["safetyRatings"].IsZero() {
				if fields["safetyRatings"] != ratings {
					t.Fatal("item metadata payload changed", fields["safetyRatings"])
				}
				observed++
			}
		}
	}
	if observed != 1 {
		t.Fatal("item metadata must be emitted once", observed)
	}

	options := p.EvaluationContext{State: p.NewEvaluationState()}
	defer options.State.Release()
	if _, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{start}}, options); err != nil {
		t.Fatal(err)
	}
	opaque := p.Event{SchemaVersion: compiled.CoreVersion(), Type: p.ItemStarted, ItemID: p.StringValue("opaque"), Item: &p.Node{Kind: p.OpaqueNode, Native: &p.Native{Source: p.Provenance{Protocol: compiled.Identity(), Direction: p.DecodeEvent}, Value: testValue(t, `{"vendor":true}`)}}}
	_, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{opaque}}, options)
	var failure *p.ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) == 0 || failure.Issues[0].Code != p.UnsupportedCapability || failure.Issues[0].Path != "/item/kind" {
		t.Fatal("unmapped item kind was silently omitted or misclassified", err)
	}
}

func encodeGeminiPackageStream(t *testing.T, compiled *p.Compiled, frames []*p.EventFrame, scope p.Scope, preserve bool) []string {
	t.Helper()
	validation, err := compiled.NewWireStreamValidation(scope)
	if err != nil {
		t.Fatal(err)
	}
	options := (p.EvaluationContext{Scope: scope, State: p.NewEvaluationState()}).WithOutputStream(validation)
	defer options.State.Release()
	var result []string
	consume := func(values []p.Value) {
		for _, value := range values {
			if err := validation.Consume(t.Context(), value); err != nil {
				t.Fatal("target contract", value.JSONString(), err)
			}
			result = append(result, value.JSONString())
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, frame := range frames {
		if !preserve {
			frame = &p.EventFrame{Events: frame.Events}
		}
		values, err := compiled.EncodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal("target mapping", err)
		}
		consume(values)
	}
	tail, err := compiled.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal("target finalize", err)
	}
	consume(tail)
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGeminiPackageStreamSignaturesToolsAndUsage(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	frames, response := decodeGeminiPackageStream(t, compiled, geminiPackageStreamFrames(), scope)
	checkGeminiPackageStreamResult(t, response)
	rendered := encodeGeminiPackageStream(t, compiled, frames, scope, false)
	_, rebuilt := decodeGeminiPackageStream(t, compiled, rendered, scope)
	checkGeminiPackageStreamResult(t, rebuilt)
	for _, raw := range rendered {
		if bytes.Contains([]byte(raw), []byte(`"signature_delta"`)) || bytes.Contains([]byte(raw), []byte(`"input_json_delta"`)) {
			t.Fatal("another protocol's stream tags leaked into Gemini", raw)
		}
	}
	native := encodeGeminiPackageStream(t, compiled, frames, scope, true)
	if len(native) != len(geminiPackageStreamFrames()) {
		t.Fatal("native source frame was duplicated", len(native))
	}
	for index, expected := range geminiPackageStreamFrames() {
		if native[index] != expected {
			t.Fatal("same-wire frame bytes changed", index, native[index])
		}
	}

	call := response.Content[2]
	request := &p.Request{SchemaVersion: compiled.CoreVersion(), Model: p.StringValue("m"), Content: []p.Node{
		call, {Kind: p.ToolResultNode, CallID: call.CallID, Name: call.Name, Payload: testValue(t, `{"ok":true}`)},
	}}
	wire, err := compiled.EncodeRequest(t.Context(), request, p.EvaluationContext{Scope: scope})
	if err != nil {
		t.Fatal("tool follow-up encoding", err)
	}
	if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
		t.Fatal("tool follow-up contract", err)
	}
}

func checkGeminiPackageStreamResult(t *testing.T, response *p.Response) {
	t.Helper()
	if response == nil || !response.ID.IsString("r") || !response.Model.IsString("m") || len(response.Content) != 3 {
		t.Fatal("stream response shape changed", response)
	}
	thinking, text, call := response.Content[0], response.Content[1], response.Content[2]
	if thinking.Kind != p.ReasoningNode || !thinking.Payload.IsString("think") || len(thinking.Resources) != 1 || !thinking.Resources[0].ID.IsString("sig-thought") {
		t.Fatal("thinking or its signature was lost", thinking)
	}
	if text.Kind != p.TextNode || !text.Payload.IsString("hello") {
		t.Fatal("text chunks did not append to their open item", text)
	}
	if call.Kind != p.ToolCallNode || !call.CallID.IsString("call") || !call.Name.IsString("lookup") || call.Input == nil || call.Input.Value != testValue(t, `{"n":9007199254740993}`) || len(call.Resources) != 1 || !call.Resources[0].ID.IsString("sig-tool") {
		t.Fatal("tool association, exact arguments or signature changed", call)
	}
	if response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 3 || response.Usage.Output == nil || response.Usage.Output.Count != 3 || response.Usage.Total == nil || response.Usage.Total.Count != 6 || response.Usage.Details["output.reasoning_tokens"].Count != 1 {
		t.Fatal("visible and thinking usage were not accounted correctly", response.Usage)
	}
}

func TestGeminiPackageStreamLateUsageAndIdlessCalls(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	frames, response := decodeGeminiPackageStream(t, compiled, []string{
		`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"n":1}}},{"functionCall":{"name":"lookup","args":{"n":2}}}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
		`{"candidates":[{"index":0,"finishReason":"STOP"}]}`,
		`{"usageMetadata":{"thoughtsTokenCount":1,"totalTokenCount":4}}`,
	}, p.Scope{})
	if len(response.Content) != 2 || response.Content[0].CallID.IsZero() || response.Content[0].CallID == response.Content[1].CallID || response.Usage == nil || response.Usage.Output == nil || response.Usage.Total == nil || response.Usage.Output.Count != 2 || response.Usage.Total.Count != 4 {
		t.Fatal("idless calls collided or late reasoning was missed", response)
	}
	rendered := encodeGeminiPackageStream(t, compiled, frames, p.Scope{}, false)
	_, rebuilt := decodeGeminiPackageStream(t, compiled, rendered, p.Scope{})
	if len(rebuilt.Content) != 2 || rebuilt.Content[0].CallID != response.Content[0].CallID || rebuilt.Content[1].CallID != response.Content[1].CallID || rebuilt.Usage == nil || rebuilt.Usage.Output == nil || rebuilt.Usage.Total == nil || rebuilt.Usage.Output.Count != 2 || rebuilt.Usage.Total.Count != 4 {
		t.Fatal("target lost call identity or usage tails", rebuilt)
	}
}

func TestGeminiPackageHistoryConsumesExplicitCall(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	raw := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup","args":{}}},{"functionCall":{"id":"b","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"a","name":"lookup","response":{"a":true}}},{"functionResponse":{"name":"lookup","response":{"b":true}}}]}]}`)
	request, err := compiled.DecodeRequest(t.Context(), raw, p.EvaluationContext{Scope: p.Scope{Model: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	var results []p.Node
	for _, message := range request.Content {
		for _, node := range message.Children {
			if node.Kind == p.ToolResultNode {
				results = append(results, node)
			}
		}
	}
	if len(results) != 2 || !results[0].CallID.IsString("a") || !results[1].CallID.IsString("b") {
		t.Fatal("explicit consumption did not narrow the idless result's pending group", results)
	}
}

func TestGeminiPackageStreamRejectsInvalidAssociations(t *testing.T) {
	compiled := compileBuiltinPackage(t, "google-generate-content")
	for _, frames := range [][]string{
		{`{"candidates":[{"index":1,"content":{"parts":[{"text":"no"}]}}]}`},
		{`{"candidates":[{},{}]}`},
		{`{"candidates":[{"content":{"parts":[{"thoughtSignature":"orphan"}]}}]}`},
		{`{"candidates":[{"content":{"parts":[{"text":"x","thoughtSignature":"one"},{"thoughtSignature":"two"}]}}]}`},
		{`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":42}}]}}]}`},
		{`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"c","name":"first","args":{}}},{"functionCall":{"id":"c","name":"changed","args":{}}}]}}]}`},
		{`{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":2}}`},
	} {
		validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		var failure error
		for _, raw := range frames {
			failure = validation.Consume(t.Context(), testValue(t, raw))
			if failure != nil {
				break
			}
		}
		if failure == nil {
			t.Fatal("invalid wire passed independent validation", frames)
		}
	}
	validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), testValue(t, `{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := validation.Finish(); err == nil {
		t.Fatal("EOF completed a response without finishReason")
	}
}
