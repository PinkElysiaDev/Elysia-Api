package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

type protocolAuthorContext struct {
	draft              json.RawMessage
	edit, baseURL, key string
}

func TestAgentProtocolV2SimulatedAuthorRepairAndEnable(t *testing.T) {
	s := newAgentIntegrationServer(t)
	modelProtocol := activateGatewayDefinition(t, s, agentEnvelopeDefinition(t))
	definition := loadGatewayDefinition(t, "text-alpha")
	raw, _ := json.Marshal(definition)
	service, _ := s.protocolService()
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	broken := strings.Replace(string(raw), `"/deployment"`, `"/wrong"`, 1)
	commands := []string{
		"elysia protocol schema",
		"elysia protocol draft '" + broken + "'",
		"elysia protocol verify",
		"elysia protocol draft '" + string(raw) + "'",
		"elysia protocol verify && elysia protocol save",
		"elysia protocol activate --id " + definition.ID + " --hash " + compiled.Hash(),
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		response := protocol.Response{SchemaVersion: 1, ID: protocol.StringValue(fmt.Sprintf("r%d", index)), Content: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("done")}}}
		if index < len(commands) {
			response.Content = []protocol.Node{{Kind: protocol.ToolCallNode, Name: protocol.StringValue("elysia_cli"), CallID: protocol.StringValue(fmt.Sprintf("c%d", index)), Input: &protocol.ToolInput{Kind: protocol.JSONInput, Value: mustEncodedProtocolValue(t, map[string]string{"command": commands[index]})}}}
		}
		body, err := modelProtocol.EncodeResponse(r.Context(), &response, protocol.EvaluationContext{})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write(body)
	}))
	defer provider.Close()
	source := storage.ModelSource{ID: "author-source", Name: "author", BaseURL: provider.URL, Platform: "custom:agent-envelope", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "author", Name: "author", BaseURL: provider.URL, ToolsCapable: true, Enabled: true, Available: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveProtocolBinding(t.Context(), storage.ProtocolBinding{Kind: "source", SourceID: source.ID, Binding: protocol.Binding{ProtocolID: modelProtocol.Identity().DefinitionID, RevisionHash: modelProtocol.Hash(), Capabilities: modelProtocol.Definition().Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}); err != nil {
		t.Fatal(err)
	}
	session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Mode: agent.ModeCreate, Settings: agent.Settings{ModelSourceID: source.ID, ModelName: "author", AllowSave: "always"}})
	if err != nil {
		t.Fatal(err)
	}
	c, rec := agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+session.ID+"/messages", session.ID, `{"content":"Create and enable the supplied text-only protocol; fix the wrong input path without changing its capability."}`)
	s.adminSendAgentMessage(c)
	if rec.Code != 200 {
		t.Fatalf("%d: %s", rec.Code, rec.Body)
	}
	if active, exists := service.Pin(definition.ID); !exists || active.Hash() != compiled.Hash() {
		t.Fatalf("Agent did not activate repaired definition: %s", rec.Body)
	}
	if calls.Load() != int32(len(commands)+1) || !strings.Contains(rec.Body.String(), "verification_mismatch") {
		t.Fatalf("repair workflow missing: %s", rec.Body)
	}
	for _, action := range []string{"activate", "rollback"} {
		notes, err := probeAgentCLI("elysia protocol " + action + " --id text-alpha --hash h")
		if err != nil || len(notes) != 1 || notes[0].PermissionKey != "save" {
			t.Fatalf("%s bypassed permissions: %+v %v", action, notes, err)
		}
	}
}

func (ctx *protocolAuthorContext) Draft() json.RawMessage { return ctx.draft }
func (ctx *protocolAuthorContext) SetDraft(raw json.RawMessage) error {
	ctx.draft = append(json.RawMessage(nil), raw...)
	return nil
}
func (ctx *protocolAuthorContext) EditProtocolID() string       { return ctx.edit }
func (*protocolAuthorContext) ReportProgress(string)            {}
func (ctx *protocolAuthorContext) TestTarget() (string, string) { return ctx.baseURL, ctx.key }
func (ctx *protocolAuthorContext) SetTestTarget(url, key string) error {
	ctx.baseURL, ctx.key = url, key
	return nil
}

func TestAgentProtocolV2AuthoringAndActivation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	tctx := &protocolAuthorContext{}
	run := func(command string) CLIResult {
		t.Helper()
		result := s.runCLIWithOptions(t.Context(), tctx, command, true)
		if !result.OK {
			t.Fatalf("%s: %s %s", command, result.Summary, result.MarshalData())
		}
		return result
	}
	definition := loadGatewayDefinition(t, "text-alpha")
	definition.Extensions = protocol.Object{"exact": mustProtocolValue(t, `9007199254740993`)}
	raw, _ := json.Marshal(definition)
	run("elysia protocol schema --section semantic --type Request")
	run("elysia protocol draft '" + string(raw) + "'")
	run("elysia protocol validate")
	run("elysia protocol verify")
	run("elysia protocol preview --direction decode_request --sample '" + string(definition.Samples[0].Input.Bytes()) + "'")
	run("elysia protocol save")
	service, _ := s.protocolService()
	draft, err := service.ReadDraft(t.Context(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := service.Pin(definition.ID); exists {
		t.Fatal("save activated revision")
	}
	compiled, _ := service.Validate(draft.Definition.Bytes())
	run("elysia protocol activate --id " + definition.ID + " --hash " + compiled.Hash())
	run("elysia protocol read --id " + definition.ID)
	oldHash, oldDraftHash := compiled.Hash(), draft.Hash
	definition.Version = "2"
	raw, _ = json.Marshal(definition)
	run("elysia protocol draft '" + string(raw) + "'")
	run("elysia protocol save --expected " + oldDraftHash)
	draft, err = service.ReadDraft(t.Context(), definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	compiled, _ = service.Validate(draft.Definition.Bytes())
	run("elysia protocol diff --id " + definition.ID + " --from " + oldHash + " --to " + compiled.Hash())
	run("elysia protocol activate --id " + definition.ID + " --hash " + compiled.Hash() + " --expected " + oldHash)
	run("elysia protocol rollback --id " + definition.ID + " --hash " + oldHash + " --expected " + compiled.Hash())
	tctx.edit = "different"
	if result := s.runCLIWithOptions(t.Context(), tctx, "elysia protocol save", true); result.OK {
		t.Fatal("edit ID changed")
	}
	view := agentSessionView(&agent.Session{DraftConfig: json.RawMessage(raw)})
	if !strings.Contains(view["definitionJSON"].(string), "9007199254740993") {
		t.Fatal("draft lost exact JSON")
	}
}

func mustProtocolValue(t *testing.T, raw string) protocol.Value {
	t.Helper()
	value, err := protocol.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAgentProtocolV2DiagnosticsAndProbe(t *testing.T) {
	s := newAgentIntegrationServer(t)
	definition := loadGatewayDefinition(t, "text-alpha")
	raw, _ := json.Marshal(definition)
	tctx := &protocolAuthorContext{draft: raw}
	service, _ := s.protocolService()
	compiled, _ := service.Validate(raw)
	request, err := compiled.DecodeRequest(t.Context(), definition.Samples[0].Input.Bytes(), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if valid {
				_, _ = w.Write(definition.Samples[2].Input.Bytes())
			} else {
				_, _ = w.Write([]byte(`{"answer":"invalid"}`))
			}
		}))
		input, _ := json.Marshal(protocolCommandInput{Operation: "generate", SampleRequest: mustEncodedProtocolValue(t, request), BaseURL: provider.URL})
		result := (&protocolV2Tool{server: s, action: "test"}).Execute(t.Context(), tctx, input)
		provider.Close()
		if result.OK != valid {
			t.Fatalf("valid=%t: %s %s", valid, result.Summary, result.MarshalData())
		}
		if _, err := s.store.ReadProtocolReport(t.Context(), definition.ID, compiled.Hash()); err == nil {
			t.Fatal("upstream probe replaced offline proof")
		}
	}
	definition.Capabilities[protocol.FunctionToolsCapability] = true
	raw, _ = json.Marshal(definition)
	tctx.draft = raw
	result := (&protocolV2Tool{server: s, action: "verify"}).Execute(t.Context(), tctx, nil)
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	want := protocol.Verify(t.Context(), compiled)
	got := result.Data.(protocol.VerificationReport)
	if result.OK || len(got.Issues) != len(want.Issues) || got.Issues[0].Code != want.Issues[0].Code {
		t.Fatal("CLI and editor diagnostics differ")
	}
}

func mustEncodedProtocolValue(t *testing.T, value any) protocol.Value {
	t.Helper()
	encoded, err := protocol.EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// This protocol uses independent envelope mappings, with no built-in shape.
func agentEnvelopeDefinition(t *testing.T) protocol.Definition {
	t.Helper()
	request := mustProtocolValue(t, `{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]},{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"x":1}}},{"kind":"tool_result","callId":"c","payload":"ok"}],"tools":[{"kind":"function","name":"lookup","inputSchema":{"type":"object"}}]}`)
	response := mustProtocolValue(t, `{"schemaVersion":1,"id":"r","content":[{"kind":"text","payload":"hi"},{"kind":"tool_call","name":"lookup","callId":"c2","input":{"kind":"json","value":{"x":2}}}]}`)
	definition := protocol.Definition{SchemaVersion: 2, ID: "agent-envelope", Name: "agent-envelope", Version: "1", Family: "agent-envelope", WireVersion: "1", Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: true}, Directions: map[protocol.Direction]protocol.Mapping{}, Operations: map[string]protocol.Operation{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}}}}
	for _, pair := range []struct {
		decode, encode protocol.Direction
		value          protocol.Value
		fields         []string
	}{
		{protocol.DecodeRequest, protocol.EncodeRequest, request, []string{"model", "content", "tools", "parameters", "toolChoice"}},
		{protocol.DecodeResponse, protocol.EncodeResponse, response, []string{"id", "content", "status", "usage"}},
	} {
		fields := map[string]protocol.Expression{"schemaVersion": {Op: "literal", Value: mustProtocolValue(t, "1")}}
		object, _ := pair.value.ReadObject()
		for _, field := range pair.fields {
			fields[field] = protocol.Expression{Op: "read", From: "input", Path: "/" + field}
		}
		wire := mustEncodedProtocolValue(t, map[string]any{"payload": object})
		definition.Directions[pair.decode] = protocol.Mapping{Transform: &protocol.Expression{Op: "read", From: "input", Path: "/payload"}}
		definition.Directions[pair.encode] = protocol.Mapping{Transform: &protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"payload": {Op: "object", Fields: fields}}}}
		definition.Samples = append(definition.Samples, protocol.Sample{ID: string(pair.decode), Direction: pair.decode, Input: wire, Expected: pair.value}, protocol.Sample{ID: string(pair.encode), Direction: pair.encode, Input: pair.value, Expected: wire})
	}
	return definition
}

func TestAgentUsesBoundCustomProtocolAndRejectsToolMismatch(t *testing.T) {
	s := newAgentIntegrationServer(t)
	compiled := activateGatewayDefinition(t, s, agentEnvelopeDefinition(t))
	var calls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Payload protocol.Request `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Payload.Tools) != 1 || string(body.Payload.Tools[0].Name.Bytes()) != `"lookup"` {
			t.Errorf("missing tools: %+v", body)
		}
		fmt.Fprint(w, `{"payload":{"schemaVersion":1,"id":"r","content":[{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"n":9007199254740993}}}]}}`)
	}))
	defer provider.Close()
	seedAgentModel(t, s, provider.URL)
	sources, err := s.store.ListSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(t.Context(), sources[0], []storage.Model{{ID: "fake-model", Name: "fake-model", BaseURL: provider.URL, ToolsCapable: true, Enabled: true, Available: true}}); err != nil {
		t.Fatal(err)
	}
	binding := storage.ProtocolBinding{Kind: "source", SourceID: "s1", Binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Definition().Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	input := agent.CallRequest{Model: "fake-model", ModelSourceID: "s1", Messages: []relay.MaheshvaraMessage{{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: "text", Text: "hello"}}}}, Tools: []relay.MaheshvaraTool{{Type: "function", Name: "lookup", Parameters: map[string]any{"type": "object"}}}}
	result, err := newAgentStreamCaller(s).Call(context.Background(), input, agent.StreamCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || string(result.ToolCalls[0].Arguments) != `{"n":9007199254740993}` || calls != 1 {
		t.Fatalf("result: %+v", result)
	}
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = false
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	if _, err := newAgentStreamCaller(s).Call(t.Context(), input, agent.StreamCallbacks{}); err == nil || calls != 1 {
		t.Fatal("tool mismatch sent upstream")
	}
}
