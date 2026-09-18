package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// aliases:用量键名整体替换,条目支持点路径(prompt_tokens_details.cached_tokens)。
func TestCustomProtocolUsageAliases(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID:      "alias-usage",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath:  "text",
			UsagePath: "usage",
		},
		Aliases: &CustomProtocolAliases{Usage: map[string][]string{
			"input":  {"inTokens"},
			"output": {"outTokens"},
			"cached": {"prompt_tokens_details.cached_tokens"},
		}},
	}
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(`{
		"text": "hi",
		"usage": {"inTokens": 3, "outTokens": 4, "prompt_tokens_details": {"cached_tokens": 2}}
	}`), protocol)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if mapped.Usage == nil || mapped.Usage.InputTokens != 3 || mapped.Usage.OutputTokens != 4 || mapped.Usage.CachedInputTokens != 2 {
		t.Fatalf("usage aliases must apply incl. dotted paths: %#v", mapped.Usage)
	}
	// 提供即整体替换:默认键名 prompt_tokens 不再生效。
	mapped, err = CustomProtocolResponseToMaheshvara([]byte(`{"text":"hi","usage":{"prompt_tokens":9,"inTokens":3,"outTokens":4}}`), protocol)
	if err != nil {
		t.Fatalf("map 2: %v", err)
	}
	if mapped.Usage.InputTokens != 3 {
		t.Fatalf("replaced alias list must ignore prompt_tokens: %#v", mapped.Usage)
	}
}

// aliases:工具调用键名替换(嵌套 function.* 形状可自定义)。
func TestCustomProtocolToolCallAliases(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID:      "alias-tool",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath:      "text",
			ToolCallsPath: "calls",
		},
		Aliases: &CustomProtocolAliases{ToolCall: map[string][]string{
			"id":        {"ref"},
			"name":      {"fn"},
			"arguments": {"params"},
		}},
	}
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(`{
		"text": "hi",
		"calls": [{"ref": "c1", "fn": "get_weather", "params": {"city": "sh"}}]
	}`), protocol)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(mapped.Output) != 2 {
		t.Fatalf("text + tool call expected: %#v", mapped.Output)
	}
	call := mapped.Output[1]
	if call.Type != MaheshvaraOutputFunctionCall || call.CallID != "c1" || call.Name != "get_weather" || string(call.Arguments) != `{"city":"sh"}` {
		t.Fatalf("tool aliases must apply: %#v", call)
	}
}

// aliases:textKeys 替换文本提取魔键。
func TestCustomProtocolTextKeysAliases(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID:      "alias-text",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath: "data",
		},
		Aliases: &CustomProtocolAliases{TextKeys: []string{"utterance"}},
	}
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(`{"data": [{"utterance": "你好"}]}`), protocol)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(mapped.Output) == 0 || mapped.Output[0].Content[0].Text != "你好" {
		t.Fatalf("custom textKeys must drive extraction: %#v", mapped.Output)
	}
}

// textFilter:textPath 指向对象数组时按元素过滤(Anthropic 式 thinking/text 块分离)。
func TestCustomProtocolTextFilter(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID:      "filter-text",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{
			TextPath:        "content",
			TextFilter:      CustomProtocolMatchSet{{Path: "type", Op: MatchOpEquals, Value: []byte(`"text"`)}},
			ReasoningPath:   "content",
			ReasoningFilter: CustomProtocolMatchSet{{Path: "type", Op: MatchOpEquals, Value: []byte(`"thinking"`)}},
		},
	}
	mapped, err := CustomProtocolResponseToMaheshvara([]byte(`{"content": [
		{"type": "thinking", "text": "let me think"},
		{"type": "text", "text": "answer"},
		{"type": "text", "text": "!"}
	]}`), protocol)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(mapped.Output) != 2 {
		t.Fatalf("text + reasoning items expected: %#v", mapped.Output)
	}
	if mapped.Output[0].Type != MaheshvaraOutputMessage || mapped.Output[0].Content[0].Text != "answer!" {
		t.Fatalf("text filter must join only text blocks: %#v", mapped.Output[0])
	}
	if mapped.Output[1].Type != MaheshvaraOutputReasoning || mapped.Output[1].Content[0].ReasoningText != "let me think" {
		t.Fatalf("reasoning filter must isolate thinking blocks: %#v", mapped.Output[1])
	}
}

// 请求体条件包含:when 条件不成立时整个键省略——「enable_thinking 仅在开启
// 思考时携带」;omitIf 值等省略(false/0 场景)。
func TestCustomProtocolBodyWhenAndOmitIf(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "conditional-body",
		Request: CustomProtocolRequest{
			Method: "POST", PathTemplate: "/x",
			Body: json.RawMessage(`{
				"model": {"field": "model", "mode": "string"},
				"parameters": {
					"enable_thinking": {"field": "thinking.enabled", "when": {"path": "thinking.enabled", "op": "isTrue"}},
					"verbosity": {"field": "verbosity", "mode": "string", "omitIf": "medium"}
				}
			}`),
		},
		Response: CustomProtocolResponse{TextPath: "text"},
	}
	if err := ValidateCustomProtocol(protocol); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// thinking 未开启 → enable_thinking 整键省略;verbosity=medium → omitIf 命中。
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m", Verbosity: "medium"}, protocol)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(rendered.Body), "enable_thinking") {
		t.Fatalf("enable_thinking must be omitted when thinking disabled: %s", rendered.Body)
	}
	if strings.Contains(string(rendered.Body), "verbosity") {
		t.Fatalf("verbosity=medium must hit omitIf: %s", rendered.Body)
	}

	// thinking 开启 → 携带 true;verbosity 其他值保留。
	req := &MaheshvaraRequest{Model: "m", Verbosity: "high", Thinking: &MaheshvaraThinking{Enabled: true}}
	rendered, err = RenderCustomProtocolRequest(req, protocol)
	if err != nil {
		t.Fatalf("render 2: %v", err)
	}
	if !strings.Contains(string(rendered.Body), `"enable_thinking":true`) {
		t.Fatalf("enable_thinking must carry true when enabled: %s", rendered.Body)
	}
	if !strings.Contains(string(rendered.Body), `"verbosity":"high"`) {
		t.Fatalf("verbosity=high must survive omitIf: %s", rendered.Body)
	}
}

// 模板值过滤器:|bool |int |string 把解析值收敛为指定类型后嵌入。
func TestCustomTemplateValueFilters(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "filter-template",
		Request: CustomProtocolRequest{
			Method: "POST", PathTemplate: "/x",
			BodyTemplate: `{"model":{{maheshvara.model | json}},"flag":{{maheshvara.stream | bool}},"count":{{maheshvara.max_output_tokens | int | default:0}},"label":"n={{maheshvara.n | int | default:1}}"}`,
		},
		Response: CustomProtocolResponse{TextPath: "text"},
	}
	if err := ValidateCustomProtocol(protocol); err != nil {
		t.Fatalf("validate: %v", err)
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m", Stream: true, MaxOutputTokens: 128}, protocol)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(rendered.Body)
	for _, want := range []string{`"flag":true`, `"count":128`, `"label":"n=1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("filter output missing %s: %s", want, body)
		}
	}
}

// stream.modes:文本累计、工具参数增量混用(全局 mode 被 per-family 覆盖)。
func TestCustomProtocolStreamPerFamilyModes(t *testing.T) {
	decoder, err := NewCustomProtocolStreamDecoder(CustomProtocolConfig{
		ID:      "mixed-modes",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", BodyTemplate: `{"m":{{maheshvara.model | json}}}`},
		Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{
			Mode:  "cumulative",
			Modes: &CustomProtocolStreamModes{Arguments: "delta"},
			Response: &CustomProtocolResponse{
				TextPath:      "text",
				ToolCallsPath: "calls",
			},
		}},
	})
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	events, _, err := decoder.Decode(SSEEvent{Data: `{"text":"你好","calls":[{"id":"c1","name":"f","arguments":{"a":1}}]}`})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	second, _, err := decoder.Decode(SSEEvent{Data: `{"text":"你好，世界","calls":[{"id":"c1","name":"f","arguments":{"b":2}}]}`})
	if err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	joined := append(append([]MaheshvaraStreamEvent{}, events...), second...)
	var sawTextDiff, sawArgsDelta bool
	for _, event := range joined {
		if event.Type == MaheshvaraEventTextDelta && event.Delta == "，世界" {
			sawTextDiff = true // cumulative:第二帧只差分出新片段
		}
		if event.Type == MaheshvaraEventFunctionCallArgumentsDelta && strings.Contains(string(event.ToolArgumentsDelta), `"b"`) {
			sawArgsDelta = true // delta:第二帧参数对象原样作为增量(非差分)
		}
	}
	if !sawTextDiff || !sawArgsDelta {
		t.Fatalf("per-family modes must mix cumulative text with delta args: %#v", joined)
	}
}

// DBG-016 回归:omitIf 对渲染后的最终值比较——default 生效的 0 也要被省略。
func TestCustomProtocolOmitIfComparesRenderedValue(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "omitif-rendered",
		Request: CustomProtocolRequest{
			Method: "POST", PathTemplate: "/x",
			Body: json.RawMessage(`{"flag":{"field":"seed","default":0,"omitIf":0}}`),
		},
		Response: CustomProtocolResponse{TextPath: "text"},
	}
	if err := ValidateCustomProtocol(protocol); err != nil {
		t.Fatalf("validate: %v", err)
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m"}, protocol)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(rendered.Body), "flag") {
		t.Fatalf("omitIf must fire on the rendered default: %s", rendered.Body)
	}
}

// DBG-017 回归:同父数组的多个条件项全部命中时,逆序删除不漏删。
func TestCustomProtocolConditionalArrayDeletion(t *testing.T) {
	protocol := CustomProtocolConfig{
		ID: "cond-array",
		Request: CustomProtocolRequest{
			Method: "POST", PathTemplate: "/x",
			Body: json.RawMessage(`{"items":[
				{"field":"model","when":{"path":"stream","op":"isTrue"}},
				{"field":"model","when":{"path":"stream","op":"isTrue"}}]}`),
		},
		Response: CustomProtocolResponse{TextPath: "text"},
	}
	rendered, err := RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m", Stream: false}, protocol)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(rendered.Body), "items") {
		t.Fatalf("both conditional items must be removed in order: %s", rendered.Body)
	}
	if string(rendered.Body) != `{}` {
		t.Fatalf("expected empty object, got %s", rendered.Body)
	}
	// 条件成立时全部保留。
	rendered, err = RenderCustomProtocolRequest(&MaheshvaraRequest{Model: "m", Stream: true}, protocol)
	if err != nil {
		t.Fatalf("render 2: %v", err)
	}
	if !strings.Contains(string(rendered.Body), `"m"`) || strings.Count(string(rendered.Body), `"m"`) != 2 {
		t.Fatalf("both items must survive when the condition holds: %s", rendered.Body)
	}
}
