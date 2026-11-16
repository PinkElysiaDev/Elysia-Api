package protocol

import (
	"strings"
	"testing"
)

func sessionVerificationDefinition(t *testing.T) ProtocolPackage {
	t.Helper()
	definition := textVerificationDefinition(t, "session-protocol")
	definition.Mapping.Native.Preserve = true
	for direction, mapping := range definition.Mapping.Directions {
		mapping.Capabilities = CapabilitySet{TextCapability: true}
		definition.Mapping.Directions[direction] = mapping
	}
	definition.Mapping.Capabilities = CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true, UsageCapability: true}
	decode := Expression{Op: "object", Fields: map[string]Expression{"schemaVersion": literalExpression(ProtocolCoreVersion)}}
	encode := Expression{Op: "object", Fields: map[string]Expression{}}
	for wire, semantic := range map[string]string{"event": "type", "session": "sessionId", "response": "responseId", "itemKey": "itemId", "part": "item", "settings": "request", "counts": "usage", "failure": "error"} {
		decode.Fields[semantic] = Expression{Op: "read", Path: "/" + wire}
		encode.Fields[wire] = Expression{Op: "read", Path: "/" + semantic}
	}
	client := CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true}
	server := CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true, UsageCapability: true}
	definition.Mapping.Directions[DecodeClientEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeClientEvent), Document: testDocument(DecodeClientEvent), Program: decode, Capabilities: client}
	definition.Mapping.Directions[EncodeUpstreamEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeUpstreamEvent), Document: testDocument(EncodeUpstreamEvent), Program: encode, Capabilities: client}
	definition.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeEvent), Document: testDocument(DecodeEvent), Program: decode, Capabilities: server}
	definition.Mapping.Directions[EncodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeEvent), Document: testDocument(EncodeEvent), Program: encode, Capabilities: server}
	definition.Spec.Documents["session-frame"] = WireContractSpec{Schema: &ValueSchema{Type: AnyType}}
	for _, direction := range []Direction{DecodeClientEvent, EncodeUpstreamEvent, DecodeEvent, EncodeEvent} {
		mapping := definition.Mapping.Directions[direction]
		mapping.Document = "session-frame"
		definition.Mapping.Directions[direction] = mapping
	}
	definition.Mapping.Operations["session"] = ProtocolOperationMapping{Request: EncodeRequest}
	config := DefaultSessionConfig()
	definition.Spec.Operations["session"] = ProtocolOperationSpec{Request: "test", Kind: "session", Method: "GET", Path: "/session", Transport: WebSocket, Auth: Credential{Location: "none"}, Session: &ProtocolSessionSpec{SessionConfig: config, ClientEvent: "session-frame", ServerEvent: "session-frame"}}
	start := sessionEvent(SessionStarted, "")
	start.SessionID = StringValue("s")
	configure := sessionEvent(SessionConfigure, "")
	configure.Request = &Request{SchemaVersion: SemanticSchemaVersion, Model: StringValue("m"), Tools: []Tool{{Kind: FunctionTool, Name: StringValue("count")}}}
	input := sessionEvent(InputAppend, "")
	input.Item = &Node{Kind: TextNode, Payload: StringValue("please count")}
	tool := sessionEvent(ItemStarted, "r")
	tool.ItemID = StringValue("i")
	tool.Item = &Node{Kind: ToolCallNode, Name: StringValue("count"), CallID: StringValue("c"), Input: &ToolInput{Kind: JSONInput, Value: fixtureValue(t, map[string]int{"n": 2})}}
	toolEnd := tool
	toolEnd.Type = ItemFinished
	result := sessionEvent(ToolResultSubmitted, "")
	result.Item = &Node{Kind: ToolResultNode, CallID: StringValue("c"), Payload: StringValue("2")}
	text := sessionEvent(ItemStarted, "r2")
	text.ItemID = StringValue("t")
	text.Item = &Node{Kind: TextNode, Payload: StringValue("two")}
	usage := sessionEvent(UsageUpdated, "r2")
	usage.Usage = &Usage{CacheRead: &Counter{Count: 40, Origin: ObservedCount}, CacheCreation: &Counter{Origin: ObservedCount}}
	trace := []struct {
		origin EventOrigin
		event  Event
	}{
		{UpstreamEvent, start}, {ClientEvent, configure}, {ClientEvent, input}, {ClientEvent, sessionEvent(InputCommit, "")}, {ClientEvent, sessionEvent(ResponseCreate, "")},
		{UpstreamEvent, sessionEvent(ResponseStarted, "r")}, {UpstreamEvent, tool}, {UpstreamEvent, toolEnd}, {UpstreamEvent, sessionEvent(ResponseFinished, "r")},
		{ClientEvent, result}, {ClientEvent, sessionEvent(ResponseCreate, "")}, {UpstreamEvent, sessionEvent(ResponseStarted, "r2")}, {UpstreamEvent, text}, {UpstreamEvent, sessionEvent(ResponseFinished, "r2")}, {UpstreamEvent, usage}, {UpstreamEvent, sessionEvent(SessionClose, "")},
	}
	for _, isDecoder := range []bool{true, false} {
		sample := SessionSample{ID: "decode-session", Operation: "session", Model: StringValue("m")}
		if !isDecoder {
			sample.ID = "encode-session"
		}
		for _, entry := range trace {
			semantic := fixtureValue(t, entry.event)
			object, _ := semantic.ReadObject()
			wire := Object{}
			for wireKey, semanticKey := range map[string]string{"event": "type", "session": "sessionId", "response": "responseId", "itemKey": "itemId", "part": "item", "settings": "request", "counts": "usage", "failure": "error"} {
				if value, exists := object[semanticKey]; exists {
					wire[wireKey] = value
				}
			}
			direction := DecodeEvent
			if entry.origin == ClientEvent {
				direction = DecodeClientEvent
			}
			step := SessionStep{Direction: direction, Input: fixtureValue(t, wire), Expected: fixtureValue(t, []Event{entry.event})}
			if !isDecoder {
				step.Direction = eventEncoder(direction)
				step.Input = semantic
				step.Expected = fixtureValue(t, wire)
			}
			sample.Steps = append(sample.Steps, step)
		}
		definition.Mapping.SessionSamples = append(definition.Mapping.SessionSamples, sample)
	}
	return definition
}

func compileSessionDefinition(t *testing.T, definition ProtocolPackage) *Compiled {
	t.Helper()
	compiler, err := NewCompiler(DefaultLimits(), []string{"transport.websocket"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(fixtureValue(t, definition).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestVerifyInterleavedSessionDirections(t *testing.T) {
	compiled := compileSessionDefinition(t, sessionVerificationDefinition(t))
	report := Verify(t.Context(), compiled)
	if !report.Passed {
		t.Fatalf("complete session failed: %+v", report.Issues)
	}
	if err := IssuesError(CanActivate(compiled, report)); err != nil {
		t.Fatal(err)
	}
}

func TestCompoundSessionFramesShareOfflineAndLivePreservation(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	decode := readExpression("input", "/events")
	encode := objectExpression(map[string]Expression{"events": {Op: "array", Items: []Expression{readExpression("input", "")}}})
	for _, direction := range []Direction{DecodeClientEvent, DecodeEvent} {
		mapping := definition.Mapping.Directions[direction]
		mapping.Program = decode
		mapping.IsEventBatch = true
		definition.Mapping.Directions[direction] = mapping
	}
	for _, direction := range []Direction{EncodeUpstreamEvent, EncodeEvent} {
		mapping := definition.Mapping.Directions[direction]
		mapping.Program = encode
		definition.Mapping.Directions[direction] = mapping
	}
	sample := definition.Mapping.SessionSamples[0]
	sample.Scope = Scope{Model: "m"}
	var steps []SessionStep
	for _, step := range sample.Steps {
		var events []Event
		if err := step.Expected.Decode(&events); err != nil {
			t.Fatal(err)
		}
		if len(steps) > 0 && steps[len(steps)-1].Direction == step.Direction {
			previous := &steps[len(steps)-1]
			var before []Event
			if err := previous.Expected.Decode(&before); err != nil {
				t.Fatal(err)
			}
			events = append(before, events...)
			steps = steps[:len(steps)-1]
		}
		step.Input = fixtureValue(t, map[string]any{"events": events, "vendor": map[string]any{"zero": 0, "flag": false}})
		step.Expected = fixtureValue(t, events)
		steps = append(steps, step)
	}
	sample.Steps = steps
	definition.Mapping.SessionSamples = []SessionSample{sample}
	compiled := compileSessionDefinition(t, definition)
	evidence, err := executeSessionSample(t.Context(), compiled, sample)
	if err != nil {
		t.Fatal(err)
	}
	operation := compiled.Operations()["session"]
	if err := replaySessionCombination(t.Context(), compiled, operation, sample, evidence); err != nil {
		t.Fatal("offline compound replay", err)
	}
	binding := Binding{ProtocolID: definition.Spec.ID, RevisionHash: compiled.Hash(), Capabilities: definition.Mapping.Capabilities, Transports: []Transport{WebSocket}}
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, sessionTestContext(t, sample.Scope), sample.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range sample.Steps {
		frames, err := adapter.Convert(t.Context(), eventOrigin(step.Direction), SessionFrame{Payload: step.Input.Bytes()})
		if err != nil || len(frames) != 1 {
			t.Fatal("live compound frame was expanded or rejected", frames, err)
		}
		actual, err := ParseValue(frames[0].Payload)
		if err != nil || !equalValues(actual, step.Input) {
			t.Fatal("live compound native fields changed", actual, err)
		}
	}
	if err := adapter.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionVerificationRejectsMissingAndInvalidTraces(t *testing.T) {
	for _, mode := range []string{"missing", "orphan-result", "truncated", "lost-model", "missing-tool-definition"} {
		t.Run(mode, func(t *testing.T) {
			definition := sessionVerificationDefinition(t)
			switch mode {
			case "missing":
				definition.Mapping.SessionSamples = nil
			case "orphan-result":
				for index := range definition.Mapping.SessionSamples {
					definition.Mapping.SessionSamples[index].Steps[5], definition.Mapping.SessionSamples[index].Steps[9] = definition.Mapping.SessionSamples[index].Steps[9], definition.Mapping.SessionSamples[index].Steps[5]
				}
			case "truncated":
				for index := range definition.Mapping.SessionSamples {
					definition.Mapping.SessionSamples[index].Steps = definition.Mapping.SessionSamples[index].Steps[:6]
				}
			case "lost-model":
				mapping := definition.Mapping.Directions[EncodeUpstreamEvent]
				copy := mapping.Program
				copy.Fields = map[string]Expression{"event": {Op: "read", Path: "/type"}}
				mapping.Program = copy
				definition.Mapping.Directions[EncodeUpstreamEvent] = mapping
			case "missing-tool-definition":
				for index := range definition.Mapping.SessionSamples {
					definition.Mapping.SessionSamples[index].Steps = append(definition.Mapping.SessionSamples[index].Steps[:1], definition.Mapping.SessionSamples[index].Steps[2:]...)
				}
			}
			report := Verify(t.Context(), compileSessionDefinition(t, definition))
			if report.Passed {
				t.Fatal("invalid session activated")
			}
		})
	}
}

func TestSessionOptionsValidationAndIsolation(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	compiled := compileSessionDefinition(t, definition)
	options := compiled.Operations()
	options["session"].Session.FrameBytes = 1
	if compiled.Operations()["session"].Session.FrameBytes != DefaultSessionFrameBytes {
		t.Fatal("mutated compiled session options")
	}
	compiler, _ := NewCompiler(DefaultLimits(), []string{"transport.websocket"})
	for _, mode := range []string{"zero-queue", "timeout-order", "wrong-direction", "undeclared-media", "resume"} {
		t.Run(mode, func(t *testing.T) {
			definition := sessionVerificationDefinition(t)
			op := definition.Spec.Operations["session"]
			switch mode {
			case "zero-queue":
				op.Session.QueueBytes = 0
			case "timeout-order":
				op.Session.PingMillis = op.Session.IdleMillis
			case "wrong-direction":
				delete(definition.Mapping.Directions, DecodeClientEvent)
				delete(definition.Mapping.Directions, EncodeUpstreamEvent)
			case "undeclared-media":
				op.Session.InputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}
			}
			raw := fixtureValue(t, definition).Bytes()
			if mode == "resume" {
				raw = []byte(strings.Replace(string(raw), `"session":{"`, `"session":{"resume":true,"`, 1))
			}
			if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
				t.Fatal("invalid session configuration compiled")
			}
		})
	}
}

func TestVerifySessionCompositionAndPureSessionBindings(t *testing.T) {
	definition := sessionVerificationDefinition(t)
	delete(definition.Spec.Operations, "generate")
	delete(definition.Mapping.Operations, "generate")
	for name, operation := range definition.Spec.Operations {
		if operation.Transport != WebSocket {
			delete(definition.Spec.Operations, name)
		}
	}
	delete(definition.Mapping.Directions, DecodeResponse)
	delete(definition.Mapping.Directions, EncodeResponse)
	samples := []Sample{}
	for _, sample := range definition.Mapping.Samples {
		if sample.Direction == DecodeRequest || sample.Direction == EncodeRequest {
			samples = append(samples, sample)
		}
	}
	definition.Mapping.Samples = samples
	ingress := compileSessionDefinition(t, definition)
	definition.Spec.ID, definition.Spec.Family = "other-session", "other-session"
	upstream := compileSessionDefinition(t, definition)
	report := VerifyBindingCombination(t.Context(), ingress, upstream, definition.Mapping.Capabilities)
	if !report.Passed {
		t.Fatalf("session composition failed: %+v", report.Issues)
	}
	binding := Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: definition.Mapping.Capabilities, Transports: []Transport{WebSocket}}
	if err := IssuesError(CheckBinding(binding, upstream)); err != nil {
		t.Fatal(err)
	}
	if err := IssuesError(CheckIngressBinding(binding, upstream)); err != nil {
		t.Fatal(err)
	}
	op := definition.Spec.Operations["session"]
	op.Session.InputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}
	if err := CheckSessionCompatibility(ingress.Operations()["session"], Operation{Transport: op.Transport, Session: &op.Session.SessionConfig}); !hasIssueCode(err, UnsupportedCapability) {
		t.Fatalf("undeclared binary bridge: %v", err)
	}
	op.Session.InputMedia = nil
	op.Session.CanGenerateAutomatically = true
	if err := CheckSessionCompatibility(ingress.Operations()["session"], Operation{Transport: op.Transport, Session: &op.Session.SessionConfig}); !hasIssueCode(err, UnsupportedCapability) {
		t.Fatalf("unapproved automatic responses: %v", err)
	}
}

func TestSessionDirectionsCanBeIndependentlyImplemented(t *testing.T) {
	var adapters []*Compiled
	for _, isIngress := range []bool{true, false} {
		definition := sessionVerificationDefinition(t)
		definition.Mapping.Native.Preserve = false
		definition.Spec.Operations = map[string]ProtocolOperationSpec{"session": definition.Spec.Operations["session"]}
		definition.Mapping.Operations = map[string]ProtocolOperationMapping{"session": {Request: EncodeRequest}}
		request, client, server := EncodeRequest, EncodeUpstreamEvent, DecodeEvent
		definition.Spec.ID, definition.Spec.Family = "session-upstream", "session-upstream"
		if isIngress {
			request, client, server = DecodeRequest, DecodeClientEvent, EncodeEvent
			definition.Spec.ID, definition.Spec.Family = "session-ingress", "session-ingress"
		}
		definition.Mapping.Operations["session"] = ProtocolOperationMapping{Request: request}
		for direction := range definition.Mapping.Directions {
			if direction != request && direction != client && direction != server {
				delete(definition.Mapping.Directions, direction)
			}
		}
		for _, sample := range definition.Mapping.Samples {
			if sample.Direction == request {
				definition.Mapping.Samples = []Sample{sample}
				break
			}
		}
		trace := SessionSample{ID: "duplex", Operation: "session", Model: StringValue("m")}
		for index, step := range definition.Mapping.SessionSamples[0].Steps {
			if !((isIngress && eventOrigin(step.Direction) == ClientEvent) || (!isIngress && eventOrigin(step.Direction) == UpstreamEvent)) {
				step = definition.Mapping.SessionSamples[1].Steps[index]
			}
			trace.Steps = append(trace.Steps, step)
		}
		definition.Mapping.SessionSamples = []SessionSample{trace}
		compiled := compileSessionDefinition(t, definition)
		if report := Verify(t.Context(), compiled); !report.Passed {
			t.Fatalf("partial session definition: %+v", report.Issues)
		}
		adapters = append(adapters, compiled)
	}
	if report := VerifyCombination(t.Context(), adapters[0], adapters[1]); !report.Passed {
		t.Fatalf("independent duplex adapters: %+v", report.Issues)
	}
}

func TestTextOnlyBindingDoesNotPromiseSessionCapabilities(t *testing.T) {
	compiled := compileSessionDefinition(t, sessionVerificationDefinition(t))
	report := VerifyBindingCombination(t.Context(), compiled, compiled, CapabilitySet{TextCapability: true})
	if !report.Passed {
		t.Fatalf("HTTP text-only binding was forced to promise sessions/tools: %+v", report.Issues)
	}
	for _, check := range report.Checks {
		for _, capability := range check.Capabilities {
			if capability != TextCapability {
				t.Fatalf("unexpected binding evidence: %+v", check)
			}
		}
	}
}
