package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func TestGatewayThinkingHTTPAndSSEMatrix(t *testing.T) {
	models := map[string]string{"openai": "gpt-5.4", "responses": "gpt-5.4", "anthropic": "claude-opus-4-6", "gemini": "gemini-3.5-flash-lite"}
	for _, target := range cacheWireFixtures() {
		t.Run(target.platform, func(t *testing.T) {
			stream := false
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
				}
				var effort any
				switch target.platform {
				case "openai":
					effort = body["reasoning_effort"]
				case "responses":
					effort = body["reasoning"].(map[string]any)["effort"]
				case "anthropic":
					effort = body["output_config"].(map[string]any)["effort"]
				case "gemini":
					effort = body["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)["thinkingLevel"]
				}
				if effort != "high" {
					t.Errorf("upstream lost effort: %s", raw)
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, target.stream)
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, target.response)
				}
			}))
			defer upstream.Close()
			groups := presetGroup(t, "custom:"+target.id, upstream.URL)
			groups[0].Models[0].ID = models[target.platform]
			s := newTestServer(t, groups)
			for _, source := range cacheWireFixtures() {
				for _, streaming := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%v", source.platform, streaming), func(t *testing.T) {
						stream = streaming
						before := calls
						var body string
						switch source.platform {
						case "openai":
							body = fmt.Sprintf(`{"model":"grp","store":false,"max_completion_tokens":8192,"reasoning_effort":"high","stream":%v,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`, stream)
						case "responses":
							body = fmt.Sprintf(`{"model":"grp","store":false,"max_output_tokens":8192,"reasoning":{"effort":"high"},"stream":%v,"input":"hi"}`, stream)
						case "anthropic":
							body = fmt.Sprintf(`{"model":"grp","max_tokens":8192,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"stream":%v,"messages":[{"role":"user","content":"hi"}]}`, stream)
						case "gemini":
							body = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":8192,"thinkingConfig":{"thinkingLevel":"high"}}}`
						}
						path := source.path
						if source.platform == "gemini" && stream {
							path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
						}
						c, rec := adminProtocolContext("POST", path, body)
						if source.platform == "gemini" {
							c.Params = gin.Params{{Key: "action", Value: strings.TrimPrefix(path, "/v1beta/models/")}}
						}
						if source.platform == "responses" {
							s.responses(c)
						} else {
							s.chatCompletions(c)
						}
						if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || calls != before+1 || !strings.Contains(rec.Body.String(), "ok") {
							t.Fatalf("%d %s", rec.Code, rec.Body)
						}
						recordID := rec.Header().Get("X-Elysia-Request-Id")
						s.stopUsageWriter()
						raw, found, err := s.store.GetUsageRecordJSON(t.Context(), recordID)
						if err != nil || !found {
							t.Fatal(err, found)
						}
						var saved usageRecord
						if err := json.Unmarshal(raw, &saved); err != nil {
							t.Fatal(err)
						}
						if saved.ProtocolUsage == nil || saved.ProtocolUsage.CacheRead == nil || saved.ProtocolUsage.CacheRead.Count != 70 || saved.Usage.Estimated {
							t.Fatal("observed cache/usage lost", saved.ProtocolUsage)
						}
						s.startUsageWriter()
					})
				}
			}
		})
	}
}

func TestGatewayThinkingGroupNameDoesNotRestrictCandidate(t *testing.T) {
	for _, groupName := range []string{"grp", "gpt-5.4"} {
		t.Run(groupName, func(t *testing.T) {
			upstream := newCapturingUpstream(t, func(w http.ResponseWriter, body string, _ int) {
				if !strings.Contains(body, `"thinkingLevel":"minimal"`) {
					t.Error("lost valid target control", body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
			})
			groups := presetGroup(t, "custom:google-generate-content", upstream.URL)
			groups[0].Name = groupName
			groups[0].Models[0].ID = "gemini-3.5-flash-lite"
			s := newTestServer(t, groups)
			for _, effort := range []string{`"minimal"`, `42`, `"invalid"`} {
				before := upstream.requestCount()
				c, rec := chatRequestContext(fmt.Sprintf(`{"model":%q,"store":false,"reasoning_effort":%s,"messages":[{"role":"user","content":"hi"}]}`, groupName, effort))
				s.chatCompletions(c)
				status, calls := 400, before
				if effort == `"minimal"` {
					status, calls = 200, before+1
				}
				if rec.Code != status || upstream.requestCount() != calls {
					t.Fatalf("effort=%s status=%d calls=%d body=%s", effort, rec.Code, upstream.requestCount(), rec.Body)
				}
			}
		})
	}
}

func TestGatewayThinkingNativeReplay(t *testing.T) {
	for _, tc := range []struct{ platform, model, body, want, absent string }{
		{"gemini", "gemini-3.8-flash", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":4096}},"future_field":true}`, `"thinkingLevel":"medium"`, "thinkingBudget"},
		{"anthropic", "claude-opus-4-5", `{"model":"grp","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"future_field":true}`, `"budget_tokens":2047`, `"budget_tokens":4096`},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			for _, fixture := range cacheWireFixtures() {
				if fixture.platform != tc.platform {
					continue
				}
				upstream := newCapturingUpstream(t, func(w http.ResponseWriter, body string, _ int) {
					if !strings.Contains(body, tc.want) || strings.Contains(body, tc.absent) || !strings.Contains(body, `"future_field":true`) {
						t.Error("incorrect converted native request", body)
					}
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, fixture.response)
				})
				groups := presetGroup(t, "custom:"+fixture.id, upstream.URL)
				groups[0].Models[0].ID = tc.model
				s := newTestServer(t, groups)
				c, rec := adminProtocolContext("POST", fixture.path, tc.body)
				if tc.platform == "gemini" {
					c.Params = gin.Params{{Key: "action", Value: strings.TrimPrefix(fixture.path, "/v1beta/models/")}}
				} else {
					c.Request.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
				}
				s.chatCompletions(c)
				if rec.Code != 200 || upstream.requestCount() != 1 {
					t.Fatal(rec.Code, rec.Body, upstream.requestCount())
				}
			}
		})
	}
}

func TestGatewayThinkingCandidateIsolation(t *testing.T) {
	for _, control := range []string{"disabled", "output-limit"} {
		for _, mode := range []string{"compatible", "strict"} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%v", control, mode, reverse), func(t *testing.T) {
					var models []string
					provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						models = append(models, r.URL.Path)
						raw, _ := io.ReadAll(r.Body)
						if control == "disabled" && (!strings.Contains(string(raw), `"thinkingBudget":0`) || strings.Contains(string(raw), "thinkingLevel")) {
							t.Error("disabled intent changed", string(raw))
						}
						if control == "output-limit" && !strings.Contains(string(raw), `"maxOutputTokens":8192`) {
							t.Error("explicit output limit changed", string(raw))
						}
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
					}))
					defer provider.Close()
					group := presetGroup(t, "custom:google-generate-content", provider.URL)
					a := group[0].Models[0]
					a.ID = "gemini-3.5-flash-lite"
					a.Name = a.ID
					b := a
					b.ID = "gemini-2.5-flash"
					b.Name = b.ID
					group[0].Models = []config.ModelRef{a, b}
					if reverse {
						group[0].Models = []config.ModelRef{b, a}
					}
					group[0].MaxRetries = 0
					s := newTestServer(t, group)
					service, _ := s.protocolService()
					bindings, _ := s.store.ListProtocolBindings(t.Context())
					if control == "output-limit" {
						models, err := s.store.ListModels(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						upstream, _ := service.Pin(protocol.PresetGeminiID)
						for _, model := range models {
							binding := makeProtocolBinding(storage.BindingKey{Kind: "model", SourceID: model.SourceID, ModelID: model.ID}, upstream)
							cap := protocol.ResolveReasoningCapabilities("gemini", model.ID)
							cap.MaxOutputTokens = 16384
							if model.ID == a.ID {
								cap.MaxOutputTokens = 4096
							}
							binding.Binding.Reasoning = &cap
							bindings = append(bindings, binding)
						}
						if len(models) != 2 {
							t.Fatalf("configured %d model profiles", len(models))
						}
					}
					for i := range bindings {
						bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "thinking-policy", Mode: mode, Rules: []protocol.ConversionRule{}}}
					}
					verified, err := s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
					if err != nil {
						t.Fatal(err)
					}
					for _, binding := range verified {
						if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
							t.Fatal(err)
						}
					}
					s.invalidateRouteCache()
					raw := `{"model":"grp","store":false,"reasoning_effort":"none","messages":[{"role":"user","content":"hi"}]}`
					if control == "output-limit" {
						raw = `{"model":"grp","store":false,"max_completion_tokens":8192,"messages":[{"role":"user","content":"hi"}]}`
					}
					c, rec := chatRequestContext(raw)
					s.chatCompletions(c)
					if rec.Code != 200 || len(models) != 1 || !strings.Contains(models[0], "gemini-2.5-flash") {
						t.Fatal(rec.Code, rec.Body, models)
					}
					// Invalid source controls reject every route before a network call.
					c, rec = chatRequestContext(`{"model":"grp","store":false,"reasoning_effort":42,"messages":[{"role":"user","content":"hi"}]}`)
					s.chatCompletions(c)
					if rec.Code != 400 || len(models) != 1 {
						t.Fatal(rec.Code, rec.Body, models)
					}
				})
			}
		}
	}
}

func TestGatewayThinkingHardConstraintsNeverCallUpstream(t *testing.T) {
	for _, tc := range []struct{ name, preset, model, controls, reason string }{
		{"cannot-disable", "google-generate-content", "gemini-3.5-flash-lite", `"reasoning_effort":"none"`, "cannot disable thinking"},
		{"explicit-too-small", "anthropic-messages", "claude-opus-4-5", `"reasoning_effort":"high","max_completion_tokens":512`, "manual thinking cannot fit"},
		{"explicit-too-large", "openai-chat-completions", "gpt-6.1-sol", `"max_completion_tokens":128001`, "exceeds confirmed hard limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			defer provider.Close()
			groups := presetGroup(t, "custom:"+tc.preset, provider.URL)
			groups[0].Models[0].ID = tc.model
			s := newTestServer(t, groups)
			c, rec := chatRequestContext(`{"model":"grp","store":false,` + tc.controls + `,"messages":[{"role":"user","content":"hi"}]}`)
			s.chatCompletions(c)
			if rec.Code != 400 || calls != 0 || !strings.Contains(rec.Body.String(), tc.reason) {
				t.Fatal(rec.Code, rec.Body, calls)
			}
		})
	}
}

func TestGatewayThinkingBudgetStrictNeverCallsUpstream(t *testing.T) {
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer provider.Close()
	groups := presetGroup(t, "custom:google-generate-content", provider.URL)
	groups[0].Models[0].ID = "gemini-2.5-flash"
	s := newTestServer(t, groups)
	service, _ := s.protocolService()
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	for i := range bindings {
		bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "strict-thinking", Mode: "strict", Rules: []protocol.ConversionRule{}}}
	}
	verified, err := s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range verified {
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	s.invalidateRouteCache()
	c, rec := chatRequestContext(`{"model":"grp","store":false,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != 400 || calls != 0 || !strings.Contains(rec.Body.String(), "approximated") {
		t.Fatal(rec.Code, rec.Body, calls)
	}
}

func TestGatewayDynamicBudgetStrictCandidateFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			upstream := newCapturingUpstream(t, func(w http.ResponseWriter, body string, _ int) {
				if !strings.Contains(body, `"thinkingBudget":-1`) {
					t.Error("automatic budget was approximated before upstream call", body)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
			})
			groups := presetGroup(t, "custom:"+protocol.PresetChatCompletionsID, upstream.URL)
			groups[0].Models[0].ID = "gpt-5.4"
			if fallback {
				compatible := presetGroup(t, "custom:"+protocol.PresetGeminiID, upstream.URL)[0].Models[0]
				compatible.ID = "gemini-2.5-flash"
				groups[0].Models = append(groups[0].Models, compatible)
			}
			groups[0].MaxRetries = 0
			s := newTestServer(t, groups)
			service, _ := s.protocolService()
			bindings, err := s.store.ListProtocolBindings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for i := range bindings {
				bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "strict-dynamic-budget", Mode: "strict", Rules: []protocol.ConversionRule{}}}
			}
			bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
			if err != nil {
				t.Fatal(err)
			}
			for _, binding := range bindings {
				if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
					t.Fatal(err)
				}
			}
			s.invalidateRouteCache()
			path := "/v1beta/models/grp:generateContent"
			c, rec := adminProtocolContext("POST", path, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`)
			c.Params = gin.Params{{Key: "action", Value: "grp:generateContent"}}
			s.chatCompletions(c)
			if fallback {
				if rec.Code != 200 || upstream.requestCount() != 1 || !strings.Contains(upstream.last().Path, "gemini-2.5-flash") {
					t.Fatal(rec.Code, rec.Body, upstream.last())
				}
			} else if rec.Code != 400 || upstream.requestCount() != 0 || !strings.Contains(rec.Body.String(), "automatic") {
				t.Fatal(rec.Code, rec.Body, upstream.requestCount())
			}
		})
	}
}

func TestGatewayLateOutputLimitNeverCallsUpstream(t *testing.T) {
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) { w.WriteHeader(500) })
	groups := presetGroup(t, "custom:"+protocol.PresetChatCompletionsID, upstream.URL)
	groups[0].Models[0].ID = "gpt-6.1-sol"
	s := newTestServer(t, groups)
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	present := true
	for i := range bindings {
		bindings[i].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "late-limit", Rules: []protocol.ConversionRule{{ID: "late-limit", Order: 999, Enabled: true, Phase: protocol.ConversionRequest, Action: "set", Path: "/parameters/max_output_tokens", Value: mustProtocolValue(t, "128001"), Match: protocol.ConversionMatch{Path: "/parameters/metadata/late_limit", Present: &present}}}}}
	}
	service, _ := s.protocolService()
	bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	s.invalidateRouteCache()
	c, rec := chatRequestContext(`{"model":"grp","store":false,"max_completion_tokens":8192,"metadata":{"late_limit":"true"},"messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)
	if rec.Code != 400 || upstream.requestCount() != 0 || !strings.Contains(rec.Body.String(), "exceeds confirmed hard limit") {
		t.Fatal(rec.Code, rec.Body, upstream.requestCount())
	}
}

func TestGatewayThinkingToolContinuation(t *testing.T) {
	for _, source := range []string{protocol.PresetChatCompletionsID, protocol.PresetAnthropicID, protocol.PresetResponsesID} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", source, stream), func(t *testing.T) {
				calls := 0
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					raw, _ := io.ReadAll(r.Body)
					if strings.Contains(string(raw), "prompt_cache_key") || strings.Contains(string(raw), "cache_control") || strings.Contains(string(raw), "eager_input_streaming") {
						t.Error("unprojected request preferences leaked", string(raw))
					}
					if !strings.Contains(string(raw), `"thinkingLevel":"high"`) {
						t.Error("lost effort", string(raw))
					}
					if source == protocol.PresetAnthropicID && !strings.Contains(string(raw), `"includeThoughts":false`) {
						t.Error("lost hidden-summary preference", string(raw))
					}
					if calls == 2 && (!strings.Contains(string(raw), `"thoughtSignature":"tool-signature"`) || !strings.Contains(string(raw), "functionResponse") || !strings.Contains(string(raw), "tool-result")) {
						t.Error("broken tool continuation", string(raw))
					}
					if calls == 2 && source == protocol.PresetAnthropicID && strings.Contains(string(raw), `"error":`) != stream {
						t.Error("tool result error state changed", string(raw))
					}
					payload := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{}},"thoughtSignature":"tool-signature"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`
					if calls == 2 {
						payload = `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}`
					}
					payload = strings.Replace(payload, `"promptTokenCount":4`, `"promptTokenCount":4,"promptTokensDetails":[{"modality":"TEXT","tokenCount":4}],"serviceTier":"standard"`, 1)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", payload)
					} else {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, payload)
					}
				}))
				defer provider.Close()
				groups := presetGroup(t, "custom:google-generate-content", provider.URL)
				groups[0].Models[0].ID = "gemini-3.5-flash-lite"
				s := newTestServer(t, groups)
				s.config.SecretKeyPath = filepath.Join(t.TempDir(), "continuation.key")
				raw := fmt.Sprintf(`{"model":"grp","store":false,"prompt_cache_key":"anonymous-session","stream_options":{"include_usage":true},"reasoning_effort":"high","max_completion_tokens":8192,"stream":%v,"messages":[{"role":"user","content":"lookup"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`, stream)
				path := "/v1/chat/completions"
				if source == protocol.PresetAnthropicID {
					raw = fmt.Sprintf(`{"model":"grp","max_tokens":8192,"cache_control":{"type":"ephemeral"},"thinking":{"type":"adaptive","display":"omitted"},"output_config":{"effort":"high"},"stream":%v,"messages":[{"role":"user","content":"lookup"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":true}]}`, stream)
					path = "/v1/messages"
				}
				if source == protocol.PresetResponsesID {
					raw = fmt.Sprintf(`{"model":"grp","store":false,"prompt_cache_key":"anonymous-session","reasoning":{"effort":"high","summary":"auto"},"include":["reasoning.encrypted_content"],"max_output_tokens":8192,"stream":%v,"input":[{"role":"user","content":"lookup"}],"tools":[{"type":"function","name":"lookup","description":"Read an item","parameters":{"type":"object"}}]}`, stream)
					path = "/v1/responses"
				}
				var request map[string]any
				if err := json.Unmarshal([]byte(raw), &request); err != nil {
					t.Fatal(err)
				}
				perform := func() *httptest.ResponseRecorder {
					body, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					c, rec := adminProtocolContext("POST", path, string(body))
					c.Request.Header.Set("x-elysia-session-id", "thinking-tools")
					if source == protocol.PresetResponsesID {
						s.responses(c)
					} else {
						s.chatCompletions(c)
					}
					if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
						t.Fatalf("%d %s", rec.Code, rec.Body)
					}
					return rec
				}
				first := perform()
				message := thinkingToolWireMessage(t, source, stream, first.Body.Bytes())
				var callID string
				var result map[string]any
				if source == protocol.PresetChatCompletionsID {
					tools := message["tool_calls"].([]any)
					if len(tools) != 1 {
						t.Fatal("missing tool call", message)
					}
					callID = tools[0].(map[string]any)["id"].(string)
					result = map[string]any{"role": "tool", "tool_call_id": callID, "content": "tool-result"}
				} else if source == protocol.PresetResponsesID {
					for _, value := range message["output"].([]any) {
						item := value.(map[string]any)
						if item["type"] == "function_call" {
							callID = item["call_id"].(string)
						}
					}
					if callID == "" {
						t.Fatal("missing tool call", message)
					}
					result = map[string]any{"type": "function_call_output", "call_id": callID, "output": "tool-result"}
				} else {
					for _, value := range message["content"].([]any) {
						block := value.(map[string]any)
						if block["type"] == "tool_use" {
							callID = block["id"].(string)
						}
					}
					if callID == "" {
						t.Fatal("missing tool call", message)
					}
					result = map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": callID, "content": "tool-result", "is_error": stream}}}
				}
				if source == protocol.PresetResponsesID {
					request["input"] = append(append(request["input"].([]any), message["output"].([]any)...), result)
				} else {
					request["messages"] = append(request["messages"].([]any), message, result)
				}
				second := perform()
				if calls != 2 || !strings.Contains(second.Body.String(), "done") {
					t.Fatal(calls, second.Body)
				}
			})
		}
	}
}

// Assemble a client wire message, preserving opaque carriers exactly as returned.
func thinkingToolWireMessage(t *testing.T, source string, stream bool, raw []byte) map[string]any {
	t.Helper()
	decode := func(raw []byte) map[string]any {
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	message := map[string]any{"role": "assistant"}
	if !stream {
		response := decode(raw)
		if source == protocol.PresetResponsesID {
			return map[string]any{"output": response["output"]}
		}
		if source == protocol.PresetChatCompletionsID {
			return response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		}
		message["content"] = response["content"]
		return message
	}
	blocks := []any{}
	args := map[int]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		event := decode([]byte(strings.TrimPrefix(line, "data: ")))
		if source == protocol.PresetResponsesID {
			if event["type"] == "response.completed" {
				message["output"] = event["response"].(map[string]any)["output"]
			}
			continue
		}
		if source == protocol.PresetChatCompletionsID {
			for _, choice := range event["choices"].([]any) {
				for key, value := range choice.(map[string]any)["delta"].(map[string]any) {
					if key != "tool_calls" {
						message[key] = value
						continue
					}
					for _, part := range value.([]any) {
						call := part.(map[string]any)
						index := int(call["index"].(float64))
						for len(blocks) <= index {
							blocks = append(blocks, map[string]any{"type": "function", "function": map[string]any{"name": "", "arguments": ""}})
						}
						dst := blocks[index].(map[string]any)
						for k, v := range call {
							if k == "function" {
								for name, fragment := range v.(map[string]any) {
									dst[k].(map[string]any)[name] = dst[k].(map[string]any)[name].(string) + fragment.(string)
								}
							} else if k != "index" {
								dst[k] = v
							}
						}
					}
				}
			}
		} else if event["type"] == "content_block_start" {
			if int(event["index"].(float64)) != len(blocks) {
				t.Fatal("unordered content block", event)
			}
			blocks = append(blocks, event["content_block"])
		} else if event["type"] == "content_block_delta" {
			index := int(event["index"].(float64))
			delta := event["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				args[index] += delta["partial_json"].(string)
			} else {
				field := map[string]string{"signature_delta": "signature", "thinking_delta": "thinking", "text_delta": "text"}[delta["type"].(string)]
				if field == "" {
					t.Fatal("unexpected delta", event)
				}
				block := blocks[index].(map[string]any)
				previous, _ := block[field].(string)
				block[field] = previous + delta[field].(string)
			}
		}
	}
	for index, raw := range args {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		blocks[index].(map[string]any)["input"] = value
	}
	if source == protocol.PresetChatCompletionsID {
		message["tool_calls"] = blocks
	} else {
		message["content"] = blocks
	}
	return message
}

func TestAgentThinkingUsesBoundModelCapabilities(t *testing.T) {
	for _, tc := range []struct{ platform, model, field, value string }{
		{"openai", "gpt-6.1-sol", "reasoning_effort", `"high"`},
		{"responses", "gpt-6.1-sol", "effort", `"high"`},
		{"gemini", "gemini-2.5-flash", "thinkingBudget", "24576"},
		{"gemini", "gemini-3.8-flash", "thinkingLevel", `"high"`},
		{"anthropic", "claude-haiku-4-5", "type", `"enabled"`},
		{"anthropic", "claude-opus-4-6", "type", `"adaptive"`},
	} {
		t.Run(tc.model+"/"+tc.platform, func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			var response string
			for _, fixture := range cacheWireFixtures() {
				if fixture.platform == tc.platform {
					response = fixture.stream
				}
			}
			if response == "" {
				t.Fatal("missing fixture")
			}
			upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, response)
			})
			source := storage.ModelSource{ID: "cs1", Name: "caller-src", BaseURL: upstream.URL, APIKey: "sk-caller-key", Platform: fixturePlatform(tc.platform), Enabled: true}
			if err := s.saveSource(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			model := storage.Model{ID: tc.model, SourceID: "cs1", Name: tc.model, BaseURL: upstream.URL, Platform: source.Platform, Type: "llm", Enabled: true, Available: true, ToolsCapable: true}
			if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{model}); err != nil {
				t.Fatal(err)
			}
			request := callerRequest()
			request.Model = tc.model
			request.Preferences = protocol.AgentPreferences{ThinkingEnabled: true, ThinkingEffort: "high"}
			result, err := newAgentStreamCaller(s).Call(t.Context(), request, agent.StreamCallbacks{})
			if err != nil || result.Text != "ok" {
				t.Fatal(result, err)
			}
			raw := upstream.last().Body
			if !strings.Contains(raw, `"`+tc.field+`":`+tc.value) {
				t.Fatal("incorrect model-specific Agent controls", raw)
			}
		})
	}
}

func TestAgentThinkingDisabledRejectsUnsupportedModel(t *testing.T) {
	for _, tc := range []struct{ platform, model string }{
		{"gemini", "gemini-3.5-flash-lite"}, {"gemini", "private-alias"},
		{"openai", "gpt-6.1-sol"}, {"anthropic", "claude-opus-5-5"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			defer provider.Close()
			source := storage.ModelSource{ID: "cs1", Name: "caller-src", BaseURL: provider.URL, Platform: fixturePlatform(tc.platform), Enabled: true}
			if err := s.saveSource(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			model := storage.Model{ID: tc.model, SourceID: source.ID, Name: tc.model, BaseURL: provider.URL, Platform: source.Platform, Type: "llm", Enabled: true, Available: true, ToolsCapable: true}
			if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{model}); err != nil {
				t.Fatal(err)
			}
			request := callerRequest()
			request.Model = tc.model
			_, err := newAgentStreamCaller(s).Call(t.Context(), request, agent.StreamCallbacks{})
			var failure *protocol.ConversionError
			if !errors.As(err, &failure) || !failure.CandidateOnly || calls != 0 || !strings.Contains(err.Error(), "cannot disable thinking") {
				t.Fatal(err, calls)
			}
		})
	}
}
