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

	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

type cacheWireFixture struct{ id, platform, path, request, response, stream string }

func TestCacheUsageMissingOutputStillHasTotal(t *testing.T) {
	var record usageRecord
	updateRecordUsageFromMaheshvara(&record, &relay.MaheshvaraUsage{
		InputTokens: 100, CachedInputTokens: 70, TotalTokens: 100, TotalTokensInferred: true,
	})
	if derefInt(record.Usage.TotalTokens) != 100 || derefInt(record.UsageDetail.TotalTokens) != 100 {
		t.Fatalf("input-only usage lost its total: %+v", record.Usage)
	}
	updateRecordUsageFromMaheshvara(&record, &relay.MaheshvaraUsage{
		OutputTokens: 5, TotalTokens: 5, TotalTokensInferred: true,
	})
	if derefInt(record.Usage.TotalTokens) != 105 || derefInt(record.UsageDetail.TotalTokens) != 105 || derefInt(record.Usage.CacheHitTokens) != 70 {
		t.Fatalf("output tail clobbered previous counters: %+v", record.Usage)
	}
}

func TestCacheGeminiReferenceDoesNotInventSystem(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	cfg := registerPresetForTest(t, "gemini-api")
	req, err := relay.GeminiToMaheshvara([]byte(`{"cachedContent":"cachedContents/existing","contents":[{"role":"user","parts":[{"text":"question"}]}]}`), "m")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := relay.RenderCustomProtocolRequest(req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(rendered.Body, &body)
	if body["systemInstruction"] != nil || body["tools"] != nil || body["cachedContent"] != "cachedContents/existing" {
		t.Fatalf("invented conflicting cache context: %s", rendered.Body)
	}
}

func TestCachePresetStablePrefix(t *testing.T) {
	for _, source := range cacheWireFixtures() {
		for _, target := range PresetProtocolConfigsMust(t) {
			t.Run(source.platform+"_to_"+target.ID, func(t *testing.T) {
				req, _, err := relay.ConvertRequestToMaheshvara([]byte(source.request), inputFormatFromPath(source.path), "grp")
				if err != nil {
					t.Fatal(err)
				}
				first, err := relay.RenderCustomProtocolRequest(req, target)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 5; i++ {
					repeat, err := relay.RenderCustomProtocolRequest(req, target)
					if err != nil || string(repeat.Body) != string(first.Body) {
						t.Fatal("identical request rendered differently")
					}
				}
				part := relay.MaheshvaraContentPart{Type: relay.MaheshvaraContentText, Text: "new last turn"}
				req.Messages = append(req.Messages, relay.MaheshvaraMessage{Role: "user", Content: []relay.MaheshvaraContentPart{part}})
				if len(req.InputItems) > 0 {
					req.InputItems = append(req.InputItems, relay.MaheshvaraInputItem{Type: "message", Role: "user", Content: []relay.MaheshvaraContentPart{part}})
				}
				next, err := relay.RenderCustomProtocolRequest(req, target)
				if err != nil {
					t.Fatal(err)
				}
				var before, after map[string]any
				_ = json.Unmarshal(first.Body, &before)
				_ = json.Unmarshal(next.Body, &after)
				key := "messages"
				if target.Request.Shape == "gemini" {
					key = "contents"
				}
				if target.Request.Shape == "responses" {
					key = "input"
				}
				oldMessages := before[key].([]any)
				newMessages := after[key].([]any)
				if target.Request.Shape == "gemini" && len(newMessages) == len(oldMessages) {
					// Gemini coalesces adjacent user turns. The existing parts must
					// still be an exact prefix of the enlarged final user turn.
					oldLast := oldMessages[len(oldMessages)-1].(map[string]any)
					newLast := newMessages[len(newMessages)-1].(map[string]any)
					oldParts := oldLast["parts"].([]any)
					newParts := newLast["parts"].([]any)
					if len(newParts) != len(oldParts)+1 || !reflect.DeepEqual(newParts[len(oldParts)], map[string]any{"text": "new last turn"}) {
						t.Fatalf("new turn was not appended to Gemini parts: %s", next.Body)
					}
					newLast["parts"] = newParts[:len(oldParts)]
				} else if len(newMessages) <= len(oldMessages) {
					t.Fatal("new turn was not appended")
				}
				after[key] = newMessages[:len(oldMessages)]
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("appending a turn changed the prefix:\nbefore=%s\nafter=%s", first.Body, next.Body)
				}
				// Append a second complete tool round after the original history.
				toolRound, _, err := relay.ConvertRequestToMaheshvara([]byte(`{"model":"grp","input":[{"type":"function_call","call_id":"t2","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"t2","output":"second result"}]}`), inputFormatFromPath("/v1/responses"), "grp")
				if err != nil {
					t.Fatal(err)
				}
				req.Messages = append(req.Messages, toolRound.Messages...)
				if len(req.InputItems) > 0 {
					req.InputItems = append(req.InputItems, toolRound.InputItems...)
				}
				secondRound, err := relay.RenderCustomProtocolRequest(req, target)
				if err != nil {
					t.Fatal(err)
				}
				_ = json.Unmarshal(next.Body, &before)
				_ = json.Unmarshal(secondRound.Body, &after)
				oldMessages = before[key].([]any)
				newMessages = after[key].([]any)
				if len(newMessages) != len(oldMessages)+2 || !strings.Contains(string(secondRound.Body), "second result") {
					t.Fatalf("second tool round lost: %s", secondRound.Body)
				}
				after[key] = newMessages[:len(oldMessages)]
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("second tool round changed the prefix: %s", secondRound.Body)
				}
			})
		}
	}
}

func cacheWireFixtures() []cacheWireFixture {
	return []cacheWireFixture{
		{"chat-completions-api", "openai", "/v1/chat/completions",
			`{"model":"grp","prompt_cache_key":"stable-key","prompt_cache_retention":"24h","messages":[{"role":"system","content":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}]},{"role":"user","content":"question"},{"role":"assistant","tool_calls":[{"id":"t1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"t1","content":"result","cache_control":{"type":"ephemeral"}}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}},"cache_control":{"type":"ephemeral"}}]}`,
			`{"id":"r1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":70}}}`,
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":5,\"total_tokens\":105,\"prompt_tokens_details\":{\"cached_tokens\":70}}}\n\ndata: [DONE]\n\n"},
		{"anthropic-api", "anthropic", "/v1/messages",
			`{"model":"grp","max_tokens":64,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"stable system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"question"},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"lookup","input":{},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"result","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]}`,
			`{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation_input_tokens":20}}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"r1\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0,\"cache_read_input_tokens\":70,\"cache_creation_input_tokens\":20}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"responses-api", "responses", "/v1/responses",
			`{"model":"grp","instructions":"stable system","prompt_cache_key":"stable-key","prompt_cache_retention":"24h","input":[{"role":"user","content":[{"type":"input_text","text":"question"}]},{"type":"function_call","call_id":"t1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"t1","output":"result"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`,
			`{"id":"r1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":70}}}`,
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":5,\"total_tokens\":105,\"input_tokens_details\":{\"cached_tokens\":70}}}}\n\n"},
		{"gemini-api", "gemini", "/v1beta/models/grp:generateContent",
			`{"cachedContent":"cachedContents/stable","systemInstruction":{"parts":[{"text":"stable system"}]},"contents":[{"role":"user","parts":[{"text":"question"}]},{"role":"model","parts":[{"functionCall":{"id":"t1","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"t1","name":"lookup","response":{"result":"result"}}}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"totalTokenCount":105,"cachedContentTokenCount":70}}`,
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":5,\"totalTokenCount\":105,\"cachedContentTokenCount\":70}}\n\n"},
	}
}

func TestCacheWireMatrix(t *testing.T) {
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)
	for _, cfg := range PresetProtocolConfigsMust(t) {
		if err := relay.RegisterCustomProtocol(cfg); err != nil {
			t.Fatal(err)
		}
		cfg.ID = "copy-" + cfg.ID
		if err := relay.RegisterCustomProtocol(cfg); err != nil {
			t.Fatal(err)
		}
	}
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
						s := newTestServerWithStore(t, groups)
						var request map[string]any
						if err := json.Unmarshal([]byte(source.request), &request); err != nil {
							t.Fatal(err)
						}
						path := source.path
						if source.platform != "gemini" {
							request["stream"] = stream
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
						if rec.Code != 200 {
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
						if len(items) != 1 {
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

func TestCacheNewCustomProtocolNestedHTTP(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			relay.ClearCustomProtocols()
			t.Cleanup(relay.ClearCustomProtocols)
			var cfg relay.CustomProtocolConfig
			if err := json.Unmarshal([]byte(`{"id":"new-nested-cache-protocol","request":{"method":"POST","path":"/vendor/generate","shape":"anthropic","body":{"payload":{"system":{"field":"anthropic_system"},"messages":{"field":"messages"},"tools":{"field":"tools"},"cache":{"field":"cache_control","omitIfEmpty":true},"retention":{"field":"prompt_cache_retention","omitIfEmpty":true}}}},"aliases":{"usage":{"input":["in"],"output":["out"],"cached":["hit"]}},"response":{"textPath":"text","usagePath":"metrics","stream":{"frames":[{"event":"text","response":{"textPath":"text","usagePath":"metrics"}},{"event":"end","terminal":true,"response":{"usagePath":"metrics"}}]}}}`), &cfg); err != nil {
				t.Fatal(err)
			}
			if err := relay.RegisterCustomProtocol(cfg); err != nil {
				t.Fatal(err)
			}
			captured := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/vendor/generate" {
					t.Errorf("wrong path: %s", r.URL.Path)
				}
				body, _ := io.ReadAll(r.Body)
				captured <- body
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: text\ndata: {\"text\":\"ok\",\"metrics\":{\"in\":100,\"hit\":70}}\n\nevent: end\ndata: {\"metrics\":{\"out\":5}}\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"text":"ok","metrics":{"in":100,"out":5,"hit":70}}`)
				}
			}))
			defer upstream.Close()
			groups := presetGroup(t, "custom:"+cfg.ID, upstream.URL)
			capable := true
			groups[0].ToolsCapable = &capable
			s := newTestServerWithStore(t, groups)
			var body map[string]any
			_ = json.Unmarshal([]byte(cacheWireFixtures()[0].request), &body)
			body["stream"] = stream
			body["cache_control"] = map[string]any{"type": "ephemeral"}
			encoded, _ := json.Marshal(body)
			parsed, err := relay.OpenAIChatToMaheshvara(encoded)
			if err != nil {
				t.Fatal(err)
			}
			parsed.Model = "preset-model"
			preview, err := relay.RenderCustomProtocolRequest(parsed, cfg)
			if err != nil {
				t.Fatal(err)
			}
			c, rec := chatRequestContext(string(encoded))
			s.chatCompletions(c)
			if rec.Code != 200 {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			wire := <-captured
			if string(wire) != string(preview.Body) {
				t.Fatalf("preview differs from HTTP: %s != %s", preview.Body, wire)
			}
			var got map[string]any
			_ = json.Unmarshal(wire, &got)
			if got["system"] != nil || len(got) != 1 {
				t.Fatalf("unconfigured root keys: %s", wire)
			}
			payload := got["payload"].(map[string]any)
			if payload["cache"] == nil || payload["retention"] != "24h" || payload["system"].([]any)[0].(map[string]any)["cache_control"] == nil {
				t.Fatalf("cache mapping lost: %s", wire)
			}
			items := latestUsageRecords(t, s)
			data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			_ = json.Unmarshal(data, &record)
			if derefInt(record.Usage.CacheHitTokens) != 70 || derefInt(record.Usage.TotalTokens) != 105 {
				t.Fatalf("custom usage mapping: %+v", record.Usage)
			}
		})
	}
}

func TestCacheUsageCreationAndAbsentFields(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, phase := range []string{"creation", "absent"} {
			t.Run(fmt.Sprintf("%s/stream=%v", phase, stream), func(t *testing.T) {
				relay.ClearCustomProtocols()
				t.Cleanup(relay.ClearCustomProtocols)
				registerPresetForTest(t, "anthropic-api")
				usage := `{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":90}`
				if phase == "absent" {
					usage = `{"input_tokens":100}`
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"message\":{\"usage\":%s}}\n\nevent: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n", usage)
					} else {
						usage = strings.TrimSuffix(usage, "}") + `,"output_tokens":5}`
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"content":[{"type":"text","text":"ok"}],"usage":%s}`, usage)
					}
				}))
				defer upstream.Close()
				s := newTestServerWithStore(t, presetGroup(t, "custom:anthropic-api", upstream.URL))
				c, rec := chatRequestContext(fmt.Sprintf(`{"model":"grp","stream":%v,"messages":[{"role":"user","content":"hello"}]}`, stream))
				s.chatCompletions(c)
				if rec.Code != 200 {
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
				if phase == "creation" && !strings.Contains(rec.Body.String(), `"cached_creation_tokens":90`) {
					t.Fatalf("creation missing from Chat-compatible downstream details: %s", rec.Body.String())
				}
			})
		}
	}
}
