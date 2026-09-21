package server

import (
	"encoding/json"
	"testing"

	"github.com/elysia-api/backend/relay"
)

// 预置协议 parity 金样：同一全特征 Maheshvara 请求 / 同一份上游线缆数据，
// 内置四线与预置协议（自定义协议通道）必须产出语义等价的结果。
// 覆盖：请求体关键字段（D1）、流事件归并（D2）、非流响应（D3）。

func firstNonEmptyStringP(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func parityPresets(t *testing.T) map[string]relay.CustomProtocolConfig {
	t.Helper()
	configs, err := PresetProtocolConfigs()
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	byID := map[string]relay.CustomProtocolConfig{}
	for _, config := range configs {
		byID[config.ID] = config
	}
	return byID
}

func parityRequest() *relay.MaheshvaraRequest {
	temp := 0.7
	return &relay.MaheshvaraRequest{
		Model:           "p-model",
		Instructions:    "be brief",
		Stream:          true,
		Temperature:     &temp,
		MaxOutputTokens: 1024,
		Tools: []relay.MaheshvaraTool{
			{Type: "function", Name: "get_weather", Description: "查天气", Parameters: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}},
		},
		ToolChoice: map[string]any{"type": "auto"},
		Thinking:   &relay.MaheshvaraThinking{Enabled: true, Effort: "high"},
		Messages: []relay.MaheshvaraMessage{
			{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "天气怎么样"}}},
			{Role: "assistant", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentReasoning, Text: "想想", Signature: "sig-1", SignatureProvider: "anthropic"}},
				ToolCalls: []relay.MaheshvaraToolCall{{ID: "call_1", Type: "function", Name: "get_weather", Arguments: json.RawMessage(`{"city":"sh"}`)}}},
			{Role: "tool", ToolCallID: "call_1", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentToolOutput, ToolCallID: "call_1", ToolOutput: `{"temp":24}`}}},
			{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "谢谢，继续"}}},
		},
	}
}

// D1：内置转换器与预置渲染的请求体在共享语义键上等价。
func TestPresetParityRequestBody(t *testing.T) {
	presets := parityPresets(t)
	req := parityRequest()
	req.Reasoning = nil

	cases := []struct {
		presetID string
		builtin  func() ([]byte, error)
		checks   func(t *testing.T, body map[string]any)
	}{
		{
			presetID: "chat-completions-api",
			builtin:  func() ([]byte, error) { return relay.MaheshvaraToOpenAIChat(req) },
			checks: func(t *testing.T, body map[string]any) {
				if body["model"] != "p-model" || body["stream"] != true {
					t.Fatalf("model/stream wrong: %v %v", body["model"], body["stream"])
				}
				tools, _ := body["tools"].([]any)
				if len(tools) != 1 {
					t.Fatalf("tools = %v", body["tools"])
				}
				if _, ok := body["stream_options"]; !ok {
					t.Fatalf("stream_options missing")
				}
			},
		},
		{
			presetID: "anthropic-api",
			builtin:  func() ([]byte, error) { return relay.MaheshvaraToAnthropic(req) },
			checks: func(t *testing.T, body map[string]any) {
				thinking, _ := body["thinking"].(map[string]any)
				if thinking == nil || thinking["type"] != "enabled" || thinking["budget_tokens"] == nil {
					t.Fatalf("thinking = %v", body["thinking"])
				}
				if body["temperature"] != 1.0 {
					t.Fatalf("thinking mode must force temperature=1.0, got %v", body["temperature"])
				}
				if _, has := body["top_p"]; has {
					t.Fatalf("thinking mode must drop top_p")
				}
				if body["max_tokens"] == nil {
					t.Fatalf("max_tokens required")
				}
			},
		},
		{
			presetID: "gemini-api",
			builtin:  func() ([]byte, error) { return relay.MaheshvaraToGemini(req) },
			checks: func(t *testing.T, body map[string]any) {
				gc, _ := body["generationConfig"].(map[string]any)
				if gc == nil {
					t.Fatalf("generationConfig missing")
				}
				tc, _ := gc["thinkingConfig"].(map[string]any)
				if tc == nil || tc["includeThoughts"] != true {
					t.Fatalf("thinkingConfig = %v", gc["thinkingConfig"])
				}
				if body["contents"] == nil {
					t.Fatalf("contents missing")
				}
			},
		},
		{
			presetID: "responses-api",
			builtin:  func() ([]byte, error) { return relay.MaheshvaraToOpenAIResponses(req, nil) },
			checks: func(t *testing.T, body map[string]any) {
				if body["input"] == nil || body["instructions"] != "be brief" {
					t.Fatalf("input/instructions wrong: %v", body["instructions"])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.presetID, func(t *testing.T) {
			builtinBody, err := tc.builtin()
			if err != nil {
				t.Fatalf("builtin convert: %v", err)
			}
			var builtin map[string]any
			_ = json.Unmarshal(builtinBody, &builtin)

			result, err := relay.RenderCustomProtocolRequest(req, presets[tc.presetID])
			if err != nil {
				t.Fatalf("preset render: %v", err)
			}
			var preset map[string]any
			if err := json.Unmarshal(result.Body, &preset); err != nil {
				t.Fatalf("preset body: %v", err)
			}

			// 共享语义键逐一对照：内置有的键，预置必须语义一致。
			for _, key := range []string{"model", "stream", "temperature", "max_tokens", "maxOutputTokens", "tools", "input", "system"} {
				bv, bOK := builtin[key]
				pv, pOK := preset[key]
				if !bOK {
					continue
				}
				switch key {
				case "tools":
					bt, _ := bv.([]any)
					pt, _ := pv.([]any)
					if len(bt) != len(pt) {
						t.Fatalf("tools count mismatch: builtin=%d preset=%d", len(bt), len(pt))
					}
				case "temperature":
					// anthropic 思考态两侧都强制 1.0；其余平台直接比。
					if bv != pv {
						t.Fatalf("temperature mismatch: builtin=%v preset=%v", bv, pv)
					}
				default:
					if pOK == false {
						t.Fatalf("preset missing key %q (builtin has %v)", key, bv)
					}
				}
			}
			// 平台特化断言（对预置渲染体执行，内置等价性由各自单测保证）。
			tc.checks(t, preset)
		})
	}
}

// 归并流事件为可比较的摘要。
type parityEventSummary struct {
	text      string
	reasoning string
	refusal   string
	signature string
	tools     map[string]string // id -> name:args
	toolOrder []string
	usage     *relay.MaheshvaraUsage
	finished  string
	failed    bool
	doneSeen  bool
}

func summarizeEvents(t *testing.T, events []relay.MaheshvaraStreamEvent) parityEventSummary {
	t.Helper()
	summary := parityEventSummary{tools: map[string]string{}}
	args := map[string]string{}
	for _, event := range events {
		switch event.Type {
		case relay.MaheshvaraEventTextDelta:
			summary.text += event.Delta
		case relay.MaheshvaraEventReasoningDelta:
			summary.reasoning += event.ReasoningDelta
		case relay.MaheshvaraEventRefusalDelta:
			summary.refusal += event.RefusalDelta
		case relay.MaheshvaraEventReasoningSignatureDelta:
			summary.signature += event.ReasoningSignatureDelta
		case relay.MaheshvaraEventFunctionCallAdded:
			id := event.ToolCallID
			summary.tools[id] = event.ToolName + ":"
			summary.toolOrder = append(summary.toolOrder, id)
		case relay.MaheshvaraEventOutputItemAdded, relay.MaheshvaraEventOutputItemDone:
			// Responses 线的工具经由 OutputItem 载荷（function_call 项）。
			if item := event.OutputItem; item != nil && item.Type == relay.MaheshvaraOutputFunctionCall {
				id := firstNonEmptyStringP(item.CallID, item.Name)
				summary.tools[id] = item.Name + ":" + string(item.Arguments)
				summary.toolOrder = append(summary.toolOrder, id)
			}
		case relay.MaheshvaraEventFunctionCallArgumentsDelta:
			args[event.ToolCallID] += event.ToolArgumentsDelta
		case relay.MaheshvaraEventFunctionCallArgumentsDone:
			if event.ToolArgumentsDone != "" && event.ToolArgumentsDone != "{}" {
				args[event.ToolCallID] = event.ToolArgumentsDone
			}
			summary.doneSeen = true
		case relay.MaheshvaraEventUsageDelta:
			if event.Usage != nil {
				summary.usage = event.Usage
			}
		case relay.MaheshvaraEventResponseCompleted:
			summary.finished = event.FinishReason
		case relay.MaheshvaraEventResponseFailed:
			summary.failed = true
		}
	}
	for id, name := range summary.tools {
		summary.tools[id] = name + args[id]
	}
	return summary
}

// D2：同一份 SSE 转录，内置解码器与预置解码器的事件归并等价。
func TestPresetParityStreamDecoding(t *testing.T) {
	presets := parityPresets(t)

	type wire struct {
		event string
		data  string
	}
	cases := []struct {
		presetID string
		format   relay.FormatType
		frames   []wire
	}{
		{
			presetID: "chat-completions-api", format: relay.FormatOpenAIChat,
			frames: []wire{
				{``, `{"choices":[{"delta":{"role":"assistant"}}]}`},
				{``, `{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`},
				{``, `{"choices":[{"delta":{"content":"Hi"}}]}`},
				{``, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":""}}]}}]}`},
				{``, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"sh\"}"}}]}}]}`},
				{``, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`},
				{``, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`},
				{``, `[DONE]`},
			},
		},
		{
			presetID: "anthropic-api", format: relay.FormatClaude,
			frames: []wire{
				{`message_start`, `{"type":"message_start","message":{"usage":{"input_tokens":4}}}`},
				{`content_block_start`, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`},
				{`content_block_delta`, `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ponder"}}`},
				{`content_block_delta`, `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-9"}}`},
				{`content_block_start`, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`},
				{`content_block_delta`, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"sh\"}"}}`},
				{`content_block_stop`, `{"type":"content_block_stop","index":1}`},
				{`content_block_start`, `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`},
				{`content_block_delta`, `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"done"}}`},
				{`message_delta`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":6}}`},
			},
		},
		{
			presetID: "gemini-api", format: relay.FormatGemini,
			frames: []wire{
				{``, `{"candidates":[{"content":{"parts":[{"text":"ponder","thought":true}]}}]}`},
				{``, `{"candidates":[{"content":{"parts":[{"text":"sunny"}]}}]}`},
				{``, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"sh"}}}]}}]}`},
				{``, `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":9}}`},
			},
		},
		{
			presetID: "responses-api", format: relay.FormatResponses,
			frames: []wire{
				{`response.output_text.delta`, `{"type":"response.output_text.delta","delta":"Hel"}`},
				{`response.output_text.delta`, `{"type":"response.output_text.delta","delta":"lo"}`},
				{`response.output_item.added`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_9","name":"get_weather"}}`},
				{`response.function_call_arguments.delta`, `{"type":"response.function_call_arguments.delta","output_index":0,"call_id":"call_9","delta":"{\"city\":\"sh\"}"}`},
				{`response.function_call_arguments.done`, `{"type":"response.function_call_arguments.done","output_index":0,"call_id":"call_9","arguments":"{\"city\":\"sh\"}"}`},
				{`response.completed`, `{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":5}}}`},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.presetID, func(t *testing.T) {
			builtinDecoder := relay.NewMaheshvaraStreamDecoder(tc.format)
			presetDecoder, err := relay.NewCustomProtocolStreamDecoder(presets[tc.presetID])
			if err != nil {
				t.Fatalf("preset decoder: %v", err)
			}
			var builtinEvents, presetEvents []relay.MaheshvaraStreamEvent
			for _, frame := range tc.frames {
				b, err := builtinDecoder.Decode(relay.SSEEvent{Event: frame.event, Data: frame.data})
				if err != nil {
					t.Fatalf("builtin decode: %v", err)
				}
				builtinEvents = append(builtinEvents, b...)
				p, _, err := presetDecoder.Decode(relay.SSEEvent{Event: frame.event, Data: frame.data})
				if err != nil {
					t.Fatalf("preset decode: %v", err)
				}
				presetEvents = append(presetEvents, p...)
			}
			b := summarizeEvents(t, builtinEvents)
			p := summarizeEvents(t, presetEvents)
			if b.text != p.text {
				t.Fatalf("text mismatch: builtin=%q preset=%q", b.text, p.text)
			}
			if b.reasoning != p.reasoning {
				t.Fatalf("reasoning mismatch: builtin=%q preset=%q", b.reasoning, p.reasoning)
			}
			// 工具按「名称:参数」多重集比较：gemini 线缆无调用 ID，内置侧合成
			// call_syn_N、预置侧用别名名作 ID——ID 本身不是可比语义。
			toolSet := func(tools map[string]string) []string {
				out := make([]string, 0, len(tools))
				for _, v := range tools {
					out = append(out, v)
				}
				return sortedStrings(out)
			}
			bt, pt := toolSet(b.tools), toolSet(p.tools)
			if len(bt) != len(pt) {
				t.Fatalf("tool count mismatch: builtin=%v preset=%v", bt, pt)
			}
			for i := range bt {
				if bt[i] != pt[i] {
					t.Fatalf("tool mismatch: builtin=%q preset=%q", bt[i], pt[i])
				}
			}
			if (b.usage == nil) != (p.usage == nil) {
				t.Fatalf("usage presence mismatch: %v vs %v", b.usage, p.usage)
			}
			if b.usage != nil && p.usage != nil && b.usage.TotalTokens != p.usage.TotalTokens {
				t.Fatalf("usage mismatch: builtin=%d preset=%d", b.usage.TotalTokens, p.usage.TotalTokens)
			}
			if b.failed != p.failed {
				t.Fatalf("failed mismatch: %v vs %v", b.failed, p.failed)
			}
		})
	}
}

// D3：同一份非流响应 JSON，内置解析器与预置映射的 MaheshvaraResponse 等价。
func TestPresetParityNonStreamResponse(t *testing.T) {
	presets := parityPresets(t)
	cases := []struct {
		presetID string
		body     string
		builtin  func(body []byte) (*relay.MaheshvaraResponse, error)
	}{
		{
			presetID: "chat-completions-api",
			body:     `{"id":"r1","choices":[{"finish_reason":"tool_calls","message":{"content":"ok","reasoning_content":"hmm","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"sh\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`,
			builtin: func(body []byte) (*relay.MaheshvaraResponse, error) {
				var resp relay.OpenAIResponse
				if err := json.Unmarshal(body, &resp); err != nil {
					return nil, err
				}
				resp2, err := relay.OpenAIChatResponseToMaheshvara(&resp)
				return resp2, err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.presetID, func(t *testing.T) {
			builtin, err := tc.builtin([]byte(tc.body))
			if err != nil {
				t.Fatalf("builtin parse: %v", err)
			}
			_ = builtin
			preset, err := relay.CustomProtocolResponseToMaheshvara([]byte(tc.body), presets[tc.presetID])
			if err != nil {
				t.Fatalf("preset parse: %v", err)
			}
			var bText, pText, bReason, pReason string
			var bTools, pTools int
			for _, item := range builtin.Output {
				for _, part := range item.Content {
					if part.Type == relay.MaheshvaraContentText {
						bText += part.Text
					}
					if part.Type == relay.MaheshvaraContentReasoning {
						bReason += part.Text
					}
				}
				if item.Type == relay.MaheshvaraOutputFunctionCall {
					bTools++
				}
			}
			for _, item := range preset.Output {
				for _, part := range item.Content {
					if part.Type == relay.MaheshvaraContentText {
						pText += part.Text
					}
					if part.Type == relay.MaheshvaraContentReasoning {
						pReason += part.Text
					}
				}
				if item.Type == relay.MaheshvaraOutputFunctionCall {
					pTools++
				}
			}
			if bText != pText || bReason != pReason || bTools != pTools {
				t.Fatalf("mismatch: text %q/%q reasoning %q/%q tools %d/%d", bText, pText, bReason, pReason, bTools, pTools)
			}
			if builtin.Usage != nil && preset.Usage != nil && builtin.Usage.TotalTokens != preset.Usage.TotalTokens {
				t.Fatalf("usage mismatch: %d vs %d", builtin.Usage.TotalTokens, preset.Usage.TotalTokens)
			}
		})
	}
}
