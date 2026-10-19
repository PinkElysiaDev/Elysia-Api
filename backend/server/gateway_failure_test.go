package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/gin-gonic/gin"
)

func declareResponseFailures(t *testing.T, definition protocol.Definition, field string) protocol.Definition {
	t.Helper()
	for _, direction := range []protocol.Direction{protocol.DecodeResponse, protocol.EncodeResponse} {
		mapping := definition.Directions[direction]
		path, output := "/"+field, "error"
		if direction == protocol.EncodeResponse {
			path, output = "/error", field
		}
		fields := map[string]protocol.Expression{output: {Op: "read", Path: path, Required: true}}
		if direction == protocol.DecodeResponse {
			fields["schemaVersion"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `1`)}
			fields["content"] = protocol.Expression{Op: "literal", Value: mustProtocolValue(t, `[]`)}
		}
		mapping.Transform = &protocol.Expression{Op: "if", When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", Path: path}}, Then: &protocol.Expression{Op: "object", Fields: fields}, Otherwise: mapping.Transform}
		definition.Directions[direction] = mapping
	}
	payload := mustProtocolValue(t, `{"message":"quota exhausted","code":"exhausted"}`)
	wire := mustEncodedProtocolValue(t, protocol.Object{field: payload})
	semantic := mustEncodedProtocolValue(t, protocol.Object{"schemaVersion": mustProtocolValue(t, `1`), "content": mustProtocolValue(t, `[]`), "error": payload})
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "failure.decode", Direction: protocol.DecodeResponse, Input: wire, Expected: semantic},
		protocol.Sample{ID: "failure.encode", Direction: protocol.EncodeResponse, Input: semantic, Expected: wire})
	return definition
}

func TestHTTPFailureKeepsStatusAndSafePrimaryCause(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	for _, sourceID := range []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID} {
		source, _ := service.Pin(sourceID)
		for _, targetID := range []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID} {
			target, _ := service.Pin(targetID)
			for _, fixture := range []struct{ body, message string }{
				{`{"cloudflare_error":true,"status":524,"error_name":"origin_response_timeout","private":"must-not-leak"}`, "上游源站响应超时"},
				{`{"cloudflare_error":true,"status":523,"error_name":"origin_response_timeout","private":"must-not-leak"}`, "上游请求失败"},
				{`{"cloudflare_error":true,"status":"524","error_name":"origin_response_timeout"}`, "上游请求失败"},
				{`<html>must-not-leak</html>`, "上游请求失败"},
				{"", "上游请求失败"},
			} {
				record := &usageRecord{UpstreamHTTPStatus: 524}
				err := mapGatewayHTTPFailure(t.Context(), record, 524, []byte(fixture.body), target, gatewayCandidate{compiled: source}, protocol.EvaluationContext{})
				var failure *gatewayFailure
				var issue *protocol.ConversionError
				if !errors.As(err, &failure) || failure.status != 524 || generationFailureClass(err) != builtin.ErrorClassUpstream || canRetryGeneration(err) {
					t.Fatalf("lost HTTP primary: %s -> %s: %v", sourceID, targetID, err)
				}
				if !errors.As(err, &issue) || len(issue.Issues) == 0 {
					t.Fatalf("lost secondary decode diagnostics: %v", err)
				}
				out := httptest.NewRecorder()
				request, _ := gin.CreateTestContext(out)
				request.Request = httptest.NewRequest("POST", "/gateway/"+targetID, nil)
				s.failGateway(request, record, 502, err)
				if out.Code != 524 || record.ErrorKind != "upstream" || len(record.ConversionIssues) == 0 || !strings.Contains(out.Body.String(), fixture.message) || strings.Contains(out.Body.String(), "must-not-leak") || strings.Contains(err.Error(), "HTTP 524: HTTP 524:") {
					t.Fatalf("unsafe or obscured failure: %s -> %s: %d %s; %+v", sourceID, targetID, out.Code, out.Body.String(), record)
				}
			}
		}
	}
}

func TestCustomHTTPFailuresUseDeclaredResponseDirections(t *testing.T) {
	t.Parallel()
	upstreamDefinition := declareResponseFailures(t, standaloneWireDefinition(t, protocol.HTTPJSON), "problem")
	ingress := declareResponseFailures(t, standaloneWireDefinition(t, protocol.HTTPJSON), "failure")
	ingress.ID, ingress.Family = "failure-client", "failure-client"
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write([]byte(`{"problem":{"message":"quota exhausted","code":"exhausted"}}`))
			}))
			defer upstream.Close()
			groups := standaloneGroup(t, upstreamDefinition, upstream.URL)
			groups[0].MaxRetries = 2
			second := groups[0].Models[0]
			second.ID, second.Name = second.ID+"-second", second.Name+"-second"
			groups[0].Models = append(groups[0].Models, second)
			server := newTestServer(t, groups, upstreamDefinition, ingress)
			recorder := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(recorder)
			request.Request = httptest.NewRequest(http.MethodPost, "/gateway/failure-client/v2/generate/grp", strings.NewReader(`{"deployment":"grp","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`))
			request.Params = gin.Params{{Key: "protocolId", Value: ingress.ID}, {Key: "path", Value: "/v2/generate/grp"}}
			server.gatewayProtocol(request)
			if calls.Load() != 1 {
				t.Fatalf("generation replayed %d times after a nonretryable outcome", calls.Load())
			}
			wantStatus := status
			if status == http.StatusOK {
				wantStatus = http.StatusBadGateway
			}
			var decoded map[string]map[string]string
			decodeErr := json.Unmarshal(recorder.Body.Bytes(), &decoded)
			if recorder.Code != wantStatus || decodeErr != nil || len(decoded) != 1 || len(decoded["failure"]) != 2 || decoded["failure"]["code"] != "exhausted" || decoded["failure"]["message"] != "quota exhausted" {
				t.Fatalf("declared failure was not converted: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGatewayUnmappedFailureKeepsProviderMessageAndConversionIssue(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	ingress, _ := service.Pin(protocol.PresetAnthropicID)
	const message = "Thinking mode does not support this tool_choice"
	const body = `{"error":{"type":"invalid_request_error","code":"vendor_tool_choice_detail","message":"` + message + `","param":null}}`
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	s.engine.POST("/v1/messages", s.authMiddleware(), s.chatCompletions)
	for _, path := range []string{"/v1/messages", "/gateway/anthropic-messages/v1/messages"} {
		before := calls.Load()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"group","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), message) || !strings.Contains(rec.Body.String(), "vendor_tool_choice_detail") || strings.Contains(rec.Body.String(), "unsupported_capability") {
			t.Fatalf("provider cause masked: %d %s", rec.Code, rec.Body.String())
		}
		if calls.Load() != before+1 {
			t.Fatal("nonretryable upstream rejection retried")
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		record := &usageRecord{}
		err := mapGatewayHTTPFailure(t.Context(), record, status, []byte(body), ingress, gatewayCandidate{compiled: upstream}, protocol.EvaluationContext{})
		var issue *protocol.ConversionError
		var failure *gatewayFailure
		if !errors.As(err, &issue) || len(issue.Issues) == 0 || issue.Issues[0].Path != "/error/code" || !errors.As(err, &failure) || failure.status != status {
			t.Fatalf("lost typed failure: %v", err)
		}
		if canRetryGeneration(err) != (status == http.StatusServiceUnavailable) {
			t.Fatal("HTTP retry classification changed")
		}
	}
	// Opaque fields remain rejected; only the typed message is added to the
	// public failure context. This is not successful provider error mapping.
	response := &protocol.Response{SchemaVersion: 1, Error: mustProtocolValue(t, `{"message":"readable provider cause","code":"unsupported-code","details":{"private":"must-not-leak"}}`)}
	failure := encodeGatewayFailure(t.Context(), http.StatusBadGateway, response, ingress, protocol.EvaluationContext{})
	if !strings.Contains(failure.Error(), "readable provider cause") || strings.Contains(failure.Error(), "must-not-leak") || canRetryGeneration(failure) {
		t.Fatalf("unsafe/misclassified conversion context: %v", failure)
	}
	// Invalid custom output still reports the existing typed error, even when
	// no provider message can be read from it.
	failure = encodeGatewayFailure(t.Context(), http.StatusBadGateway, nil, ingress, protocol.EvaluationContext{})
	var missing *protocol.ConversionError
	if !errors.As(failure, &missing) || strings.Contains(failure.Error(), "upstream failure:") {
		t.Fatalf("missing response lost its validation error: %v", failure)
	}
}
