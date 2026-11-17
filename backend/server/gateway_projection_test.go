package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayGeminiIncludeAndReasoningProjection(t *testing.T) {
	testGatewayGeminiIncludeAndReasoningProjection(t, false)
}

func TestPackageGatewayGeminiContinuationDelivery(t *testing.T) {
	testGatewayGeminiIncludeAndReasoningProjection(t, true)
}

func testGatewayGeminiIncludeAndReasoningProjection(t *testing.T, isPackage bool) {
	t.Parallel()
	for _, client := range []string{"responses", "messages", "chat"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", client, stream), func(t *testing.T) {
				s, _ := newProtocolAdminTestServer(t)
				var upstream, ingress *protocol.Compiled
				if isPackage {
					packages := defaultGatewayPackages(t, s)
					upstream = packages["google-generate-content"]
					name := map[string]string{"responses": "openai-responses", "messages": "anthropic", "chat": "openai-chat"}[client]
					ingress = packages[name]
				} else {
					activateDiscoveryPresets(t, s)
					service, _ := s.protocolService()
					upstream, _ = service.Pin(protocol.PresetGeminiID)
				}
				clientKind := client
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
				path, body := "/gateway/openai-responses/v1/responses", fmt.Sprintf(`{"model":"group","input":"hi","include":["reasoning.encrypted_content"],"stream":%v}`, stream)
				if clientKind == "messages" {
					path = "/gateway/anthropic-messages/v1/messages"
					body = fmt.Sprintf(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"stream":%v}`, stream)
				}
				if clientKind == "chat" {
					path = "/gateway/openai-chat-completions/v1/chat/completions"
					body = fmt.Sprintf(`{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v,"stream_options":{"include_usage":true}}`, stream)
				}
				if isPackage {
					path = "/gateway/" + ingress.Identity().DefinitionID + ingress.Operations()["generate"].Path
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer gateway-test-token")
				req.Header.Set("x-elysia-session-id", "projection-session")
				rec := httptest.NewRecorder()
				serveTestHTTP(s.engine, rec, req)
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
				if err = sonic.ConfigStd.Unmarshal(data, &record); err != nil {
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
					for _, issue := range record.ConversionIssues {
						if issue.RuleID == "responses-include" {
							t.Fatal("canonical continuation selection used a retired foreign selector rule", issue)
						}
					}
				}
			})
		}
	}
}

func TestGatewayInvalidIncludeDoesNotCallUpstream(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, fixture := range []struct{ value, failure string }{
		{`42`, "invalid_input at /include:"},
		{`[null]`, "invalid_input at /include/0:"},
		{`["future"]`, "conversion_rejected at /parameters:"},
		{`["reasoning.encrypted_content","future"]`, "conversion_rejected at /parameters:"},
	} {
		value := fixture.value
		req := httptest.NewRequest("POST", "/gateway/openai-responses/v1/responses", strings.NewReader(`{"model":"group","input":"hi","include":`+value+`}`))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		serveTestHTTP(s.engine, rec, req)
		if rec.Code != 400 || calls != 0 || !strings.Contains(rec.Body.String(), fixture.failure) {
			t.Fatal(value, rec.Code, calls, rec.Body.String())
		}
	}
}

func TestGatewayIncludeCarrierDeliveryModes(t *testing.T) {
	testGatewayIncludeCarrierDeliveryModes(t, false)
}

func TestPackageGatewayIncludeRequiresClientCarrier(t *testing.T) {
	testGatewayIncludeCarrierDeliveryModes(t, true)
}

func testGatewayIncludeCarrierDeliveryModes(t *testing.T, isPackage bool) {
	t.Parallel()
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict=%v", strict), func(t *testing.T) {
			s, path := newProtocolAdminTestServer(t)
			s.store.Close()
			var err error
			s.store, err = storage.OpenWithKey(filepath.Join(filepath.Dir(path), "encrypted.db"), s.config.GetDBEncryptionKey())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				s.store.Close()
			})
			var upstream, ingress *protocol.Compiled
			if isPackage {
				packages := defaultGatewayPackages(t, s)
				upstream, ingress = packages["google-generate-content"], packages["openai-responses"]
			} else {
				activateDiscoveryPresets(t, s)
				service, _ := s.protocolService()
				upstream, _ = service.Pin(protocol.PresetGeminiID)
				ingress, _ = service.Pin(protocol.PresetResponsesID)
			}
			service, _ := s.protocolService()
			var providerCalls atomic.Int32
			var signedResponse atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				signature := ""
				if signedResponse.Load() {
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
			bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "delivery-mode", Mode: mode, Rules: []protocol.ConversionRule{}, Continuation: settings}}
			bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			// Keep one encrypted store per mode. Unsigned runs first and creates no
			// continuation, so the signed case still proves exactly one record.
			for _, signed := range []bool{false, true} {
				t.Run(fmt.Sprintf("signed=%v", signed), func(t *testing.T) {
					signedResponse.Store(signed)
					providerCalls.Store(0)
					req := httptest.NewRequest("POST", "/gateway/"+ingress.Identity().DefinitionID+ingress.Operations()["generate"].Path, strings.NewReader(`{"model":"group","input":"hello","store":false,"include":["reasoning.encrypted_content"]}`))
					req.Header.Set("Authorization", "Bearer gateway-test-token")
					req.Header.Set("x-elysia-session-id", "delivery-test")
					rec := httptest.NewRecorder()
					serveTestHTTP(s.engine, rec, req)
					if signed {
						if rec.Code != 502 || !strings.Contains(rec.Body.String(), "/continuation") {
							t.Fatal(rec.Code, rec.Body.String())
						}
						stats, err := s.store.ContinuationStats(t.Context())
						if err != nil || stats["records"] != 1 {
							t.Fatal("test did not exercise successful persistence without client delivery", stats, err)
						}
						return
					}
					if rec.Code != 200 || strings.Contains(rec.Body.String(), protocol.ContinuationPrefix) {
						t.Fatal(rec.Code, rec.Body.String())
					}
					if strict {
						streamRequest := httptest.NewRequest("POST", req.URL.String(), strings.NewReader(`{"model":"group","input":"hello","stream":true,"store":false,"include":["reasoning.encrypted_content"]}`))
						streamRequest.Header = req.Header.Clone()
						streamResponse := httptest.NewRecorder()
						serveTestHTTP(s.engine, streamResponse, streamRequest)
						if streamResponse.Code != 400 || providerCalls.Load() != 1 {
							t.Fatal("JSON-only proof authorized a strict stream", streamResponse.Code, providerCalls.Load(), streamResponse.Body.String())
						}
					}
				})
			}
		})
	}
}
