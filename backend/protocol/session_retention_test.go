package protocol

import (
	"errors"
	"testing"
)

func TestSessionSharesBudgetWithMapping(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	session := testSession(t, SessionPolicy{}, limits)
	state := NewEvaluationState()
	defer session.Release()
	defer state.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(state, session); err != nil {
		t.Fatal(err)
	}
	compiled := combinedRetentionPackage(t)
	for _, key := range []string{"a", "b"} {
		if _, err := decodePrimitiveText(t, compiled, EvaluationContext{State: state}, map[string]any{"record_key": key, "record": "retained"}); err != nil {
			t.Fatal(err)
		}
	}
	consumeSession(t, session, ClientEvent, sessionEvent(ResponseCreate, ""))
	consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseStarted, "r"))
	if budget.items != 3 {
		t.Fatal("response creation duplicated or lost its pending slot", budget.items)
	}
	item := sessionEvent(ItemStarted, "r")
	item.ItemID, item.Item = StringValue("text"), &Node{Kind: TextNode}
	_, err := session.Consume(UpstreamEvent, item)
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 4 {
		t.Fatal("response replay and Mapping obtained separate budgets", err)
	}
	if err := session.Finish(); !errors.As(err, &failure) {
		t.Fatal("failed shared budget did not prevent successful Finish", err)
	}
	session.Release()
	if budget.items != state.packageStateItems || budget.bytes != state.packageStateBytes || len(budget.sequences) != 0 {
		t.Fatal("session release retained response or lane state")
	}
	state.Release()
	if budget.items != 0 || budget.bytes != 0 {
		t.Fatal("mapping release left session charges")
	}
}

func TestSessionLaneHistorySharesBytes(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 256
	session := testSession(t, SessionPolicy{}, limits)
	defer session.Release()
	budget := NewStreamBudget(limits)
	if err := budget.Bind(session); err != nil {
		t.Fatal(err)
	}
	latest := map[EventOrigin]Event{}
	for sequence := int64(1); sequence <= 20; sequence++ {
		for _, origin := range []EventOrigin{ClientEvent, UpstreamEvent} {
			event := sessionEvent(SessionConfigured, "")
			if origin == ClientEvent {
				event.Type = SessionConfigure
				event.Request = &Request{SchemaVersion: SemanticSchemaVersion}
			}
			event.Sequence = coreInteger(sequence)
			consumeSession(t, session, origin, event)
			latest[origin] = event
		}
	}
	if session.clientSequence.count+session.serverSequence.count != 3 {
		t.Fatal("session lanes ignored their combined history byte limit")
	}
	for origin, event := range latest {
		if accepted, err := session.Consume(origin, event); err != nil || accepted {
			t.Fatal("history eviction lost a lane's latest receipt", origin, err)
		}
	}
	session.Release()
	if len(budget.sequences) != 0 || budget.bytes != 0 {
		t.Fatal("lane registration survived release")
	}
}

func TestSessionReleasePreservesReturnedAccounting(t *testing.T) {
	session := testSession(t, SessionPolicy{}, DefaultLimits())
	consumeSession(t, session, ClientEvent, sessionEvent(ResponseCreate, ""))
	consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseStarted, "r"))
	consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseFinished, "r"))
	tail := sessionEvent(UsageUpdated, "r")
	tail.Usage = &Usage{Input: &Counter{Count: 17, Origin: ObservedCount}}
	consumeSession(t, session, UpstreamEvent, tail)
	consumeSession(t, session, UpstreamEvent, sessionEvent(SessionClose, ""))
	responses, usage := session.Responses(), session.ResponseUsage()
	session.Release()
	session.Release()
	if responses[0].ID != "r" || responses[0].Terminal != ResponseFinished || responses[0].Usage.Input.Count != 17 || usage["r"].Input.Count != 17 {
		t.Fatal("session cleanup changed already returned accounting")
	}
	if len(session.responses) != 0 || len(session.tools) != 0 || session.clientSequence != nil || session.serverSequence != nil {
		t.Fatal("released session retained histories")
	}
	if _, err := session.Consume(UpstreamEvent, tail); err == nil || session.Finish() == nil {
		t.Fatal("released session accepted new state or successful completion")
	}
}

func TestSessionAdapterReleasesSharedState(t *testing.T) {
	_, document, compiled := loadTransportPackage(t, "sessions", "session-alpha", featureWebSocketTransport)
	operation, _ := compiled.Operation("session")
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, Binding{Capabilities: compiled.capabilities}, sessionTestContext(t, Scope{Model: "m"}), StringValue("m"))
	if err != nil {
		t.Fatal(err)
	}
	budget := adapter.options.State.retention.budget
	if budget == nil || adapter.replay.retention.budget != budget {
		t.Fatal("duplex adapter did not bind its Mapping and session state")
	}
	for _, step := range document.Mapping.SessionSamples[0].Steps {
		if _, err := adapter.Convert(t.Context(), eventOrigin(step.Direction), SessionFrame{Payload: step.Input.Bytes()}); err != nil {
			t.Fatal(err)
		}
	}
	responses := adapter.Responses()
	adapter.Release()
	if len(responses) != 2 || budget.items != 0 || budget.bytes != 0 || len(budget.sequences) != 0 {
		t.Fatal("duplex release lost accounting or retained translation state")
	}
	for _, frame := range []SessionFrame{{Payload: []byte(`{}`)}, {IsBinary: true, Payload: []byte("audio")}} {
		if output, err := adapter.Convert(t.Context(), ClientEvent, frame); err == nil || len(output) != 0 {
			t.Fatal("released duplex adapter resumed translation", err)
		}
	}
}

func TestSessionAdapterRejectsCombinedRetentionBeforeForwarding(t *testing.T) {
	compiler, document, _ := loadTransportPackage(t, "sessions", "session-alpha", featureWebSocketTransport)
	limits := DefaultLimits()
	limits.StateItems = 1
	document.Mapping.Limits = &limits
	document.Spec.Operations["session"].Session.QueueItems = 1
	steps := document.Mapping.SessionSamples[0].Steps
	document.Mapping.Samples, document.Mapping.SessionSamples = nil, nil
	mapping := document.Mapping.Directions[DecodeClientEvent]
	mapping.Indexes = map[string]ProtocolStateIndex{"records": {KeyMode: "string", Policy: "last", Schema: ValueSchema{Type: StringType}}}
	program := mapping.Program
	mapping.Program = Expression{Op: "let", Name: "recorded", Source: &Expression{
		Op: "remember", Name: "records", Source: expressionPointer(literalExpression("context")), Body: expressionPointer(literalExpression("retained")),
	}, Body: &program}
	document.Mapping.Directions[DecodeClientEvent] = mapping
	compiled, issues := compiler.Compile(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	operation, _ := compiled.Operation("session")
	adapter, err := NewSessionAdapter(compiled, compiled, operation, operation, Binding{Capabilities: compiled.capabilities}, sessionTestContext(t, Scope{Model: "m"}), StringValue("m"))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Release()
	for _, step := range steps[:4] {
		if _, err := adapter.Convert(t.Context(), eventOrigin(step.Direction), SessionFrame{Payload: step.Input.Bytes()}); err != nil {
			t.Fatal(err)
		}
	}
	// The fifth step requests a response. Its pending slot and the retained
	// Mapping record each fit alone, but cannot occupy the same one-item budget.
	output, err := adapter.Convert(t.Context(), ClientEvent, SessionFrame{Payload: steps[4].Input.Bytes()})
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "state_items" || failure.Observed != 2 || len(output) != 0 {
		t.Fatal("over-budget response request was forwarded", output, err)
	}
}
