package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayGeminiSafetySettingsReportedRequest(t *testing.T) {
	fixture, err := os.ReadFile("../protocol/builtin/testdata/gemini-disabled-safety.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetResponsesID)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), "safety") || strings.Contains(string(raw), "BLOCK_NONE") || !strings.Contains(string(raw), "Reply exactly OK.") {
					t.Error("incorrect request projection", string(raw))
				}
				stream := strings.Contains(string(raw), `"stream":true`)
				wanted := "text-decode-response"
				if stream {
					wanted = "stream-text-decode"
				}
				for _, sample := range upstream.Definition().Samples {
					if sample.ID != wanted {
						continue
					}
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(sample.Input.Bytes())
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					var frames []protocol.Value
					_ = sample.Input.Decode(&frames)
					for _, frame := range frames {
						fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
					}
					return
				}
				t.Error("missing response fixture")
				w.WriteHeader(500)
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			if mode != "compatible" {
				policy := &protocol.ConversionPolicy{SchemaVersion: 1, ID: "safety-test", Mode: "strict", Rules: []protocol.ConversionRule{}}
				if mode == "disabled" {
					policy.Mode = "compatible"
					policy.Rules = []protocol.ConversionRule{{ID: "gemini-safety-settings", Enabled: false, Order: 170, Phase: protocol.ConversionRequest, Action: "gemini_safety_settings", Value: protocol.StringValue("responses")}}
				}
				bindings, _ := s.store.ListProtocolBindings(t.Context())
				bindings[0].Conversion = &protocol.ConversionSelection{Overrides: policy}
				bindings, err = s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
					t.Fatal(err)
				}
				s.invalidateRouteCache()
			}
			for _, stream := range []bool{false, true} {
				for _, threshold := range []string{"BLOCK_NONE", "BLOCK_LOW_AND_ABOVE"} {
					body := strings.ReplaceAll(string(fixture), "BLOCK_NONE", threshold)
					path := "/gateway/google-generate-content/v1beta/models/group:generateContent"
					if stream {
						path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
					}
					r := httptest.NewRequest("POST", path, strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer gateway-test-token")
					before := calls.Load()
					rec := httptest.NewRecorder()
					s.engine.ServeHTTP(rec, r)
					accepted := mode == "compatible" && threshold == "BLOCK_NONE"
					if !accepted {
						if rec.Code != 400 || calls.Load() != before || !strings.Contains(rec.Body.String(), "safety") {
							t.Fatal(mode, threshold, rec.Code, rec.Body)
						}
						continue
					}
					if rec.Code != 200 || calls.Load() != before+1 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(rec.Body.String(), `"text":"hi"`) {
						t.Fatal(rec.Code, rec.Body, calls.Load()-before)
					}
					records := latestUsageRecords(t, s)
					if len(records) == 0 {
						t.Fatal("missing call record")
					}
					raw, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
					if err != nil {
						t.Fatal(err)
					}
					var record usageRecord
					if err = json.Unmarshal(raw, &record); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, issue := range record.ConversionIssues {
						if issue.RuleID == "gemini-safety-settings" && issue.Code == protocol.ConversionDegraded && issue.Path == "/safetySettings" && issue.Fidelity == "lossy_compatible" && issue.PolicyHash == record.ConversionPolicyHash {
							found = true
						}
					}
					if !found {
						t.Fatal("safety diagnostic not persisted", record.ConversionIssues)
					}
				}
			}
		})
	}
}
