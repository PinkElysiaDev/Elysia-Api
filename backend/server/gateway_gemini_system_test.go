package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayGeminiSystemInstructionToResponses(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetResponsesID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Input []struct {
				Role    string
				Content []struct{ Text string }
			}
			Stream bool
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if len(request.Input) != 2 || request.Input[0].Role != "system" || len(request.Input[0].Content) != 1 || request.Input[0].Content[0].Text != "prefix" || request.Input[1].Role != "user" || strings.Contains(string(raw), "systemInstruction") {
			t.Error("system authority or text changed", string(raw))
			w.WriteHeader(500)
			return
		}
		wanted := "text-decode-response"
		if request.Stream {
			wanted = "stream-text-decode"
		}
		for _, sample := range upstream.Definition().Samples {
			if sample.ID != wanted {
				continue
			}
			if !request.Stream {
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
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, system string
			status       int
			calls        int32
		}{
			{"sdk-role", `{"role":"user","parts":[{"text":"prefix"}]}`, 200, 1},
			{"invalid-role", `{"role":42,"parts":[{"text":"prefix"}]}`, 400, 0},
			{"unknown-state", `{"role":"user","parts":[{"text":"prefix"}],"vendor":{"enabled":true}}`, 400, 0},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				path := "/gateway/google-generate-content/v1beta/models/group:generateContent"
				if stream {
					path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
				}
				body := `{"systemInstruction":` + tc.system + `,"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":32}}`
				r := httptest.NewRequest("POST", path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer gateway-test-token")
				before := calls.Load()
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, r)
				if rec.Code != tc.status || calls.Load()-before != tc.calls || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
					t.Fatal(rec.Code, calls.Load()-before, rec.Body, rec.Result().Trailer)
				}
				if tc.status == 200 && !strings.Contains(rec.Body.String(), `"text":"hi"`) {
					t.Fatal(rec.Body)
				}
				if tc.name == "invalid-role" && (!strings.Contains(rec.Body.String(), "/systemInstruction/role") || !strings.Contains(rec.Body.String(), "invalid_input")) {
					t.Fatal(rec.Body)
				}
				if tc.name == "unknown-state" && !strings.Contains(rec.Body.String(), "/wire:gemini/systemInstruction/vendor/enabled") {
					t.Fatal("missing extension field path", rec.Body)
				}
				records := latestUsageRecords(t, s)
				if len(records) == 0 || records[0].StatusCode != tc.status {
					t.Fatal(records)
				}
			})
		}
	}
}
