package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/agent"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

const gatewayMetadataPayload = `{"openai_chatgpt_moderation_metadata":{"generation":{},"prompt":{"omnimod":{"outputs":[{"protection_type":"fixture"}]}}},"fixture":"auxiliary-metadata-fixture"}`

func insertResponsesMetadata(t *testing.T, input []p.Value) []p.Value {
	t.Helper()
	frames := append([]p.Value(nil), input...)
	first, _ := frames[0].ReadObject()
	response, _ := first["response"].ReadObject()
	for i, frame := range frames {
		fields, _ := frame.ReadObject()
		fields["sequence_number"], _ = p.EncodeValue(i)
		frames[i], _ = p.EncodeValue(fields)
	}
	last := len(frames) - 1
	metadata := mustProtocolValue(t, fmt.Sprintf(`{"type":"response.metadata","metadata":%s,"response_id":%s,"sequence_number":%d}`, gatewayMetadataPayload, response["id"].JSONString(), last))
	terminal, _ := frames[last].ReadObject()
	terminal["sequence_number"], _ = p.EncodeValue(last + 1)
	frames[last], _ = p.EncodeValue(terminal)
	return append(frames[:last], metadata, frames[last])
}

func TestGatewayResponsesMetadataFullStreamsAndPreview(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(p.PresetResponsesID)
	var active atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range active.Load().([]p.Value) {
			fmt.Fprintf(w, "data: %s\n\n", frame.JSONString())
		}
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
		if sample.ID != "stream-text-decode" && sample.ID != "stream-tool-decode" {
			continue
		}
		var input []p.Value
		if err := sample.Input.Decode(&input); err != nil {
			t.Fatal(err)
		}
		frames := insertResponsesMetadata(t, input)
		active.Store(frames)
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		var metadata p.Event
		for _, frame := range frames[:len(frames)-1] {
			decoded, err := upstream.DecodeFrame(t.Context(), frame, options)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range decoded.Events {
				if event.Type == p.NativeEvent {
					metadata = event
				}
			}
		}
		for _, ingress := range []struct{ id, path, body, tools string }{
			{p.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":true}`, `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`},
			{p.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":true}`, `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`},
			{p.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":true}`, `[{"name":"lookup","input_schema":{"type":"object"}}]`},
			{p.PresetGeminiID, "/v1beta/models/group:streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, `[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]`},
		} {
			t.Run(sample.ID+"/"+ingress.id, func(t *testing.T) {
				body := ingress.body
				if sample.ID == "stream-tool-decode" {
					body = strings.TrimSuffix(body, "}") + `,"tools":` + ingress.tools + "}"
				}
				request := httptest.NewRequest("POST", "/gateway/"+ingress.id+ingress.path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gateway-test-token")
				out, before := httptest.NewRecorder(), calls.Load()
				serveTestHTTP(s.engine, out, request)
				if out.Code != http.StatusOK || out.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || calls.Load() != before+1 {
					t.Fatal(out.Code, out.Body.String(), out.Result().Trailer)
				}
				if strings.Contains(out.Body.String(), "auxiliary-metadata-fixture") != (ingress.id == p.PresetResponsesID) {
					t.Fatal("incorrect auxiliary metadata delivery")
				}
				target, _ := service.Pin(ingress.id)
				validator, _ := target.NewWireStreamValidation(p.Scope{})
				for _, line := range strings.Split(out.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if !strings.HasPrefix(data, "{") {
						continue
					}
					if err := validator.Consume(t.Context(), mustProtocolValue(t, data)); err != nil {
						t.Fatal(err)
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
				if err := sonic.ConfigStd.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.StatusCode != 200 || saved.RetryCount != 0 {
					t.Fatal(saved.StatusCode, saved.RetryCount)
				}
				omissions := 0
				for _, issue := range saved.ConversionIssues {
					if strings.Contains(issue.Reason, "continuation stream collection unavailable") {
						t.Fatal("metadata disabled continuation collection")
					}
					if issue.RuleID == "event-metadata" && issue.Path == "/metadata" {
						omissions++
						if issue.PolicyHash != saved.ConversionPolicyHash || issue.Fidelity != "lossy_compatible" {
							t.Fatal(issue)
						}
					}
				}
				if (ingress.id == p.PresetResponsesID && omissions != 0) || (ingress.id != p.PresetResponsesID && omissions != 1) {
					t.Fatal("missing or repeated omission diagnostic", saved.ConversionIssues)
				}
				semantic, _ := p.EncodeValue(metadata)
				preview := service.PreviewWorkflow(t.Context(), p.PreviewInput{Mode: "conversion", ConversionPhase: p.ConversionEvent, ConversionContext: p.ConversionContext{Source: upstream.Identity(), Target: target.Identity(), Transport: p.SSE}, Input: semantic})
				if err := p.IssuesError(preview.Issues); err != nil || preview.Output.IsZero() {
					t.Fatal(preview, err)
				}
				if ingress.id != p.PresetResponsesID && (len(preview.Issues) != 1 || preview.Issues[0].RuleID != "event-metadata") {
					t.Fatal(preview.Issues)
				}
			})
		}
	}
	definition := upstream.Definition()
	for i := range definition.Samples {
		if definition.Samples[i].ID == "stream-text-decode" {
			var input []p.Value
			_ = definition.Samples[i].Input.Decode(&input)
			definition.Samples[i].Input, _ = p.EncodeValue(insertResponsesMetadata(t, input))
		}
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), builtin.Modules(), nil)
	raw, _ := p.EncodeValue(definition)
	fixture, issues := compiler.Compile(raw.Bytes())
	if fixture == nil {
		t.Fatal(issues)
	}
	for _, id := range []string{p.PresetResponsesID, p.PresetChatCompletionsID, p.PresetAnthropicID, p.PresetGeminiID} {
		target, _ := service.Pin(id)
		report := p.VerifyCombination(t.Context(), target, fixture)
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
			t.Fatal("missing metadata combination evidence")
		}
	}
}

func TestAgentCallerResponsesMetadataToolContinuation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	const reasoning = `{"type":"reasoning","id":"thought","status":"completed","summary":[],"encrypted_content":"synthetic-agent-encrypted-state"}`
	const tool = `{"type":"function_call","id":"tool","call_id":"call-agent","name":"elysia_cli","arguments":"{\"command\":\"elysia group ls\"}","status":"completed"}`
	complete := `{"id":"r","object":"response","created_at":1,"model":"fake-model","status":"completed","output":[` + reasoning + `,` + tool + `],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`
	var initial []p.Value
	for _, frame := range []string{
		`{"type":"response.created","response":{"id":"r","object":"response","created_at":1,"model":"fake-model","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"thought","status":"in_progress","summary":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + reasoning + `}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"tool","call_id":"call-agent","name":"elysia_cli","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"tool","delta":"{\"command\":\"elysia group ls\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":` + tool + `}`,
		`{"type":"response.completed","response":` + complete + `}`,
	} {
		initial = append(initial, mustProtocolValue(t, frame))
	}
	initial = insertResponsesMetadata(t, initial)
	var followup []p.Value
	for _, fixture := range cacheWireFixtures() {
		if fixture.platform == "responses" {
			for _, line := range strings.Split(fixture.stream, "\n") {
				if strings.HasPrefix(line, "data: {") {
					followup = append(followup, mustProtocolValue(t, strings.TrimPrefix(line, "data: ")))
				}
			}
		}
	}
	followup = insertResponsesMetadata(t, followup)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, body string, call int) {
		if call > 1 {
			for _, want := range []string{`"encrypted_content":"synthetic-agent-encrypted-state"`, `"type":"function_call"`, `"type":"function_call_output"`, `"call_id":"call-agent"`} {
				if !strings.Contains(body, want) {
					t.Error("missing continuation history", want)
				}
			}
		}
		if strings.Contains(body, "auxiliary-metadata-fixture") || strings.Contains(body, "openai_chatgpt_moderation_metadata") {
			t.Error("metadata entered Agent continuation request")
		}
		frames := initial
		if call > 1 {
			frames = followup
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			_, _ = io.WriteString(w, "data: "+frame.JSONString()+"\n\n")
		}
	})
	seedCallerModel(t, s, upstream.URL, "responses")
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		binding.Binding.Transports = []p.Transport{p.SSE}
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Mode: "create", Settings: agent.Settings{ModelSourceID: "cs1", ModelName: "fake-model", ThinkingEnabled: true, ThinkingEffort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agent.NewRegistry(&elysiaCLITool{server: s})
	if err != nil {
		t.Fatal(err)
	}
	engine := agent.NewEngine(newAgentStreamCaller(s), s.store, registry, newAgentUserContentRenderer(s), nil, agent.Options{})
	events, err := engine.RunTurn(t.Context(), session.ID, &agent.UserContent{Text: "inspect groups"})
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Type == agent.EventError {
			t.Fatal(event.Text)
		}
	}
	if upstream.requestCount() != 2 {
		t.Fatal("tool round did not complete once", upstream.requestCount())
	}
}
