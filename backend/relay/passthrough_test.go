package relay

import (
	"encoding/json"
	"testing"
)

// PassthroughBody 必须保留原始请求里的全部字段（含上游特有/未知扩展），
// 只覆盖 model，并按需补 stream / stream_options.include_usage。
func TestPassthroughBodyPreservesUnknownFields(t *testing.T) {
	original := []byte(`{
		"model": "client-model",
		"messages": [{"role": "user", "content": "hi"}],
		"cache_control": {"type": "ephemeral"},
		"thinking": {"type": "enabled", "budget_tokens": 1024},
		"some_future_field": {"nested": [1, 2, 3]}
	}`)

	out, err := PassthroughBody(original, "upstream-model", false, false)
	if err != nil {
		t.Fatalf("PassthroughBody error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal passthrough body: %v", err)
	}

	if parsed["model"] != "upstream-model" {
		t.Errorf("model not rewritten: got %v", parsed["model"])
	}
	for _, key := range []string{"messages", "cache_control", "thinking", "some_future_field"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("field %q dropped by passthrough", key)
		}
	}
	if _, ok := parsed["stream"]; ok {
		t.Errorf("stream should not be set when ensureStream=false")
	}
}

// ensureStream=true 时补 stream=true；OpenAI 系还应补 stream_options.include_usage，
// 且不覆盖调用方已有的 stream_options 字段。
func TestPassthroughBodyEnsureStream(t *testing.T) {
	original := []byte(`{"model":"m","stream_options":{"foo":"bar"}}`)

	out, err := PassthroughBody(original, "", true, true)
	if err != nil {
		t.Fatalf("PassthroughBody error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["stream"] != true {
		t.Errorf("stream not set to true: %v", parsed["stream"])
	}
	if parsed["model"] != "m" {
		t.Errorf("empty modelName must not overwrite model: %v", parsed["model"])
	}
	so, ok := parsed["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing or wrong type: %v", parsed["stream_options"])
	}
	if so["include_usage"] != true {
		t.Errorf("include_usage not set: %v", so["include_usage"])
	}
	if so["foo"] != "bar" {
		t.Errorf("existing stream_options field clobbered: %v", so["foo"])
	}
}

func TestFormatMatchesPlatform(t *testing.T) {
	cases := []struct {
		format   FormatType
		platform Platform
		want     bool
	}{
		{FormatClaude, PlatformAnthropic, true},
		{FormatGemini, PlatformGemini, true},
		{FormatOpenAI, PlatformOpenAI, true},
		{FormatOpenAI, PlatformDeepSeek, true},
		{FormatOpenAI, PlatformAzure, true},
		{FormatDeepSeek, PlatformOpenAI, true},
		{FormatClaude, PlatformOpenAI, false},
		{FormatGemini, PlatformAnthropic, false},
		{FormatOpenAI, PlatformAnthropic, false},
		{FormatOpenAI, PlatformGemini, false},
		{FormatUnknown, PlatformOpenAI, false},
	}
	for _, tc := range cases {
		if got := FormatMatchesPlatform(tc.format, tc.platform); got != tc.want {
			t.Errorf("FormatMatchesPlatform(%s, %s) = %v, want %v", tc.format, tc.platform, got, tc.want)
		}
	}
}
