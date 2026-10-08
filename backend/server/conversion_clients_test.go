package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// Acceptance runs standard HTTP and opt-in installed clients against a loopback gateway and a
// deterministic upstream. It never uses a real provider account or executes a
// provider-supplied command. Claude has only the read-only Read tool available.
func TestConversionClientToolRoundTrip(t *testing.T) {
	for _, client := range []string{"http-reopen", "openai", "claude"} {
		t.Run(client, func(t *testing.T) {
			location := os.Getenv("ELYSIA_TEST_OPENAI_MODULE")
			if client == "claude" {
				location = os.Getenv("ELYSIA_TEST_CLAUDE_BIN")
			}
			if location == "" && client != "http-reopen" {
				t.Skip("requires an explicitly supplied isolated client installation")
			}
			s, cfgPath := newProtocolAdminTestServer(t)
			s.store.Close()
			var err error
			dbPath := filepath.Join(filepath.Dir(cfgPath), "client.db")
			s.store, err = storage.OpenWithKey(dbPath, s.config.GetDBEncryptionKey())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.store.Close() })
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetGeminiID)
			work := t.TempDir()
			file := filepath.Join(work, "client-fixture.txt")
			if err := os.WriteFile(file, []byte("LOCAL_CLIENT_FIXTURE"), 0600); err != nil {
				t.Fatal(err)
			}
			var requests, restored, returnedCarriers atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), protocol.ContinuationPrefix) {
					t.Error("client envelope leaked to provider")
				}
				part := map[string]any{"text": "LOCAL_CLIENT_OK"}
				if !strings.Contains(string(body), `"functionResponse"`) {
					name, args := "lookup", map[string]any{"value": "fixture"}
					if client == "claude" {
						name, args = "Read", map[string]any{"file_path": file}
					}
					part = map[string]any{"functionCall": map[string]any{"id": "client-call", "name": name, "args": args}, "thoughtSignature": "client-original-signature"}
				} else {
					if !strings.Contains(string(body), `"thoughtSignature":"client-original-signature"`) {
						t.Error("client's next tool round lost the original signature")
					} else {
						restored.Add(1)
					}
				}
				response := map[string]any{"responseId": "client-response", "modelVersion": "upstream-model", "candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{part}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 3, "candidatesTokenCount": 2, "totalTokenCount": 5}}
				encoded, _ := json.Marshal(response)
				if strings.Contains(r.URL.Path, "streamGenerateContent") {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", encoded)
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.Write(encoded)
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
			// This explicitly configured loss is unrelated to signature preservation:
			// This fixture drops Claude request tracking and effort configuration
			// through named rules, without claiming Gemini's reasoning is equivalent.
			bindings, _ := s.store.ListProtocolBindings(t.Context())
			bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "client-acceptance", Rules: []protocol.ConversionRule{{ID: "client-metadata", Order: 110, Enabled: true, Phase: protocol.ConversionRequest, Match: protocol.ConversionMatch{SourceFamily: "claude", TargetFamily: "gemini"}, Action: "remove", Path: "/parameters/anthropic_metadata"}, {ID: "client-effort", Order: 120, Enabled: true, Phase: protocol.ConversionRequest, Match: protocol.ConversionMatch{SourceFamily: "claude", TargetFamily: "gemini"}, Action: "remove", Path: "/parameters/anthropic_output_config"}}}}
			verified, err := s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.store.SaveProtocolBinding(t.Context(), verified[0]); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), protocol.ContinuationPrefix) {
					returnedCarriers.Add(1)
				}
				r.Body = io.NopCloser(strings.NewReader(string(body)))
				s.engine.ServeHTTP(w, r)
			})
			gateway := httptest.NewServer(handler)
			defer gateway.Close()
			if client == "http-reopen" {
				post := func(body string) string {
					r, _ := http.NewRequestWithContext(t.Context(), "POST", gateway.URL+"/gateway/openai-chat-completions/chat/completions", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer gateway-test-token")
					r.Header.Set("x-elysia-session-id", "reopen-tools")
					response, err := gateway.Client().Do(r)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					data, err := io.ReadAll(response.Body)
					if err != nil || response.StatusCode != 200 {
						t.Fatal(response.StatusCode, err, string(data))
					}
					return string(data)
				}
				first := post(`{"model":"group","stream":true,"messages":[{"role":"user","content":"fixture"}]}`)
				if !strings.Contains(first, protocol.ContinuationPrefix) {
					t.Fatal("missing streamed client carrier")
				}
				gateway.Close()
				if err := s.store.Close(); err != nil {
					t.Fatal(err)
				}
				s.store, err = storage.OpenWithKey(dbPath, s.config.GetDBEncryptionKey())
				if err != nil {
					t.Fatal(err)
				}
				s.protocolServiceOnce, s.protocolServiceInst, s.protocolServiceErr = sync.Once{}, nil, nil
				s.invalidateRouteCache()
				gateway = httptest.NewServer(handler)
				defer gateway.Close()
				// SDKs commonly add content:null. It must not shift an identified
				// tool out of its exact persistent lookup after reopening storage.
				second := post(`{"model":"group","messages":[{"role":"user","content":"fixture"},{"role":"assistant","content":null,"tool_calls":[{"id":"client-call","type":"function","function":{"name":"lookup","arguments":"{\"value\":\"fixture\"}"}}]},{"role":"tool","tool_call_id":"client-call","content":"result"}]}`)
				if !strings.Contains(second, "LOCAL_CLIENT_OK") || restored.Load() != 1 || returnedCarriers.Load() != 0 {
					t.Fatal("reopened persistent tool recovery failed", second)
				}
				return
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var command *exec.Cmd
			if client == "openai" {
				script, _ := filepath.Abs("testdata/conversion-openai.mjs")
				command = exec.CommandContext(ctx, "node", script, location, gateway.URL)
			} else {
				command = exec.CommandContext(ctx, location, "--bare", "--restricted", "--no-session-persistence", "--model", "group", "--tools", "Read", "--allowedTools", "Read", "--system-prompt", "Read the requested file and reply LOCAL_CLIENT_OK.", "--output-format", "json", "--print", "Read "+file)
			}
			command.Dir = work
			for _, variable := range os.Environ() {
				upper := strings.ToUpper(strings.SplitN(variable, "=", 2)[0])
				if strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "CLAUDE") || strings.HasPrefix(upper, "OPENAI_") {
					continue
				}
				command.Env = append(command.Env, variable)
			}
			command.Env = append(command.Env, "ANTHROPIC_API_KEY=gateway-test-token", "ANTHROPIC_BASE_URL="+gateway.URL+"/gateway/anthropic-messages", "CLAUDE_CONFIG_DIR="+filepath.Join(work, "claude-config"), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_PROMPT_CACHING=1", "MAX_THINKING_TOKENS=0")
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "LOCAL_CLIENT_OK") || restored.Load() == 0 {
				t.Fatalf("client=%s requests=%d restored=%d error=%v output=%s", client, requests.Load(), restored.Load(), err, output)
			}
			t.Logf("client=%s requests=%d restored=%d returnedCarriers=%d", client, requests.Load(), restored.Load(), returnedCarriers.Load())
		})
	}
}
