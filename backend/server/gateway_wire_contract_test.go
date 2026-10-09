package server

import (
	"encoding/json"
	"fmt"
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
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestGatewayChatWireContractFailure(t *testing.T) {
	for _, tc := range []struct {
		name, role, id, model, path string
		created                     int
	}{
		{"missing-role", "", "r", "m", "/choices/0/delta/role", 1},
		{"changed-id", `"role":"assistant",`, "other", "m", "/id", 1},
		{"changed-model", `"role":"assistant",`, "r", "other", "/model", 1},
		{"changed-created", `"role":"assistant",`, "r", "m", "/created", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := `{"id":"r","model":"m","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{` + tc.role + `"content":"hi"},"finish_reason":null}]}`
			terminal := fmt.Sprintf(`{"id":%q,"model":%q,"object":"chat.completion.chunk","created":%d,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, tc.id, tc.model, tc.created)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+prefix+"\n\ndata: "+terminal+"\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()
			s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, Strategy: "round-robin", MaxRetries: 3,
				Models: []config.ModelRef{openAIModel("m", provider.URL)}}})
			s.engine.POST("/v1/chat/completions", s.chatCompletions)
			gateway := httptest.NewServer(s.engine)
			defer gateway.Close()
			response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || calls.Load() != 1 || response.Trailer.Get(gatewayStreamErrorTrailer) == "" {
				t.Fatal(response.StatusCode, calls.Load(), string(body), response.Trailer)
			}
			var frames []string
			for _, line := range strings.Split(string(body), "\n") {
				if strings.HasPrefix(line, "data: ") {
					frames = append(frames, strings.TrimPrefix(line, "data: "))
				}
			}
			if len(frames) != 2 || frames[0] != prefix {
				t.Fatal("native prefix changed or success/duplicate error was emitted", string(body))
			}
			var failure struct{ Error struct{ Message string } }
			if err := json.Unmarshal([]byte(frames[1]), &failure); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(failure.Error.Message, tc.path) {
				t.Fatal(string(body))
			}
			records := latestUsageRecords(t, s)
			if len(records) != 1 || records[0].StatusCode < 400 || !strings.Contains(records[0].Error, tc.path) {
				t.Fatal("failed client output recorded as success", records)
			}
			if directory := os.Getenv("ELYSIA_AUDIT_CAPTURE"); directory != "" {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]any{"body": string(body), "status": response.StatusCode, "trailer": response.Trailer, "cause": failure.Error.Message})
				if err := os.WriteFile(filepath.Join(directory, "chat-contract-error-"+tc.name+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWireContractPreviewAndCombinationUseFinalFrames(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	d := presetDefinition(t, protocol.PresetChatCompletionsID)
	d.ID, d.Native.Preserve = "chat-after-without-role", false
	bad := mustEncodedProtocolValue(t, []any{map[string]any{"id": "r", "model": "m", "created": 1, "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}, "finish_reason": "stop"}}}})
	mapping := d.Directions[protocol.EncodeEvent]
	mapping.After = &protocol.Expression{Op: "literal", Value: bad}
	d.Directions[protocol.EncodeEvent] = mapping
	definition := mustEncodedProtocolValue(t, d)
	service, _ := s.protocolService()
	input := mustEncodedProtocolValue(t, []protocol.Event{{SchemaVersion: protocol.SemanticSchemaVersion, Type: protocol.OperationFailed, Error: mustEncodedProtocolValue(t, map[string]string{"message": "synthetic"})}})
	preview := service.Preview(t.Context(), definition.Bytes(), protocol.EncodeEvent, input, true, protocol.EvaluationContext{})
	if err := protocol.IssuesError(preview.Issues); err == nil || !strings.Contains(err.Error(), "/choices/0/delta/role") {
		t.Fatal("preview accepted malformed after mapping", preview.Issues)
	}
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), builtin.Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, issues := compiler.Compile(definition.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	report := protocol.VerifyCombination(t.Context(), c, c)
	if err := protocol.IssuesError(report.Issues); report.Passed || err == nil || !strings.Contains(err.Error(), "/choices/0/delta/role") {
		t.Fatal("combination ignored final after mapping", report.Issues)
	}
}
