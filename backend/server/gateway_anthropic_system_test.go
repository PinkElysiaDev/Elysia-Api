package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayAnthropicSystemMessageClientHistory(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../protocol/builtin/testdata/claude-code-system-message.json")
	if err != nil {
		t.Fatal(err)
	}
	var request protocol.Object
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	request["model"] = protocol.StringValue("group")
	request["thinking"] = mustProtocolValue(t, `{"type":"adaptive","display":"updates"}`)
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetAnthropicID)
	captured := make(chan protocol.Value, 4)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		value, err := protocol.ParseValue(body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		captured <- value
		fields, _ := value.ReadObject()
		var stream bool
		_ = fields["stream"].Decode(&stream)
		fixture := cacheWireFixtures()[1]
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, fixture.stream)
		} else {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, fixture.response)
		}
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	isToolCapable := true
	if found, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &isToolCapable}); err != nil || !found {
		t.Fatalf("enable test model tools: found=%v err=%v", found, err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	for _, stream := range []bool{false, true} {
		for _, isSecondRound := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/tool-history=%v", stream, isSecondRound), func(t *testing.T) {
				body, _ := protocol.EncodeValue(request)
				fields, _ := body.ReadObject()
				fields["stream"], _ = protocol.EncodeValue(stream)
				if isSecondRound {
					var messages []protocol.Value
					_ = fields["messages"].Decode(&messages)
					messages = append(messages,
						mustProtocolValue(t, `{"role":"assistant","content":[{"type":"tool_use","id":"call-a","name":"audit_tool_00","input":{"value":"one"}},{"type":"tool_use","id":"call-b","name":"audit_tool_00","input":{"value":"two"}}]}`),
						mustProtocolValue(t, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-a","content":"first result"},{"type":"tool_result","tool_use_id":"call-b","content":"second result"}]}`))
					fields["messages"], _ = protocol.EncodeValue(messages)
				}
				body, _ = protocol.EncodeValue(fields)
				semantic, err := upstream.DecodeRequest(t.Context(), body.Bytes(), protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				semanticValue, _ := protocol.EncodeValue(semantic)
				preview := service.PreviewWorkflow(t.Context(), protocol.PreviewInput{Mode: "conversion", ConversionPhase: protocol.ConversionRequest, Input: semanticValue, ConversionContext: protocol.ConversionContext{Source: upstream.Identity(), Target: upstream.Identity(), Model: "upstream-model"}})
				if err := protocol.IssuesError(preview.Issues); err != nil {
					t.Fatal("preview differs from gateway", err)
				}
				var previewRequest protocol.Request
				if err := preview.Output.Decode(&previewRequest); err != nil {
					t.Fatal(err)
				}
				previewRequest.Model = protocol.StringValue("upstream-model")
				previewWire, err := upstream.EncodeRequest(t.Context(), &previewRequest, protocol.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/gateway/anthropic-messages/v1/messages", strings.NewReader(string(body.Bytes())))
				req.Header.Set("Authorization", "Bearer gateway-test-token")
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, req)
				if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || strings.Contains(rec.Body.String(), "event: error") {
					t.Fatalf("status=%d trailer=%v body=%s", rec.Code, rec.Result().Trailer, rec.Body)
				}
				select {
				case actual := <-captured:
					fields["model"] = protocol.StringValue("upstream-model")
					expected, _ := protocol.EncodeValue(fields)
					var actualJSON, expectedJSON any
					if err := actual.Decode(&actualJSON); err != nil {
						t.Fatal(err)
					}
					if err := expected.Decode(&expectedJSON); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(actualJSON, expectedJSON) {
						t.Fatalf("request structure changed: got %s; want %s", actual.Bytes(), expected.Bytes())
					}
					var previewJSON any
					previewValue, _ := protocol.ParseValue(previewWire)
					if err := previewValue.Decode(&previewJSON); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(actualJSON, previewJSON) {
						t.Fatal("preview differs from forwarded request")
					}
				default:
					t.Fatal("no upstream request")
				}
				logs := latestUsageRecords(t, s)
				stored, _, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
				if err != nil {
					t.Fatal(err)
				}
				var record usageRecord
				if err := json.Unmarshal(stored, &record); err != nil {
					t.Fatal(err)
				}
				for _, issue := range record.ConversionIssues {
					if issue.RuleID == "system-instruction-hoist" {
						t.Fatal("native system request falsely degraded", issue)
					}
				}
			})
		}
	}
}

func TestGatewaySystemHoistFailureRetainsRequestEvidence(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	ingress, _ := service.Pin(protocol.PresetAnthropicID)
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	body := `{"model":"group","max_tokens":64,"system":[{"type":"text","text":"private prefix","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"private instruction"}],"output_config":{"effort":"medium"}}]}`
	semantic, err := ingress.DecodeRequest(t.Context(), []byte(body), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := protocol.EncodeValue(semantic)
	preview := service.PreviewWorkflow(t.Context(), protocol.PreviewInput{Mode: "conversion", ConversionPhase: protocol.ConversionRequest, Input: input, ConversionContext: protocol.ConversionContext{Source: ingress.Identity(), Target: upstream.Identity(), Model: "upstream-model"}})
	if len(preview.Issues) != 2 || preview.Issues[1].Path != "/messages/1/output_config" {
		t.Fatal("preview lost request diagnostic", preview.Issues)
	}
	req := httptest.NewRequest(http.MethodPost, "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gateway-test-token")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if rec.Code != 400 || calls.Load() != 0 || !strings.Contains(rec.Body.String(), "/messages/1/output_config") {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, calls.Load(), rec.Body)
	}
	logs := latestUsageRecords(t, s)
	stored, _, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	var record usageRecord
	if err := json.Unmarshal(stored, &record); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.ConversionIssues, preview.Issues) {
		t.Fatal("gateway/preview diagnostic mismatch", record.ConversionIssues, preview.Issues)
	}
	issue := record.ConversionIssues[1]
	if issue.Stage != "conversion.request" || issue.Direction != protocol.EncodeRequest || issue.Protocol != upstream.Identity() || issue.RuleID != "system-instruction-hoist" || issue.PolicyHash == "" || issue.PolicyRevision == "" {
		t.Fatal("request evidence incomplete", issue)
	}
	diagnostics, _ := json.Marshal(record.ConversionIssues)
	if strings.Contains(string(diagnostics), "private") {
		t.Fatal("diagnostics included request text")
	}
}

func TestGatewayInvalidThinkingDisplayRequestEvidence(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetAnthropicID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"model":"group","max_tokens":8192,"stream":%v,"thinking":{"type":"adaptive","display":"future"},"messages":[{"role":"user","content":"hi"}]}`, stream)
		req := httptest.NewRequest(http.MethodPost, "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != 400 || calls.Load() != 0 {
			t.Fatalf("status=%d upstream calls=%d", rec.Code, calls.Load())
		}
		logs := latestUsageRecords(t, s)
		stored, found, err := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
		if err != nil || !found {
			t.Fatal("missing persisted request evidence", found, err)
		}
		var record usageRecord
		if err := json.Unmarshal(stored, &record); err != nil {
			t.Fatal(err)
		}
		if len(record.ConversionIssues) != 1 {
			t.Fatal(record.ConversionIssues)
		}
		issue := record.ConversionIssues[0]
		if issue.Direction != protocol.DecodeRequest || issue.Stage != "request.validation" || issue.Protocol != upstream.Identity() || issue.Path != "/thinking/display" {
			t.Fatal("invalid display logged as an upstream stream failure", issue)
		}
	}
}
