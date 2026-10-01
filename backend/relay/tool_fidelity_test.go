package relay

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func renderToolFixture(t *testing.T, req *MaheshvaraRequest, target FormatType, shape string) ([]byte, error) {
	t.Helper()
	if shape == "" {
		return MaheshvaraToTargetRequest(req, target, nil)
	}
	config := CustomProtocolConfig{ID: "independent-tools", Request: CustomProtocolRequest{
		PathTemplate: "/generate", Shape: shape,
		Body: json.RawMessage(`{"model":{"field":"model"},"input":{"field":"input_items"},"tools":{"field":"tools","omitIfEmpty":true},"tool_choice":{"field":"tool_choice","omitIfEmpty":true}}`),
	}, Response: CustomProtocolResponse{TextPath: "text"}}
	result, err := RenderCustomProtocolRequest(req, config)
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func TestToolFidelityNativeStream(t *testing.T) {
	frames := []string{
		`{"type":"response.created","response":{"id":"r","model":"m","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"patch","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","item_id":"i1","output_index":0,"delta":"not JSON"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"patch","input":"not JSON"}}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"type":"custom_tool_call","id":"i1","call_id":"c1","name":"patch","input":"not JSON"}],"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":50}}}}`,
	}
	decoder := NewMaheshvaraStreamDecoder(FormatResponses)
	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatResponses, writer, "m")
	for _, frame := range frames {
		events, err := decoder.Decode(SSEEvent{Data: frame})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if err := renderer.Write(&event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := renderer.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	var output []any
	for _, line := range strings.Split(writer.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			output = append(output, cacheJSON(t, []byte(strings.TrimPrefix(line, "data: "))))
		}
	}
	if len(output) != len(frames) {
		t.Fatalf("lost/duplicated events: %s", writer.String())
	}
	for index, frame := range frames {
		if !reflect.DeepEqual(output[index], cacheJSON(t, []byte(frame))) {
			t.Fatalf("event %d changed", index)
		}
	}
	foreign := NewMaheshvaraStreamRenderer(FormatClaude, &captureStreamWriter{}, "m")
	events, err := NewMaheshvaraStreamDecoder(FormatResponses).Decode(SSEEvent{Data: frames[1]})
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.Write(&events[0]); err == nil {
		t.Fatal("foreign native tool silently dropped")
	}
}

func TestToolFidelityResponsesDefinitions(t *testing.T) {
	body := `{"model":"m","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false},{"type":"custom","name":"patch","format":{"type":"text"}},{"type":"web_search_preview","search_context_size":"high","user_location":{"type":"approximate","country":"GB"}},{"type":"mcp","server_label":"docs","server_url":"https://example.invalid/mcp"},{"type":"local_shell"}]}`
	req, _, err := OpenAIResponsesToMaheshvara([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"", "responses"} {
		t.Run("shape="+shape, func(t *testing.T) {
			output, err := renderToolFixture(t, req, FormatResponses, shape)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cacheJSON(t, output)["tools"], cacheJSON(t, []byte(body))["tools"]) {
				t.Fatalf("tool definitions changed: %s", output)
			}
		})
	}
}

func TestToolFidelityFlatChatAndChoice(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(`{"model":"m","messages":[],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Tools[0].Name != "lookup" {
		t.Fatal("flat function name missing from semantics")
	}
	for _, shape := range []string{"", "responses"} {
		output, err := renderToolFixture(t, req, FormatResponses, shape)
		if err != nil {
			t.Fatal(err)
		}
		choice := cacheJSON(t, output)["tool_choice"]
		if !reflect.DeepEqual(choice, map[string]any{"type": "function", "name": "lookup"}) {
			t.Fatalf("shape %q choice: %#v", shape, choice)
		}
	}
}

func TestToolFidelityRejectsMissingNameAndForeignTools(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","messages":[],"tools":[{"input_schema":{"type":"object"}}]}`,
		`{"model":"m","messages":[],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":3}]}`,
	} {
		req, err := AnthropicToMaheshvara([]byte(body))
		if err != nil {
			if !strings.Contains(err.Error(), "invalid_tool:") {
				t.Fatal(err)
			}
			continue
		}
		for _, shape := range []string{"", "responses"} {
			if _, err := renderToolFixture(t, req, FormatResponses, shape); err == nil {
				t.Errorf("shape %q silently accepted invalid/foreign tool: %s", shape, body)
			}
		}
	}
}

func TestToolFidelityNativeClaudeTool(t *testing.T) {
	body := []byte(`{"model":"m","messages":[],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":3}]}`)
	req, err := AnthropicToMaheshvara(body)
	if err != nil {
		t.Fatal(err)
	}
	output, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cacheJSON(t, output)["tools"], cacheJSON(t, body)["tools"]) {
		t.Fatalf("native hosted tool changed: %s", output)
	}
}

func TestToolFidelityEditsOverrideOriginal(t *testing.T) {
	req, original, err := OpenAIResponsesToMaheshvara([]byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"old","parameters":{"type":"object"},"vendor_option":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Tools[0].Name = "new"
	output, err := MaheshvaraToOpenAIResponses(req, original)
	if err != nil {
		t.Fatal(err)
	}
	tool := cacheJSON(t, output)["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "new" || tool["vendor_option"] != false {
		t.Fatalf("semantic edit/extension lost: %s", output)
	}
	req.Tools = nil
	output, err = MaheshvaraToOpenAIResponses(req, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasTools := cacheJSON(t, output)["tools"]; hasTools {
		t.Fatalf("deleted tools resurrected: %s", output)
	}
}

func TestToolFidelityCustomCallHistory(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"custom_tool_call","call_id":"c1","name":"patch","input":"free text\nnot JSON"},{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}]}`)
	req, _, err := OpenAIResponsesToMaheshvara(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"", "responses"} {
		output, err := renderToolFixture(t, req, FormatResponses, shape)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cacheJSON(t, output)["input"], cacheJSON(t, body)["input"]) {
			t.Fatalf("custom history changed: %s", output)
		}
	}
	for _, target := range []FormatType{FormatOpenAIChat, FormatClaude, FormatGemini} {
		if _, err := MaheshvaraToTargetRequest(req, target, nil); err == nil {
			t.Errorf("%s silently dropped free text calls", target)
		}
	}
}

func TestToolFidelityCustomResponse(t *testing.T) {
	var response OpenAIResponsesResponse
	if err := json.Unmarshal([]byte(`{"id":"r1","model":"m","status":"completed","output":[{"type":"custom_tool_call","call_id":"c1","name":"patch","input":"not JSON","vendor_option":0}]}`), &response); err != nil {
		t.Fatal(err)
	}
	semantic, err := OpenAIResponsesResponseToMaheshvara(&response)
	if err != nil {
		t.Fatal(err)
	}
	output, err := MaheshvaraToOpenAIResponsesResponse(semantic)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"input":"not JSON"`) || !strings.Contains(string(encoded), `"vendor_option":0`) {
		t.Fatalf("custom response lost: %s", encoded)
	}
	if _, err := MaheshvaraToAnthropicResponse(semantic); err == nil {
		t.Fatal("foreign free text tool silently dropped")
	}
}

func TestToolFidelityCustomMappingSeparatesInput(t *testing.T) {
	config := CustomProtocolConfig{ID: "independent-tools", Request: CustomProtocolRequest{PathTemplate: "/generate", BodyTemplate: `{}`}, Response: CustomProtocolResponse{ToolCallsPath: "result.actions"}}
	semantic, err := CustomProtocolResponseToMaheshvara([]byte(`{"result":{"actions":[{"type":"custom_tool_call","id":"item1","call_id":"call1","name":"patch","input":"not JSON"}]}}`), config)
	if err != nil {
		t.Fatal(err)
	}
	if semantic.Output[0].CallID != "call1" || semantic.Output[0].Input != "not JSON" || len(semantic.Output[0].Arguments) != 0 {
		t.Fatalf("free text coerced to JSON or call identity lost: %+v", semantic.Output[0])
	}
	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatResponses, writer, "m")
	if err := renderer.WriteResponse(semantic); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.String(), `"input":"not JSON"`) || !strings.Contains(writer.String(), `"call_id":"call1"`) {
		t.Fatalf("custom output lost: %s", writer.String())
	}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.Decode(SSEEvent{Data: `{"type":"response.custom_tool_call_input.delta","delta":"not JSON"}`}); err == nil {
		t.Fatal("legacy stream mapping silently skipped free text tool input")
	}
}
