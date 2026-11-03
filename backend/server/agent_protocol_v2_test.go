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

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

type protocolAuthorContext struct {
	draft              json.RawMessage
	edit, baseURL, key string
}

func TestAgentProtocolPackageAuthoringAndCatalog(t *testing.T) {
	s := newAgentIntegrationServer(t)
	tctx := &protocolAuthorContext{}
	document := loadGatewayPackage(t, "testdata/sessions/session-alpha")
	raw, err := protocol.EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	run := func(command string) CLIResult {
		t.Helper()
		result := s.runCLIWithOptions(t.Context(), tctx, command, true)
		if !result.OK {
			t.Fatalf("%s: %s %s", command, result.Summary, mustCLIData(t, result))
		}
		return result
	}
	run("elysia protocol schema --section package --type ProtocolPackage")
	run("elysia protocol schema --section spec --type ProtocolSpec")
	run("elysia protocol schema --section mapping --type ProtocolMapping")
	run("elysia protocol draft '" + raw.JSONString() + "'")
	run("elysia protocol preview --mode session --sample-id " + document.Mapping.SessionSamples[0].ID)
	run("elysia protocol save")
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.ReadDraft(t.Context(), document.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := service.Validate(draft.Definition.Bytes())
	if protocol.IssuesError(issues) != nil {
		t.Fatal(issues)
	}
	run("elysia protocol activate --id " + document.Spec.ID + " --hash " + compiled.Hash())
	c, response := adminProtocolContext("GET", "/api/admin/protocols/enabled", "")
	s.adminEnabledProtocols(contextForTest(c), c)
	flushTestResponse(c)
	var envelope struct {
		Data struct {
			Items []enabledProtocol `json:"items"`
		} `json:"data"`
	}
	if err := sonic.ConfigStd.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, item := range envelope.Data.Items {
		if item.ID == document.Spec.ID {
			if item.Name != document.Spec.Name || item.Revision != compiled.Hash() || !item.CanGenerate || !item.Capabilities[protocol.TextCapability] {
				t.Fatal("enabled package lost its authored catalog metadata", item)
			}
			return
		}
	}
	t.Fatal("enabled package missing from catalog")
}

func TestAgentProtocolV2SimulatedAuthorRepairAndEnable(t *testing.T) {
	t.Parallel()
	s := newAgentIntegrationServer(t)
	modelProtocol := activateGatewayPackage(t, s, agentEnvelopePackage(t))
	definition := loadGatewayPackage(t, "testdata/packages/text-alpha")
	raw, _ := sonic.ConfigStd.Marshal(definition)
	service, _ := s.protocolService()
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	broken := strings.Replace(string(raw), `"path":"/model"`, `"path":"/wrong"`, 1)
	commands := []string{
		"elysia protocol schema",
		"elysia protocol draft '" + broken + "'",
		"elysia protocol verify",
		"elysia protocol draft '" + string(raw) + "'",
		"elysia protocol verify && elysia protocol save",
		"elysia protocol activate --id " + definition.Spec.ID + " --hash " + compiled.Hash(),
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(calls.Add(1)) - 1
		response := protocol.Response{SchemaVersion: protocol.ProtocolCoreVersion, ID: protocol.StringValue(fmt.Sprintf("r%d", index)), Content: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("done")}}}
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
	if err := s.store.SaveProtocolBinding(t.Context(), makeProtocolBinding(storage.BindingKey{Kind: "source", SourceID: source.ID}, modelProtocol)); err != nil {
		t.Fatal(err)
	}
	session, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Mode: agent.ModeCreate, Settings: agent.Settings{ModelSourceID: source.ID, ModelName: "author", AllowSave: "always"}})
	if err != nil {
		t.Fatal(err)
	}
	c, rec := agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+session.ID+"/messages", session.ID, `{"content":"Create and enable the supplied text-only protocol; fix the wrong input path without changing its capability."}`)
	s.adminSendAgentMessage(contextForTest(c), c)
	flushTestResponse(c)
	if rec.Code != 200 {
		t.Fatalf("%d: %s", rec.Code, rec.Body)
	}
	if active, exists := service.Pin(definition.Spec.ID); !exists || active.Hash() != compiled.Hash() {
		t.Fatalf("Agent did not activate repaired definition: %s", rec.Body)
	}
	if calls.Load() != int32(len(commands)+1) || !strings.Contains(rec.Body.String(), "invalid_definition") || !strings.Contains(rec.Body.String(), "/mapping/directions/decode_request/program/fields/model") {
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

func (ctx *protocolAuthorContext) EditProtocolID() string { return ctx.edit }

func (*protocolAuthorContext) ReportProgress(string) {}

func (ctx *protocolAuthorContext) TestTarget() (string, string) { return ctx.baseURL, ctx.key }

func (ctx *protocolAuthorContext) SetTestTarget(url, key string) error {
	ctx.baseURL, ctx.key = url, key
	return nil
}

func TestAgentProtocolV2AuthoringAndActivation(t *testing.T) {
	t.Parallel()
	s := newAgentIntegrationServer(t)
	tctx := &protocolAuthorContext{}
	run := func(command string) CLIResult {
		t.Helper()
		result := s.runCLIWithOptions(t.Context(), tctx, command, true)
		if !result.OK {
			t.Fatalf("%s: %s %s", command, result.Summary, mustCLIData(t, result))
		}
		return result
	}
	definition := loadGatewayPackage(t, "testdata/packages/text-alpha")
	definition.Mapping.Samples[0].Context = protocol.Object{"exact": mustProtocolValue(t, `9007199254740993`)}
	raw, _ := sonic.ConfigStd.Marshal(definition)
	run("elysia protocol schema --section semantic --type Request")
	run("elysia protocol draft '" + string(raw) + "'")
	run("elysia protocol validate")
	run("elysia protocol verify")
	run("elysia protocol preview --direction decode_request --sample '" + definition.Mapping.Samples[0].Input.JSONString() + "'")
	run("elysia protocol save")
	service, _ := s.protocolService()
	draft, err := service.ReadDraft(t.Context(), definition.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := service.Pin(definition.Spec.ID); exists {
		t.Fatal("save activated revision")
	}
	compiled, _ := service.Validate(draft.Definition.Bytes())
	run("elysia protocol activate --id " + definition.Spec.ID + " --hash " + compiled.Hash())
	run("elysia protocol read --id " + definition.Spec.ID)
	oldHash, oldDraftHash := compiled.Hash(), draft.Hash
	definition.Spec.Version = "2"
	raw, _ = sonic.ConfigStd.Marshal(definition)
	run("elysia protocol draft '" + string(raw) + "'")
	run("elysia protocol save --expected " + oldDraftHash)
	draft, err = service.ReadDraft(t.Context(), definition.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	compiled, _ = service.Validate(draft.Definition.Bytes())
	run("elysia protocol diff --id " + definition.Spec.ID + " --from " + oldHash + " --to " + compiled.Hash())
	run("elysia protocol activate --id " + definition.Spec.ID + " --hash " + compiled.Hash() + " --expected " + oldHash)
	run("elysia protocol rollback --id " + definition.Spec.ID + " --hash " + oldHash + " --expected " + compiled.Hash())
	tctx.edit = "different"
	if result := s.runCLIWithOptions(t.Context(), tctx, "elysia protocol save", true); result.OK {
		t.Fatal("edit ID changed")
	}
	view := agentSessionView(&agent.Session{DraftConfig: json.RawMessage(raw)})
	if !strings.Contains(view["definitionJSON"].(string), "9007199254740993") {
		t.Fatal("draft lost exact JSON")
	}
}

func mustProtocolValue(t testing.TB, raw string) protocol.Value {
	t.Helper()
	value, err := protocol.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAgentProtocolV2DiagnosticsAndProbe(t *testing.T) {
	t.Parallel()
	s := newAgentIntegrationServer(t)
	definition := loadGatewayPackage(t, "testdata/packages/text-alpha")
	raw, _ := sonic.ConfigStd.Marshal(definition)
	tctx := &protocolAuthorContext{draft: raw}
	service, _ := s.protocolService()
	compiled, _ := service.Validate(raw)
	request, err := compiled.DecodeRequest(t.Context(), definition.Mapping.Samples[0].Input.Bytes(), protocol.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, valid := range []bool{false, true} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if valid {
				_, _ = w.Write(definition.Mapping.Samples[2].Input.Bytes())
			} else {
				_, _ = w.Write([]byte(`{"answer":"invalid"}`))
			}
		}))
		input, _ := sonic.ConfigStd.Marshal(protocolCommandInput{Operation: "generate", SampleRequest: mustEncodedProtocolValue(t, request), BaseURL: provider.URL})
		result := (&protocolV2Tool{server: s, action: "test"}).Execute(t.Context(), tctx, input)
		provider.Close()
		if result.OK != valid {
			t.Fatalf("valid=%t: %s %s", valid, result.Summary, mustCLIData(t, result))
		}
		if _, err := s.store.ReadProtocolReport(t.Context(), definition.Spec.ID, compiled.Hash()); err == nil {
			t.Fatal("upstream probe replaced offline proof")
		}
	}
	definition.Mapping.Capabilities[protocol.FunctionToolsCapability] = true
	raw, _ = sonic.ConfigStd.Marshal(definition)
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

func mustEncodedProtocolValue(t testing.TB, value any) protocol.Value {
	t.Helper()
	encoded, err := protocol.EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func agentEnvelopePackage(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	document := protocol.ProtocolPackage{
		SchemaVersion: protocol.ProtocolPackageSchemaVersion,
		Spec: protocol.ProtocolSpec{ID: "agent-envelope", Name: "agent-envelope", Version: "1", Family: "agent-envelope", WireVersion: "1",
			Documents: map[string]protocol.WireContractSpec{},
			Operations: map[string]protocol.ProtocolOperationSpec{"generate": {Kind: "generate", Method: "POST", Path: "/generate", Transport: protocol.HTTPJSON,
				Request: "request", Response: "response", Auth: protocol.Credential{Location: "none"}}}},
		Mapping: protocol.ProtocolMapping{CoreVersion: protocol.ProtocolCoreVersion, DSLVersion: protocol.ProtocolDSLVersion,
			Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: true},
			Directions:   map[protocol.Direction]protocol.ProtocolDirectionMapping{},
			Operations:   map[string]protocol.ProtocolOperationMapping{"generate": {Request: protocol.EncodeRequest, Response: protocol.DecodeResponse}}},
	}
	for _, pair := range []struct {
		name           string
		decode, encode protocol.Direction
		semantic       string
		fields         []string
	}{
		{"request", protocol.DecodeRequest, protocol.EncodeRequest,
			`{"schemaVersion":2,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]},{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"x":1}}},{"kind":"tool_result","callId":"c","payload":"ok"}],"tools":[{"kind":"function","name":"lookup","inputSchema":{"type":"object"}}]}`,
			[]string{"model", "content", "tools", "parameters", "toolChoice"}},
		{"response", protocol.DecodeResponse, protocol.EncodeResponse,
			`{"schemaVersion":2,"id":"r","content":[{"kind":"text","payload":"hi"},{"kind":"tool_call","name":"lookup","callId":"c2","input":{"kind":"json","value":{"x":2}}}]}`,
			[]string{"id", "content", "status", "usage"}},
	} {
		document.Spec.Documents[pair.name] = protocol.WireContractSpec{Schema: &protocol.ValueSchema{Type: protocol.ObjectType,
			Required: []string{"payload"}, Properties: map[string]protocol.ValueSchema{"payload": {Type: protocol.ObjectType, AllowUnknown: true}}}}
		fields := map[string]protocol.Expression{"schemaVersion": {Op: "literal", Value: mustEncodedProtocolValue(t, protocol.ProtocolCoreVersion)}}
		for _, field := range pair.fields {
			fields[field] = protocol.Expression{Op: "read", From: "input", Path: "/" + field}
		}
		document.Mapping.Directions[pair.decode] = protocol.ProtocolDirectionMapping{Document: pair.name,
			Program: protocol.Expression{Op: "read", From: "input", Path: "/payload"}}
		document.Mapping.Directions[pair.encode] = protocol.ProtocolDirectionMapping{Document: pair.name,
			Program: protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"payload": {Op: "object", Fields: fields}}}}
		for _, direction := range []protocol.Direction{pair.decode, pair.encode} {
			mapping := document.Mapping.Directions[direction]
			for _, field := range pair.fields {
				path := "/" + field
				if direction == pair.decode {
					path = "/payload" + path
				}
				mapping.Effects = append(mapping.Effects, protocol.ProtocolSemanticEffect{ID: field, Kind: "emit", Path: "/" + field, Target: "/payload/" + field,
					When: &protocol.Expression{Op: "exists", Source: &protocol.Expression{Op: "read", From: "input", Path: path}}})
			}
			document.Mapping.Directions[direction] = mapping
		}
		semantic := mustProtocolValue(t, pair.semantic)
		wire := mustEncodedProtocolValue(t, protocol.Object{"payload": semantic})
		document.Mapping.Samples = append(document.Mapping.Samples,
			protocol.Sample{ID: string(pair.decode), Direction: pair.decode, Input: wire, Expected: semantic},
			protocol.Sample{ID: string(pair.encode), Direction: pair.encode, Input: semantic, Expected: wire})
	}
	document.Mapping.Agent = &protocol.ProtocolAgentMapping{
		ToolResults: protocol.JSONInput,
		Parameters:  protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"max_output_tokens": {Op: "read", From: "input", Path: "/maxOutputTokens"}}},
		Samples:     []protocol.AgentSample{{ID: "agent-disabled", Preferences: protocol.AgentPreferences{MaxOutputTokens: 8192}, Expected: mustProtocolValue(t, `{"max_output_tokens":8192}`)}},
	}
	return document
}

// Legacy cache and conversion comparisons retain their original core fixture.
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
	definition.Agent = &protocol.AgentConfig{
		ToolResults: protocol.JSONInput,
		Parameters:  protocol.Mapping{Transform: &protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"max_output_tokens": {Op: "read", From: "input", Path: "/maxOutputTokens"}}}},
		Samples:     []protocol.AgentSample{{ID: "agent-disabled", Preferences: protocol.AgentPreferences{MaxOutputTokens: 8192}, Expected: mustProtocolValue(t, `{"max_output_tokens":8192}`)}},
	}
	return definition
}

func TestAgentUsesBoundCustomProtocolAndRejectsToolMismatch(t *testing.T) {
	t.Parallel()
	s := newAgentIntegrationServer(t)
	compiled := activateGatewayPackage(t, s, agentEnvelopePackage(t))
	var calls int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Payload protocol.Request `json:"payload"`
		}
		if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Payload.SchemaVersion != protocol.ProtocolCoreVersion || len(body.Payload.Tools) != 1 || !body.Payload.Tools[0].Name.IsString("lookup") {
			t.Errorf("missing tools: %+v", body)
		}
		fmt.Fprint(w, `{"payload":{"schemaVersion":2,"id":"r","content":[{"kind":"tool_call","name":"lookup","callId":"c","input":{"kind":"json","value":{"n":9007199254740993}}}]}}`)
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
	binding := makeProtocolBinding(storage.BindingKey{Kind: "source", SourceID: "s1"}, compiled)
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	input := agent.CallRequest{Model: "fake-model", ModelSourceID: "s1", Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hello")}}}}, Tools: []protocol.Tool{{Kind: protocol.FunctionTool, Name: protocol.StringValue("lookup"), InputSchema: mustProtocolValue(t, `{"type":"object"}`)}}}
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
