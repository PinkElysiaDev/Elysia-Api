package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

type cacheWireFixture struct{ id, platform, path, request, response, stream string }

func TestCacheUsageMissingOutputRemainsUnknownUntilTail(t *testing.T) {
	var record usageRecord
	updateRecordProtocolUsage(&record, &protocol.Usage{
		Input:     &protocol.Counter{Count: 100, Origin: protocol.ObservedCount},
		CacheRead: &protocol.Counter{Count: 70, Origin: protocol.ObservedCount},
	})
	if record.Usage.TotalTokens != nil || record.Usage.OutputTokens != nil {
		t.Fatal("missing output became zero", record.Usage)
	}
	updateRecordProtocolUsage(&record, &protocol.Usage{Output: &protocol.Counter{Count: 5, Origin: protocol.ObservedCount}})
	if derefInt(record.Usage.TotalTokens) != 105 || derefInt(record.UsageDetail.TotalTokens) != 105 || derefInt(record.Usage.CacheHitTokens) != 70 {
		t.Fatal("output tail clobbered previous counters", record.Usage)
	}
}

func TestCacheGeminiReferenceDoesNotInventSystem(t *testing.T) {
	compiled := compileFixtureDefinition(t, presetDefinition(t, "google-generate-content"))
	options := protocol.EvaluationContext{Scope: protocol.Scope{Provider: "provider", Account: "account", Model: "m"}, Values: protocol.Object{"model": protocol.StringValue("m")}}
	request, err := compiled.DecodeRequest(t.Context(), []byte(`{"cachedContent":"cachedContents/existing","contents":[{"role":"user","parts":[{"text":"question"}]}]}`), options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := compiled.EncodeRequest(t.Context(), request, options)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["systemInstruction"] != nil || wire["tools"] != nil || wire["cachedContent"] != "cachedContents/existing" {
		t.Fatal("invented conflicting cache context", string(body))
	}
}

func TestCachePresetStablePrefix(t *testing.T) {
	options := protocol.EvaluationContext{Scope: protocol.Scope{Provider: "provider", Account: "account", Model: "grp"}, Values: protocol.Object{"model": protocol.StringValue("grp")}}
	for _, source := range cacheWireFixtures() {
		ingress := compileFixtureDefinition(t, presetDefinition(t, source.id))
		for _, target := range cacheWireFixtures() {
			t.Run(source.platform+"_to_"+target.id, func(t *testing.T) {
				upstream := compileFixtureDefinition(t, presetDefinition(t, target.id))
				request, err := ingress.DecodeRequest(t.Context(), []byte(source.request), options)
				if err != nil {
					t.Fatal(err)
				}
				first, err := upstream.EncodeRequest(t.Context(), request, options)
				isOpenAI := func(name string) bool { return name == "openai" || name == "responses" }
				isCompatible := source.platform == target.platform || (isOpenAI(source.platform) && isOpenAI(target.platform))
				if !isCompatible {
					if err == nil {
						t.Fatal("foreign cache semantics silently accepted", string(first))
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				for range 5 {
					repeated, err := upstream.EncodeRequest(t.Context(), request, options)
					if err != nil || string(first) != string(repeated) {
						t.Fatal("identical request rendered differently", err)
					}
				}
				request.Content = append(request.Content, protocol.Node{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("new last turn")}}})
				next, err := upstream.EncodeRequest(t.Context(), request, options)
				if err != nil {
					t.Fatal(err)
				}
				assertCacheWirePrefix(t, target.platform, first, next)
				result := protocol.StringValue("second result")
				if target.platform == "gemini" {
					result = mustProtocolValue(t, `{"result":"second result"}`)
				}
				request.Content = append(request.Content,
					protocol.Node{Kind: protocol.ToolCallNode, CallID: protocol.StringValue("t2"), Name: protocol.StringValue("lookup"), Input: &protocol.ToolInput{Kind: protocol.JSONInput, Value: mustProtocolValue(t, `{}`)}},
					protocol.Node{Kind: protocol.ToolResultNode, CallID: protocol.StringValue("t2"), Name: protocol.StringValue("lookup"), Payload: result})
				secondRound, err := upstream.EncodeRequest(t.Context(), request, options)
				if err != nil || !strings.Contains(string(secondRound), "second result") {
					t.Fatal(string(secondRound), err)
				}
				assertCacheWirePrefix(t, target.platform, next, secondRound)
			})
		}
	}
}

func assertCacheWirePrefix(t *testing.T, platform string, beforeBody, afterBody []byte) {
	t.Helper()
	var before, after map[string]any
	if err := json.Unmarshal(beforeBody, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(afterBody, &after); err != nil {
		t.Fatal(err)
	}
	field := map[string]string{"openai": "messages", "anthropic": "messages", "responses": "input", "gemini": "contents"}[platform]
	oldItems, newItems := before[field].([]any), after[field].([]any)
	if len(newItems) <= len(oldItems) {
		t.Fatal("new turn missing", string(afterBody))
	}
	after[field] = newItems[:len(oldItems)]
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("appending changed prefix: before=%s after=%s", beforeBody, afterBody)
	}
}

func cacheWireFixtures() []cacheWireFixture {
	return []cacheWireFixture{
		{"openai-chat-completions", "openai", "/v1/chat/completions",
			`{"model":"grp","prompt_cache_key":"stable-key","prompt_cache_retention":"24h","messages":[{"role":"system","content":[{"type":"text","text":"stable system"}]},{"role":"user","content":"question"},{"role":"assistant","tool_calls":[{"id":"t1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"t1","content":"result"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
			`{"id":"r1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":70}}}`,
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":5,\"total_tokens\":105,\"prompt_tokens_details\":{\"cached_tokens\":70}}}\n\ndata: [DONE]\n\n"},
		{"anthropic-messages", "anthropic", "/v1/messages",
			`{"model":"grp","max_tokens":64,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"question"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"lookup","input":{},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"result","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]}`,
			`{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation_input_tokens":20}}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"r1\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0,\"cache_read_input_tokens\":70,\"cache_creation_input_tokens\":20}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"openai-responses", "responses", "/v1/responses",
			`{"model":"grp","instructions":"stable system","prompt_cache_key":"stable-key","prompt_cache_retention":"24h","input":[{"role":"user","content":[{"type":"input_text","text":"question"}]},{"type":"function_call","call_id":"t1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"t1","output":"result"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`,
			`{"id":"r1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":70}}}`,
			"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg1\",\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"response.content_part.added\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"total_tokens\":105,\"input_tokens_details\":{\"cached_tokens\":70}}}}\n\n"},
		{"google-generate-content", "gemini", "/v1beta/models/grp:generateContent",
			`{"cachedContent":"cachedContents/stable","systemInstruction":{"parts":[{"text":"stable system"}]},"contents":[{"role":"user","parts":[{"text":"question"}]},{"role":"model","parts":[{"functionCall":{"id":"t1","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"t1","name":"lookup","response":{"result":"result"}}}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"totalTokenCount":105,"cachedContentTokenCount":70}}`,
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":5,\"totalTokenCount\":105,\"cachedContentTokenCount\":70}}\n\n"},
	}
}

func TestCacheWireMatrix(t *testing.T) {
	for _, source := range cacheWireFixtures() {
		for _, target := range cacheWireFixtures() {
			for _, mode := range []string{"builtin", "preset", "copy"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s_to_%s/%s/stream=%v", source.platform, target.platform, mode, stream), func(t *testing.T) {
						captured := make(chan []byte, 1)
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							body, _ := io.ReadAll(r.Body)
							captured <- body
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								_, _ = io.WriteString(w, target.stream)
							} else {
								w.Header().Set("Content-Type", "application/json")
								_, _ = io.WriteString(w, target.response)
							}
						}))
						defer upstream.Close()
						platform := target.platform
						if mode == "preset" {
							platform = "custom:" + target.id
						}
						if mode == "copy" {
							platform = "custom:copy-" + target.id
						}
						base := upstream.URL
						if target.platform == "openai" || target.platform == "responses" {
							base += "/v1"
						}
						groups := presetGroup(t, platform, base)
						tools := true
						groups[0].ToolsCapable = &tools
						groups[0].Models[0].ToolsCapable = true
						definition := presetDefinition(t, target.id)
						if mode == "copy" {
							definition.ID = "copy-" + definition.ID
						}
						s := newTestServerWithStore(t, groups, definition)
						var request map[string]any
						if err := json.Unmarshal([]byte(source.request), &request); err != nil {
							t.Fatal(err)
						}
						path := source.path
						if source.platform != "gemini" {
							request["stream"] = stream
							if source.platform == "openai" && stream {
								request["stream_options"] = map[string]any{"include_usage": true}
							}
						} else if stream {
							path = strings.ReplaceAll(path, ":generateContent", ":streamGenerateContent")
						}
						encoded, _ := json.Marshal(request)
						c, rec := adminProtocolContext("POST", path, string(encoded))
						if source.platform == "gemini" {
							c.Params = gin.Params{{Key: "action", Value: strings.TrimPrefix(path, "/v1beta/models/")}}
						}
						if source.platform == "responses" {
							s.responses(c)
						} else {
							s.chatCompletions(c)
						}
						isOpenAI := func(platform string) bool { return platform == "openai" || platform == "responses" }
						isCompatible := source.platform == target.platform || (isOpenAI(source.platform) && isOpenAI(target.platform))
						if !isCompatible {
							if rec.Code < 400 || rec.Code >= 500 {
								t.Fatalf("incompatible cache contract not rejected: %d %s", rec.Code, rec.Body)
							}
							select {
							case wire := <-captured:
								t.Fatalf("incompatible request reached upstream: %s", wire)
							default:
							}
							return
						}
						if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
							t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
						}
						var outbound []byte
						select {
						case outbound = <-captured:
						default:
							t.Fatal("no upstream request")
						}
						if !strings.Contains(string(outbound), "stable system") || !strings.Contains(string(outbound), "lookup") || !strings.Contains(string(outbound), "result") {
							t.Fatalf("stable prompt/tool history lost: %s", outbound)
						}
						var sent map[string]any
						_ = json.Unmarshal(outbound, &sent)
						if (source.platform == "openai" || source.platform == "responses") && (target.platform == "openai" || target.platform == "responses") {
							if sent["prompt_cache_key"] != "stable-key" || sent["prompt_cache_retention"] != "24h" {
								t.Fatalf("cache key/retention lost: %s", outbound)
							}
						}
						if target.platform == "gemini" {
							if source.platform == "gemini" && sent["cachedContent"] != "cachedContents/stable" {
								t.Fatalf("cache reference lost: %s", outbound)
							}
							if source.platform != "gemini" && sent["cachedContent"] != nil {
								t.Fatalf("foreign cache control leaked: %s", outbound)
							}
						}
						if target.platform == "anthropic" && (source.platform == "openai" || source.platform == "anthropic") {
							blocks, ok := sent["system"].([]any)
							if !ok || len(blocks) == 0 || blocks[0].(map[string]any)["cache_control"] == nil {
								t.Fatalf("system cache breakpoint lost: %s", outbound)
							}
							if !reflect.DeepEqual(blocks[0].(map[string]any)["cache_control"], map[string]any{"type": "ephemeral", "ttl": "1h"}) {
								t.Fatalf("cache TTL changed: %s", outbound)
							}
							if sent["tools"].([]any)[0].(map[string]any)["cache_control"] == nil {
								t.Fatalf("tool cache breakpoint lost: %s", outbound)
							}
						}
						// Assert persisted normalized usage separately from the response wire.
						items := latestUsageRecords(t, s)
						if len(items) != 1 || items[0].StatusCode != http.StatusOK {
							t.Fatalf("usage records: %d", len(items))
						}
						data, found, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
						if err != nil || !found {
							t.Fatalf("persisted usage: %v", err)
						}
						var record usageRecord
						if err := json.Unmarshal(data, &record); err != nil {
							t.Fatal(err)
						}
						if derefInt(record.Usage.CacheHitTokens) != 70 || derefInt(record.Usage.InputTokens) != 100 || derefInt(record.Usage.TotalTokens) != 105 {
							t.Fatalf("wrong persisted usage: %+v response=%s", record.Usage, rec.Body.String())
						}
						cacheField := "cached_tokens"
						if source.platform == "anthropic" {
							cacheField = "cache_read_input_tokens"
						}
						if source.platform == "gemini" {
							cacheField = "cachedContentTokenCount"
						}
						if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"%s":70`, cacheField)) {
							t.Fatalf("downstream cache counter lost: %s", rec.Body.String())
						}
					})
				}
			}
		}
	}
}

func TestCacheUsageCreationAndAbsentFields(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, phase := range []string{"creation", "absent"} {
			t.Run(fmt.Sprintf("%s/stream=%v", phase, stream), func(t *testing.T) {
				usage := `{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":90}`
				if phase == "absent" {
					usage = `{"input_tokens":100}`
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"r\",\"role\":\"assistant\",\"content\":[],\"usage\":%s}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\ndata: {\"type\":\"message_stop\"}\n\n", usage)
					} else {
						usage = strings.TrimSuffix(usage, "}") + `,"output_tokens":5}`
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"content":[{"type":"text","text":"ok"}],"usage":%s}`, usage)
					}
				}))
				defer upstream.Close()
				s := newTestServerWithStore(t, presetGroup(t, "custom:anthropic-messages", upstream.URL))
				c, rec := chatRequestContext(fmt.Sprintf(`{"model":"grp","max_tokens":64,"stream":%v,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`, stream))
				s.chatCompletions(c)
				if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				items := latestUsageRecords(t, s)
				data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
				if err != nil {
					t.Fatal(err)
				}
				var record usageRecord
				_ = json.Unmarshal(data, &record)
				if derefInt(record.Usage.InputTokens) != 100 || derefInt(record.Usage.TotalTokens) != 105 || derefInt(record.Usage.CacheHitTokens) != 0 {
					t.Fatalf("phase usage: %+v", record.Usage)
				}
				if phase == "creation" && derefInt(record.UsageDetail.CacheCreationInputTokens) != 90 {
					t.Fatalf("creation not persisted: %+v", record.UsageDetail)
				}
				if phase == "creation" && !strings.Contains(rec.Body.String(), `"cache_write_tokens":90`) {
					t.Fatalf("creation missing from Chat-compatible downstream details: %s", rec.Body.String())
				}
			})
		}
	}
}
