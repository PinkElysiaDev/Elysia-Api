package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

func turnStateFrames(t *testing.T, sample p.Sample) []p.Value {
	t.Helper()
	var frames []p.Value
	if err := sample.Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	control := mustProtocolValue(t, `{"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic-private-state"}}`)
	frames = append([]p.Value{control}, frames...)
	return append(frames[:2], append([]p.Value{control}, frames[2:]...)...)
}

func TestGatewayResponsesTurnStateFullStreamsAndCollector(t *testing.T) {
	t.Parallel()
	s, _ := newProtocolAdminTestServer(t)
	onErrorOnly, bodyMaxKB := false, 1024
	s.config.SetUsageLogConfig(config.UsageLogConfig{BodyOnErrorOnly: &onErrorOnly, BodyMaxKB: &bodyMaxKB})
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(p.PresetResponsesID)
	var active atomic.Value
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range active.Load().([]p.Value) {
			fmt.Fprintf(w, "data: %s\n\n", frame.Bytes())
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
		if sample.ID != "stream-text-decode" && sample.ID != "stream-tool-decode" && sample.ID != "signed-stream-reasoning-decode" {
			continue
		}
		active.Store(turnStateFrames(t, sample))
		for _, ingress := range []struct{ id, path, body, tools string }{
			{p.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`, `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`},
			{p.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":true}`, `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`},
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
				if out.Code != 200 || out.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || calls.Load() != before+1 {
					t.Fatal(out.Code, out.Body.String(), out.Result().Trailer)
				}
				if strings.Contains(out.Body.String(), "synthetic-private-state") != (ingress.id == p.PresetResponsesID) {
					t.Fatal("incorrect control delivery")
				}
				target, _ := service.Pin(ingress.id)
				validator, _ := target.NewWireStreamValidation(p.Scope{})
				for _, line := range strings.Split(out.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					value, err := p.ParseValue([]byte(strings.TrimPrefix(line, "data: ")))
					if err == nil {
						err = validator.Consume(t.Context(), value)
					}
					if err != nil {
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
				if strings.Contains(string(raw), "synthetic-private-state") {
					t.Fatal("state persisted in call log")
				}
				var saved usageRecord
				if err := sonic.ConfigStd.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.StatusCode != 200 || saved.RetryCount != 0 {
					t.Fatal(saved.StatusCode, saved.RetryCount)
				}
				stateIssues := 0
				for _, issue := range saved.ConversionIssues {
					if strings.Contains(issue.Reason, "continuation stream collection unavailable") {
						t.Fatal("control disabled continuation collector", issue)
					}
					if issue.RuleID == "responses-turn-state" {
						stateIssues++
						if issue.PolicyHash != saved.ConversionPolicyHash || issue.Path != p.ResponsesTurnStatePath {
							t.Fatal(issue)
						}
					}
				}
				if (ingress.id == p.PresetResponsesID && stateIssues != 0) || (ingress.id != p.PresetResponsesID && stateIssues != 2) {
					t.Fatal("missing frame diagnostics", saved.ConversionIssues)
				}
				frame, err := upstream.DecodeFrame(t.Context(), active.Load().([]p.Value)[0], p.EvaluationContext{State: p.NewEvaluationState()})
				if err != nil {
					t.Fatal(err)
				}
				semantic, _ := p.EncodeValue(frame.Events[0])
				preview := service.PreviewWorkflow(t.Context(), p.PreviewInput{Mode: "conversion", ConversionPhase: p.ConversionEvent, ConversionContext: p.ConversionContext{Source: upstream.Identity(), Target: target.Identity(), Transport: p.SSE}, Input: semantic})
				if err := p.IssuesError(preview.Issues); err != nil || preview.Output.IsZero() {
					t.Fatal(preview, err)
				}
				if ingress.id != p.PresetResponsesID && (len(preview.Issues) != 1 || preview.Issues[0].RuleID != "responses-turn-state") {
					t.Fatal(preview.Issues)
				}
			})
		}
		var operation p.Operation
		var operationName string
		for name, op := range upstream.Operations() {
			if op.Kind == "generate" && op.Transport == p.SSE {
				operation, operationName = op, name
			}
		}
		candidate := gatewayCandidate{compiled: upstream, operation: operation, operationName: operationName, model: config.ModelRef{ID: "m", Name: "m", BaseURL: provider.URL}, binding: p.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: []p.Transport{p.SSE}}}
		request, err := upstream.DecodeRequest(t.Context(), []byte(`{"model":"m","input":"hi","store":false,"stream":true}`), p.EvaluationContext{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.collectProtocolGeneration(t.Context(), candidate, request, nil, nil)
		if err != nil {
			t.Fatal(sample.ID, err)
		}
		raw, _ := p.EncodeValue(result)
		if strings.Contains(string(raw.Bytes()), "synthetic-private-state") {
			t.Fatal("state entered Agent result")
		}
	}
	definition := upstream.Definition()
	for i := range definition.Samples {
		if definition.Samples[i].ID == "stream-text-decode" {
			definition.Samples[i].Input, _ = p.EncodeValue(turnStateFrames(t, definition.Samples[i]))
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
			t.Fatal("missing composition evidence")
		}
	}
}
