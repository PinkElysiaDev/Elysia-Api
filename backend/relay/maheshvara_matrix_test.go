package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMaheshvaraRequestConversionMatrix(t *testing.T) {
	targets := []struct {
		name      string
		rootField string
		render    func(*MaheshvaraRequest) ([]byte, error)
	}{
		{name: "openai_chat", rootField: "messages", render: MaheshvaraToOpenAIChat},
		{name: "anthropic", rootField: "messages", render: MaheshvaraToAnthropic},
		{name: "gemini", rootField: "contents", render: MaheshvaraToGemini},
		{
			name:      "openai_responses",
			rootField: "input",
			render: func(request *MaheshvaraRequest) ([]byte, error) {
				return MaheshvaraToOpenAIResponses(request, nil)
			},
		},
	}

	for _, source := range maheshvaraRequestSources() {
		source := source
		t.Run(source.name, func(t *testing.T) {
			request, err := source.parse()
			if err != nil {
				t.Fatalf("parse %s request: %v", source.name, err)
			}
			if request.Model != "source-model" || len(request.Messages) == 0 || len(request.Tools) == 0 {
				t.Fatalf("%s request lost common Maheshvara fields: %+v", source.name, request)
			}

			for _, target := range targets {
				target := target
				t.Run("to_"+target.name, func(t *testing.T) {
					body, err := target.render(request)
					if err != nil {
						t.Fatalf("render %s -> %s: %v", source.name, target.name, err)
					}
					payload := decodeMaheshvaraTestJSON(t, body)
					assertMaheshvaraWireFieldNotEmpty(t, payload, target.rootField)
				})
			}
		})
	}
}

type maheshvaraRequestSource struct {
	name  string
	parse func() (*MaheshvaraRequest, error)
}

func maheshvaraRequestSources() []maheshvaraRequestSource {
	return []maheshvaraRequestSource{
		{
			name: "openai_chat",
			parse: func() (*MaheshvaraRequest, error) {
				return OpenAIChatToMaheshvara([]byte(`{
					"model":"source-model",
					"messages":[
						{"role":"system","content":"follow policy"},
						{"role":"user","content":"hello"}
					],
					"max_completion_tokens":64,
					"temperature":0.3,
					"tools":[{"type":"function","function":{"name":"lookup","description":"look up data","parameters":{"type":"object"}}}]
				}`))
			},
		},
		{
			name: "anthropic",
			parse: func() (*MaheshvaraRequest, error) {
				return AnthropicToMaheshvara([]byte(`{
					"model":"source-model",
					"system":"follow policy",
					"max_tokens":64,
					"temperature":0.3,
					"messages":[{"role":"user","content":"hello"}],
					"tools":[{"name":"lookup","description":"look up data","input_schema":{"type":"object"}}]
				}`))
			},
		},
		{
			name: "gemini",
			parse: func() (*MaheshvaraRequest, error) {
				return GeminiToMaheshvara([]byte(`{
					"systemInstruction":{"parts":[{"text":"follow policy"}]},
					"contents":[{"role":"user","parts":[{"text":"hello"}]}],
					"generationConfig":{"maxOutputTokens":64,"temperature":0.3},
					"tools":[{"functionDeclarations":[{"name":"lookup","description":"look up data","parameters":{"type":"object"}}]}]
				}`), "source-model")
			},
		},
		{
			name: "openai_responses",
			parse: func() (*MaheshvaraRequest, error) {
				request, _, err := OpenAIResponsesToMaheshvara([]byte(`{
					"model":"source-model",
					"instructions":"follow policy",
					"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
					"max_output_tokens":64,
					"temperature":0.3,
					"tools":[{"type":"function","name":"lookup","description":"look up data","parameters":{"type":"object"}}]
				}`))
				return request, err
			},
		},
	}
}

func TestMaheshvaraResponseConversionMatrix(t *testing.T) {
	targets := []struct {
		name      string
		rootField string
		render    func(*MaheshvaraResponse) (any, error)
	}{
		{
			name: "openai_chat", rootField: "choices",
			render: func(response *MaheshvaraResponse) (any, error) {
				return MaheshvaraToOpenAIChatResponse(response)
			},
		},
		{
			name: "anthropic", rootField: "content",
			render: func(response *MaheshvaraResponse) (any, error) {
				return MaheshvaraToAnthropicResponse(response)
			},
		},
		{
			name: "gemini", rootField: "candidates",
			render: func(response *MaheshvaraResponse) (any, error) {
				return MaheshvaraToGeminiResponse(response)
			},
		},
		{
			name: "openai_responses", rootField: "output",
			render: func(response *MaheshvaraResponse) (any, error) {
				return MaheshvaraToOpenAIResponsesResponse(response)
			},
		},
	}

	for _, source := range maheshvaraResponseSources() {
		source := source
		t.Run(source.name, func(t *testing.T) {
			response, err := source.parse()
			if err != nil {
				t.Fatalf("parse %s response: %v", source.name, err)
			}
			if len(response.Output) == 0 || response.Usage == nil || response.Usage.TotalTokens != 5 {
				t.Fatalf("%s response lost common Maheshvara fields: %+v", source.name, response)
			}

			for _, target := range targets {
				target := target
				t.Run("to_"+target.name, func(t *testing.T) {
					wire, err := target.render(response)
					if err != nil {
						t.Fatalf("render %s -> %s: %v", source.name, target.name, err)
					}
					body, err := json.Marshal(wire)
					if err != nil {
						t.Fatalf("marshal %s response: %v", target.name, err)
					}
					payload := decodeMaheshvaraTestJSON(t, body)
					assertMaheshvaraWireFieldNotEmpty(t, payload, target.rootField)
					if !strings.Contains(string(body), "hello") {
						t.Fatalf("%s -> %s lost response text: %s", source.name, target.name, body)
					}
				})
			}
		})
	}
}

type maheshvaraResponseSource struct {
	name  string
	parse func() (*MaheshvaraResponse, error)
}

func maheshvaraResponseSources() []maheshvaraResponseSource {
	return []maheshvaraResponseSource{
		{
			name: "openai_chat",
			parse: func() (*MaheshvaraResponse, error) {
				return OpenAIChatResponseToMaheshvara(&OpenAIResponse{
					ID: "chatcmpl_source", Object: "chat.completion", Created: 1, Model: "source-model",
					Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
					Usage:   Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
				})
			},
		},
		{
			name: "anthropic",
			parse: func() (*MaheshvaraResponse, error) {
				return AnthropicResponseToMaheshvara(&ClaudeResponse{
					ID: "msg_source", Type: "message", Role: "assistant", Model: "source-model", StopReason: "end_turn",
					Content: []ClaudeContent{{Type: "text", Text: "hello"}},
					Usage:   ClaudeUsage{InputTokens: 2, OutputTokens: 3},
				})
			},
		},
		{
			name: "gemini",
			parse: func() (*MaheshvaraResponse, error) {
				response, err := GeminiResponseToMaheshvara(&GeminiResponse{
					Candidates: []GeminiCandidate{{
						Content:      GeminiContent{Role: "model", Parts: []GeminiPart{{Text: "hello"}}},
						FinishReason: "STOP",
					}},
					UsageMetadata: GeminiUsageMeta{PromptTokenCount: 2, CandidatesTokenCount: 3, TotalTokenCount: 5},
				})
				if response != nil {
					response.Model = "source-model"
				}
				return response, err
			},
		},
		{
			name: "openai_responses",
			parse: func() (*MaheshvaraResponse, error) {
				return OpenAIResponsesResponseToMaheshvara(&OpenAIResponsesResponse{
					ID: "resp_source", Object: "response", CreatedAt: 1, Status: "completed", Model: "source-model",
					Output: []ResponsesOutput{{
						ID: "msg_1", Type: "message", Status: "completed", Role: "assistant",
						Content: []ResponsesOutputContent{{Type: "output_text", Text: "hello"}},
					}},
					Usage: &ResponsesUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
				})
			},
		},
	}
}

func TestMaheshvaraReasoningSignaturesAreProviderBoundAndLegacyEnvelopeIsReadOnly(t *testing.T) {
	response := &MaheshvaraResponse{
		ID: "resp_reasoning", Model: "source-model", Status: "completed", StopReason: "stop",
		Output: []MaheshvaraOutputItem{{
			ID: "rs_1", Type: CanonicalOutputReasoning, Status: "completed",
			Content: []MaheshvaraContentPart{{
				Type: CanonicalContentReasoning, Text: "concise rationale", ReasoningText: "concise rationale",
				EncryptedContent: "provider-ciphertext",
				ReasoningSummary: []CanonicalReasoningSummary{{Type: "summary_text", Text: "summary"}},
			}},
		}},
	}

	claude, err := MaheshvaraToAnthropicResponse(response)
	if err != nil {
		t.Fatalf("render Anthropic reasoning: %v", err)
	}
	if len(claude.Content) != 1 || claude.Content[0].Type != "thinking" || claude.Content[0].Signature != "" {
		t.Fatalf("provider ciphertext must not be smuggled into an Anthropic signature: %+v", claude.Content)
	}

	gemini, err := MaheshvaraToGeminiResponse(response)
	if err != nil {
		t.Fatalf("render Gemini reasoning: %v", err)
	}
	if len(gemini.Candidates) != 1 || len(gemini.Candidates[0].Content.Parts) != 1 || gemini.Candidates[0].Content.Parts[0].ThoughtSignature != "" {
		t.Fatalf("provider ciphertext must not be smuggled into a Gemini signature: %+v", gemini)
	}

	legacyEnvelope, err := encodeMaheshvaraReasoningEnvelope("concise rationale", "provider-ciphertext", []CanonicalReasoningSummary{{Type: "summary_text", Text: "summary"}})
	if err != nil {
		t.Fatalf("encode legacy envelope: %v", err)
	}
	roundTripped, err := AnthropicResponseToMaheshvara(&ClaudeResponse{ID: "msg_legacy", Model: "source-model", Content: []ClaudeContent{{Type: "thinking", Thinking: "concise rationale", Signature: legacyEnvelope}}})
	if err != nil {
		t.Fatalf("parse legacy Anthropic reasoning envelope: %v", err)
	}
	if len(roundTripped.Output) != 1 || len(roundTripped.Output[0].Content) != 1 {
		t.Fatalf("legacy reasoning envelope did not decode: %+v", roundTripped.Output)
	}
	part := roundTripped.Output[0].Content[0]
	if part.EncryptedContent != "provider-ciphertext" || part.ReasoningText != "concise rationale" || len(part.ReasoningSummary) != 1 {
		t.Fatalf("legacy reasoning envelope fields were lost: %+v", part)
	}
	if part.Signature != "" || part.SignatureProvider != CanonicalSignatureProviderMaheshvara {
		t.Fatalf("legacy envelope must remain Maheshvara-owned: %+v", part)
	}

	filtered, err := AnthropicResponseToMaheshvara(&ClaudeResponse{
		ID: "msg_external", Model: "source-model", Content: []ClaudeContent{{Type: "redacted_thinking", Data: "opaque-vendor-data"}},
	})
	if err != nil {
		t.Fatalf("parse external redacted thinking: %v", err)
	}
	if len(filtered.Output) != 0 {
		t.Fatalf("external redacted thinking must not become Maheshvara output: %+v", filtered.Output)
	}
}

func decodeMaheshvaraTestJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("wire payload is not valid JSON: %v\n%s", err, body)
	}
	return payload
}

func assertMaheshvaraWireFieldNotEmpty(t *testing.T, payload map[string]any, field string) {
	t.Helper()
	value, ok := payload[field]
	if !ok {
		t.Fatalf("wire payload is missing %q: %+v", field, payload)
	}
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			t.Fatalf("wire payload field %q is empty: %+v", field, payload)
		}
	case string:
		if strings.TrimSpace(typed) == "" {
			t.Fatalf("wire payload field %q is empty: %+v", field, payload)
		}
	case nil:
		t.Fatalf("wire payload field %q is null: %+v", field, payload)
	}
}
