package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestAgentReadinessDistinguishesModelBindingAndRevision(t *testing.T) {
	s := newAgentIntegrationServer(t)
	compiled := activateGatewayDefinition(t, s, agentEnvelopeDefinition(t))
	model := storage.Model{ID: "grok-4.7", SourceID: "s", Name: "grok-4.7", Enabled: true, CapabilitySource: "catalog"}
	binding := makeProtocolBinding(upgradeBindingKey{kind: "source", source: "s"}, compiled)
	check := func(want string) {
		t.Helper()
		got := checkAgentModel(s.protocolServiceInst.View(), []storage.ProtocolBinding{binding}, model)
		if got.ReasonCode != want {
			t.Fatalf("want %s got %+v", want, got)
		}
	}
	check("model_tools_disabled")
	model.ToolsCapable = true
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = false
	check("binding_tools_disabled")
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = true
	binding.Binding.RevisionHash = "old"
	check("binding_verification_stale")
	binding.Binding.RevisionHash = compiled.Hash()
	check("")
	blocked := storage.ProtocolBinding{Kind: "model", SourceID: "s", ModelID: model.ID, Unbound: true}
	if got := checkAgentModel(s.protocolServiceInst.View(), []storage.ProtocolBinding{binding, blocked}, model); got.ReasonCode != "unbound" {
		t.Fatal(got)
	}
}

func TestAgentToolProbeOnlyCommitsValidatedCapability(t *testing.T) {
	for _, mode := range []string{"success", "missing_tool", "wrong_arguments", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			s.setupAgentRoutes(s.engine.Group("/api/admin"))
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				match := regexp.MustCompile(`nonce ([A-Za-z0-9]+)\.`).FindSubmatch(body)
				if len(match) != 2 {
					t.Errorf("probe nonce missing")
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "missing_tool" {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"I cannot call tools"},"finish_reason":"stop"}]}`)
					return
				}
				nonce := string(match[1])
				if mode == "wrong_arguments" {
					nonce = "wrong"
				}
				if mode == "conflict" {
					name := "changed"
					if _, err := s.store.UpdateModel(t.Context(), "fake-model", "cs1", storage.ModelPatch{Name: &name}); err != nil {
						t.Error(err)
					}
				}
				args, _ := json.Marshal(map[string]string{"nonce": nonce})
				argsString, _ := json.Marshal(string(args))
				fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"test-call","type":"function","function":{"name":"elysia_capability_probe","arguments":%s}}]},"finish_reason":"tool_calls"}]}`, argsString)
			}))
			defer provider.Close()
			seedCallerModel(t, s, provider.URL, "custom:openai-chat-completions")
			bindings, _ := s.store.ListProtocolBindings(t.Context())
			binding := bindings[0]
			binding.Binding.Transports = []protocol.Transport{protocol.HTTPJSON}
			binding.Binding.Operation = "generate"
			binding.Binding.Capabilities[protocol.FunctionToolsCapability] = false
			if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
				t.Fatal(err)
			}
			tools := false
			if _, err := s.store.UpdateModel(t.Context(), "fake-model", "cs1", storage.ModelPatch{ToolsCapable: &tools}); err != nil {
				t.Fatal(err)
			}
			response := revisionAdminRequest(t, s.engine, "POST", "/api/admin/agent/models/verify-tools", []byte(`{"sourceId":"cs1","modelId":"fake-model"}`), "")
			want := 400
			if mode == "success" {
				want = 200
			}
			if mode == "conflict" {
				want = 409
			}
			if response.Code != want || calls != 1 {
				t.Fatal(response.Code, response.Body, calls)
			}
			models, _ := s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
			after, _ := s.store.ListProtocolBindings(t.Context())
			if mode != "success" {
				if models[0].ToolsCapable || len(after) != 1 {
					t.Fatal("failed probe changed capability or binding", models, after)
				}
				return
			}
			if !models[0].ToolsCapable || models[0].CapabilitySource != "manual" || len(after) != 2 {
				t.Fatal("repair did not commit atomically")
			}
			entry, _ := selectProtocolBinding(after, modelReference(models[0]))
			if entry.Kind != "model" || !entry.Binding.Capabilities[protocol.FunctionToolsCapability] {
				t.Fatal(entry)
			}
			sources, _ := s.store.ListSources(t.Context())
			if _, err := s.store.MergeSourceModels(t.Context(), sources[0], []storage.Model{{ID: "fake-model", Name: "fake-model", Enabled: true, ToolsCapable: false, CapabilitySource: "catalog"}}); err != nil {
				t.Fatal(err)
			}
			models, _ = s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
			if !models[0].ToolsCapable {
				t.Fatal("catalog refresh erased manual override")
			}
		})
	}
}

// 中转站在 chat SSE 帧上携带未映射扩展字段（顶层 system_fingerprint、usage 尾帧
// 的 provider_metrics）。收集路径必须保真保留而不是拒绝，探针才能完成验证。
func TestAgentToolProbeStreamsRelayExtensions(t *testing.T) {
	s := newAgentIntegrationServer(t)
	s.setupAgentRoutes(s.engine.Group("/api/admin"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		match := regexp.MustCompile(`nonce ([A-Za-z0-9]+)\.`).FindSubmatch(body)
		if len(match) != 2 {
			t.Errorf("probe nonce missing")
			w.WriteHeader(400)
			return
		}
		args, _ := json.Marshal(map[string]string{"nonce": string(match[1])})
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(payload string) { fmt.Fprintf(w, "data: %s\n\n", payload) }
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","system_fingerprint":"fp_123","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"elysia_capability_probe","arguments":""}}]},"finish_reason":null}]}`)
		chunk(fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","system_fingerprint":"fp_123","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%s}}]},"finish_reason":null}]}`, strconv.Quote(string(args))))
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","system_fingerprint":"fp_123","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		chunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"fake-model","system_fingerprint":"fp_123","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13,"provider_metrics":{"write_tokens":3}}}`)
		chunk(`[DONE]`)
	}))
	defer provider.Close()
	seedCallerModel(t, s, provider.URL, "custom:openai-chat-completions")
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	binding := bindings[0]
	// 复刻迁移生成的真实形态：两个 generate 传输、未钉选操作——
	// agentTransport 优先 SSE，探针走流式（即用户报错的路径）。
	binding.Binding.Operation = ""
	binding.Binding.Transports = []protocol.Transport{protocol.HTTPJSON, protocol.SSE}
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = false
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	tools := false
	if _, err := s.store.UpdateModel(t.Context(), "fake-model", "cs1", storage.ModelPatch{ToolsCapable: &tools}); err != nil {
		t.Fatal(err)
	}
	response := revisionAdminRequest(t, s.engine, "POST", "/api/admin/agent/models/verify-tools", []byte(`{"sourceId":"cs1","modelId":"fake-model"}`), "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	models, _ := s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
	if !models[0].ToolsCapable {
		t.Fatal("streamed probe with relay extensions did not commit capability")
	}
}
