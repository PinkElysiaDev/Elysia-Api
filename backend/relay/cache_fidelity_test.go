package relay

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const cacheChatFixture = `{"model":"m","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"system","content":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"developer","content":"stable developer"},{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}},"cache_control":{"type":"ephemeral"}}]}`

func cacheJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCacheChatToAnthropicSemanticFields(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(cacheChatFixture))
	if err != nil {
		t.Fatal(err)
	}
	if req.CacheControl == nil || req.Messages[0].Content[0].CacheControl == nil || req.Tools[0].CacheControl == nil {
		t.Error("cache intent must enter semantic fields, not only Raw")
	}
	body, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatal(err)
	}
	got := cacheJSON(t, body)
	system, ok := got["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system blocks lost: %s", body)
	}
	if system[0].(map[string]any)["cache_control"] == nil || system[1].(map[string]any)["text"] != "stable developer" {
		t.Fatalf("system fidelity: %s", body)
	}
	if got["cache_control"] == nil || got["tools"].([]any)[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("cache intent lost: %s", body)
	}
	msg := got["messages"].([]any)[0].(map[string]any)
	if msg["content"].([]any)[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("text marker lost: %s", body)
	}
}

func TestCacheCustomMappingIndependentOfPresetID(t *testing.T) {
	ClearCustomProtocols()
	t.Cleanup(ClearCustomProtocols)
	req, _ := OpenAIChatToMaheshvara([]byte(cacheChatFixture))
	req.PromptCacheRetention = json.RawMessage(`"24h"`)
	var config CustomProtocolConfig
	err := json.Unmarshal([]byte(`{"id":"independent-cache-wire","request":{"path":"/vendor","shape":"anthropic","body":{"payload":{"system":{"field":"anthropic_system"},"cache":{"field":"cache_control","omitIfEmpty":true},"retention":{"field":"prompt_cache_retention"},"messages":{"field":"messages"},"tools":{"field":"tools"}}}},"response":{"textPath":"text","usagePath":"metrics"}}`), &config)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterCustomProtocol(config); err != nil {
		t.Fatal(err)
	}
	preview, err := RenderCustomProtocolRequest(req, config)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := RenderRegisteredCustomProtocolRequest(req, config.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(preview.Body) != string(wire.Body) {
		t.Fatal("preview and wire differ")
	}
	root := cacheJSON(t, wire.Body)
	if _, leaked := root["system"]; leaked {
		t.Fatal("shape must not inject unconfigured top-level system")
	}
	payload := root["payload"].(map[string]any)
	if payload["cache"] == nil || payload["retention"] != "24h" || len(payload["system"].([]any)) != 2 {
		t.Fatalf("mapped fields lost: %s", wire.Body)
	}
	for i := 0; i < 10; i++ {
		repeat, err := RenderRegisteredCustomProtocolRequest(req, config.ID)
		if err != nil || string(repeat.Body) != string(wire.Body) {
			t.Fatal("unstable rendering")
		}
	}
	// A template which does not request system/cache must not have either injected.
	config.Request.Body = json.RawMessage(`{"messages":{"field":"messages"}}`)
	native, _ := AnthropicToMaheshvara([]byte(`{"model":"m","system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"}]}`))
	omitted, err := RenderCustomProtocolRequest(native, config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(omitted.Body), "system") || strings.Contains(string(omitted.Body), "cache_control") {
		t.Fatalf("unconfigured fields injected: %s", omitted.Body)
	}
}

func TestCacheAnthropicMediaAndToolResult(t *testing.T) {
	req, err := AnthropicToMaheshvara([]byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/image.png"},"cache_control":{"type":"ephemeral"}},{"type":"document","source":{"type":"url","url":"https://example.com/doc.pdf"},"cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral"}}],"is_error":true,"cache_control":{"type":"ephemeral"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatal(err)
	}
	blocks := cacheJSON(t, body)["messages"].([]any)[0].(map[string]any)["content"].([]any)
	for _, block := range blocks {
		if block.(map[string]any)["cache_control"] == nil {
			t.Errorf("marker lost: %v", block)
		}
	}
	result := blocks[2].(map[string]any)
	if _, ok := result["content"].([]any); !ok || result["is_error"] != true {
		t.Fatalf("structured tool result changed: %s", body)
	}
}

func TestCacheUsageNormalizationAndStreamMerge(t *testing.T) {
	cases := []struct {
		name, body                     string
		input, cached, created, output int
	}{
		{"anthropic", `{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}`, 100, 70, 20, 5},
		{"anthropic tiers only", `{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation":{"ephemeral_5m_input_tokens":12,"ephemeral_1h_input_tokens":8}}`, 100, 70, 20, 5},
		{"chat", `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":70}}`, 100, 70, 0, 5},
		{"responses", `{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}`, 100, 70, 0, 5},
		{"gemini", `{"promptTokenCount":100,"candidatesTokenCount":5,"cachedContentTokenCount":70}`, 100, 70, 0, 5},
		{"gemini thinking and tools", `{"promptTokenCount":80,"toolUsePromptTokenCount":20,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":70}`, 100, 70, 0, 5},
		{"chat zero root", `{"prompt_tokens":100,"completion_tokens":5,"cached_tokens":0,"prompt_tokens_details":{"cached_tokens":70}}`, 100, 70, 0, 5},
		{"chat compatible details", `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cache_read_tokens":70,"cached_creation_tokens":20}}`, 100, 70, 20, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := cacheJSON(t, []byte(tc.body))
			if strings.HasPrefix(tc.name, "anthropic") {
				var native ClaudeUsage
				if err := json.Unmarshal([]byte(tc.body), &native); err != nil {
					t.Fatal(err)
				}
				u := maheshvaraUsageFromClaudeUsage(native)
				if u.InputTokens != tc.input || u.CacheCreationInputTokens != tc.created {
					t.Errorf("native usage mismatch: %+v", u)
				}
			}
			for _, usage := range []*MaheshvaraUsage{customUsageAtWithAliases(map[string]any{"u": raw}, "u", nil), maheshvaraUsageFromRawMap(raw)} {
				if usage.InputTokens != tc.input || usage.CachedInputTokens != tc.cached || usage.CacheCreationInputTokens != tc.created || usage.TotalTokens != tc.input+tc.output {
					t.Errorf("usage = %+v", usage)
				}
			}
		})
	}
	start := maheshvaraUsageFromRawMap(cacheJSON(t, []byte(cases[0].body)))
	tail := maheshvaraUsageFromRawMap(cacheJSON(t, []byte(`{"output_tokens":9}`)))
	merged := mergeMaheshvaraStreamUsage(start, tail)
	if merged.InputTokens != 100 || merged.CachedInputTokens != 70 || merged.TotalTokens != 109 {
		t.Fatalf("tail clobbered usage: %+v", merged)
	}
	aliases := map[string][]string{"input": {"total_in"}, "cached": {"hit"}, "cache_creation": {"write"}}
	u := customUsageAtWithAliases(map[string]any{"u": cacheJSON(t, []byte(`{"total_in":100,"hit":70,"write":20,"cache_read_input_tokens":999}`))}, "u", aliases)
	if u.InputTokens != 100 || u.CachedInputTokens != 70 || u.CacheCreationInputTokens != 20 {
		t.Fatalf("aliases overridden: %+v", u)
	}
}

func TestCacheProtocolIsolationAndNoAutomaticCaching(t *testing.T) {
	gemini, _ := GeminiToMaheshvara([]byte(`{"cachedContent":"cachedContents/abc","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`), "m")
	claude, err := MaheshvaraToAnthropic(gemini)
	if err != nil {
		t.Fatal(err)
	}
	if cacheJSON(t, claude)["cache_control"] != nil {
		t.Fatal("Gemini resource emitted as Anthropic cache object")
	}
	anthropic, _ := OpenAIChatToMaheshvara([]byte(cacheChatFixture))
	geminiBody, err := MaheshvaraToGemini(anthropic)
	if err != nil {
		t.Fatal(err)
	}
	if cacheJSON(t, geminiBody)["cachedContent"] != nil {
		t.Fatal("Anthropic object emitted as Gemini resource")
	}
	plain, _ := OpenAIChatToMaheshvara([]byte(`{"model":"m","messages":[{"role":"system","content":"stable"},{"role":"user","content":"hi"}]}`))
	body, _ := MaheshvaraToAnthropic(plain)
	if strings.Contains(string(body), "cache_control") {
		t.Fatal("automatic caching was not requested")
	}
	if !reflect.DeepEqual(cacheJSON(t, body)["system"], "stable") {
		t.Fatalf("unmarked system compatibility: %s", body)
	}
}

func TestCacheNativeBlockOrderAndAppendedTurn(t *testing.T) {
	const body = `{"model":"m","system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"lookup","input":{},"cache_control":{"type":"ephemeral"}},{"type":"text","text":"after breakpoint"}]}]}`
	req, _ := AnthropicToMaheshvara([]byte(body))
	first, _ := MaheshvaraToAnthropic(req)
	original := cacheJSON(t, []byte(body))
	if !reflect.DeepEqual(cacheJSON(t, first)["messages"], original["messages"]) {
		t.Fatalf("cache boundary moved: %s", first)
	}
	req.Messages = append(req.Messages, MaheshvaraMessage{Role: "user", Content: []MaheshvaraContentPart{{Type: MaheshvaraContentText, Text: "next turn"}}})
	next, _ := MaheshvaraToAnthropic(req)
	if !reflect.DeepEqual(cacheJSON(t, first)["messages"].([]any)[0], cacheJSON(t, next)["messages"].([]any)[0]) {
		t.Fatal("appended turn changed cached prefix")
	}
	chat, _ := MaheshvaraToOpenAIChat(req)
	system := cacheJSON(t, chat)["messages"].([]any)[0].(map[string]any)["content"]
	if _, ok := system.([]any); !ok {
		t.Fatalf("Anthropic -> Chat lost system blocks: %s", chat)
	}
}

func TestCacheAliasesOverrideDefaultsEvenWhenZero(t *testing.T) {
	raw := cacheJSON(t, []byte(`{"in":100,"hit":0,"cache_read_input_tokens":70}`))
	u := customUsageAtWithAliases(map[string]any{"u": raw}, "u", map[string][]string{"input": {"in"}, "cached": {"hit"}})
	if u.CachedInputTokens != 0 {
		t.Fatalf("explicit zero alias replaced by default: %+v", u)
	}
}

func TestCacheMessageMarkerAndTextDocument(t *testing.T) {
	req, _ := OpenAIChatToMaheshvara([]byte(`{"model":"m","messages":[{"role":"user","content":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`))
	body, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatal(err)
	}
	msg := cacheJSON(t, body)["messages"].([]any)[0].(map[string]any)
	if msg["cache_control"] != nil || msg["content"].([]any)[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("message marker not translated to a block: %s", body)
	}
	const native = `{"model":"m","messages":[{"role":"user","content":[{"type":"document","title":"reference","source":{"type":"text","media_type":"text/plain","data":"stable document"},"cache_control":{"type":"ephemeral"}}]}]}`
	req, _ = AnthropicToMaheshvara([]byte(native))
	body, err = MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cacheJSON(t, body)["messages"], cacheJSON(t, []byte(native))["messages"]) {
		t.Fatalf("native document changed: %s", body)
	}
}

func TestCachePartialUsageFramesAreCumulative(t *testing.T) {
	config := CustomProtocolConfig{ID: "partial-cache", Request: CustomProtocolRequest{PathTemplate: "/x", BodyTemplate: `{}`}, Response: CustomProtocolResponse{Stream: &CustomProtocolStreamMapping{Frames: []CustomProtocolStreamFrame{
		{Event: "message_start", Response: &CustomProtocolResponse{UsagePath: "message.usage"}},
		{Event: "message_delta", Response: &CustomProtocolResponse{UsagePath: "usage"}},
	}}}}
	custom, err := NewCustomProtocolStreamDecoder(config)
	if err != nil {
		t.Fatal(err)
	}
	native := NewMaheshvaraStreamDecoder(FormatClaude)
	wires := []SSEEvent{
		{Event: "message_start", Data: `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":20}}}`},
		{Event: "message_delta", Data: `{"type":"message_delta","usage":{"cache_read_input_tokens":70}}`},
		{Event: "message_delta", Data: `{"type":"message_delta","usage":{"input_tokens":10,"output_tokens":5}}`},
	}
	for _, name := range []string{"native", "custom"} {
		var snapshots []*MaheshvaraUsage
		for _, wire := range wires {
			var events []MaheshvaraStreamEvent
			if name == "native" {
				events, err = native.Decode(wire)
			} else {
				events, _, err = custom.Decode(wire)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Usage != nil {
					snapshots = append(snapshots, event.Usage)
				}
			}
		}
		last := snapshots[len(snapshots)-1]
		if last.InputTokens != 100 || last.CachedInputTokens != 70 || last.CacheCreationInputTokens != 20 || last.TotalTokens != 105 {
			t.Errorf("%s partial merge: %+v", name, last)
		}
		if snapshots[0].InputTokens != 30 || snapshots[0].CachedInputTokens != 0 {
			t.Errorf("%s previous snapshot mutated", name)
		}
	}
}

func TestCacheToolMarkersSurviveChatRoundTrip(t *testing.T) {
	const body = `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"lookup","input":{},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"result","cache_control":{"type":"ephemeral"}}]}]}`
	req, _ := AnthropicToMaheshvara([]byte(body))
	chat, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := OpenAIChatToMaheshvara(chat)
	if err != nil {
		t.Fatal(err)
	}
	again, err := MaheshvaraToAnthropic(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cacheJSON(t, again)["messages"], cacheJSON(t, []byte(body))["messages"]) {
		t.Fatalf("tool cache markers lost through Chat: %s", again)
	}
}
