package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayResponsesContextBoundaries(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		for _, forbidden := range []string{"previous_response", "truncation", "include"} {
			if strings.Contains(string(raw), forbidden) {
				t.Error("client context leaked", string(raw))
			}
		}
		body := `{"responseId":"r","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: "+body+"\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, tc := range []struct{ field, path, code string }{
		{`"previous_response_id":null`, "", ""},
		{`"previous_response_id":"r"`, "/previous_response_id", "unsupported_capability"},
		{`"previous_response_id":""`, "/previous_response_id", "invalid_input"},
		{`"previous_response_id":42`, "/previous_response_id", "invalid_input"},
		{`"truncation":42`, "/truncation", "invalid_input"},
		{`"truncation":"future"`, "/truncation", "invalid_input"},
		{`"truncation":"auto"`, "/truncation", "conversion_rejected"},
		{`"truncation":"disabled"`, "/truncation", "conversion_rejected"},
		{`"truncation":null`, "/truncation", "conversion_rejected"},
	} {
		for _, stream := range []bool{false, true} {
			req := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(fmt.Sprintf(`{"model":"group","input":"hi","store":false,"stream":%v,%s}`, stream, tc.field)))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			before := calls
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if tc.path != "" {
				if rec.Code != 400 || before != calls || !strings.Contains(rec.Body.String(), tc.path) || !strings.Contains(rec.Body.String(), tc.code) {
					t.Fatal(tc, rec.Code, calls-before, rec.Body.String())
				}
				continue
			}
			if rec.Code != 200 || before+1 != calls || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal(rec.Code, rec.Body.String())
			}
			items := latestUsageRecords(t, s)
			for deadline := time.Now().Add(3 * time.Second); len(items) < calls && time.Now().Before(deadline); {
				time.Sleep(20 * time.Millisecond)
				items = latestUsageRecords(t, s)
			}
			found := false
			for _, item := range items {
				if item.Stream != stream {
					continue
				}
				raw, _, err := s.store.GetUsageRecordJSON(t.Context(), item.RequestID)
				if err != nil {
					t.Fatal(err)
				}
				var record usageRecord
				if err = json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				for _, issue := range record.ConversionIssues {
					if issue.Code == protocol.ConversionNormalized && issue.RuleID == "responses-context" && issue.Path == "/previous_response_id" && issue.PolicyHash != "" {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("normalization not persisted")
			}
		}
	}
}

func TestUsageRecordDiagnosticDeduplication(t *testing.T) {
	base := protocol.ConversionIssue{Code: protocol.ConversionDegraded, Path: "/include", Stage: "conversion.request", Severity: protocol.SeverityInfo, Fidelity: "preserved", RuleID: "responses-include", PolicyHash: "p", Protocol: protocol.Identity{Revision: "a"}}
	response := base
	response.Stage, response.Severity, response.Fidelity = "conversion.response", protocol.SeverityWarning, "lossy_compatible"
	other := base
	other.Protocol.Revision = "b"
	record := &usageRecord{}
	sink := &protocol.DiagnosticSink{}
	for _, issue := range []protocol.ConversionIssue{base, response, other, base, response, other} {
		sink.Add(issue)
		record.appendConversionIssues([]protocol.ConversionIssue{issue})
	}
	if len(record.ConversionIssues) != 3 || len(sink.Issues()) != 3 {
		t.Fatal(record.ConversionIssues, sink.Issues())
	}
}
