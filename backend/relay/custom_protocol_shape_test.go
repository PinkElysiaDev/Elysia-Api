package relay

import (
	"encoding/json"
	"testing"
)

// A1 回归：shape 整形把思考/推理配置按平台写进模板上下文——budget 量化、
// effort 省略、anthropic 思考态温度强制、gemini thinkingConfig、responses
// include 联动，预置 body-tree 直接映射即可与内置线行为一致。
func renderShapeFixture(t *testing.T, shape, template string, req *MaheshvaraRequest) map[string]any {
	t.Helper()
	config := CustomProtocolConfig{ID: "shape-test", Type: "llm",
		Request: CustomProtocolRequest{Method: "POST", PathTemplate: "/x", Shape: shape, BodyTemplate: template}}
	result, err := RenderCustomProtocolRequest(req, config)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatalf("body not json (%s): %v", result.Body, err)
	}
	return body
}

func TestShapeThinkingAnthropicBudgetAndForcedTemperature(t *testing.T) {
	high := 16384 // budgetFromEffort("high")
	req := &MaheshvaraRequest{Model: "m", Stream: true,
		Thinking: &MaheshvaraThinking{Enabled: true, Effort: "high"}}
	body := renderShapeFixture(t, "anthropic", `{"thinking":{{maheshvara.thinking}},"temperature":{{maheshvara.temperature}},"top_p":{{maheshvara.top_p|default:null}}}`, req)
	if got, _ := body["thinking"].(map[string]any); got["type"] != "enabled" || got["budget_tokens"] != float64(high) {
		t.Fatalf("thinking = %v", body["thinking"])
	}
	if body["temperature"] != 1.0 {
		t.Fatalf("thinking mode must force temperature=1.0, got %v", body["temperature"])
	}
	if _, has := body["top_p"]; has && body["top_p"] != nil {
		t.Fatalf("thinking mode must drop top_p, got %v", body["top_p"])
	}

	adaptive := &MaheshvaraRequest{Model: "m", Thinking: &MaheshvaraThinking{Enabled: true, Adaptive: true, Effort: "medium"}}
	adaptiveBody := renderShapeFixture(t, "anthropic", `{"thinking":{{maheshvara.thinking}},"output_config":{{maheshvara.output_config}}}`, adaptive)
	if got, _ := adaptiveBody["thinking"].(map[string]any); got["type"] != "adaptive" {
		t.Fatalf("adaptive thinking = %v", adaptiveBody["thinking"])
	}
	if got, _ := adaptiveBody["output_config"].(map[string]any); got["effort"] != "medium" {
		t.Fatalf("output_config = %v", adaptiveBody["output_config"])
	}
}

func TestShapeThinkingChatEffortAndGeminiConfig(t *testing.T) {
	chat := &MaheshvaraRequest{Model: "m", Reasoning: &MaheshvaraReasoning{Effort: "low"}}
	chatBody := renderShapeFixture(t, "openai-chat", `{"reasoning_effort":"{{maheshvara.reasoning_effort}}"}`, chat)
	if chatBody["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %v", chatBody["reasoning_effort"])
	}

	gemini := &MaheshvaraRequest{Model: "m", Thinking: &MaheshvaraThinking{Enabled: true, Effort: "high", BudgetTokens: 2048},
		Messages: []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "hi"}}}}}
	geminiBody := renderShapeFixture(t, "gemini", `{"thinking_config":{{maheshvara.thinking_config}}}`, gemini)
	got, _ := geminiBody["thinking_config"].(map[string]any)
	if got["includeThoughts"] != true || got["thinkingLevel"] != "high" || got["thinkingBudget"] != float64(2048) {
		t.Fatalf("thinking_config = %v", geminiBody["thinking_config"])
	}
}

func TestShapeThinkingResponsesEffortNoneOmitted(t *testing.T) {
	none := &MaheshvaraRequest{Model: "m", Reasoning: &MaheshvaraReasoning{Effort: "none"},
		InputItems: []MaheshvaraInputItem{{Type: "message", Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "hi"}}}}}
	body := renderShapeFixture(t, "responses", `{"reasoning":{{maheshvara.reasoning|default:null}}}`, none)
	if body["reasoning"] != nil {
		t.Fatalf("effort none must omit reasoning entirely, got %v", body["reasoning"])
	}
	effort := &MaheshvaraRequest{Model: "m", Reasoning: &MaheshvaraReasoning{Effort: "medium", Raw: map[string]any{"summary": "auto"}},
		InputItems: []MaheshvaraInputItem{{Type: "message", Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "hi"}}}}}
	effortBody := renderShapeFixture(t, "responses", `{"reasoning":{{maheshvara.reasoning}}}`, effort)
	if got, _ := effortBody["reasoning"].(map[string]any); got["effort"] != "medium" || got["summary"] != "auto" {
		t.Fatalf("reasoning = %v", effortBody["reasoning"])
	}
}
