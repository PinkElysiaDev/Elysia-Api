package protocol

import (
	"strings"
	"testing"
)

func sessionVerificationDefinition(t *testing.T) Definition {
	t.Helper()
	definition := textVerificationDefinition(t, "session-protocol")
	definition.Native.Preserve = true
	for direction, mapping := range definition.Directions {
		mapping.Capabilities = CapabilitySet{TextCapability: true}
		definition.Directions[direction] = mapping
	}
	definition.Capabilities = CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true, UsageCapability: true}
	decode := Expression{Op: "object", Fields: map[string]Expression{"schemaVersion": literalExpression(1)}}
	encode := Expression{Op: "object", Fields: map[string]Expression{}}
	for wire, semantic := range map[string]string{"event": "type", "session": "sessionId", "response": "responseId", "itemKey": "itemId", "part": "item", "settings": "request", "counts": "usage", "failure": "error"} {
		decode.Fields[semantic] = Expression{Op: "read", Path: "/" + wire}
		encode.Fields[wire] = Expression{Op: "read", Path: "/" + semantic}
	}
	client := CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true}
	server := CapabilitySet{TextCapability: true, SessionsCapability: true, FunctionToolsCapability: true, UsageCapability: true}
	definition.Directions[DecodeClientEvent] = Mapping{Transform: &decode, Capabilities: client}
	definition.Directions[EncodeUpstreamEvent] = Mapping{Transform: &encode, Capabilities: client}
	definition.Directions[DecodeEvent] = Mapping{Transform: &decode, Capabilities: server}
	definition.Directions[EncodeEvent] = Mapping{Transform: &encode, Capabilities: server}
	config := DefaultSessionConfig()
	definition.Operations["session"] = Operation{Kind: "session", Method: "GET", Path: "/session", Transport: WebSocket, Auth: Credential{Location: "none"}, Session: &config}
	start := sessionEvent(SessionStarted, "")
	start.SessionID = StringValue("s")
	configure := sessionEvent(SessionConfigure, "")
	configure.Request = &Request{SchemaVersion: 1, Model: StringValue("m"), Tools: []Tool{{Kind: FunctionTool, Name: StringValue("count")}}}
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
		definition.SessionSamples = append(definition.SessionSamples, sample)
	}
	return definition
}

func compileSessionDefinition(t *testing.T, definition Definition) *Compiled {
	t.Helper()
	compiler, err := NewCompiler(DefaultLimits(), nil, []string{"transport.websocket"})
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
		mapping := definition.Directions[direction]
		mapping.Transform = &decode
		definition.Directions[direction] = mapping
	}
	for _, direction := range []Direction{EncodeUpstreamEvent, EncodeEvent} {
		mapping := definition.Directions[direction]
		mapping.Transform = &encode
		definition.Directions[direction] = mapping
	}
	sample := definition.SessionSamples[0]
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
	definition.SessionSamples = []SessionSample{sample}
	compiled := compileSessionDefinition(t, definition)
	evidence, err := executeSessionSample(t.Context(), compiled, sample)
	if err != nil {
		t.Fatal(err)
	}
	operation := compiled.Operations()["session"]
	if err := replaySessionCombination(t.Context(), compiled, operation, sample, evidence); err != nil {
		t.Fatal("offline compound replay", err)
	}
	binding := Binding{ProtocolID: definition.ID, RevisionHash: compiled.Hash(), Capabilities: definition.Capabilities, Transports: []Transport{WebSocket}}
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, binding, sample.Scope, sample.Model)
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
				definition.SessionSamples = nil
			case "orphan-result":
				for index := range definition.SessionSamples {
					definition.SessionSamples[index].Steps[5], definition.SessionSamples[index].Steps[9] = definition.SessionSamples[index].Steps[9], definition.SessionSamples[index].Steps[5]
				}
			case "truncated":
				for index := range definition.SessionSamples {
					definition.SessionSamples[index].Steps = definition.SessionSamples[index].Steps[:6]
				}
			case "lost-model":
				mapping := definition.Directions[EncodeUpstreamEvent]
				copy := *mapping.Transform
				copy.Fields = map[string]Expression{"event": {Op: "read", Path: "/type"}}
				mapping.Transform = &copy
				definition.Directions[EncodeUpstreamEvent] = mapping
			case "missing-tool-definition":
				for index := range definition.SessionSamples {
					definition.SessionSamples[index].Steps = append(definition.SessionSamples[index].Steps[:1], definition.SessionSamples[index].Steps[2:]...)
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
	compiler, _ := NewCompiler(DefaultLimits(), nil, []string{"transport.websocket"})
	for _, mode := range []string{"zero-queue", "timeout-order", "wrong-direction", "undeclared-media", "resume"} {
		t.Run(mode, func(t *testing.T) {
			definition := sessionVerificationDefinition(t)
			op := definition.Operations["session"]
			switch mode {
			case "zero-queue":
				op.Session.QueueBytes = 0
			case "timeout-order":
				op.Session.PingMillis = op.Session.IdleMillis
			case "wrong-direction":
				delete(definition.Directions, DecodeClientEvent)
				delete(definition.Directions, EncodeUpstreamEvent)
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
	delete(definition.Operations, "generate")
	for name, operation := range definition.Operations {
		if operation.Transport != WebSocket {
			delete(definition.Operations, name)
		}
	}
	delete(definition.Directions, DecodeResponse)
	delete(definition.Directions, EncodeResponse)
	samples := []Sample{}
	for _, sample := range definition.Samples {
		if sample.Direction == DecodeRequest || sample.Direction == EncodeRequest {
			samples = append(samples, sample)
		}
	}
	definition.Samples = samples
	ingress := compileSessionDefinition(t, definition)
	definition.ID, definition.Family = "other-session", "other-session"
	upstream := compileSessionDefinition(t, definition)
	report := VerifyBindingCombination(t.Context(), ingress, upstream, definition.Capabilities)
	if !report.Passed {
		t.Fatalf("session composition failed: %+v", report.Issues)
	}
	binding := Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: definition.Capabilities, Transports: []Transport{WebSocket}}
	if err := IssuesError(CheckBinding(binding, upstream)); err != nil {
		t.Fatal(err)
	}
	if err := IssuesError(CheckIngressBinding(binding, upstream)); err != nil {
		t.Fatal(err)
	}
	op := definition.Operations["session"]
	op.Session.InputMedia = &MediaFormat{Type: "audio", Format: "pcm16"}
	if err := CheckSessionCompatibility(ingress.Operations()["session"], op); !hasIssueCode(err, UnsupportedCapability) {
		t.Fatalf("undeclared binary bridge: %v", err)
	}
	op.Session.InputMedia = nil
	op.Session.CanGenerateAutomatically = true
	if err := CheckSessionCompatibility(ingress.Operations()["session"], op); !hasIssueCode(err, UnsupportedCapability) {
		t.Fatalf("unapproved automatic responses: %v", err)
	}
}

func TestSessionDirectionsCanBeIndependentlyImplemented(t *testing.T) {
	var adapters []*Compiled
	for _, isIngress := range []bool{true, false} {
		definition := sessionVerificationDefinition(t)
		definition.Native.Preserve = false
		definition.Operations = map[string]Operation{"session": definition.Operations["session"]}
		request, client, server := EncodeRequest, EncodeUpstreamEvent, DecodeEvent
		definition.ID, definition.Family = "session-upstream", "session-upstream"
		if isIngress {
			request, client, server = DecodeRequest, DecodeClientEvent, EncodeEvent
			definition.ID, definition.Family = "session-ingress", "session-ingress"
		}
		for direction := range definition.Directions {
			if direction != request && direction != client && direction != server {
				delete(definition.Directions, direction)
			}
		}
		for _, sample := range definition.Samples {
			if sample.Direction == request {
				definition.Samples = []Sample{sample}
				break
			}
		}
		trace := SessionSample{ID: "duplex", Operation: "session", Model: StringValue("m")}
		for index, step := range definition.SessionSamples[0].Steps {
			if !((isIngress && eventOrigin(step.Direction) == ClientEvent) || (!isIngress && eventOrigin(step.Direction) == UpstreamEvent)) {
				step = definition.SessionSamples[1].Steps[index]
			}
			trace.Steps = append(trace.Steps, step)
		}
		definition.SessionSamples = []SessionSample{trace}
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
