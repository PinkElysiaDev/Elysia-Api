package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayRequestProjectionPreviewAndRejection(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"compatible", "strict"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			source, _ := service.Pin(protocol.PresetResponsesID)
			target, _ := service.Pin(protocol.PresetGeminiID)
			calls := 0
			var sent []byte
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				sent, _ = io.ReadAll(r.Body)
				if r.URL.Query().Get("alt") == "sse" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
			}))
			defer provider.Close()
			setupGatewayModel(t, s, target, provider.URL)
			canTools := true
			if _, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &canTools}); err != nil {
				t.Fatal(err)
			}
			if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
				t.Fatal(err)
			}
			policy := protocol.ConversionPolicy{SchemaVersion: 1, ID: "hint-mode", Mode: mode, Rules: []protocol.ConversionRule{}}
			bindings, err := s.store.ListProtocolBindings(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &policy}
			bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			perform := func(id, path, raw string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", "/gateway/"+id+path, strings.NewReader(raw))
				req.Header.Set("Authorization", "Bearer gateway-test-token")
				rec := httptest.NewRecorder()
				serveTestHTTP(s.engine, rec, req)
				return rec
			}
			raw := `{"model":"group","input":"question","store":false,"max_output_tokens":100,"prompt_cache_key":"session","metadata":{"trace":"anonymous"},"user":"opaque"}`
			decoded, err := source.DecodeRequest(t.Context(), []byte(raw), protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			previewBody, _ := sonic.ConfigStd.Marshal(map[string]any{"policy": policy, "phase": "request", "context": protocol.ConversionContext{Source: source.Identity(), Target: target.Identity(), Model: "upstream-model"}, "input": decoded})
			ctx, preview := adminProtocolContext("POST", "/api/admin/protocols/conversion-policies/preview", string(previewBody))
			s.adminConversionPreview(contextForTest(ctx), ctx)
			flushTestResponse(ctx)
			rec := perform(protocol.PresetResponsesID, "/responses", raw)
			if mode == "strict" {
				if preview.Code != 400 || rec.Code != 400 || calls != 0 {
					t.Fatal(preview.Code, rec.Code, calls, rec.Body)
				}
			} else {
				if preview.Code != 200 || rec.Code != 200 || calls != 1 {
					t.Fatal(preview.Code, rec.Code, calls, rec.Body)
				}
				var result struct {
					Data struct {
						Output           protocol.Request
						Issues           []protocol.ConversionIssue
						PersistentWrites bool
					}
				}
				if err := sonic.ConfigStd.Unmarshal(preview.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Data.PersistentWrites {
					t.Fatal("preview wrote persistent data")
				}
				result.Data.Output.Model = protocol.StringValue("upstream-model")
				predicted, err := target.EncodeRequest(t.Context(), &result.Data.Output, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				var a, b any
				sonic.ConfigStd.Unmarshal(predicted, &a)
				sonic.ConfigStd.Unmarshal(sent, &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("preview and forwarding differ: %s vs %s", predicted, sent)
				}
				for _, issue := range result.Data.Issues {
					if issue.RuleID == "request-projection" && (issue.Severity != protocol.SeverityInfo || issue.Fidelity != "lossy_compatible") {
						t.Fatal(issue)
					}
				}
			}
			anthropic, _ := service.Pin(protocol.PresetAnthropicID)
			toolRaw := `{"model":"group","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"lookup"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":true}]}`
			toolRequest, err := anthropic.DecodeRequest(t.Context(), []byte(toolRaw), protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			previewBody, _ = sonic.ConfigStd.Marshal(map[string]any{"policy": policy, "phase": "request", "context": protocol.ConversionContext{Source: anthropic.Identity(), Target: target.Identity(), Model: "upstream-model"}, "input": toolRequest})
			ctx, preview = adminProtocolContext("POST", "/api/admin/protocols/conversion-policies/preview", string(previewBody))
			s.adminConversionPreview(contextForTest(ctx), ctx)
			flushTestResponse(ctx)
			before := calls
			rec = perform(protocol.PresetAnthropicID, "/v1/messages", toolRaw)
			if mode == "strict" {
				if preview.Code != 400 || rec.Code != 400 || calls != before || !strings.Contains(rec.Body.String(), "eager_input_streaming") {
					t.Fatal("strict tool preference reached upstream", preview.Code, rec.Code, calls-before, rec.Body)
				}
			} else if preview.Code != 200 || rec.Code != 200 || calls != before+1 || strings.Contains(string(sent), "eager_input_streaming") || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal("tool preference conversion failed", preview.Code, rec.Code, calls-before, rec.Body)
			}
			for _, value := range []string{`1`, `"true"`, `[]`, `{}`} {
				before := calls
				bad := strings.Replace(toolRaw, `"eager_input_streaming":true`, `"eager_input_streaming":`+value, 1)
				rec := perform(protocol.PresetAnthropicID, "/v1/messages", bad)
				if rec.Code != 400 || calls != before {
					t.Fatal("invalid tool preference reached upstream", value, rec.Code, calls-before)
				}
			}
			before = calls
			unknown := strings.Replace(toolRaw, `"eager_input_streaming":true`, `"eager_input_streaming":true,"future_streaming":true`, 1)
			if rec := perform(protocol.PresetAnthropicID, "/v1/messages", unknown); rec.Code != 400 || calls != before {
				t.Fatal("unknown tool preference reached upstream", rec.Code, calls-before)
			}
			for _, flag := range []string{"false", "true"} {
				resultRaw := `{"model":"group","max_tokens":100,"stream":true,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result-body","is_error":` + flag + `}]}]}`
				decoded, err := anthropic.DecodeRequest(t.Context(), []byte(resultRaw), protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				previewBody, _ := sonic.ConfigStd.Marshal(map[string]any{"policy": policy, "phase": "request", "context": protocol.ConversionContext{Source: anthropic.Identity(), Target: target.Identity(), Model: "upstream-model"}, "input": decoded})
				ctx, preview := adminProtocolContext("POST", "/api/admin/protocols/conversion-policies/preview", string(previewBody))
				s.adminConversionPreview(contextForTest(ctx), ctx)
				flushTestResponse(ctx)
				before := calls
				rec := perform(protocol.PresetAnthropicID, "/v1/messages", resultRaw)
				if mode == "strict" {
					if preview.Code != 400 || rec.Code != 400 || calls != before {
						t.Fatal("strict result shape conversion reached upstream", preview.Code, rec.Code, calls-before)
					}
				} else {
					if preview.Code != 200 || rec.Code != 200 || calls != before+1 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
						t.Fatal("result status conversion failed", preview.Code, rec.Code, calls-before, rec.Body)
					}
					var result struct {
						Data struct {
							Output           protocol.Request
							PersistentWrites bool
						}
					}
					if err := sonic.ConfigStd.Unmarshal(preview.Body.Bytes(), &result); err != nil || result.Data.PersistentWrites {
						t.Fatal("invalid result preview", err)
					}
					result.Data.Output.Model = protocol.StringValue("upstream-model")
					predicted, err := target.EncodeRequest(t.Context(), &result.Data.Output, protocol.EvaluationContext{})
					if err != nil {
						t.Fatal(err)
					}
					var a, b any
					sonic.ConfigStd.Unmarshal(predicted, &a)
					sonic.ConfigStd.Unmarshal(sent, &b)
					if !reflect.DeepEqual(a, b) || strings.Contains(string(sent), `"error":`) != (flag == "true") || !strings.Contains(string(sent), `"id":"call_1"`) || !strings.Contains(string(sent), "result-body") {
						t.Fatal("result preview or error state differs from forwarding", string(predicted), string(sent))
					}
				}
				for _, value := range []string{`null`, `"false"`, `1`, `[]`} {
					before := calls
					bad := strings.Replace(resultRaw, `"is_error":`+flag, `"is_error":`+value, 1)
					if rec := perform(protocol.PresetAnthropicID, "/v1/messages", bad); rec.Code != 400 || calls != before {
						t.Fatal("invalid result flag reached upstream", rec.Code, calls-before)
					}
				}
			}
			for _, fields := range []string{
				`"prompt_cache_key":42`, `"prompt_cache_retention":[]`, `"metadata":{"trace":false}`, `"user":42`,
				`"previous_response_id":"prior"`, `"future":true`, `"prompt_cache_options":{"mode":"explicit"}`, `"prompt_cache_options":{"ttl":"1h"}`, `"prewarm":true`,
			} {
				before := calls
				rec := perform(protocol.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,`+fields+`}`)
				if rec.Code != 400 || calls != before {
					t.Fatal("invalid or unmapped input reached upstream", fields, rec.Code, calls-before, rec.Body)
				}
			}
			for _, fields := range []string{
				`"cache_control":{"type":42}`, `"cache_control":{"type":"future"}`, `"cache_control":{"type":"ephemeral","ttl":42}`, `"cache_control":{"type":"ephemeral","future":true}`,
				`"metadata":{"user_id":42}`, `"metadata":{"future":true}`,
			} {
				before := calls
				rec := perform(protocol.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}],`+fields+`}`)
				if rec.Code != 400 || calls != before {
					t.Fatal("invalid marker reached upstream", fields, rec.Code, calls-before, rec.Body)
				}
			}
		})
	}
}
