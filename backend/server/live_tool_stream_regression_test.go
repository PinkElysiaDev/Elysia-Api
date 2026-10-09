package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayAnthropicDirectToolToResponsesStream(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetAnthropicID)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"echo","caller":{"type":"direct"},"input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{ \"value\": 7 }"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprint(w, "data: "+frame+"\n\n")
		}
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	yes := true
	if _, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	body := `{"model":"group","stream":true,"store":false,"max_output_tokens":64,"input":"call echo","tools":[{"type":"function","name":"echo","parameters":{"type":"object"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/gateway/openai-responses/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gateway-test-token")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(rec.Body.String(), "response.completed") || strings.Contains(rec.Body.String(), "event: error") {
		t.Fatal(rec.Code, rec.Result().Trailer, rec.Body.String())
	}
}
