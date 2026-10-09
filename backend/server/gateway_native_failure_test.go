package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

// A late local failure must reach clients as an error event as well as a trailer.
// An HTTP 200 after headers were flushed must not be mistaken for completion.
func TestGatewayResponsesNativeFailureSequence(t *testing.T) {
	for _, scenario := range []struct {
		name, tail string
		sequence   int64
	}{
		{"truncated", "", 51},
		{"invalid-terminal", `data: {"type":"response.completed","sequence_number":80,"response":{"id":"r","object":"response","created_at":1,"model":"m","status":"completed","output":[]}}` + "\n\n", 51},
		{"provider-nested", `data: {"type":"error","sequence_number":80,"error":{"type":"api_error","message":"synthetic provider failed","code":null,"param":null}}` + "\n\n", 80},
		{"provider-flat", `data: {"type":"error","sequence_number":80,"message":"synthetic provider failed","code":null,"param":null}` + "\n\n", 80},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			prefix := []string{
				`{"type":"response.created","sequence_number":41,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[]}}`,
				`{"type":"response.output_item.added","sequence_number":42,"output_index":0,"item":{"type":"message","id":"msg","status":"in_progress","role":"assistant","content":[]}}`,
				`{"type":"response.content_part.added","sequence_number":47,"output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`,
				`{"type":"response.output_text.delta","sequence_number":50,"output_index":0,"content_index":0,"item_id":"msg","delta":"hi"}`,
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, body := range prefix {
					_, _ = io.WriteString(w, "data: "+body+"\n\n")
				}
				_, _ = io.WriteString(w, scenario.tail)
			}))
			defer upstream.Close()
			group := config.ModelGroupConfig{ID: "g", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 3,
				Models: []config.ModelRef{{ID: "m", Name: "m", BaseURL: upstream.URL, Platform: "responses", APIKey: "synthetic"}}}
			s := newTestServerWithStore(t, []config.ModelGroupConfig{group})
			s.engine.POST("/v1/responses", s.responses)
			gateway := httptest.NewServer(s.engine)
			defer gateway.Close()
			response, err := http.Post(gateway.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"grp","stream":true,"store":false,"input":"hi"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || calls.Load() != 1 || response.Trailer.Get(gatewayStreamErrorTrailer) == "" {
				t.Fatal(response.StatusCode, calls.Load(), response.Trailer, string(body))
			}
			var frames []json.RawMessage
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(line, "data: ") {
					frames = append(frames, json.RawMessage(strings.TrimPrefix(line, "data: ")))
				}
			}
			if len(frames) != len(prefix)+1 {
				t.Fatalf("missing or duplicate late error: %s", body)
			}
			for i := range prefix {
				if string(frames[i]) != prefix[i] {
					t.Fatalf("native frame %d changed: %s", i, frames[i])
				}
			}
			var failure struct {
				Type     string
				Sequence int64 `json:"sequence_number"`
				Message  string
				Error    struct{ Message string }
			}
			if err := json.Unmarshal(frames[len(prefix)], &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Message == "" {
				failure.Error.Message = failure.Message
			}
			if failure.Type != "error" || failure.Sequence != scenario.sequence || failure.Error.Message == "" || strings.Contains(failure.Error.Message, "sequence_number") {
				t.Fatal(string(body))
			}
			records := latestUsageRecords(t, s)
			if len(records) != 1 {
				t.Fatal("generation replayed or missing persisted failure", len(records))
			}
			if records[0].StatusCode < 400 || !strings.Contains(records[0].Error, failure.Error.Message) {
				t.Fatal("late failure was recorded as success", records[0])
			}
			if dir := os.Getenv("ELYSIA_AUDIT_CAPTURE"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				record, _ := json.Marshal(map[string]any{"body": string(body), "status": response.StatusCode, "trailer": response.Trailer, "cause": failure.Error.Message})
				if err := os.WriteFile(filepath.Join(dir, "responses-native-error-"+scenario.name+".json"), record, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGatewayFailureEncodingRetainsOriginalCause(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	compiled, ok := service.Pin(protocol.PresetResponsesID)
	if !ok {
		t.Fatal("Responses preset not loaded")
	}
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	value, _ := protocol.ParseValue([]byte(`{"type":"response.created","sequence_number":9223372036854775807,"response":{"id":"r","object":"response","model":"m","created_at":1,"status":"in_progress","output":[]}}`))
	frame, err := compiled.DecodeFrame(t.Context(), value, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.EncodeFrame(t.Context(), frame, options); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Writer.WriteHeaderNow()
	cause := errors.New("original upstream failure")
	err = emitFailureEvent(c, &gatewayPlan{ingress: compiled}, options, func(protocol.Value) error {
		t.Fatal("exhausted sequence produced a frame")
		return nil
	}, cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "downstream error frame") || !strings.Contains(err.Error(), "/sequence_number") {
		t.Fatal("encoding error was swallowed or replaced the original cause", err)
	}
	if recorder.Header().Get(gatewayStreamErrorTrailer) == "" {
		t.Fatal("no trailer after encoding failed")
	}
}
