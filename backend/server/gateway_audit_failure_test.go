package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
)

func TestGatewayAuditFailureDetailsReachAllClients(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	var isStreaming atomic.Bool
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if isStreaming.Load() {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":\"gateway_concurrency_limit\",\"message\":\"synthetic limit\"}}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"synthetic tool_choice.name","code":null,"param":""}}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, ingress := range []struct{ id, path, body string }{
		{protocol.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":%v}`},
		{protocol.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetGeminiID, "/v1beta/models/group:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		for _, stream := range []bool{false, true} {
			isStreaming.Store(stream)
			body, path := ingress.body, ingress.path
			if ingress.id == protocol.PresetGeminiID {
				if stream {
					path = "/v1beta/models/group:streamGenerateContent"
				}
			} else {
				body = fmt.Sprintf(body, stream)
			}
			req := httptest.NewRequest("POST", "/gateway/"+ingress.id+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			out := httptest.NewRecorder()
			before := calls.Load()
			serveTestHTTP(s.engine, out, req)
			want := "synthetic tool_choice.name"
			if stream {
				want = "gateway_concurrency_limit"
			}
			if calls.Load() != before+1 || !strings.Contains(out.Body.String(), want) || strings.Contains(out.Body.String(), "unsupported_capability") {
				t.Fatal(ingress.id, stream, calls.Load()-before, out.Code, out.Body.String())
			}
			if stream && out.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" || !stream && out.Code != 400 {
				t.Fatal(out.Code, out.Header())
			}
			records := latestUsageRecords(t, s)
			if len(records) == 0 {
				t.Fatal("missing persisted failure")
			}
			raw, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var saved usageRecord
			if err := sonic.ConfigStd.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if stream {
				if saved.UpstreamHTTPStatus != 200 || saved.StatusCode != 429 || out.Code != 200 {
					t.Fatal(saved.UpstreamHTTPStatus, saved.StatusCode, out.Code)
				}
			} else if saved.UpstreamHTTPStatus != 400 || saved.StatusCode != 400 {
				t.Fatal(saved.UpstreamHTTPStatus, saved.StatusCode)
			}
		}
	}
}

func TestHTTP524GatewayAndAgentPreserveObservedFailure(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetResponsesID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(524)
		fmt.Fprint(w, `{"cloudflare_error":true,"status":524,"error_name":"origin_response_timeout","private":"must-not-leak"}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, ingress := range []struct{ id, path, body string }{
		{protocol.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":%v}`},
		{protocol.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetGeminiID, "/v1beta/models/group:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		for _, stream := range []bool{false, true} {
			body, path := ingress.body, ingress.path
			if ingress.id == protocol.PresetGeminiID {
				if stream {
					path = "/v1beta/models/group:streamGenerateContent"
				}
			} else {
				body = fmt.Sprintf(body, stream)
			}
			req := httptest.NewRequest("POST", "/gateway/"+ingress.id+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			out := httptest.NewRecorder()
			before := calls.Load()
			serveTestHTTP(s.engine, out, req)
			if calls.Load() != before+1 || out.Code != 524 || !strings.Contains(out.Body.String(), "上游源站响应超时") || strings.Contains(out.Body.String(), "must-not-leak") {
				t.Fatalf("%s stream=%v: %d %s", ingress.id, stream, out.Code, out.Body.String())
			}
			records := latestUsageRecords(t, s)
			raw, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := sonic.ConfigStd.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record.UpstreamHTTPStatus != 524 || record.StatusCode != 524 || record.ErrorKind != "upstream" || len(record.ConversionIssues) == 0 {
				t.Fatalf("lost recorded primary or secondary: %+v", record)
			}
			for _, issue := range record.ConversionIssues {
				if issue.Severity == protocol.SeverityError && issue.Direction != protocol.DecodeResponse {
					t.Fatalf("wrong failure direction: %+v", issue)
				}
			}
		}
	}
	for name, operation := range upstream.Operations() {
		if operation.Kind != "generate" {
			continue
		}
		request, err := upstream.DecodeRequest(t.Context(), []byte(fmt.Sprintf(`{"model":"m","input":"hi","store":false,"stream":%v}`, operation.Transport != protocol.HTTPJSON)), protocol.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		candidate := gatewayCandidate{compiled: upstream, operation: operation, operationName: name, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: protocol.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []protocol.Transport{operation.Transport}}}
		record := &usageRecord{}
		before := calls.Load()
		result, err := s.collectProtocolGeneration(t.Context(), candidate, request, record, nil)
		var failure *upstreamHTTPFailure
		if !errors.As(err, &failure) || failure.status != 524 || result == nil || calls.Load() != before+1 || record.UpstreamHTTPStatus != 524 || len(record.ConversionIssues) == 0 || !strings.Contains(err.Error(), "上游源站响应超时") {
			t.Fatalf("collector diverged: %+v %v", record, err)
		}
	}
}
