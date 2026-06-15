package server

import (
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

func TestSelectResponsesTargetFormatDefaultClaudeTransforms(t *testing.T) {
	model := config.ModelRef{Name: "claude-opus-4-8", Platform: "anthropic"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected Claude model to transform by default, got error: %v", err)
	}
	if format != relay.FormatClaude || mode != "transformed_responses" {
		t.Fatalf("expected Claude transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatNativeClaudeErrors(t *testing.T) {
	model := config.ModelRef{Name: "claude-opus-4-8", Platform: "anthropic"}

	_, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{UpstreamMode: "native"})

	if err == nil {
		t.Fatal("expected native Claude model without Responses support to error")
	}
	if mode != "native_responses" {
		t.Fatalf("expected native_responses mode, got %q", mode)
	}
	if !strings.Contains(err.Error(), "does not declare Responses API support") {
		t.Fatalf("expected Responses support error, got %v", err)
	}
}

// 旧的 "openai" apiFormat 现在归一化为 chat_completions，因此走 transform 而非
// native Responses——这是有意的契约变更：只有用户明确选 responses 才透传，避免
// 旧实现里 Responses→Responses 的有损重建导致 codex 1 秒断连。
func TestSelectResponsesTargetFormatLegacyOpenAITransforms(t *testing.T) {
	model := config.ModelRef{Name: "gpt-4.1", Platform: "openai"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformOpenAI, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected legacy openai model to transform, got error: %v", err)
	}
	if format != relay.FormatOpenAIChat || mode != "transformed_responses" {
		t.Fatalf("expected chat transform target, got format=%q mode=%q", format, mode)
	}
}

// 明确选 responses apiFormat → 走 native Responses（透传），不转换。
func TestSelectResponsesTargetFormatExplicitResponsesIsNative(t *testing.T) {
	model := config.ModelRef{Name: "gpt-5-codex", Platform: "responses"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformOpenAI, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected responses apiFormat to use native Responses, got error: %v", err)
	}
	if format != relay.FormatResponses || mode != "native_responses" {
		t.Fatalf("expected native Responses target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatAutoGeminiTransforms(t *testing.T) {
	model := config.ModelRef{Name: "gemini-2.5-pro", Platform: "gemini"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformGemini, config.ResponsesConfig{UpstreamMode: "auto"})

	if err != nil {
		t.Fatalf("expected Gemini model to transform, got error: %v", err)
	}
	if format != relay.FormatGemini || mode != "transformed_responses" {
		t.Fatalf("expected Gemini transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatUnknownWithChatEndpointTransforms(t *testing.T) {
	chatCompletions := true
	model := config.ModelRef{
		Name:      "custom-chat-model",
		Platform:  "custom",
		Endpoints: &config.EndpointCapabilities{ChatCompletions: &chatCompletions},
	}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformUnknown, config.ResponsesConfig{UpstreamMode: "auto"})

	if err != nil {
		t.Fatalf("expected explicit chat endpoint to transform, got error: %v", err)
	}
	if format != relay.FormatOpenAIChat || mode != "transformed_responses" {
		t.Fatalf("expected OpenAI chat transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatExplicitClaudeMessagesFalseErrors(t *testing.T) {
	claudeMessages := false
	model := config.ModelRef{
		Name:      "claude-opus-4-8",
		Platform:  "anthropic",
		Endpoints: &config.EndpointCapabilities{ClaudeMessages: &claudeMessages},
	}

	_, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{UpstreamMode: "auto"})

	if err == nil {
		t.Fatal("expected explicit Claude Messages false to prevent transform")
	}
	if mode != "auto_responses" {
		t.Fatalf("expected auto_responses mode, got %q", mode)
	}
	if !strings.Contains(err.Error(), "does not declare a transformable endpoint") {
		t.Fatalf("expected transformable endpoint error, got %v", err)
	}
}
