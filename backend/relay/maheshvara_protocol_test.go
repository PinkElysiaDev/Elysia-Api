package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaheshvaraFourWireRequestConversions(t *testing.T) {
	openAI := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]},{"role":"assistant","content":null,"reasoning_content":"plan","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true}}],"seed":7,"presence_penalty":0.2}`)
	req, err := OpenAIChatToMaheshvara(openAI)
	if err != nil {
		t.Fatalf("OpenAI parse failed: %v", err)
	}
	if req.Seed == nil || *req.Seed != 7 || len(req.Messages) != 3 || req.Messages[1].ToolCalls[0].Name != "lookup" {
		t.Fatalf("OpenAI fields were not preserved: %+v", req)
	}
	if _, err := MaheshvaraToAnthropic(req); err != nil {
		t.Fatalf("Maheshvara -> Anthropic failed: %v", err)
	}
	if _, err := MaheshvaraToGemini(req); err != nil {
		t.Fatalf("Maheshvara -> Gemini failed: %v", err)
	}
	if _, err := MaheshvaraToOpenAIChat(req); err != nil {
		t.Fatalf("Maheshvara -> OpenAI failed: %v", err)
	}

	claude, err := AnthropicToMaheshvara([]byte(`{"model":"claude-test","max_tokens":100,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"reason"},{"type":"redacted_thinking","data":"secret"},{"type":"text","text":"answer"}]}]}`))
	if err != nil {
		t.Fatalf("Anthropic parse failed: %v", err)
	}
	if len(claude.Messages) != 1 || len(claude.Messages[0].Content) != 2 || claude.Messages[0].Content[0].Type != CanonicalContentReasoning {
		t.Fatalf("Anthropic reasoning filtering failed: %+v", claude.Messages)
	}

	gemini, err := GeminiToMaheshvara([]byte(`{"contents":[{"role":"user","parts":[{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"AQI="}}]}],"generationConfig":{"temperature":0.4,"seed":9}}`), "gemini-test")
	if err != nil {
		t.Fatalf("Gemini parse failed: %v", err)
	}
	if gemini.Model != "gemini-test" || gemini.Seed == nil || *gemini.Seed != 9 || len(gemini.Messages[0].Content) != 2 {
		t.Fatalf("Gemini fields were not preserved: %+v", gemini)
	}

	responses, _, err := OpenAIResponsesToMaheshvara([]byte(`{"model":"gpt-test","input":"hello","reasoning":{"effort":"medium"},"max_output_tokens":12}`))
	if err != nil {
		t.Fatalf("Responses parse failed: %v", err)
	}
	if responses.Reasoning == nil || responses.MaxOutputTokens != 12 || len(responses.Messages) != 1 {
		t.Fatalf("Responses fields were not preserved: %+v", responses)
	}
}

func TestCustomProtocolTemplateAndResponseMapping(t *testing.T) {
	config := CustomProtocolConfig{
		ID: "vendor-json",
		Request: CustomProtocolRequest{
			Method:       "POST",
			PathTemplate: "/v2/generate/{{maheshvara.model}}",
			Headers:      map[string]string{"X-Model": "{{maheshvara.model}}"},
			BodyTemplate: `{"model":"{{maheshvara.model}}","messages":{{maheshvara.messages}},"temperature":{{maheshvara.temperature | default:0.2}},"metadata":{{maheshvara.metadata}}}`,
			OmitIfEmpty:  []string{"metadata"},
		},
		Response: CustomProtocolResponse{
			TextPath:         "answer.text",
			UsagePath:        "usage",
			FinishReasonPath: "finish",
		},
	}
	req := &MaheshvaraRequest{Model: "vendor-model", Messages: []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: CanonicalContentText, Text: "hello"}}}}}
	rendered, err := RenderCustomProtocolRequest(req, config)
	if err != nil {
		t.Fatalf("custom render failed: %v", err)
	}
	if rendered.Path != "/v2/generate/vendor-model" || rendered.Headers["X-Model"] != "vendor-model" {
		t.Fatalf("custom path/header interpolation failed: %+v", rendered)
	}
	var body map[string]any
	if err := json.Unmarshal(rendered.Body, &body); err != nil {
		t.Fatalf("custom body is invalid JSON: %v", err)
	}
	if _, exists := body["metadata"]; exists {
		t.Fatalf("empty metadata was not omitted: %s", rendered.Body)
	}
	if body["model"] != "vendor-model" {
		t.Fatalf("model interpolation failed: %s", rendered.Body)
	}

	response, err := CustomProtocolResponseToCanonical([]byte(`{"answer":{"text":"ok"},"finish":"stop","usage":{"prompt_tokens":2,"completion_tokens":3}}`), config)
	if err != nil {
		t.Fatalf("custom response mapping failed: %v", err)
	}
	if canonicalText(response.Output[0].Content) != "ok" || response.StopReason != "stop" || response.Usage.TotalTokens != 5 {
		t.Fatalf("custom response was not mapped: %+v", response)
	}
}

func TestCustomProtocolMapsNestedFunctionToolCalls(t *testing.T) {
	config := CustomProtocolConfig{
		ID:       "nested-tools",
		Request:  CustomProtocolRequest{BodyTemplate: `{"prompt":{{maheshvara.messages}}}`},
		Response: CustomProtocolResponse{ToolCallsPath: "data.choices"},
	}
	response, err := CustomProtocolResponseToCanonical([]byte(`{"data":{"choices":[{"id":"call_1","function":{"name":"lookup","arguments":{"q":"x"}}}]}}`), config)
	if err != nil {
		t.Fatalf("nested tool response mapping failed: %v", err)
	}
	if len(response.Output) != 1 || response.Output[0].Name != "lookup" || string(response.Output[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("nested tool call was not preserved: %+v", response.Output)
	}
}

func TestGeminiFileDataSurvivesOpenAIConversion(t *testing.T) {
	req, err := GeminiToMaheshvara([]byte(`{"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"application/pdf","fileUri":"https://example.com/file.pdf"}}]}]}`), "gemini-test")
	if err != nil {
		t.Fatalf("Gemini parse failed: %v", err)
	}
	body, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatalf("OpenAI render failed: %v", err)
	}
	if !strings.Contains(string(body), `"file_url":"https://example.com/file.pdf"`) {
		t.Fatalf("file URI was lost during OpenAI conversion: %s", body)
	}
}

func TestGeminiConversionNeverEmitsEmptyDataPart(t *testing.T) {
	req := &MaheshvaraRequest{
		Model:    "gemini-test",
		Messages: []MaheshvaraMessage{{Role: "assistant", Content: []MaheshvaraContentPart{{Type: CanonicalContentReasoning, Text: ""}}}},
	}
	if _, err := MaheshvaraToGemini(req); err == nil || !strings.Contains(err.Error(), "no representable message content") {
		t.Fatalf("expected clear empty-content error, got %v", err)
	}
	if _, err := MaheshvaraToGeminiResponse(&MaheshvaraResponse{}); err == nil || !strings.Contains(err.Error(), "no representable output part") {
		t.Fatalf("expected clear empty-response error, got %v", err)
	}
}

func TestMaheshvaraStreamEventAdapters(t *testing.T) {
	event, err := OpenAIChatStreamChunkToMaheshvara([]byte(`{"id":"chatcmpl_1","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`))
	if err != nil || event.Type != CanonicalEventTextDelta || event.Delta != "hi" {
		t.Fatalf("OpenAI stream event was not normalized: %+v, %v", event, err)
	}
	chunk, err := MaheshvaraStreamEventToOpenAIChatChunk(event)
	if err != nil || !strings.Contains(string(chunk), `"content":"hi"`) {
		t.Fatalf("Maheshvara stream event was not rendered: %s, %v", chunk, err)
	}

	event, err = AnthropicStreamEventToMaheshvara([]byte(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"plan"}}`))
	if err != nil || event.Type != CanonicalEventReasoningDelta || event.ReasoningDelta != "plan" {
		t.Fatalf("Anthropic reasoning event was not normalized: %+v, %v", event, err)
	}
	event, err = GeminiStreamChunkToMaheshvara([]byte(`{"candidates":[{"content":{"parts":[{"text":"thought","thought":true}]}}]}`))
	if err != nil || event.Type != CanonicalEventReasoningDelta || event.ReasoningDelta != "thought" {
		t.Fatalf("Gemini reasoning event was not normalized: %+v, %v", event, err)
	}
	event, err = OpenAIResponsesStreamEventToMaheshvara([]byte(`{"type":"response.output_text.delta","response_id":"resp_1","delta":"ok"}`))
	if err != nil || event.Type != CanonicalEventTextDelta || event.Delta != "ok" {
		t.Fatalf("Responses event was not normalized: %+v, %v", event, err)
	}
}
