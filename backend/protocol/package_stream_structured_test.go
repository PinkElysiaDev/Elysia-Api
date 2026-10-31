package protocol

import (
	"strings"
	"testing"
)

func structuredStreamSpec(t *testing.T) *ProtocolStreamSpec {
	t.Helper()
	var spec ProtocolStreamSpec
	raw := Value{raw: `{
		"discriminator":"/object","discriminatorValue":{"op":"literal","value":"chunk"},
		"responseId":"/id","optionalResponseId":true,"itemId":"/index","sequence":"/seq","closeItemsOnFinish":true,
		"identities":[{"path":"/model","type":"string","when":{"op":"not","source":{"op":"present","source":{"op":"read","path":"/error"}}}},{"path":"/created","type":"integer"}],
		"events":{"chunk":[
			{"action":"failure","allowBeforeStart":true,"error":"/error","when":{"op":"present","source":{"op":"read","path":"/error"}}},
			{"foreach":"/choices","actions":[
				{"action":"response_start","implicit":true},
				{"action":"item_start","implicit":true,"itemIdValue":{"op":"literal","value":"text"},"when":{"op":"present","source":{"op":"read","path":"/delta/content"}}},
				{"action":"text_delta","text":"/delta/content","itemIdValue":{"op":"literal","value":"text"},"when":{"op":"present","source":{"op":"read","path":"/delta/content"}}},
				{"foreach":"/delta/tool_calls","actions":[
					{"action":"tool_start","implicit":true,"sparseIdentity":true,"itemIdValue":{"op":"cast","source":{"op":"read","path":"/index"},"to":"string"},"tool":{"name":"/function/name","callId":"/id"}},
					{"action":"tool_delta","arguments":"/function/arguments","itemIdValue":{"op":"cast","source":{"op":"read","path":"/index"},"to":"string"},"when":{"op":"exists","source":{"op":"read","path":"/function/arguments"}}}
				]},
				{"action":"response_finish","when":{"op":"present","source":{"op":"read","path":"/finish_reason"}}}
			]},
			{"action":"usage","usage":{"output":"/usage/output"},"when":{"op":"present","source":{"op":"read","path":"/usage"}}}
		]}
	}`}
	if err := raw.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	return &spec
}

func TestPackageStreamStructuredActions(t *testing.T) {
	state := newSparseIdentityValidation(t, structuredStreamSpec(t))
	for _, raw := range []string{
		`{"seq":0,"id":"r","model":"m","created":0,"choices":[]}`,
		`{"seq":1,"id":"r","choices":[{"delta":{"content":"hi","tool_calls":[{"index":2,"function":{"arguments":"{\"n\":"}}]}}]}`,
		`{"seq":2,"id":"r","model":"\u006d","choices":[{"delta":{"content":"!","tool_calls":[{"index":2,"id":"call","function":{"name":"lookup","arguments":"1}"}}]}}]}`,
		`{"seq":3,"id":"r","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"seq":4,"choices":[],"usage":{"output":5}}`,
	} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal(raw, err)
		}
	}
	if err := state.finish(); err != nil {
		t.Fatal(err)
	}
	if len(state.replay.items) != 2 || state.openItems != 0 || state.replay.state.buffered != 0 || state.accepted.sequence != 4 {
		t.Fatal("nested actions repeated starts, retained completed arguments, or advanced the sequence per member")
	}
	if usage := state.replay.Usage(); usage == nil || usage.Output == nil || usage.Output.Count != 5 {
		t.Fatal("usage tail lost", usage)
	}
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"seq":5,"choices":[{"delta":{"content":"late"}}]}`}), UpstreamContractViolation, "/object")
	state = newSparseIdentityValidation(t, structuredStreamSpec(t))
	if err := state.consume(t.Context(), Value{raw: `{"seq":0,"model":false,"error":{"message":"failed"}}`}); err != nil {
		t.Fatal("failure before a response must not acquire an unrelated identity", err)
	}
	if state.accepted.hasStarted || state.finish() != nil {
		t.Fatal("failure invented a successful response start")
	}
}

func TestPackageStreamStructuredRejections(t *testing.T) {
	const started = `{"seq":0,"id":"r","model":"m","choices":[{"delta":{"content":"a","tool_calls":[{"index":2,"function":{"arguments":"{\"n\":"}}]}}]}`
	for _, fixture := range []struct{ name, frame, path string }{
		{"changed-root-model", `{"seq":1,"id":"r","model":"other","choices":[]}`, "/model"},
		{"invalid-delta", `{"seq":1,"choices":[{"delta":{"tool_calls":[{"index":2,"function":{"arguments":false}}]}}]}`, "/choices/0/delta/tool_calls/0/function/arguments"},
		{"null-tool-id", `{"seq":1,"choices":[{"delta":{"tool_calls":[{"index":2,"id":null}]}}]}`, "/choices/0/delta/tool_calls/0/id"},
		{"null-tool-array", `{"seq":1,"choices":[{"delta":{"tool_calls":null}}]}`, "/choices/0/delta/tool_calls"},
		{"missing-tool-name", `{"seq":1,"choices":[{"delta":{},"finish_reason":"stop"}]}`, "/choices/0/delta/tool_calls/0/function/name"},
		{"stale-frame", `{"seq":0,"choices":[]}`, "/seq"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			state := newSparseIdentityValidation(t, structuredStreamSpec(t))
			if err := state.consume(t.Context(), Value{raw: started}); err != nil {
				t.Fatal(err)
			}
			assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: fixture.frame}), UpstreamContractViolation, fixture.path)
		})
	}
	state := newSparseIdentityValidation(t, structuredStreamSpec(t))
	if err := state.consume(t.Context(), Value{raw: started}); err != nil {
		t.Fatal(err)
	}
	assertPackageRuntimeIssue(t, state.finish(), UpstreamContractViolation, "/object")
	if err := state.consume(t.Context(), Value{raw: `{"seq":1,"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call","function":{"name":"lookup"}}]}}]}`}); err != nil {
		t.Fatal(err)
	}
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"seq":2,"choices":[{"delta":{},"finish_reason":"stop"}]}`}), UpstreamContractViolation, "/choices/0/delta/tool_calls/0/function/arguments")
}

func TestPackageStreamStructuredCompilation(t *testing.T) {
	for _, edit := range []func(*ProtocolStreamSpec){
		func(spec *ProtocolStreamSpec) { spec.Events["chunk"][1].Action = "observe" },
		func(spec *ProtocolStreamSpec) { spec.Events["chunk"][1].Actions[2].Implicit = true },
		func(spec *ProtocolStreamSpec) { spec.Events["chunk"][1].Actions[1].SparseIdentity = true },
		func(spec *ProtocolStreamSpec) { spec.Identities[0].Type = ObjectType },
		func(spec *ProtocolStreamSpec) {
			spec.DiscriminatorValue = expressionPointer(readExpression("state", "/kind"))
		},
	} {
		spec := structuredStreamSpec(t)
		edit(spec)
		if _, err := compilePackageStreamContract(spec, "/stream", DefaultLimits()); err == nil {
			t.Fatal("invalid structured stream declaration compiled")
		}
	}
}

func TestPackageStreamDeclaredSparseUsage(t *testing.T) {
	spec := structuredStreamSpec(t)
	usage := &spec.Events["chunk"][2]
	usage.AllowBeforeStart = true
	usage.Usage.AllowEmpty = true
	state := newSparseIdentityValidation(t, spec)
	if err := state.consume(t.Context(), Value{raw: `{"seq":0,"usage":{}}`}); err != nil {
		t.Fatal("declared empty usage observation rejected", err)
	}
	if state.accepted.hasStarted || state.replay.usage == nil || state.replay.usage.Input != nil || state.replay.usage.Output != nil || state.replay.usage.Total != nil {
		t.Fatal("empty usage invented a response or zero counters")
	}
	usage.Usage.AllowEmpty = false
	state = newSparseIdentityValidation(t, spec)
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"seq":0,"usage":{}}`}), UpstreamContractViolation, "/object")
}

func TestPackageWireSharedRetentionBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 512
	contract, err := compilePackageStreamContract(structuredStreamSpec(t), "/stream", limits)
	if err != nil {
		t.Fatal(err)
	}
	state, err := newPackageStreamValidation(contract, Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true, FunctionToolsCapability: true}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	frame := func(sequence int, text string) Value {
		return fixtureValue(t, map[string]any{"seq": sequence, "id": "r", "model": strings.Repeat("m", 80), "choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 2, "id": "call", "function": map[string]any{"name": "lookup", "arguments": text}}}}},
		}})
	}
	initial := frame(0, `{"text":"`+strings.Repeat("x", 100))
	if initial.Len() > limits.BufferBytes {
		t.Fatal("fixture must fit the individual frame budget")
	}
	if err := state.consume(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	additional := limits.BufferBytes - state.identityBytes - state.replay.state.buffered + 1
	if additional <= 0 || state.replay.state.buffered+additional > limits.BufferBytes {
		t.Fatal("fixture must exceed only the shared retention budget")
	}
	next := frame(1, strings.Repeat("x", additional))
	if next.Len() > limits.BufferBytes {
		t.Fatal("second fixture must fit the individual frame budget")
	}
	assertPackageRuntimeIssue(t, state.consume(t.Context(), next), LimitExceeded, "/choices/0/delta/tool_calls/0/function/arguments")
}
