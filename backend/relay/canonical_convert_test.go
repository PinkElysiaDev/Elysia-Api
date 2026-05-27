package relay

import (
	"encoding/json"
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
