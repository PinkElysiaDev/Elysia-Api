package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSSEEventReaderAssemblesMultilineAndFlushesEOF(t *testing.T) {
	reader := NewSSEEventReader(strings.NewReader("event: message\ndata: {\"answer\":\ndata: \"ok\"}\n\nid: final\ndata: {\"done\":true}"))
	defer reader.Close()

	first, ok, err := reader.Read(context.Background(), DefaultSSEIdleTimeout)
	if err != nil || !ok {
		t.Fatalf("read first event: ok=%v err=%v", ok, err)
	}
	if first.Event != "message" || first.Data != "{\"answer\":\n\"ok\"}" {
		t.Fatalf("unexpected multiline event: %#v", first)
	}
	second, ok, err := reader.Read(context.Background(), DefaultSSEIdleTimeout)
	if err != nil || !ok {
		t.Fatalf("read EOF-flushed event: ok=%v err=%v", ok, err)
	}
	if second.ID != "final" || second.Data != "{\"done\":true}" {
		t.Fatalf("unexpected EOF event: %#v", second)
	}
	_, ok, err = reader.Read(context.Background(), DefaultSSEIdleTimeout)
	if err != nil || ok {
		t.Fatalf("expected clean EOF, ok=%v err=%v", ok, err)
	}
}

func TestTransformStreamViaMaheshvaraOpenAIToAnthropicPreservesParallelTools(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chat_1","model":"upstream","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat_1","model":"upstream","choices":[{"index":0,"delta":{"reasoning_content":"plan"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"alpha","arguments":"{\"a\":"}},{"index":1,"id":"call_b","type":"function","function":{"name":"beta","arguments":"{\"b\":"}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}},{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":null}]}`,
		``,
		`data: {"id":"chat_1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
		`data: {"id":"chat_1","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatOpenAIChat, FormatClaude, writer, "claude-target"); err != nil {
		t.Fatalf("transform stream: %v\n%s", err, writer.String())
	}
	output := writer.String()
	for _, required := range []string{`"type":"thinking_delta"`, `"thinking":"plan"`, `"name":"alpha"`, `"name":"beta"`, `"partial_json":"{\"a\":1}"`, `"partial_json":"{\"b\":2}"`, `"stop_reason":"tool_use"`, `event: message_stop`} {
		if !strings.Contains(output, required) {
			t.Fatalf("missing %s in output:\n%s", required, output)
		}
	}
	if strings.Count(output, `"name":"alpha"`) != 1 || strings.Count(output, `"name":"beta"`) != 1 {
		t.Fatalf("parallel tools were duplicated or reordered into multiple blocks:\n%s", output)
	}
}

func TestTransformStreamViaMaheshvaraAnthropicToGeminiEmitsOnlyValidParts(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude","usage":{"input_tokens":3,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatClaude, FormatGemini, writer, "gemini-target"); err != nil {
		t.Fatalf("transform stream: %v\n%s", err, writer.String())
	}
	payloads := decodeSSEDataObjects(t, writer.String())
	var sawThought bool
	var sawFunction bool
	for _, payload := range payloads {
		candidates, _ := payload["candidates"].([]any)
		for _, candidateValue := range candidates {
			candidate, _ := candidateValue.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, partValue := range parts {
				part, _ := partValue.(map[string]any)
				if !validGeminiStreamPart(part) {
					t.Fatalf("invalid Gemini Part emitted: %#v\n%s", part, writer.String())
				}
				if part["thought"] == true && part["text"] == "reason" {
					sawThought = true
				}
				if call, _ := part["functionCall"].(map[string]any); call != nil {
					sawFunction = call["name"] == "lookup" && part["thoughtSignature"] == geminiCrossProviderThoughtSignature
					if part["thoughtSignature"] == "sig-1" {
						t.Fatalf("Anthropic signature leaked into Gemini function call: %+v", part)
					}
				}
			}
		}
	}
	if !sawThought || !sawFunction {
		t.Fatalf("missing thought or signed function call:\n%s", writer.String())
	}
}

func TestTransformStreamViaMaheshvaraGeminiToResponsesCompletesWithToolsAndUsage(t *testing.T) {
	body := `data: {"responseId":"gem_1","modelVersion":"gemini","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"},{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}}` + "\n\n"
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatGemini, FormatResponses, writer, "responses-target"); err != nil {
		t.Fatalf("transform stream: %v\n%s", err, writer.String())
	}
	output := writer.String()
	for _, required := range []string{`event: response.created`, `event: response.output_text.delta`, `"delta":"hello"`, `event: response.function_call_arguments.done`, `"name":"lookup"`, `event: response.completed`, `"input_tokens":4`, `"output_tokens":3`, `"total_tokens":7`} {
		if !strings.Contains(output, required) {
			t.Fatalf("missing %s in Responses stream:\n%s", required, output)
		}
	}
}

func TestTransformStreamViaMaheshvaraGeminiKeepsSignatureOnSameThoughtPart(t *testing.T) {
	body := `data: {"responseId":"gem_1","modelVersion":"gemini","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"reason","thought":true,"thoughtSignature":"sig-1"}]},"finishReason":"STOP"}]}` + "\n\n"
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatGemini, FormatGemini, writer, "gemini-target"); err != nil {
		t.Fatalf("transform Gemini stream: %v\n%s", err, writer.String())
	}
	payloads := decodeSSEDataObjects(t, writer.String())
	for _, payload := range payloads {
		candidates, _ := payload["candidates"].([]any)
		for _, candidateValue := range candidates {
			candidate, _ := candidateValue.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, partValue := range parts {
				part, _ := partValue.(map[string]any)
				if part["text"] == "reason" {
					if part["thoughtSignature"] != "sig-1" {
						t.Fatalf("thought signature moved to another Part: %+v\n%s", part, writer.String())
					}
					return
				}
			}
		}
	}
	t.Fatalf("signed thought Part was not emitted:\n%s", writer.String())
}

func TestGeminiStreamRendererNormalizesFunctionResponse(t *testing.T) {
	writer := &captureStreamWriter{}
	renderer := NewMaheshvaraStreamRenderer(FormatGemini, writer, "gemini-target")
	part := CanonicalContentPart{
		Type:       CanonicalContentToolOutput,
		ToolCallID: "call_1",
		ToolOutput: `["one","two"]`,
		Raw:        map[string]any{"id": "call_1", "name": "lookup"},
	}
	if err := renderer.Write(&MaheshvaraStreamEvent{Type: CanonicalEventContentPartAdded, ContentPart: &part}); err != nil {
		t.Fatalf("render function response: %v", err)
	}
	if err := renderer.Finish(); err != nil {
		t.Fatalf("finish Gemini stream: %v", err)
	}
	payloads := decodeSSEDataObjects(t, writer.String())
	for _, payload := range payloads {
		candidates, _ := payload["candidates"].([]any)
		for _, candidateValue := range candidates {
			candidate, _ := candidateValue.(map[string]any)
			content, _ := candidate["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, partValue := range parts {
				part, _ := partValue.(map[string]any)
				response, _ := part["functionResponse"].(map[string]any)
				if response == nil {
					continue
				}
				payload, ok := response["response"].(map[string]any)
				if response["id"] != "call_1" || response["name"] != "lookup" || !ok {
					t.Fatalf("invalid functionResponse payload: %+v", response)
				}
				if result, ok := payload["result"].([]any); !ok || len(result) != 2 {
					t.Fatalf("functionResponse array was not wrapped: %+v", payload)
				}
				return
			}
		}
	}
	t.Fatalf("functionResponse Part was not emitted:\n%s", writer.String())
}

func TestTransformStreamViaMaheshvaraResponsesToOpenAICompletes(t *testing.T) {
	body := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"status":"in_progress","model":"responses","output":[]}}`,
		``,
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		``,
		`event: response.content_part.added`,
		`data: {"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"ok"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"responses","output":[{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatResponses, FormatOpenAIChat, writer, "chat-target"); err != nil {
		t.Fatalf("transform stream: %v\n%s", err, writer.String())
	}
	output := writer.String()
	for _, required := range []string{`"content":"ok"`, `"finish_reason":"stop"`, `"prompt_tokens":2`, `data: [DONE]`} {
		if !strings.Contains(output, required) {
			t.Fatalf("missing %s in Chat stream:\n%s", required, output)
		}
	}
}

func decodeSSEDataObjects(t *testing.T, stream string) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(payload), &object); err != nil {
			t.Fatalf("decode SSE payload %q: %v", payload, err)
		}
		result = append(result, object)
	}
	return result
}
