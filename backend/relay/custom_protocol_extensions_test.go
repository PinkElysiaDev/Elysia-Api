package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// A2/A3/A4 回归：响应新路径（签名/加密/拒答/引用/工具签名）、流完成信号、
// 用量缓存别名。

func decodeSSELine(t *testing.T, decoder *CustomProtocolStreamDecoder, data string) []MaheshvaraStreamEvent {
	t.Helper()
	events, _, err := decoder.Decode(SSEEvent{Data: data})
	if err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return events
}

func decodeSSELineNamed(t *testing.T, decoder *CustomProtocolStreamDecoder, event, data string) []MaheshvaraStreamEvent {
	t.Helper()
	events, _, err := decoder.Decode(SSEEvent{Event: event, Data: data})
	if err != nil {
		t.Fatalf("decode %s: %v", event, err)
	}
	return events
}

func eventTypes(events []MaheshvaraStreamEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func containsEvent(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// 响应映射：signature/encryptedContent/citations/toolCall signature 别名。
func TestCustomResponseNewPaths(t *testing.T) {
	config := CustomProtocolConfig{ID: "ext", Type: "llm",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"model":"{{maheshvara.model}}"}`},
		Response: CustomProtocolResponse{
			TextPath: "content.text", CitationsPath: "content.citations",
			ReasoningPath: "content.thinking", SignaturePath: "content.signature", SignatureProviderPath: "content.provider",
			EncryptedContentPath: "encrypted", ToolCallsPath: "tool_calls",
		},
		Aliases: &CustomProtocolAliases{ToolCall: map[string][]string{"signature": {"thought_signature"}}},
	}
	if err := ValidateCustomProtocol(config); err != nil {
		t.Fatalf("validate: %v", err)
	}
	root := map[string]any{
		"content": map[string]any{
			"text": "答案", "thinking": "思考", "signature": "sig-1", "provider": "anthropic",
			"citations": []any{map[string]any{"cited_text": "x"}},
		},
		"encrypted":  "enc-blob",
		"tool_calls": []any{map[string]any{"id": "c1", "name": "lookup", "arguments": `{"q":1}`, "thought_signature": "sig-tool"}},
	}
	encoded, encodeErr := json.Marshal(root)
	if encodeErr != nil {
		t.Fatalf("marshal fixture: %v", encodeErr)
	}
	response, err := CustomProtocolResponseToMaheshvara(encoded, config)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var sawSignaturePart, sawCitations, sawToolSignature bool
	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Type == MaheshvaraContentReasoning && part.Signature == "sig-1" && part.SignatureProvider == "anthropic" && part.EncryptedContent == "enc-blob" {
				sawSignaturePart = true
			}
			if part.Type == MaheshvaraContentText && strings.Contains(string(part.Citations), "cited_text") {
				sawCitations = true
			}
		}
		for _, call := range item.ToolCalls {
			if call.ThoughtSignature == "sig-tool" {
				sawToolSignature = true
			}
		}
	}
	if !sawSignaturePart {
		t.Fatalf("reasoning signature/encrypted not mapped")
	}
	if !sawCitations {
		t.Fatalf("citations not attached to text part")
	}
	if !sawToolSignature {
		t.Fatalf("tool thought_signature not mapped via alias")
	}
}

// 流：toolDone 帧 → 参数完成事件；签名与拒答事件；[DONE] 终态。
func TestCustomStreamDoneSignatureRefusal(t *testing.T) {
	config := CustomProtocolConfig{ID: "ext-stream", Type: "llm",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"model":"{{maheshvara.model}}"}`},
		Response: CustomProtocolResponse{
			Stream: &CustomProtocolStreamMapping{
				Mode: "delta",
				Frames: []CustomProtocolStreamFrame{
					{Event: "tool_start", Tool: &CustomProtocolStreamTool{IndexPath: "index", IDPath: "id", NamePath: "name"}},
					{Event: "tool_args", Tool: &CustomProtocolStreamTool{IndexPath: "index", ArgumentsPath: "args"}},
					{Event: "tool_stop", ToolDone: true, Tool: &CustomProtocolStreamTool{IndexPath: "index"}},
					{Event: "thinking_delta", Response: &CustomProtocolResponse{ReasoningPath: "thinking"}},
					{Event: "signature", Response: &CustomProtocolResponse{SignaturePath: "signature", SignatureProviderPath: "provider"}},
					{Event: "refusal_frame", Response: &CustomProtocolResponse{RefusalPath: "refusal"}},
				},
			},
		},
	}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	toolEvents := decodeSSELineNamed(t, decoder, "tool_start", `{"index":0,"id":"call_1","name":"lookup"}`)
	if !containsEvent(eventTypes(toolEvents), MaheshvaraEventFunctionCallAdded) {
		t.Fatalf("tool added missing: %v", eventTypes(toolEvents))
	}
	argsEvents := decodeSSELineNamed(t, decoder, "tool_args", `{"index":0,"args":"{\"q\":"}`)
	if !containsEvent(eventTypes(argsEvents), MaheshvaraEventFunctionCallArgumentsDelta) {
		t.Fatalf("args delta missing: %v", eventTypes(argsEvents))
	}
	// toolDone 帧身份只带 index → 经 frameTools 关联到 call_1
	stopEvents := decodeSSELineNamed(t, decoder, "tool_stop", `{"index":0}`)
	foundDone := false
	for _, event := range stopEvents {
		if event.Type == MaheshvaraEventFunctionCallArgumentsDone && event.ToolCallID == "call_1" && strings.HasPrefix(event.ToolArgumentsDone, `{"q":`) {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatalf("toolDone frame must flush arguments.done for call_1: %+v", stopEvents)
	}
	sigEvents := decodeSSELineNamed(t, decoder, "thinking_delta", `{"thinking":"嗯"}`)
	if !containsEvent(eventTypes(sigEvents), MaheshvaraEventReasoningDelta) {
		t.Fatalf("reasoning delta missing: %v", eventTypes(sigEvents))
	}
	signatureEvents := decodeSSELineNamed(t, decoder, "signature", `{"signature":"sig-x","provider":"anthropic"}`)
	foundSig := false
	for _, event := range signatureEvents {
		if event.Type == MaheshvaraEventReasoningSignatureDelta && event.ReasoningSignatureDelta == "sig-x" && event.ReasoningSignatureProvider == "anthropic" {
			foundSig = true
		}
	}
	if !foundSig {
		t.Fatalf("signature delta missing: %+v", signatureEvents)
	}
	refusalEvents := decodeSSELineNamed(t, decoder, "refusal_frame", `{"refusal":"不能帮"}`)
	if !containsEvent(eventTypes(refusalEvents), MaheshvaraEventRefusalDelta) {
		t.Fatalf("refusal delta missing: %v", eventTypes(refusalEvents))
	}
	doneEvents := decodeSSELine(t, decoder, "[DONE]")
	if !containsEvent(eventTypes(doneEvents), MaheshvaraEventResponseCompleted) {
		t.Fatalf("terminal missing: %v", eventTypes(doneEvents))
	}
}

// 终态未显式 done 的工具也要在完成时统一补发参数完成。
func TestCustomStreamTerminalFlushesRemainingTools(t *testing.T) {
	config := CustomProtocolConfig{ID: "ext-flush", Type: "llm",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"model":"{{maheshvara.model}}"}`},
		Response: CustomProtocolResponse{
			TextPath: "delta.content",
			Stream: &CustomProtocolStreamMapping{Mode: "delta", Frames: []CustomProtocolStreamFrame{
				{Match: &CustomProtocolMatch{Path: "delta.tool_calls", Op: "nonEmpty"},
					Tool: &CustomProtocolStreamTool{Path: "delta.tool_calls", IndexPath: "index", IDPath: "id", NamePath: "function.name", ArgumentsPath: "function.arguments"}},
				{Match: &CustomProtocolMatch{Path: "delta", Op: "nonEmpty"},
					Response: &CustomProtocolResponse{TextPath: "delta.content", FinishReasonPath: "delta.finish_reason"}},
			}},
		},
	}
	decoder, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	decodeSSELine(t, decoder, `{"delta":{"tool_calls":[{"index":0,"id":"call_z","function":{"name":"fetch","arguments":"{}"}}]}}`)
	finish := decodeSSELine(t, decoder, `{"delta":{"finish_reason":"tool_calls"}}`)
	found := false
	for _, event := range finish {
		if event.Type == MaheshvaraEventFunctionCallArgumentsDone && event.ToolCallID == "call_z" {
			found = true
		}
	}
	if !found {
		t.Fatalf("terminal must flush remaining tool done: %+v", finish)
	}
}

// 用量别名：cache_creation / cache_read。
func TestCustomUsageCacheCreationAlias(t *testing.T) {
	root := map[string]any{"usage": map[string]any{
		"input_tokens": 100, "output_tokens": 20,
		"cache_creation_input_tokens": 40, "cache_read_input_tokens": 60,
	}}
	usage := customUsageAtWithAliases(root, "usage", nil)
	if usage == nil || usage.CacheCreationInputTokens != 40 || usage.CachedInputTokens != 60 {
		t.Fatalf("usage = %+v", usage)
	}
	aliased := customUsageAtWithAliases(root, "usage", map[string][]string{"cache_creation": {"cache_creation_input_tokens"}})
	if aliased.CacheCreationInputTokens != 40 {
		t.Fatalf("aliased cache_creation = %+v", aliased)
	}
}
