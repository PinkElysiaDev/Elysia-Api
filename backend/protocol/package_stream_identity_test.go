package protocol

import (
	"strings"
	"testing"
)

// Real wire layouts put the initial response ID in an envelope and omit it
// from later blocks. Their content indexes are identities, not array offsets.
func sparseIdentityStream(t *testing.T) *ProtocolStreamSpec {
	t.Helper()
	var spec ProtocolStreamSpec
	raw := Value{raw: `{"discriminator":"/type","responseId":"/response_id","inheritResponseId":true,"namespaces":{"item":{"keyType":"integer"}},"lifecycle":{"eof":"require_terminal","openEntities":"reject"},"events":{"message_start":[{"action":"stream_start","responseId":"/message/id"}],"content_block_start":[{"action":"entity_start","when":{"op":"equal","items":[{"op":"read","from":"input","path":"/content_block/type"},{"op":"literal","value":"text"}]},"entity":"text","key":{"path":"/index"}},{"action":"entity_start","when":{"op":"equal","items":[{"op":"read","from":"input","path":"/content_block/type"},{"op":"literal","value":"tool_use"}]},"entity":"function_call","key":{"path":"/index"},"identities":{"name":{"path":"/content_block/name"},"call_id":{"path":"/content_block/id"}}}],"content_block_delta":[{"action":"entity_append","when":{"op":"equal","items":[{"op":"read","from":"input","path":"/delta/type"},{"op":"literal","value":"text_delta"}]},"entity":"text","key":{"path":"/index"},"payload":{"path":"/delta/text"}},{"action":"entity_append","when":{"op":"equal","items":[{"op":"read","from":"input","path":"/delta/type"},{"op":"literal","value":"input_json_delta"}]},"entity":"function_call","key":{"path":"/index"},"payload":{"path":"/delta/partial_json"}}],"content_block_stop":[{"action":"entity_finish","namespace":"item","key":{"path":"/index"}}],"message_delta":[{"action":"assert","assertions":[{"path":"/usage/output_tokens","when":{"op":"exists","source":{"op":"read","from":"input","path":"/usage/output_tokens"}},"schema":{"type":"integer"},"code":"upstream_contract_violation","reason":"counter must be nonnegative","check":{"op":"integer","operation":"gte","items":[{"op":"read","from":"input","path":""},{"op":"literal","value":0}]}},{"path":"","code":"upstream_contract_violation","reason":"count observation requires a value","check":{"op":"any","items":[{"op":"exists","source":{"op":"read","from":"input","path":"/usage/output_tokens"}}]}}],"afterTerminal":true},{"action":"observe","afterTerminal":true}],"message_stop":[{"action":"stream_finish"}]},"entities":{"text":{"namespace":"item","payload":"text"},"function_call":{"namespace":"item","payload":"json","identities":{"name":{"type":"string","requiredAt":"start"},"call_id":{"type":"string","requiredAt":"start","unique":true}}}}}`}
	if err := raw.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	return &spec
}

func newSparseIdentityValidation(t *testing.T, spec *ProtocolStreamSpec) *packageStreamValidation {
	t.Helper()
	contract, err := compilePackageStreamContract(spec, "/stream", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	state, err := newPackageStreamValidation(contract, Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true, FunctionToolsCapability: true}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPackageStreamSparseIdentities(t *testing.T) {
	state := newSparseIdentityValidation(t, sparseIdentityStream(t))
	frames := []string{
		`{"type":"message_start","message":{"id":"message-1"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Shanghai\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	for _, raw := range frames {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal(raw, err)
		}
	}
	if err := state.finish(); err != nil {
		t.Fatal(err)
	}
	if state.accepted.responseID != "message-1" || state.openItems != 0 || !state.accepted.isTerminal {
		t.Fatal("sparse wire identities lost their lifecycle", state.accepted, state.openItems)
	}
}

func TestPackageStreamSparseIdentityRejections(t *testing.T) {
	for _, test := range []struct {
		name, frame, path string
	}{
		{"explicit-null", `{"type":"message_stop","response_id":null}`, "/response_id"},
		{"changed-id", `{"type":"message_stop","response_id":"other"}`, "/response_id"},
		{"negative-index", `{"type":"content_block_start","index":-1,"content_block":{"type":"text"}}`, "/index"},
		{"string-index", `{"type":"content_block_start","index":"0","content_block":{"type":"text"}}`, "/index"},
		{"unknown-block", `{"type":"content_block_start","index":0,"content_block":{"type":"future"}}`, "/type"},
		{"orphan-delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`, "/index"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newSparseIdentityValidation(t, sparseIdentityStream(t))
			if err := state.consume(t.Context(), Value{raw: `{"type":"message_start","message":{"id":"r"}}`}); err != nil {
				t.Fatal(err)
			}
			assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: test.frame}), UpstreamContractViolation, test.path)
		})
	}
	state := newSparseIdentityValidation(t, sparseIdentityStream(t))
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"type":"message_start","message":{}}`}), UpstreamContractViolation, "/message/id")
	for _, predicate := range []Expression{literalExpression("true"), readExpression("runtime", "/stream/started")} {
		spec := sparseIdentityStream(t)
		spec.Events["message_start"][0].When = &predicate
		if _, err := compilePackageStreamContract(spec, "/stream", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "/when") {
			t.Fatal("wire conditions must be boolean and independent of mapping state", err)
		}
	}
}

func TestPackageStreamDeclaredIdentityDefaults(t *testing.T) {
	spec := sparseIdentityStream(t)
	spec.OptionalResponseID = true
	setWireTestKeyValue(spec, &Expression{Op: "if", When: &Expression{Op: "present", Source: expressionPointer(readExpression("input", "/index"))}, Then: expressionPointer(readExpression("input", "/index")), Otherwise: expressionPointer(literalExpression(0))})
	state := newSparseIdentityValidation(t, spec)
	for _, raw := range []string{
		`{"type":"message_start","message":{}}`,
		`{"type":"content_block_start","content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":null,"response_id":"late-id","delta":{"type":"text_delta","text":"hello"}}`,
		`{"type":"content_block_stop"}`,
		`{"type":"message_stop"}`,
	} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal(raw, err)
		}
	}
	if state.accepted.responseID != "late-id" || state.finish() != nil {
		t.Fatal("declared missing identity and default index lost the stream", state.accepted)
	}
	state = newSparseIdentityValidation(t, spec)
	if err := state.consume(t.Context(), Value{raw: `{"type":"message_start","message":{"id":null}}`}); err != nil {
		t.Fatal(err)
	}
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"type":"content_block_start","index":"0","content_block":{"type":"text"}}`}), UpstreamContractViolation, "/index")
	setWireTestKeyValue(spec, expressionPointer(readExpression("state", "/index")))
	if _, err := compilePackageStreamContract(spec, "/stream", DefaultLimits()); err == nil {
		t.Fatal("wire identity normalization read mapping state")
	}
}

func TestPackageStreamDeclaredResponseIdentity(t *testing.T) {
	spec := sparseIdentityStream(t)
	spec.ResponseID = "/message/id"
	isStart := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression("message_start")}}
	spec.ResponseIDValue = &Expression{Op: "if", When: &isStart, Then: expressionPointer(readExpression("input", "/message/id")), Otherwise: &Expression{Op: "omit"}}
	for index := range spec.Events["message_start"] {
		spec.Events["message_start"][index].ResponseID = ""
	}
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		t.Run(string(direction), func(t *testing.T) {
			state := newSparseIdentityValidation(t, spec)
			state.direction, state.target.Direction = direction, direction
			for _, raw := range []string{
				`{"type":"message_start","message":{"id":"actual"},"response_id":false}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text"},"response_id":"extension"}`,
				`{"type":"content_block_stop","index":0,"response_id":null}`,
				`{"type":"message_stop","response_id":{"opaque":true}}`,
			} {
				if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
					t.Fatal("extension field was interpreted as a response identity", err)
				}
			}
			if state.accepted.responseID != "actual" || state.finish() != nil {
				t.Fatal("declared identity was not inherited")
			}
		})
	}
	state := newSparseIdentityValidation(t, spec)
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"type":"message_start","message":{"id":null}}`}), UpstreamContractViolation, "/message/id")
	spec.ResponseIDValue = expressionPointer(readExpression("state", "/identity"))
	if _, err := compilePackageStreamContract(spec, "/stream", DefaultLimits()); err == nil {
		t.Fatal("wire response identity read mapping state")
	}
}

func setWireTestKeyValue(spec *ProtocolStreamSpec, value *Expression) {
	var update func([]ProtocolStreamActionSpec)
	update = func(actions []ProtocolStreamActionSpec) {
		for index := range actions {
			if actions[index].Key != nil {
				actions[index].Key.Value = value
			}
			update(actions[index].Actions)
		}
	}
	for _, actions := range spec.Events {
		update(actions)
	}
}
