package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// 三协议客户端 → Responses 上游的工具调用兼容性矩阵:
// 定义(tools/tool_choice 扁平形状)、结果回传(function_call_output 的
// call_id 配对与载荷)必须经真实解析链验证——早期测试手工构造中间形态,
// 绕过了 Raw 透传与 tool_output part 的两个 400 缺口。

// mustJSON 校验 JSON 合法并返回原始字节(入口解析函数收 []byte)。
func mustJSON(t *testing.T, body string) []byte {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("bad json: %v\n%s", err, body)
	}
	return []byte(body)
}

// mustDecode 断言侧:JSON 文本 → map。
func mustDecode(t *testing.T, body string) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("bad json: %v\n%s", err, body)
	}
	return raw
}

// Chat 客户端带工具定义与 tool 结果历史 → Responses 目标:
// tools 必须是扁平 function 形状(嵌套 function 键会被 Responses 400)。
func TestChatToolsToResponsesFlatShape(t *testing.T) {
	req, err := OpenAIChatToMaheshvara(mustJSON(t, `{
		"model": "gpt",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"type": "function", "function": {
			"name": "get_weather",
			"description": "query weather",
			"parameters": {"type": "object", "properties": {"city": {"type": "string"}}},
			"strict": true
		}}],
		"tool_choice": {"type": "function", "function": {"name": "get_weather"}}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	responsesBody, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(responsesBody))

	tools, _ := out["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", out["tools"])
	}
	tool := tools[0].(map[string]any)
	if _, nested := tool["function"]; nested {
		t.Fatalf("Responses tool must be flat, got nested function key: %v", tool)
	}
	if tool["name"] != "get_weather" || tool["type"] != "function" {
		t.Fatalf("flat tool fields wrong: %v", tool)
	}
	params := tool["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Fatalf("parameters lost: %v", tool)
	}
	if tool["strict"] != true {
		t.Fatalf("strict lost: %v", tool)
	}

	choice := out["tool_choice"].(map[string]any)
	if _, nested := choice["function"]; nested {
		t.Fatalf("tool_choice must be flat {type,name}, got: %v", choice)
	}
	if choice["name"] != "get_weather" || choice["type"] != "function" {
		t.Fatalf("tool_choice fields wrong: %v", choice)
	}
}

// Claude 客户端的 tools(input_schema 形状)→ Responses 扁平形状。
func TestClaudeToolsToResponsesFlatShape(t *testing.T) {
	req, err := AnthropicToMaheshvara(mustJSON(t, `{
		"model": "claude",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"name": "lookup", "description": "find", "input_schema": {"type": "object", "properties": {"q": {"type": "string"}}}}],
		"tool_choice": {"type": "tool", "name": "lookup"}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(body))

	tools := out["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "lookup" {
		t.Fatalf("Claude tool must rebuild as flat function, got: %v", tool)
	}
	if tool["parameters"] == nil {
		t.Fatalf("input_schema must map to parameters: %v", tool)
	}
	if _, leak := tool["input_schema"]; leak {
		t.Fatalf("input_schema key must not leak to Responses: %v", tool)
	}

	choice := out["tool_choice"].(map[string]any)
	if _, nested := choice["function"]; nested || choice["name"] != "lookup" {
		t.Fatalf("tool_choice must be flat with name, got: %v", choice)
	}
}

// Gemini functionDeclarations + functionCallingConfig(ANY + 单名)→ Responses。
func TestGeminiToolsToResponsesFlatShape(t *testing.T) {
	req, err := GeminiToMaheshvara(mustJSON(t, `{
		"contents": [{"role": "user", "parts": [{"text": "hi"}]}],
		"tools": [{"functionDeclarations": [{"name": "search", "description": "web", "parameters": {"type": "object", "properties": {"k": {"type": "string"}}}}]}],
		"toolConfig": {"functionCallingConfig": {"mode": "ANY", "allowedFunctionNames": ["search"]}}
	}`), "gem")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	body, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(body))

	tools := out["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "search" {
		t.Fatalf("Gemini tool must rebuild as flat function, got: %v", tool)
	}
	choice := out["tool_choice"].(map[string]any)
	if choice["name"] != "search" {
		t.Fatalf("single allowedFunctionNames must pin the tool, got: %v", choice)
	}
}

// Chat 工具循环历史(assistant tool_calls + role:tool 结果)→ Responses:
// function_call item 与 function_call_output 的 call_id 配对,且载荷非空。
func TestChatToolRoundTripToResponses(t *testing.T) {
	req, err := OpenAIChatToMaheshvara(mustJSON(t, `{
		"model": "gpt",
		"messages": [
			{"role": "user", "content": "weather in Shanghai?"},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Shanghai\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny 28C"}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	responsesBody, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(responsesBody))

	input := out["input"].([]any)
	var sawCall, sawOutput bool
	for _, itemAny := range input {
		item := itemAny.(map[string]any)
		switch item["type"] {
		case "function_call":
			sawCall = true
			if item["call_id"] != "call_1" || item["name"] != "get_weather" {
				t.Fatalf("function_call wrong: %v", item)
			}
		case "function_call_output":
			sawOutput = true
			if item["call_id"] != "call_1" {
				t.Fatalf("output call_id mismatch: %v", item)
			}
			if output, _ := item["output"].(string); output != "sunny 28C" {
				t.Fatalf("tool output payload lost (empty or wrong): %q", output)
			}
		}
	}
	if !sawCall || !sawOutput {
		t.Fatalf("input must contain paired function_call + function_call_output: %v", input)
	}
}

// Claude 工具循环(assistant tool_use + user tool_result)→ Responses:
// 修复前 tool_result 落入 user content 转换的 default 分支被整个丢弃,
// Responses 上游以「No tool output found for function call」400。
func TestClaudeToolRoundTripToResponses(t *testing.T) {
	req, err := AnthropicToMaheshvara(mustJSON(t, `{
		"model": "claude",
		"max_tokens": 64,
		"messages": [
			{"role": "user", "content": "weather?"},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "Shanghai"}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "sunny 28C"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	responsesBody, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(responsesBody))

	input := out["input"].([]any)
	var sawCall, sawOutput bool
	for _, itemAny := range input {
		item := itemAny.(map[string]any)
		switch item["type"] {
		case "function_call":
			sawCall = true
			if item["call_id"] != "toolu_1" {
				t.Fatalf("call_id must preserve tool_use_id, got: %v", item)
			}
		case "function_call_output":
			sawOutput = true
			if item["call_id"] != "toolu_1" {
				t.Fatalf("output call_id mismatch: %v", item)
			}
			if output, _ := item["output"].(string); !strings.Contains(output, "sunny 28C") {
				t.Fatalf("tool_result payload lost: %q", output)
			}
		}
	}
	if !sawCall || !sawOutput {
		t.Fatalf("input must contain paired function_call + function_call_output: %v", input)
	}
}

// Gemini 工具循环(model functionCall + user functionResponse)→ Responses。
func TestGeminiToolRoundTripToResponses(t *testing.T) {
	req, err := GeminiToMaheshvara(mustJSON(t, `{
		"contents": [
			{"role": "user", "parts": [{"text": "weather?"}]},
			{"role": "model", "parts": [{"functionCall": {"name": "get_weather", "args": {"city": "Shanghai"}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "get_weather", "response": {"result": "sunny 28C"}}}]}
		]
	}`), "gem")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	responsesBody, err := MaheshvaraToTargetRequest(req, FormatResponses, nil)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	out := mustDecode(t, string(responsesBody))

	input := out["input"].([]any)
	var sawCall, sawOutput bool
	for _, itemAny := range input {
		item := itemAny.(map[string]any)
		switch item["type"] {
		case "function_call":
			sawCall = true
		case "function_call_output":
			sawOutput = true
			if output, _ := item["output"].(string); !strings.Contains(output, "sunny 28C") {
				t.Fatalf("functionResponse payload lost: %q", output)
			}
		}
	}
	if !sawCall || !sawOutput {
		t.Fatalf("input must contain paired function_call + function_call_output: %v", input)
	}
}

// Responses 上游返回 function_call 时 StopReason 应为 tool_calls
//(否则 Chat 客户端 finish_reason 塌缩为 stop)。
func TestResponsesFunctionCallSetsStopReason(t *testing.T) {
	var resp OpenAIResponsesResponse
	if err := json.Unmarshal([]byte(`{
		"id": "resp_1", "object": "response", "status": "completed", "model": "gpt",
		"output": [{"type": "function_call", "id": "fc_1", "call_id": "call_9", "name": "get_weather", "arguments": "{}"}]
	}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mResp, err := OpenAIResponsesResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if mResp.StopReason != "tool_calls" {
		t.Fatalf("StopReason = %q, want tool_calls", mResp.StopReason)
	}
	// 纯文本响应不受影响。
	var textResp OpenAIResponsesResponse
	if err := json.Unmarshal([]byte(`{"id":"r2","object":"response","status":"completed","model":"gpt","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`), &textResp); err != nil {
		t.Fatal(err)
	}
	m2, _ := OpenAIResponsesResponseToMaheshvara(&textResp)
	if m2.StopReason == "tool_calls" {
		t.Fatalf("text response must not set tool_calls, got %q", m2.StopReason)
	}
}

// Responses 平台映射:apiFormat=responses 的线路返回 FormatResponses。
func TestTargetFormatForResponsesPlatform(t *testing.T) {
	format, err := TargetFormatForPlatform(Platform("responses"))
	if err != nil || format != FormatResponses {
		t.Fatalf("responses platform -> %v, %v; want FormatResponses", format, err)
	}
}

