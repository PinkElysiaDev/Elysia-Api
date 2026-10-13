package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

func chatStartupFrames(t *testing.T, sample protocol.Sample, firstTime int) []protocol.Value {
	t.Helper()
	var frames []protocol.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	canonicalTime := 1
	if firstTime == 81 {
		firstTime, canonicalTime = 1, 81
		for index, frame := range frames {
			value, _ := protocol.EncodeValue(canonicalTime)
			updated, err := protocol.ApplyMutations(frame, []protocol.Mutation{{Op: protocol.ReplaceValue, Path: "/created", Value: value}}, protocol.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			frames[index] = updated
		}
	}
	first, err := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"synthetic-prelude","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null,"logprobs":null}],"usage":null,"system_fingerprint":null}`, firstTime)))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := protocol.ParseValue([]byte(fmt.Sprintf(`{"id":"r","model":"m","created":%d,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"service_tier":"auto"}`, canonicalTime)))
	return append([]protocol.Value{first, second}, frames...)
}

func TestGatewayChatStartupIdentityMatrix(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	onErrorOnly := false
	bodyMaxKB := 1024
	s.config.SetUsageLogConfig(config.UsageLogConfig{BodyOnErrorOnly: &onErrorOnly, BodyMaxKB: &bodyMaxKB})
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	var active atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range active.Load().([]protocol.Value) {
			fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	source := storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: provider.URL, Platform: "custom:" + upstream.Identity().DefinitionID, Enabled: true}
	if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "upstream-model", Name: "upstream-model", Available: true, Enabled: true, ToolsCapable: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	for _, sample := range upstream.Definition().Samples {
		if sample.ID != "stream-text-decode" && sample.ID != "stream-reasoning-decode" && sample.ID != "stream-tool-decode" {
			continue
		}
		for _, firstTime := range []int{1, 2, 81} {
			frames := chatStartupFrames(t, sample, firstTime)
			active.Store(frames)
			for _, ingress := range []struct{ id, path, body string }{
				{protocol.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`},
				{protocol.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":true}`},
				{protocol.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":true}`},
				{protocol.PresetGeminiID, "/v1beta/models/group:streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
			} {
				t.Run(fmt.Sprintf("%s/%d/%s", sample.ID, firstTime, ingress.id), func(t *testing.T) {
					body := ingress.body
					if sample.ID == "stream-tool-decode" {
						tools := map[string]string{
							protocol.PresetChatCompletionsID: `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`,
							protocol.PresetResponsesID:       `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`,
							protocol.PresetAnthropicID:       `[{"name":"lookup","input_schema":{"type":"object"}}]`,
							protocol.PresetGeminiID:          `[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]`,
						}
						body = strings.TrimSuffix(body, "}") + `,"tools":` + tools[ingress.id] + "}"
					}
					request := httptest.NewRequest("POST", "/gateway/"+ingress.id+ingress.path, strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer gateway-test-token")
					out := httptest.NewRecorder()
					before := calls.Load()
					s.engine.ServeHTTP(out, request)
					if out.Code != 200 || out.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || calls.Load() != before+1 || strings.Contains(out.Body.String(), "synthetic-prelude") {
						t.Fatal(out.Code, out.Body.String(), out.Result().Trailer)
					}
					if !strings.Contains(out.Body.String(), "hi") && !strings.Contains(out.Body.String(), "lookup") {
						t.Fatal("lost response content", out.Body)
					}
					target, _ := service.Pin(ingress.id)
					validator, err := target.NewWireStreamValidation(protocol.Scope{})
					if err != nil {
						t.Fatal(err)
					}
					for _, line := range strings.Split(out.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						value, err := protocol.ParseValue([]byte(strings.TrimPrefix(line, "data: ")))
						if err == nil {
							err = validator.Consume(t.Context(), value)
						}
						if err != nil {
							t.Fatal(err, line)
						}
					}
					if err := validator.Finish(); err != nil {
						t.Fatal(err)
					}
					records := latestUsageRecords(t, s)
					raw, _, err := s.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
					if err != nil {
						t.Fatal(err)
					}
					var saved usageRecord
					if err := json.Unmarshal(raw, &saved); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(saved.ProviderResponse.Content, "synthetic-prelude") || saved.RetryCount != 0 || saved.StatusCode != 200 {
						t.Fatal("raw evidence or status changed", saved.StatusCode, saved.ProviderResponse.Content)
					}
					issues := 0
					for _, issue := range saved.ConversionIssues {
						if issue.RuleID == "chat-stream-identity" {
							issues++
							if issue.PolicyHash != saved.ConversionPolicyHash || issue.Direction != protocol.DecodeEvent || issue.Protocol.DefinitionID != upstream.Identity().DefinitionID {
								t.Fatal(issue)
							}
						}
					}
					want := 1
					if firstTime != 1 {
						want++
					}
					if issues != want {
						t.Fatal(saved.ConversionIssues)
					}
					if firstTime == 1 && sample.ID == "stream-text-decode" {
						sequence, _ := protocol.EncodeValue(frames)
						route := protocol.ConversionContext{Source: upstream.Identity(), Target: target.Identity(), Transport: protocol.SSE}
						preview := service.PreviewWorkflow(t.Context(), protocol.PreviewInput{Mode: "conversion", ConversionPhase: protocol.ConversionSourceEvent, ConversionContext: route, Sequence: true, Input: sequence})
						if err := protocol.IssuesError(preview.Issues); err != nil || preview.Output.IsZero() {
							t.Fatal(preview, err)
						}
						if len(preview.Issues) != 1 || preview.Issues[0].RuleID != "chat-stream-identity" {
							t.Fatal(preview.Issues)
						}
					}
				})
			}
		}
	}
	// Composition must normalize before decoding; standalone codec verification
	// still rejects this deliberately invalid provider fixture.
	definition := upstream.Definition()
	for i := range definition.Samples {
		if definition.Samples[i].ID == "stream-text-decode" {
			definition.Samples[i].Input, _ = protocol.EncodeValue(chatStartupFrames(t, definition.Samples[i], 81))
		}
	}
	compiler, _ := protocol.NewCompiler(protocol.DefaultLimits(), builtin.Modules(), nil)
	raw, _ := protocol.EncodeValue(definition)
	fixture, issues := compiler.Compile(raw.Bytes())
	if fixture == nil {
		t.Fatal(issues)
	}
	for _, id := range []string{protocol.PresetChatCompletionsID, protocol.PresetResponsesID, protocol.PresetAnthropicID, protocol.PresetGeminiID} {
		target, _ := service.Pin(id)
		report := protocol.VerifyCombination(t.Context(), target, fixture)
		found := false
		for _, check := range report.Checks {
			if check.SampleID == "stream-text-decode" {
				found = true
				if !check.Passed {
					t.Fatal(id, report.Issues)
				}
			}
		}
		if !found {
			t.Fatal("missing composition check")
		}
	}
	if report := protocol.Verify(t.Context(), fixture); report.Passed {
		t.Fatal("invalid fixture passed standalone wire verification")
	}
}
