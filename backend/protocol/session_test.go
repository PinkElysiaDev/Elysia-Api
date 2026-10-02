package protocol

import "testing"

func sessionEvent(kind EventType, response string) Event {
	event := Event{SchemaVersion: SemanticSchemaVersion, Type: kind}
	if response != "" {
		event.ResponseID = StringValue(response)
	}
	return event
}

func testSession(t *testing.T, policy SessionPolicy, limits Limits) *SessionReplay {
	t.Helper()
	target := Target{Capabilities: CapabilitySet{SessionsCapability: true, TextCapability: true, FunctionToolsCapability: true, FreeTextToolsCapability: true, UsageCapability: true}}
	session, err := NewSessionReplay(target, target, limits, policy)
	if err != nil {
		t.Fatal(err)
	}
	start := sessionEvent(SessionStarted, "")
	start.SessionID = StringValue("session-1")
	consumeSession(t, session, UpstreamEvent, start)
	return session
}

func consumeSession(t *testing.T, session *SessionReplay, origin EventOrigin, event Event) {
	t.Helper()
	if accepted, err := session.Consume(origin, event); err != nil || !accepted {
		t.Fatalf("%s %s: accepted=%v err=%v", origin, event.Type, accepted, err)
	}
}

func TestSessionInterleavedToolsAndUsageTails(t *testing.T) {
	session := testSession(t, SessionPolicy{}, DefaultLimits())
	for _, id := range []string{"response-1", "response-2"} {
		consumeSession(t, session, ClientEvent, sessionEvent(ResponseCreate, ""))
		consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseStarted, id))
	}
	for index, kind := range []InputKind{JSONInput, TextInput} {
		id, input := "response-1", fixtureValue(t, map[string]any{"n": 9007199254740993})
		if index == 1 {
			id, input = "response-2", StringValue("run --without-json {}")
		}
		call := Node{Kind: ToolCallNode, CallID: StringValue("call-" + id), Name: StringValue("tool"), Input: &ToolInput{Kind: kind, Value: input}}
		event := sessionEvent(ItemStarted, id)
		event.ItemID, event.Item = StringValue("item-"+id), &call
		consumeSession(t, session, UpstreamEvent, event)
		event.Type = ItemFinished
		consumeSession(t, session, UpstreamEvent, event)
		result := sessionEvent(ToolResultSubmitted, "")
		result.Item = &Node{Kind: ToolResultNode, CallID: call.CallID, Payload: StringValue("client executed")}
		consumeSession(t, session, ClientEvent, result)
		if _, err := session.Consume(ClientEvent, result); !hasIssueCode(err, InvalidAssociation) {
			t.Fatalf("duplicate tool result accepted: %v", err)
		}
	}
	consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseFinished, "response-2"))
	consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseFinished, "response-1"))
	tail := sessionEvent(UsageUpdated, "response-1")
	tail.Usage = &Usage{CacheRead: &Counter{Count: 40, Origin: ObservedCount}, CacheCreation: &Counter{Count: 0, Origin: ObservedCount}}
	consumeSession(t, session, UpstreamEvent, tail)
	consumeSession(t, session, UpstreamEvent, sessionEvent(SessionClose, ""))
	usage := session.ResponseUsage()
	if usage["response-2"] != nil || usage["response-1"].Input != nil || usage["response-1"].CacheCreation == nil || usage["response-1"].CacheRead.Count != 40 {
		t.Fatalf("missing/zero/per-response usage changed: %+v", usage)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionSequenceSuppressesResponseAndCloseReplays(t *testing.T) {
	session := testSession(t, SessionPolicy{CanGenerateAutomatically: true}, DefaultLimits())
	for index, kind := range []EventType{ResponseStarted, ResponseFinished, SessionClose} {
		id := "r"
		if kind == SessionClose {
			id = ""
		}
		event := sessionEvent(kind, id)
		event.Sequence = fixtureValue(t, index)
		consumeSession(t, session, UpstreamEvent, event)
		if accepted, err := session.Consume(UpstreamEvent, event); err != nil || accepted {
			t.Fatalf("replayed %s: accepted=%v error=%v", kind, accepted, err)
		}
	}
}

func TestSessionSequenceIncludesNativeExtensions(t *testing.T) {
	session := testSession(t, SessionPolicy{CanGenerateAutomatically: true}, DefaultLimits())
	event := sessionEvent(ResponseStarted, "r")
	event.Sequence = fixtureValue(t, 1)
	event.Native = &Native{Value: fixtureValue(t, map[string]any{"vendor": map[string]int{"revision": 1}})}
	consumeSession(t, session, UpstreamEvent, event)
	event.Native = &Native{Value: fixtureValue(t, map[string]any{"vendor": map[string]int{"revision": 2}})}
	if _, err := session.Consume(UpstreamEvent, event); !hasIssueCode(err, UpstreamContractViolation) {
		t.Fatalf("different native payload was silently deduplicated: %v", err)
	}
	event.Sequence = fixtureValue(t, nil)
	if _, err := session.Consume(UpstreamEvent, event); !hasIssueCode(err, InvalidInput) {
		t.Fatalf("null sequence became zero: %v", err)
	}
}

func TestSessionAuthorizationAndAssociationFailures(t *testing.T) {
	cases := []struct {
		name   string
		origin EventOrigin
		event  Event
		code   IssueCode
	}{
		{"unsolicited", UpstreamEvent, sessionEvent(ResponseStarted, "r"), UpstreamContractViolation},
		{"wrong-lane", ClientEvent, sessionEvent(ResponseStarted, "r"), UnsupportedCapability},
		{"empty-input", ClientEvent, sessionEvent(InputCommit, ""), InvalidAssociation},
		{"unknown-cancel", ClientEvent, sessionEvent(ResponseCancel, "missing"), InvalidAssociation},
		{"unknown-response", UpstreamEvent, sessionEvent(ResponseFinished, "missing"), InvalidAssociation},
		{"missing-response", UpstreamEvent, sessionEvent(ResponseFinished, ""), InvalidAssociation},
		{"model-change", ClientEvent, Event{SchemaVersion: 1, Type: SessionConfigure, Request: &Request{SchemaVersion: 1, Model: StringValue("other")}}, InvalidAssociation},
		{"unknown-tool", ClientEvent, Event{SchemaVersion: 1, Type: ToolResultSubmitted, Item: &Node{Kind: ToolResultNode, CallID: StringValue("unknown"), Payload: StringValue("x")}}, InvalidAssociation},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			session := testSession(t, SessionPolicy{Model: StringValue("authorized")}, DefaultLimits())
			if _, err := session.Consume(test.origin, test.event); !hasIssueCode(err, test.code) {
				t.Fatalf("want %s: %v", test.code, err)
			}
		})
	}
}

func TestSessionDisconnectCancellationAndConnectionError(t *testing.T) {
	for _, terminal := range []EventType{"", SessionClose, OperationFailed, OperationCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			session := testSession(t, SessionPolicy{}, DefaultLimits())
			consumeSession(t, session, ClientEvent, sessionEvent(ResponseCreate, ""))
			consumeSession(t, session, UpstreamEvent, sessionEvent(ResponseStarted, "r"))
			switch terminal {
			case SessionClose:
				consumeSession(t, session, ClientEvent, sessionEvent(SessionClose, ""))
			case OperationFailed:
				event := sessionEvent(OperationFailed, "")
				event.Error = StringValue("connection failed")
				consumeSession(t, session, UpstreamEvent, event)
			case OperationCancelled:
				consumeSession(t, session, ClientEvent, sessionEvent(ResponseCancel, "r"))
				consumeSession(t, session, UpstreamEvent, sessionEvent(OperationCancelled, "r"))
			}
			if err := session.Finish(); (err == nil) != (terminal != "") {
				t.Fatalf("terminal %q finish: %v", terminal, err)
			}
		})
	}
}

func TestSessionAggregateStateIsBounded(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	session := testSession(t, SessionPolicy{}, limits)
	for range limits.StateItems {
		consumeSession(t, session, ClientEvent, sessionEvent(ResponseCreate, ""))
	}
	if _, err := session.Consume(ClientEvent, sessionEvent(ResponseCreate, "")); !hasIssueCode(err, LimitExceeded) {
		t.Fatalf("unbounded pending responses: %v", err)
	}
}
