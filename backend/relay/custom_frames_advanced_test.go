package relay

import (
	"strings"
	"testing"
)

// 谓词帧:无事件名协议(Gemini data-only 帧)按 JSON 谓词分流
// thought/text/functionCall 部件。
func TestCustomProtocolPredicateFrames(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(CustomProtocolConfig{
		ID: "predicate-frames",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Aliases: &CustomProtocolAliases{ToolCall: map[string][]string{
			"name":      {"functionCall.name"},
			"arguments": {"functionCall.args"},
		}},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Frames: []CustomProtocolStreamFrame{
				{Match: &CustomProtocolMatch{Path: "candidates[0].content.parts[0].thought", Op: MatchOpIsTrue},
					Response: &CustomProtocolResponse{ReasoningPath: "candidates[0].content.parts[0].text"}},
				{Match: &CustomProtocolMatch{Path: "candidates[0].content.parts[0].functionCall", Op: MatchOpNotNull},
					Response: &CustomProtocolResponse{ToolCallsPath: "candidates[0].content.parts"}},
				{Match: &CustomProtocolMatch{Path: "candidates[0].finishReason", Op: MatchOpNotNull},
					Terminal: true,
					Response: &CustomProtocolResponse{UsagePath: "usageMetadata"}},
				// 末位通用谓词帧兜底:普通文本帧(首帧匹配优先)。
				{Match: &CustomProtocolMatch{Path: "candidates[0].content.parts[0].text", Op: MatchOpNonEmpty},
					Response: &CustomProtocolResponse{TextPath: "candidates[0].content.parts[0].text"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}

	thinking, _, err := decoder.Decode(SSEEvent{Data: `{"candidates":[{"content":{"parts":[{"text":"pondering","thought":true}]}}]}`})
	if err != nil {
		t.Fatalf("decode thinking: %v", err)
	}
	if len(thinking) != 1 || thinking[0].Type != MaheshvaraEventReasoningDelta || thinking[0].ReasoningDelta != "pondering" {
		t.Fatalf("thought part must map to reasoning via predicate frame: %#v", thinking)
	}

	tool, _, err := decoder.Decode(SSEEvent{Data: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"sh"}}}]}}]}`})
	if err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	sawCall := false
	for _, event := range tool {
		if event.Type == MaheshvaraEventFunctionCallAdded && event.ToolName == "get_weather" {
			sawCall = true
		}
	}
	if !sawCall {
		t.Fatalf("functionCall part must map via predicate frame + aliases: %#v", tool)
	}

	text, _, err := decoder.Decode(SSEEvent{Data: `{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`})
	if err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if len(text) != 1 || text[0].Type != MaheshvaraEventTextDelta || text[0].Delta != "answer" {
		t.Fatalf("plain text frame must fall through to default mapping: %#v", text)
	}

	finish, _, err := decoder.Decode(SSEEvent{Data: `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7}}`})
	if err != nil {
		t.Fatalf("decode finish: %v", err)
	}
	if !decoder.TerminalReceived() {
		t.Fatal("finish predicate frame must terminate")
	}
	var sawUsage bool
	for _, event := range finish {
		if event.Type == MaheshvaraEventUsageDelta && event.Usage != nil && event.Usage.InputTokens == 5 {
			sawUsage = true
		}
	}
	if !sawUsage {
		t.Fatalf("finish frame usage must map: %#v", finish)
	}
}

// 分帧工具拼装:Anthropic 式 content_block_start(身份) + input_json_delta(参数)
// 按 index 关联,参数片段按 delta 追加。
func TestCustomProtocolSplitFrameToolAssembly(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(CustomProtocolConfig{
		ID: "split-tool",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Frames: []CustomProtocolStreamFrame{
				{Event: "content_block_start",
					Tool: &CustomProtocolStreamTool{IndexPath: "index", IDPath: "content_block.id", NamePath: "content_block.name"}},
				{Event: "content_block_delta", Match: &CustomProtocolMatch{Path: "delta.type", Op: MatchOpEquals, Value: []byte(`"text_delta"`)},
					Response: &CustomProtocolResponse{TextPath: "delta.text"}},
				{Event: "content_block_delta", Match: &CustomProtocolMatch{Path: "delta.type", Op: MatchOpEquals, Value: []byte(`"input_json_delta"`)},
					Tool: &CustomProtocolStreamTool{IndexPath: "index", ArgumentsPath: "delta.partial_json"}},
				{Event: "message_delta", Terminal: true,
					Response: &CustomProtocolResponse{UsagePath: "usage"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}

	if _, _, err := decoder.Decode(SSEEvent{Event: "content_block_start", Data: `{"index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`}); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	// 文本块的身份帧不带工具身份(index 未注册且无 id/name):不产出事件。
	added, _, err := decoder.Decode(SSEEvent{Event: "content_block_start", Data: `{"index":1,"content_block":{"type":"text"}}`})
	if err != nil {
		t.Fatalf("decode text-block start: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("identity-less frame must not emit events: %#v", added)
	}
	fragment1, _, err := decoder.Decode(SSEEvent{Event: "content_block_delta", Data: `{"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`})
	if err != nil {
		t.Fatalf("decode fragment 1: %v", err)
	}
	fragment2, _, err := decoder.Decode(SSEEvent{Event: "content_block_delta", Data: `{"index":0,"delta":{"type":"input_json_delta","partial_json":"\"sh\"}"}}`})
	if err != nil {
		t.Fatalf("decode fragment 2: %v", err)
	}
	text, _, err := decoder.Decode(SSEEvent{Event: "content_block_delta", Data: `{"index":1,"delta":{"type":"text_delta","text":"done"}}`})
	if err != nil {
		t.Fatalf("decode text: %v", err)
	}
	finish, _, err := decoder.Decode(SSEEvent{Event: "message_delta", Data: `{"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":6}}`})
	if err != nil {
		t.Fatalf("decode finish: %v", err)
	}

	joined := fragment1
	joined = append(joined, fragment2...)
	joined = append(joined, text...)
	joined = append(joined, finish...)
	var args strings.Builder
	for _, event := range joined {
		if event.Type == MaheshvaraEventFunctionCallArgumentsDelta {
			args.WriteString(event.ToolArgumentsDelta)
		}
	}
	if args.String() != `{"city":"sh"}` {
		t.Fatalf("split-frame arguments must assemble in order: %q", args.String())
	}
	var sawText, sawUsage bool
	for _, event := range joined {
		if event.Type == MaheshvaraEventTextDelta && event.Delta == "done" {
			sawText = true
		}
		if event.Type == MaheshvaraEventUsageDelta {
			sawUsage = true
		}
	}
	if !sawText || !sawUsage {
		t.Fatalf("text frame and usage must map alongside tool frames: %#v", joined)
	}
	if !decoder.TerminalReceived() {
		t.Fatal("message_delta terminal frame must terminate")
	}
}

// pathStream:流式请求切换到独立路径(Gemini 按流切换动词)。
func TestCustomProtocolPathStream(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "dual-path",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v1beta/models/{{maheshvara.model}}:generateContent",
			PathStream:   "/v1beta/models/{{maheshvara.model}}:streamGenerateContent?alt=sse",
			BodyTemplate: `{"m":{{maheshvara.model | json}}}`,
		},
		Response: CustomProtocolResponse{TextPath: "text"},
	}
	if err := ValidateCustomProtocol(protocol); err != nil {
		t.Fatalf("validate: %v", err)
	}
	plain, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "gemini-x"}, protocol)
	if err != nil {
		t.Fatalf("render plain: %v", err)
	}
	if plain.Path != "/v1beta/models/gemini-x:generateContent" {
		t.Fatalf("non-stream must use path: %s", plain.Path)
	}
	streamed, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "gemini-x", Stream: true}, protocol)
	if err != nil {
		t.Fatalf("render stream: %v", err)
	}
	if streamed.Path != "/v1beta/models/gemini-x:streamGenerateContent?alt=sse" {
		t.Fatalf("stream must switch to pathStream: %s", streamed.Path)
	}
}
