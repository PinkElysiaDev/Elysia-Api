package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayAnthropicEnvelopeAndClients(t *testing.T) {
	for _, scenario := range []string{"first", "late", "missing", "zero", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetGeminiID)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				first := `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]`
				if scenario == "first" {
					first += `,"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}`
				}
				if scenario == "zero" {
					first += `,"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0}`
				}
				if scenario == "partial" {
					first += `,"usageMetadata":{"promptTokenCount":3}`
				}
				fmt.Fprint(w, "data: "+first+"}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				fmt.Fprint(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
				if scenario == "first" || scenario == "late" {
					fmt.Fprint(w, "data: {\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"thoughtsTokenCount\":3,\"totalTokenCount\":8}}\n\n")
				}
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			req := httptest.NewRequest("POST", "/gateway/anthropic-messages/v1/messages", strings.NewReader(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":true}`))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal(rec.Code, rec.Body.String(), rec.Result().Trailer)
			}
			var start, tail map[string]any
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if strings.HasPrefix(line, "data: ") {
					var frame map[string]any
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) != nil {
						continue
					}
					switch frame["type"] {
					case "message_start":
						start = frame["message"].(map[string]any)["usage"].(map[string]any)
					case "message_delta":
						tail = frame["usage"].(map[string]any)
					}
				}
			}
			expectedStart := float64(0)
			if scenario == "first" || scenario == "partial" {
				expectedStart = 3
			}
			if start == nil || start["input_tokens"] != expectedStart || tail == nil || tail["output_tokens"] == nil {
				t.Fatal(start, tail, rec.Body.String())
			}
			if scenario == "first" || scenario == "late" {
				if tail["input_tokens"] != float64(3) || tail["output_tokens"] != float64(5) {
					t.Fatal(tail)
				}
			}
			records := latestUsageRecords(t, s)
			if len(records) != 1 {
				t.Fatal(records)
			}
			data, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err = json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if scenario == "missing" && record.ProtocolUsage != nil {
				t.Fatal("placeholder reached billing", record.ProtocolUsage)
			}
			if scenario == "partial" && (record.ProtocolUsage == nil || record.ProtocolUsage.Output != nil) {
				t.Fatal("placeholder reached billing", record.ProtocolUsage)
			}
			if scenario == "first" || scenario == "late" {
				if record.ProtocolUsage.Output.Count != 5 || record.ProtocolUsage.Details["output.reasoning_tokens"].Count != 3 {
					t.Fatal(record.ProtocolUsage)
				}
			}
			if moduleRoot := os.Getenv("ELYSIA_TEST_ANTHROPIC_MODULES"); moduleRoot != "" {
				gateway := httptest.NewServer(s.engine)
				defer gateway.Close()
				script, _ := filepath.Abs("testdata/conversion-anthropic.mjs")
				cmd := exec.CommandContext(t.Context(), "node", script, moduleRoot, gateway.URL, scenario)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatal(err, string(output))
				}
				t.Log(string(output))
			}
		})
	}
}

func TestGatewayResponsesStorage(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "store") {
			t.Error("store leaked", string(raw))
		}
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"responseId\":\"r\",\"modelVersion\":\"m\",\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}]}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"responseId":"r","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, value := range []string{"", "null", "false", "true", "42", `"false"`} {
		raw := `{"model":"group","input":"hello"`
		if value != "" {
			raw += `,"store":` + value
		}
		raw += `}`
		req := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		rec := httptest.NewRecorder()
		before := calls
		s.engine.ServeHTTP(rec, req)
		if value == "42" || value == `"false"` {
			if rec.Code != 400 || calls != before || !strings.Contains(rec.Body.String(), "/store") {
				t.Fatal(rec.Code, rec.Body.String(), calls)
			}
			continue
		}
		var result map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		if rec.Code != 200 || calls != before+1 || result["store"] != false {
			t.Fatal(value, rec.Code, rec.Body.String())
		}
	}
	streamReq := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(`{"model":"group","input":"hello","stream":true,"store":true,"include":["reasoning.encrypted_content"]}`))
	streamReq.Header.Set("Authorization", "Bearer gateway-test-token")
	streamRec := httptest.NewRecorder()
	s.engine.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != 200 || streamRec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
		t.Fatal(streamRec.Code, streamRec.Body.String())
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(streamRec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame struct {
			Type     string         `json:"type"`
			Response map[string]any `json:"response"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) != nil {
			continue
		}
		if frame.Type == "response.created" || frame.Type == "response.completed" {
			if frame.Response["store"] != false {
				t.Fatal("stream claimed storage", frame)
			}
			seen[frame.Type] = true
		}
	}
	if len(seen) != 2 {
		t.Fatal(streamRec.Body.String())
	}
	req := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(`{"model":"group","input":"next","store":false,"previous_response_id":"old-response"}`))
	req.Header.Set("Authorization", "Bearer gateway-test-token")
	before := calls
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if rec.Code == 200 || calls != before {
		t.Fatal("stateful request lost context", rec.Code, rec.Body.String())
	}
}
