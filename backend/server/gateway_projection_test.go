package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayGeminiIncludeAndReasoningProjection(t *testing.T) {
	for _, client := range []string{"responses", "messages", "chat", "custom-responses", "custom-messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", client, stream), func(t *testing.T) {
				s, _ := newProtocolAdminTestServer(t)
				activateDiscoveryPresets(t, s)
				service, _ := s.protocolService()
				upstream, _ := service.Pin(protocol.PresetGeminiID)
				clientKind := strings.TrimPrefix(client, "custom-")
				customIngress := ""
				if clientKind != client {
					definition := upstream.Definition()
					definition.ID = "legacy-gemini"
					definition.Requires = nil
					upstream = persistPreviousRevision(t, s, mustEncodedProtocolValue(t, definition))
					id := protocol.PresetResponsesID
					if clientKind == "messages" {
						id = protocol.PresetAnthropicID
					}
					original, _ := service.Pin(id)
					definition = original.Definition()
					definition.ID = "legacy-" + clientKind
					definition.Requires = nil
					customIngress = definition.ID
					persistPreviousRevision(t, s, mustEncodedProtocolValue(t, definition))
					if err := s.reloadProtocolRuntime(t.Context()); err != nil {
						t.Fatal(err)
					}
					upstream, _ = service.Pin("legacy-gemini")
				}
				calls := 0
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					raw, _ := io.ReadAll(r.Body)
					if strings.Contains(string(raw), "include") || strings.Contains(string(raw), protocol.ContinuationPrefix) {
						t.Error("client-only state reached Gemini", string(raw))
					}
					body := `{"responseId":"r","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"test-signed-state"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":3,"toolUsePromptTokenCount":1,"totalTokenCount":8}}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: "+`{"responseId":"r","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"test-signed-state"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`+"\n\n")
						fmt.Fprint(w, "data: "+`{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"thoughtsTokenCount":3,"toolUsePromptTokenCount":1,"totalTokenCount":8}}`+"\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, body)
					}
				}))
				defer provider.Close()
				setupGatewayModel(t, s, upstream, provider.URL)
				path, body := "/gateway/openai-responses/responses", fmt.Sprintf(`{"model":"group","input":"hi","include":["reasoning.encrypted_content"],"stream":%v}`, stream)
				if clientKind == "messages" {
					path = "/gateway/anthropic-messages/v1/messages"
					body = fmt.Sprintf(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"stream":%v}`, stream)
				}
				if clientKind == "chat" {
					path = "/gateway/openai-chat-completions/chat/completions"
					body = fmt.Sprintf(`{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v,"stream_options":{"include_usage":true}}`, stream)
				}
				if customIngress != "" {
					suffix := "/responses"
					if clientKind == "messages" {
						suffix = "/v1/messages"
					}
					path = "/gateway/" + customIngress + suffix
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer gateway-test-token")
				req.Header.Set("x-elysia-session-id", "projection-session")
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, req)
				if rec.Code != 200 || calls != 1 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(rec.Body.String(), "hello") {
					t.Fatal(rec.Code, calls, rec.Header(), rec.Body.String())
				}
				if clientKind == "messages" && strings.Contains(rec.Body.String(), "reasoning_tokens") {
					t.Fatal("unsupported detail reached Messages", rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), protocol.ContinuationPrefix) {
					t.Fatal("signed state not wrapped", rec.Body.String())
				}
				items := latestUsageRecords(t, s)
				if len(items) != 1 {
					t.Fatal(items)
				}
				data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
				if err != nil {
					t.Fatal(err)
				}
				var record usageRecord
				if err = json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				if record.ProtocolUsage == nil || record.ProtocolUsage.Output.Count != 5 || record.ProtocolUsage.Details["output.reasoning_tokens"].Count != 3 || record.ProtocolUsage.Details["toolUsePromptTokenCount"].Count != 1 {
					t.Fatal("original usage lost", record.ProtocolUsage)
				}
				if record.UsageDetail.ReasoningTokens == nil || *record.UsageDetail.ReasoningTokens != 3 {
					t.Fatal("reasoning billing lost", record.UsageDetail)
				}
				found := false
				for _, issue := range record.ConversionIssues {
					if issue.RuleID != "" && issue.PolicyHash != "" && strings.Contains(issue.Path, "toolUsePromptTokenCount") {
						found = true
					}
				}
				if !found {
					t.Fatal("projection not diagnosed", record.ConversionIssues)
				}
				if clientKind == "responses" {
					found = false
					for _, issue := range record.ConversionIssues {
						if issue.Code == protocol.ConversionNormalized && issue.RuleID == "responses-include" && issue.Fidelity == "preserved" && issue.Severity == protocol.SeverityInfo && issue.PolicyHash != "" {
							found = true
						}
					}
					if !found {
						t.Fatal("include normalization not persisted", record.ConversionIssues)
					}
				}
			})
		}
	}
}

func TestGatewayInvalidIncludeDoesNotCallUpstream(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, value := range []string{`42`, `[null]`, `["future"]`, `["reasoning.encrypted_content","future"]`} {
		req := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(`{"model":"group","input":"hi","include":`+value+`}`))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != 400 || calls != 0 || !strings.Contains(rec.Body.String(), "/include") {
			t.Fatal(value, rec.Code, calls, rec.Body.String())
		}
	}
}

func TestGatewayIncludeCarrierDeliveryModes(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, signed := range []bool{false, true} {
			t.Run(fmt.Sprintf("strict=%v/signed=%v", strict, signed), func(t *testing.T) {
				s, path := newProtocolAdminTestServer(t)
				s.store.Close()
				var err error
				s.store, err = storage.OpenWithKey(filepath.Join(filepath.Dir(path), "encrypted.db"), s.config.GetDBEncryptionKey())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.store.Close() })
				activateDiscoveryPresets(t, s)
				service, _ := s.protocolService()
				upstream, _ := service.Pin(protocol.PresetGeminiID)
				ingress, _ := service.Pin(protocol.PresetResponsesID)
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					signature := ""
					if signed {
						signature = `,"thoughtSignature":"signed-state"`
					}
					fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"`+signature+`}]},"finishReason":"STOP"}]}`)
				}))
				defer provider.Close()
				setupGatewayModel(t, s, upstream, provider.URL)
				settings := protocol.DefaultConversionPolicy(ingress, upstream).Continuation
				settings.ClientCarrier = false
				mode := "compatible"
				if strict {
					mode = "strict"
				}
				bindings, _ := s.store.ListProtocolBindings(t.Context())
				bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "delivery-mode", Mode: mode, Rules: []protocol.ConversionRule{}, Continuation: settings}}
				bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
					t.Fatal(err)
				}
				s.invalidateRouteCache()
				req := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(`{"model":"group","input":"hello","store":false,"include":["reasoning.encrypted_content"]}`))
				req.Header.Set("Authorization", "Bearer gateway-test-token")
				req.Header.Set("x-elysia-session-id", "delivery-test")
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, req)
				if strict && signed {
					if rec.Code != 502 || !strings.Contains(rec.Body.String(), "/include") {
						t.Fatal(rec.Code, rec.Body.String())
					}
					return
				}
				if rec.Code != 200 || strings.Contains(rec.Body.String(), protocol.ContinuationPrefix) {
					t.Fatal(rec.Code, rec.Body.String())
				}
				if signed {
					stats, err := s.store.ContinuationStats(t.Context())
					if err != nil || stats["records"] != 1 {
						t.Fatal(stats, err)
					}
					items := latestUsageRecords(t, s)
					if len(items) != 1 {
						t.Fatal(items)
					}
					data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
					if err != nil {
						t.Fatal(err)
					}
					var record usageRecord
					_ = json.Unmarshal(data, &record)
					found, normalized := false, false
					for _, issue := range record.ConversionIssues {
						if issue.RuleID == "responses-include" && issue.Code == protocol.ConversionNormalized {
							normalized = true
						}
						if issue.RuleID == "responses-include" && issue.Fidelity == "lossy_compatible" {
							found = true
						}
					}
					if !found || !normalized {
						t.Fatal("server persistence incorrectly satisfied client delivery", record.ConversionIssues)
					}
				}
			})
		}
	}
}
