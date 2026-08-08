package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesRequestToCanonicalCoversResponsesSpecificFields(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4.1",
		"instructions":"be concise",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"https://example.com/a.png"},{"type":"input_file","file_id":"file_123","filename":"a.pdf"}]},
			{"type":"function_call_output","call_id":"call_1","output":"{\"ok\":true}"}
		],
		"tools":[
			{"type":"function","name":"lookup","description":"Lookup","parameters":{"type":"object"}},
			{"type":"web_search_preview","search_context_size":"low"},
			{"type":"file_search","vector_store_ids":["vs_1","vs_2"]}
		],
		"reasoning":{"effort":"medium"},
		"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}},
		"previous_response_id":"resp_prev",
		"store":true,
		"include":["reasoning.encrypted_content"],
		"truncation":"auto",
		"background":false,
		"conversation":{"id":"conv_1"},
		"prompt":{"id":"pmpt_1"},
		"metadata":{"trace":"abc"},
		"parallel_tool_calls":true,
		"stream":true,
		"max_output_tokens":321
	}`)

	req, original, err := ResponsesRequestToCanonical(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if original == nil {
		t.Fatal("expected original responses request to be returned")
	}
	if req.Model != "gpt-4.1" || req.Instructions != "be concise" || !req.Stream || req.MaxOutputTokens != 321 {
		t.Fatalf("basic fields were not mapped correctly: %+v", req)
	}
	if req.PreviousResponseID != "resp_prev" || req.Store == nil || !*req.Store || req.Truncation != "auto" {
		t.Fatalf("responses metadata fields were not mapped correctly: %+v", req)
	}
	if len(req.Include) != 1 || req.Include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include was not mapped: %+v", req.Include)
	}
	if req.ParallelToolCalls == nil || !*req.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls was not mapped: %+v", req.ParallelToolCalls)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "medium" || req.Thinking == nil || !req.Thinking.Enabled {
		t.Fatalf("reasoning/thinking were not mapped: %+v %+v", req.Reasoning, req.Thinking)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_schema" || req.ResponseFormat.Name != "answer" {
		t.Fatalf("text.format was not mapped: %+v", req.ResponseFormat)
	}
	if len(req.Tools) != 3 {
		t.Fatalf("expected three tools, got %+v", req.Tools)
	}
	if req.Tools[0].Type != CanonicalToolFunction || req.Tools[0].Name != "lookup" {
		t.Fatalf("function tool was not mapped: %+v", req.Tools[0])
	}
	if req.Tools[1].Type != CanonicalToolWebSearchPreview || req.Tools[1].SearchContextSize != "low" {
		t.Fatalf("web search tool was not mapped: %+v", req.Tools[1])
	}
	if req.Tools[2].Type != CanonicalToolFileSearch || len(req.Tools[2].VectorStoreIDs) != 2 {
		t.Fatalf("file search tool was not mapped: %+v", req.Tools[2])
	}
	if len(req.InputItems) != 2 || len(req.Messages) != 2 {
		t.Fatalf("input items/messages were not mapped: items=%+v messages=%+v", req.InputItems, req.Messages)
	}
	if got := canonicalText(req.InputItems[0].Content); got != "hello" {
		t.Fatalf("expected input text, got %q", got)
	}
	if req.InputItems[1].Type != CanonicalInputFunctionCallOutput || req.InputItems[1].CallID != "call_1" {
		t.Fatalf("function_call_output was not mapped: %+v", req.InputItems[1])
	}
}

func TestOpenAIChatRequestToCanonicalCoversToolsReasoningAndStreamUsage(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"result"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object"}}}],
		"reasoning_effort":"high",
		"stream":true,
		"stream_options":{"include_usage":true},
		"max_tokens":10,
		"max_completion_tokens":20,
		"parallel_tool_calls":true,
		"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":true}}
	}`)

	req, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.MaxOutputTokens != 20 {
		t.Fatalf("expected max_completion_tokens to win, got %d", req.MaxOutputTokens)
	}
	if !req.Stream || req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Fatalf("stream options were not mapped: %+v", req.StreamOptions)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "high" || req.Thinking == nil || !req.Thinking.Enabled {
		t.Fatalf("reasoning was not mapped: %+v %+v", req.Reasoning, req.Thinking)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "lookup" {
		t.Fatalf("tools were not mapped: %+v", req.Tools)
	}
	if len(req.Messages) != 3 || len(req.Messages[1].ToolCalls) != 1 || req.Messages[1].ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls were not mapped: %+v", req.Messages)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.Name != "answer" || req.ResponseFormat.Strict == nil || !*req.ResponseFormat.Strict {
		t.Fatalf("response format was not mapped: %+v", req.ResponseFormat)
	}
}

func TestCanonicalToTargetRequestRejectsBuiltinToolsForChatClaudeGemini(t *testing.T) {
	req := &CanonicalRequest{
		Model:    "model",
		Messages: []CanonicalMessage{{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "hello"}}}},
		Tools:    []CanonicalTool{{Type: CanonicalToolWebSearchPreview}},
	}

	if _, err := CanonicalToOpenAIChatRequest(req); err == nil {
		t.Fatal("expected OpenAI chat conversion to reject builtin tool")
	}
	if _, err := CanonicalToClaudeRequest(req); err == nil {
		t.Fatal("expected Claude conversion to reject builtin tool")
	}
	if _, err := CanonicalToGeminiRequest(req); err == nil {
		t.Fatal("expected Gemini conversion to reject builtin tool")
	}
}

func TestCanonicalToResponsesRequestPreservesRawBuiltinToolsAndReasoning(t *testing.T) {
	store := true
	original := &OpenAIResponsesRequest{Store: &store}
	req := &CanonicalRequest{
		Model:           "gpt-4.1",
		Instructions:    "be concise",
		MaxOutputTokens: 99,
		Stream:          true,
		Messages:        []CanonicalMessage{{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "hello"}}}},
		Tools: []CanonicalTool{{
			Type: CanonicalToolWebSearchPreview,
			Raw:  map[string]any{"type": "web_search_preview", "search_context_size": "medium"},
		}},
		Reasoning:      &CanonicalReasoning{Effort: "low", Raw: map[string]any{"summary": "auto"}},
		ResponseFormat: &CanonicalResponseFormat{Type: "json_schema", Name: "answer", Schema: map[string]any{"type": "object"}},
	}

	body, err := CanonicalToResponsesRequest(req, original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if out["model"] != "gpt-4.1" || out["instructions"] != "be concise" || out["stream"] != true {
		t.Fatalf("basic fields were not emitted: %s", body)
	}
	if out["store"] != true {
		t.Fatalf("original field was not preserved: %s", body)
	}
	tools, _ := out["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search_preview" {
		t.Fatalf("raw builtin tool was not preserved: %s", body)
	}
	reasoning, _ := out["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning was not emitted correctly: %s", body)
	}
	text, _ := out["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" {
		t.Fatalf("text.format was not emitted correctly: %s", body)
	}
}

func TestCanonicalToResponsesRequestMatchesNewAPIChatHistorySemantics(t *testing.T) {
	req := &CanonicalRequest{
		Model:        "gpt-4.1",
		Instructions: "base instructions",
		Messages: []CanonicalMessage{
			{Role: "system", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "system instructions"}}},
			{Role: "developer", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "developer instructions"}}},
			{Role: "user", Content: []CanonicalContentPart{
				{Type: CanonicalContentText, Text: "hello"},
				{Type: CanonicalContentImage, ImageURL: "https://example.com/image.png"},
				{Type: CanonicalContentFile, FileID: "file_123", FileName: "a.pdf"},
			}},
			{Role: "assistant", Content: []CanonicalContentPart{
				{Type: CanonicalContentText, Text: "hi"},
				{Type: CanonicalContentImage, ImageURL: "https://example.com/assistant-image.png"},
				{Type: CanonicalContentFile, FileID: "file_assistant"},
			}, ToolCalls: []CanonicalToolCall{{
				ID:        "call_1",
				Type:      CanonicalToolFunction,
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"x"}`),
			}}},
			{Role: "tool", ToolCallID: "call_1", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "tool result"}}},
		},
	}

	body, err := CanonicalToResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if got := out["instructions"]; got != "base instructions\n\nsystem instructions\n\ndeveloper instructions" {
		t.Fatalf("expected system/developer messages to be merged into instructions, got %#v", got)
	}

	input, _ := out["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("expected user, assistant, function_call and function_call_output items, got %d: %s", len(input), body)
	}

	userItem, _ := input[0].(map[string]any)
	if userItem["role"] != "user" {
		t.Fatalf("expected first item to be user, got %#v", userItem)
	}
	userContent, _ := userItem["content"].([]any)
	if userContent[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("expected user text to use input_text, got %#v", userContent[0])
	}
	if userContent[1].(map[string]any)["type"] != "input_image" || userContent[2].(map[string]any)["type"] != "input_file" {
		t.Fatalf("expected user media to remain input media, got %#v", userContent)
	}

	assistantItem, _ := input[1].(map[string]any)
	if assistantItem["role"] != "assistant" {
		t.Fatalf("expected second item to be assistant, got %#v", assistantItem)
	}
	assistantContent, _ := assistantItem["content"].([]any)
	if len(assistantContent) != 1 {
		t.Fatalf("expected assistant input media to be suppressed, got %#v", assistantContent)
	}
	if assistantContent[0].(map[string]any)["type"] != "output_text" {
		t.Fatalf("expected assistant text to use output_text, got %#v", assistantContent[0])
	}

	functionCall, _ := input[2].(map[string]any)
	if functionCall["type"] != "function_call" || functionCall["call_id"] != "call_1" || functionCall["name"] != "lookup" || functionCall["arguments"] != `{"q":"x"}` {
		t.Fatalf("expected assistant tool call to become function_call item, got %#v", functionCall)
	}

	functionOutput, _ := input[3].(map[string]any)
	if functionOutput["type"] != "function_call_output" || functionOutput["call_id"] != "call_1" || functionOutput["output"] != "tool result" {
		t.Fatalf("expected tool result to become function_call_output item, got %#v", functionOutput)
	}
}

func TestCanonicalToResponsesRequestRewritesOriginalInvalidInput(t *testing.T) {
	original := &OpenAIResponsesRequest{
		Model: "gpt-4.1",
		Input: json.RawMessage(`[
			{"role":"assistant","content":[{"type":"input_text","text":"bad historical assistant content"}]}
		]`),
		Include: []string{"reasoning.encrypted_content"},
	}
	req := &CanonicalRequest{
		Model: "gpt-4.1",
		Messages: []CanonicalMessage{
			{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "one"}}},
			{Role: "assistant", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "two"}}},
			{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "three"}}},
			{Role: "assistant", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "four"}}},
			{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "five"}}},
			{Role: "assistant", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "six"}}},
			{Role: "user", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "seven"}}},
			{Role: "assistant", Content: []CanonicalContentPart{{Type: CanonicalContentText, Text: "eight"}}},
		},
	}

	body, err := CanonicalToResponsesRequest(req, original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if include, _ := out["include"].([]any); len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("expected original non-input fields to be preserved, got %s", body)
	}

	input, _ := out["input"].([]any)
	if len(input) != 8 {
		t.Fatalf("expected rewritten canonical input to replace original input, got %d: %s", len(input), body)
	}
	item7, _ := input[7].(map[string]any)
	content7, _ := item7["content"].([]any)
	if item7["role"] != "assistant" || content7[0].(map[string]any)["type"] != "output_text" {
		t.Fatalf("expected input[7] assistant content to use output_text, got %#v", item7)
	}
}

func TestCanonicalToResponsesRequestPreservesRawResponsesInputItems(t *testing.T) {
	raw := json.RawMessage(`{"type":"item_reference","id":"item_123"}`)
	req := &CanonicalRequest{
		Model: "gpt-4.1",
		InputItems: []CanonicalInputItem{{
			Type:     CanonicalInputItemReference,
			RawExtra: map[string]json.RawMessage{"raw": raw},
		}},
	}

	body, err := CanonicalToResponsesRequest(req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	input, _ := out["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["type"] != "item_reference" || input[0].(map[string]any)["id"] != "item_123" {
		t.Fatalf("expected raw responses item to be preserved, got %s", body)
	}
}

func TestResponsesResponseToCanonicalIncludesUsageDetailsAndBuiltinCounts(t *testing.T) {
	resp := &OpenAIResponsesResponse{
		ID:        "resp_1",
		Model:     "gpt-4.1",
		CreatedAt: 123,
		Status:    "completed",
		Usage: &ResponsesUsage{
			InputTokens:         100,
			OutputTokens:        50,
			TotalTokens:         150,
			InputTokensDetails:  &ResponsesInputTokensDetails{CachedTokens: 25},
			OutputTokensDetails: &ResponsesOutputTokensDetails{ReasoningTokens: 7},
		},
		Output: []ResponsesOutput{
			{ID: "msg_1", Type: "message", Role: "assistant", Content: []ResponsesOutputContent{{Type: "output_text", Text: "hello"}}},
			{ID: "call_1", Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)},
			{ID: "web_1", Type: "web_search_call"},
			{ID: "file_1", Type: "file_search_call"},
			{ID: "img_1", Type: "image_generation_call"},
		},
	}

	got, err := ResponsesResponseToCanonical(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Usage == nil || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 50 || got.Usage.CachedInputTokens != 25 || got.Usage.ReasoningTokens != 7 {
		t.Fatalf("usage was not mapped: %+v", got.Usage)
	}
	if got.Usage.WebSearchCallCount != 1 || got.Usage.FileSearchCallCount != 1 || got.Usage.ImageGenerationCallCount != 1 {
		t.Fatalf("builtin call counts were not mapped: %+v", got.Usage)
	}
	if len(got.Output) != 5 || canonicalText(got.Output[0].Content) != "hello" {
		t.Fatalf("output was not mapped: %+v", got.Output)
	}
}

func TestCanonicalUsageRoundTripsProviderUsageShapes(t *testing.T) {
	u := &CanonicalUsage{
		InputTokens:              100,
		OutputTokens:             50,
		TotalTokens:              150,
		CachedInputTokens:        25,
		CacheCreationInputTokens: 5,
		ReasoningTokens:          7,
		ToolUseTokens:            3,
	}

	openai := openAIUsageFromCanonical(u)
	if openai.PromptTokens != 100 || openai.CompletionTokens != 50 || openai.PromptTokensDetails.CachedTokens != 25 || openai.CompletionTokensDetails.ReasoningTokens != 7 {
		t.Fatalf("OpenAI usage mapping failed: %+v", openai)
	}
	claude := claudeUsageFromCanonical(u)
	if claude.InputTokens != 100 || claude.OutputTokens != 50 || claude.CacheReadInputTokens != 25 || claude.CacheCreationInputTokens != 5 {
		t.Fatalf("Claude usage mapping failed: %+v", claude)
	}
	gemini := geminiUsageFromCanonical(u)
	if gemini.PromptTokenCount != 100 || gemini.CandidatesTokenCount != 50 || gemini.ThoughtsTokenCount != 7 || gemini.ToolUsePromptTokenCount != 3 {
		t.Fatalf("Gemini usage mapping failed: %+v", gemini)
	}
	responses := responsesUsageFromCanonical(u)
	if responses.InputTokens != 100 || responses.OutputTokens != 50 || responses.InputTokensDetails.CachedTokens != 25 || responses.OutputTokensDetails.ReasoningTokens != 7 {
		t.Fatalf("Responses usage mapping failed: %+v", responses)
	}
}

// 回归：Anthropic → Gemini 转换中 tool_result 块的 functionResponse 必须满足 Gemini API 约束：
// 1. functionResponse.response 必须是 JSON 对象（google.protobuf.Struct），不能是字符串/数组/null/空
// 2. functionResponse.name 必须是函数名（如 "Read"），而非 Anthropic 的 tool_use_id（如 "toolu_01ABC"）
// 覆盖场景：空 tool_result、纯文本 tool_result、JSON 数组 tool_result、JSON 对象 tool_result、多个 tool_result
func TestCanonicalMessagesToGeminiFunctionResponse(t *testing.T) {
	req := &CanonicalRequest{
		Messages: []CanonicalMessage{
			// Assistant 消息：包含 tool_use（functionCall）
			{
				Role: "assistant",
				Content: []CanonicalContentPart{
					{Type: CanonicalContentText, Text: "Let me check that for you."},
				},
				ToolCalls: []CanonicalToolCall{
					{ID: "toolu_01A", Type: CanonicalToolFunction, Name: "Read", Arguments: json.RawMessage(`{"file":"a.txt"}`)},
					{ID: "toolu_01B", Type: CanonicalToolFunction, Name: "Bash", Arguments: json.RawMessage(`{"cmd":"ls"}`)},
					{ID: "toolu_01C", Type: CanonicalToolFunction, Name: "Grep", Arguments: json.RawMessage(`{"pattern":"foo"}`)},
				},
			},
			// User 消息：包含 tool_result（functionResponse）
			{
				Role: "user",
				Content: []CanonicalContentPart{
					// 场景 1：空 tool_result（content: []）→ ToolOutput = "" → 应包装为 {"content": ""}
					{Type: CanonicalContentToolOutput, ToolCallID: "toolu_01A", ToolOutput: ""},
					// 场景 2：纯文本 tool_result（非 JSON）→ 应包装为 {"content": "file contents"}
					{Type: CanonicalContentToolOutput, ToolCallID: "toolu_01B", ToolOutput: "file contents here"},
					// 场景 3：JSON 数组 tool_result → 应包装为 {"result": [...]}
					{Type: CanonicalContentToolOutput, ToolCallID: "toolu_01C", ToolOutput: `["item1","item2"]`},
				},
			},
		},
	}

	contents, err := canonicalMessagesToGemini(req)
	if err != nil {
		t.Fatalf("canonicalMessagesToGemini: %v", err)
	}
	if len(contents) != 2 {
		t.Fatalf("expected 2 contents (assistant + user), got %d", len(contents))
	}

	// 验证 assistant 消息：应包含 3 个 functionCall parts
	assistantMsg := contents[0]
	if assistantMsg["role"] != "model" {
		t.Fatalf("assistant role should map to 'model', got %v", assistantMsg["role"])
	}
	assistantParts := assistantMsg["parts"].([]map[string]any)
	if len(assistantParts) != 4 { // 1 text + 3 functionCall
		t.Fatalf("expected 4 parts in assistant message (1 text + 3 functionCall), got %d", len(assistantParts))
	}
	for i := 1; i < 4; i++ {
		if assistantParts[i]["functionCall"] == nil {
			t.Fatalf("assistantParts[%d] should have functionCall, got %+v", i, assistantParts[i])
		}
	}

	// 验证 user 消息：应包含 3 个 functionResponse parts
	userMsg := contents[1]
	if userMsg["role"] != "user" {
		t.Fatalf("user role should stay 'user', got %v", userMsg["role"])
	}
	userParts := userMsg["parts"].([]map[string]any)
	if len(userParts) != 3 {
		t.Fatalf("expected 3 functionResponse parts in user message, got %d", len(userParts))
	}

	// 场景 1：空 tool_result → response 应为 {"content": ""}，name 应为 "Read"（非 "toolu_01A"）
	fr1 := userParts[0]["functionResponse"].(map[string]any)
	if fr1["name"] != "Read" {
		t.Fatalf("functionResponse[0].name should be 'Read' (function name), got %v", fr1["name"])
	}
	resp1 := fr1["response"].(map[string]any)
	if resp1["content"] != "" {
		t.Fatalf("functionResponse[0].response should be {\"content\": \"\"}, got %+v", resp1)
	}

	// 场景 2：纯文本 → response 应为 {"content": "file contents here"}，name 应为 "Bash"
	fr2 := userParts[1]["functionResponse"].(map[string]any)
	if fr2["name"] != "Bash" {
		t.Fatalf("functionResponse[1].name should be 'Bash', got %v", fr2["name"])
	}
	resp2 := fr2["response"].(map[string]any)
	if resp2["content"] != "file contents here" {
		t.Fatalf("functionResponse[1].response should be {\"content\": \"file contents here\"}, got %+v", resp2)
	}

	// 场景 3：JSON 数组 → response 应为 {"result": ["item1", "item2"]}，name 应为 "Grep"
	fr3 := userParts[2]["functionResponse"].(map[string]any)
	if fr3["name"] != "Grep" {
		t.Fatalf("functionResponse[2].name should be 'Grep', got %v", fr3["name"])
	}
	resp3 := fr3["response"].(map[string]any)
	resultArr, ok := resp3["result"].([]any)
	if !ok || len(resultArr) != 2 || resultArr[0] != "item1" || resultArr[1] != "item2" {
		t.Fatalf("functionResponse[2].response should be {\"result\": [\"item1\", \"item2\"]}, got %+v", resp3)
	}

	// 额外验证：JSON 对象应直接使用（不包装）
	reqWithJsonObject := &CanonicalRequest{
		Messages: []CanonicalMessage{
			{
				Role: "assistant",
				ToolCalls: []CanonicalToolCall{
					{ID: "toolu_999", Type: CanonicalToolFunction, Name: "GetStatus", Arguments: json.RawMessage(`{}`)},
				},
			},
			{
				Role: "user",
				Content: []CanonicalContentPart{
					{Type: CanonicalContentToolOutput, ToolCallID: "toolu_999", ToolOutput: `{"status":"ok","code":200}`},
				},
			},
		},
	}
	contentsWithObj, err := canonicalMessagesToGemini(reqWithJsonObject)
	if err != nil {
		t.Fatalf("canonicalMessagesToGemini JSON object: %v", err)
	}
	userMsgWithObj := contentsWithObj[1]
	userPartsWithObj := userMsgWithObj["parts"].([]map[string]any)
	frObj := userPartsWithObj[0]["functionResponse"].(map[string]any)
	respObj := frObj["response"].(map[string]any)
	// JSON 对象应直接使用，不包装
	if respObj["status"] != "ok" || respObj["code"].(float64) != 200 {
		t.Fatalf("JSON object should be used as-is, got %+v", respObj)
	}
}

func TestClaudeToGeminiFiltersEmptyPartsAndPreservesThinking(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first"}]},
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]},
			{"role":"user","content":[{"type":"text","text":""},{"type":"unknown_block","value":"ignored"},{"type":"text","text":"second"}]},
			{"role":"assistant","content":[{"type":"thinking","thinking":"consider the result","signature":"sig"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"done"}]}
		]
	}`)

	req, err := ClaudeRequestToCanonical(body)
	if err != nil {
		t.Fatalf("ClaudeRequestToCanonical: %v", err)
	}
	out, err := CanonicalToGeminiRequest(req)
	if err != nil {
		t.Fatalf("CanonicalToGeminiRequest: %v", err)
	}

	contents := assertGeminiPartsHaveData(t, out)
	if len(contents) != 2 {
		t.Fatalf("expected adjacent user messages to merge into 2 contents, got %d: %s", len(contents), out)
	}

	userContent := contents[0].(map[string]any)
	if userContent["role"] != "user" {
		t.Fatalf("expected first content role user, got %v", userContent["role"])
	}
	userParts := userContent["parts"].([]any)
	if len(userParts) != 2 || userParts[0].(map[string]any)["text"] != "first" || userParts[1].(map[string]any)["text"] != "second" {
		t.Fatalf("expected merged user text parts, got %+v", userParts)
	}

	modelContent := contents[1].(map[string]any)
	if modelContent["role"] != "model" {
		t.Fatalf("expected assistant role to map to model, got %v", modelContent["role"])
	}
	modelParts := modelContent["parts"].([]any)
	if len(modelParts) != 2 {
		t.Fatalf("expected thinking and text parts, got %+v", modelParts)
	}
	thoughtPart := modelParts[0].(map[string]any)
	if thoughtPart["text"] != "consider the result" || thoughtPart["thought"] != true {
		t.Fatalf("expected Gemini thought part, got %+v", thoughtPart)
	}
	if _, exists := thoughtPart["thoughtSignature"]; exists {
		t.Fatalf("Anthropic signature must not be replayed as Gemini thoughtSignature: %+v", thoughtPart)
	}
	if modelParts[1].(map[string]any)["text"] != "done" {
		t.Fatalf("expected final assistant text, got %+v", modelParts[1])
	}
	if strings.Contains(string(out), "opaque") || strings.Contains(string(out), "unknown_block") || strings.Contains(string(out), `"text":""`) {
		t.Fatalf("unrepresentable or empty content leaked into Gemini request: %s", out)
	}
}

func TestClaudeToGeminiRejectsRequestWithoutRepresentableContent(t *testing.T) {
	body := []byte(`{
		"model":"claude-test",
		"max_tokens":128,
		"messages":[
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]},
			{"role":"user","content":[{"type":"text","text":""},{"type":"unknown_block"}]}
		]
	}`)

	req, err := ClaudeRequestToCanonical(body)
	if err != nil {
		t.Fatalf("ClaudeRequestToCanonical: %v", err)
	}
	_, err = CanonicalToGeminiRequest(req)
	if err == nil || !strings.Contains(err.Error(), "no representable message content") {
		t.Fatalf("expected clear no-content conversion error, got %v", err)
	}
}

func TestCanonicalMessagesToGeminiRejectsUnmatchedFunctionResponse(t *testing.T) {
	req := &CanonicalRequest{
		Messages: []CanonicalMessage{
			{
				Role: "user",
				Content: []CanonicalContentPart{
					{Type: CanonicalContentToolOutput, ToolCallID: "toolu_missing", ToolOutput: "result"},
				},
			},
		},
	}

	_, err := canonicalMessagesToGemini(req)
	if err == nil || !strings.Contains(err.Error(), "message 0 part 0") || !strings.Contains(err.Error(), "toolu_missing") {
		t.Fatalf("expected indexed unmatched function response error, got %v", err)
	}
}

func TestGeminiThinkingConfigUsesGenerationConfig(t *testing.T) {
	request, err := GeminiRequestToCanonical([]byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{"thinkingConfig":{"includeThoughts":true,"thinkingLevel":"high","thinkingBudget":2048}}
	}`), "gemini-test")
	if err != nil {
		t.Fatalf("parse Gemini thinking config: %v", err)
	}
	if request.Thinking == nil || !request.Thinking.Enabled || request.Thinking.Effort != "high" || request.Thinking.BudgetTokens != 2048 {
		t.Fatalf("Gemini thinking config was not normalized: %+v", request.Thinking)
	}

	body, err := CanonicalToGeminiRequest(request)
	if err != nil {
		t.Fatalf("render Gemini thinking config: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode rendered Gemini request: %v", err)
	}
	if _, exists := payload["thinkingConfig"]; exists {
		t.Fatalf("thinkingConfig must not be rendered at the request top level: %s", body)
	}
	generationConfig, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("rendered request has no generationConfig: %s", body)
	}
	thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]any)
	if !ok || thinkingConfig["thinkingLevel"] != "high" || thinkingConfig["thinkingBudget"] != float64(2048) {
		t.Fatalf("unexpected rendered thinkingConfig: %+v", generationConfig["thinkingConfig"])
	}
}

func TestGeminiToolPartsPreserveIDsAndThoughtSignature(t *testing.T) {
	request := &CanonicalRequest{Messages: []CanonicalMessage{
		{Role: "assistant", ToolCalls: []CanonicalToolCall{{
			ID: "call_1", Type: CanonicalToolFunction, Name: "lookup", Arguments: json.RawMessage(`{"q":"elysia"}`), ThoughtSignature: "sig_1", ThoughtSignatureProvider: CanonicalSignatureProviderGemini,
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: []CanonicalContentPart{{
			Type: CanonicalContentToolOutput, ToolCallID: "call_1", ToolOutput: `{"ok":true}`,
		}}},
	}}

	contents, err := canonicalMessagesToGemini(request)
	if err != nil {
		t.Fatalf("render Gemini tool history: %v", err)
	}
	modelParts := contents[0]["parts"].([]map[string]any)
	functionCall := modelParts[0]["functionCall"].(map[string]any)
	if functionCall["id"] != "call_1" || modelParts[0]["thoughtSignature"] != "sig_1" {
		t.Fatalf("function call ID or thought signature was lost: %+v", modelParts[0])
	}
	if _, nested := functionCall["thoughtSignature"]; nested {
		t.Fatalf("thoughtSignature must be a Part field, not a functionCall field: %+v", functionCall)
	}

	userParts := contents[1]["parts"].([]map[string]any)
	functionResponse := userParts[0]["functionResponse"].(map[string]any)
	if functionResponse["id"] != "call_1" || functionResponse["name"] != "lookup" {
		t.Fatalf("function response ID or name was lost: %+v", functionResponse)
	}
}

func TestGeminiResponsePreservesResponseAndToolMetadata(t *testing.T) {
	canonical, err := GeminiResponseToCanonical(&GeminiResponse{
		ResponseID:   "resp_1",
		ModelVersion: "gemini-test",
		Candidates: []GeminiCandidate{{Content: GeminiContent{Role: "model", Parts: []GeminiPart{{
			FunctionCall:     map[string]any{"id": "call_1", "name": "lookup", "args": map[string]any{"q": "elysia"}},
			ThoughtSignature: "sig_1",
		}}}}},
	})
	if err != nil {
		t.Fatalf("normalize Gemini response: %v", err)
	}
	if canonical.ID != "resp_1" || canonical.Model != "gemini-test" || len(canonical.Output) != 1 || len(canonical.Output[0].ToolCalls) != 1 {
		t.Fatalf("Gemini response metadata was lost: %+v", canonical)
	}
	if canonical.Output[0].ToolCalls[0].ThoughtSignature != "sig_1" || canonical.Output[0].ToolCalls[0].ThoughtSignatureProvider != CanonicalSignatureProviderGemini {
		t.Fatalf("Gemini tool thought signature was lost: %+v", canonical.Output[0])
	}

	rendered, err := CanonicalToGeminiResponse(canonical)
	if err != nil {
		t.Fatalf("render Gemini response: %v", err)
	}
	if rendered.ResponseID != "resp_1" || rendered.ModelVersion != "gemini-test" || rendered.Candidates[0].Content.Parts[0].ThoughtSignature != "sig_1" {
		t.Fatalf("Gemini response metadata did not round-trip: %+v", rendered)
	}
}

func TestOpenAIExtendedGoogleThoughtSignatureRoundTripsThroughMaheshvara(t *testing.T) {
	req, err := OpenAIChatRequestToCanonical([]byte(`{
		"model":"openai-compatible",
		"messages":[{"role":"assistant","content":null,"tool_calls":[{
			"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"elysia\"}"},
			"extra_content":{"google":{"thought_signature":"sig_google"}}
		}]}]
	}`))
	if err != nil {
		t.Fatalf("parse extended OpenAI request: %v", err)
	}
	call := req.Messages[0].ToolCalls[0]
	if call.ThoughtSignature != "sig_google" || call.ThoughtSignatureProvider != CanonicalSignatureProviderGemini {
		t.Fatalf("Google thought signature was not normalized: %+v", call)
	}

	gemini, err := canonicalMessagesToGemini(req)
	if err != nil {
		t.Fatalf("render Gemini request: %v", err)
	}
	geminiPart := gemini[0]["parts"].([]map[string]any)[0]
	if geminiPart["thoughtSignature"] != "sig_google" {
		t.Fatalf("Gemini signature was not restored: %+v", geminiPart)
	}

	openAI, err := CanonicalToOpenAIChatRequest(req)
	if err != nil {
		t.Fatalf("render extended OpenAI request: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(openAI, &payload); err != nil {
		t.Fatalf("decode extended OpenAI request: %v", err)
	}
	message := payload["messages"].([]any)[0].(map[string]any)
	wireCall := message["tool_calls"].([]any)[0].(map[string]any)
	extra := wireCall["extra_content"].(map[string]any)
	google := extra["google"].(map[string]any)
	if google["thought_signature"] != "sig_google" {
		t.Fatalf("extended OpenAI signature was not restored: %+v", wireCall)
	}
}

func assertGeminiPartsHaveData(t *testing.T, body []byte) []any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal Gemini request: %v", err)
	}
	contents, ok := payload["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("Gemini request has no contents: %s", body)
	}
	dataFields := []string{"text", "inlineData", "fileData", "functionCall", "functionResponse"}
	for contentIndex, rawContent := range contents {
		content, ok := rawContent.(map[string]any)
		if !ok {
			t.Fatalf("contents[%d] is not an object: %+v", contentIndex, rawContent)
		}
		parts, ok := content["parts"].([]any)
		if !ok || len(parts) == 0 {
			t.Fatalf("contents[%d] has no parts: %+v", contentIndex, content)
		}
		for partIndex, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				t.Fatalf("contents[%d].parts[%d] is not an object: %+v", contentIndex, partIndex, rawPart)
			}
			dataCount := 0
			for _, field := range dataFields {
				value, exists := part[field]
				if !exists {
					continue
				}
				dataCount++
				if field == "text" && value == "" {
					t.Fatalf("contents[%d].parts[%d] contains empty text: %+v", contentIndex, partIndex, part)
				}
			}
			if dataCount != 1 {
				t.Fatalf("contents[%d].parts[%d] must contain exactly one data field, got %+v", contentIndex, partIndex, part)
			}
		}
	}
	return contents
}
