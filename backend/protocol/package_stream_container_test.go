package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestPackageStreamContainerRetentionBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 256
	state, err := newPackageStreamValidation(containerStreamContract(t, "reject"), Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"start","response":"r"}`,
		`{"type":"message","response":"r","id":"m","wireId":"wire-m","role":"assistant","properties":{"phase":"` + strings.Repeat("x", 180) + `"}}`,
	} {
		frame, _ := ParseValue([]byte(raw))
		if err := state.consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
	}
	before := state.snapshotBytes
	frame, _ := ParseValue([]byte(`{"type":"part","response":"r","id":"p","parent":"m","position":0,"text":"","properties":{"detail":"` + strings.Repeat("y", 80) + `"}}`))
	err = state.consume(t.Context(), frame)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "state_bytes" || state.snapshotBytes != before || state.entityCount != 1 {
		t.Fatal("properties escaped the shared retention budget or committed on failure", err, state.snapshotBytes, state.entityCount)
	}
}

func containerStreamContract(t *testing.T, openEntities string) *compiledPackageStreamContract {
	t.Helper()
	field := func(path string) *ProtocolStreamValueSpec { return &ProtocolStreamValueSpec{Path: path} }
	properties := readExpression("input", "/properties")
	messageProperties := Expression{Op: "merge", Policy: "last", Items: []Expression{properties, objectExpression(map[string]Expression{"status": readExpression("input", "/status")})}}
	count := Expression{Op: "length", Source: expressionPointer(readExpression("input", "/parts"))}
	identities := map[string]ProtocolStreamValueSpec{"id": {Path: "/wireId"}, "role": {Path: "/role"}}
	message := ProtocolStreamActionSpec{Action: "entity_finish", Entity: "message", Key: field("/id"), Identities: identities, Properties: &ProtocolStreamValueSpec{Path: "/properties", Value: &messageProperties}, ChildrenCount: &ProtocolStreamValueSpec{Path: "/parts", Value: &count}, ChildrenNamespace: "part"}
	part := ProtocolStreamActionSpec{Action: "entity_verify", Entity: "text", Key: field("/id"), Payload: field("/text"), Parent: field("/parent"), Ordinal: field("/position"), Properties: field("/properties")}
	partWithoutOrdinal := part
	partWithoutOrdinal.Ordinal = nil
	terminalPart := part
	terminalPart.Parent = &ProtocolStreamValueSpec{Path: "/parent", Value: expressionPointer(Expression{Op: "read", From: "binding", Name: "message", Path: "/id"})}
	terminalPart.Ordinal = &ProtocolStreamValueSpec{Path: "/position", Value: expressionPointer(Expression{Op: "index"})}
	terminalMessage := message
	terminalMessage.VerifyCompleted = true
	messageStart := message
	messageStart.Action = "entity_start"
	messageStart.ChildrenCount = nil
	messageStart.ChildrenNamespace = ""
	messageSnapshot := messageStart
	messageSnapshot.Action = "entity_snapshot"
	partStart := part
	partStart.Action = "entity_start"
	contract, err := compilePackageStreamContract(&ProtocolStreamSpec{
		Discriminator: "/type", ResponseID: "/response", Lifecycle: ProtocolStreamLifecycleSpec{EOF: "require_terminal", OpenEntities: openEntities},
		Namespaces: map[string]ProtocolStreamNamespaceSpec{"message": {KeyType: StringType}, "part": {KeyType: StringType}},
		Entities: map[string]ProtocolStreamEntitySpec{
			"message": {Namespace: "message", Payload: "none", Identities: map[string]ProtocolStreamEntityIdentitySpec{"id": {Type: StringType, RequiredAt: "start"}, "role": {Type: StringType, RequiredAt: "start"}}},
			"text":    {Namespace: "part", Payload: "text", ParentNamespace: "message"},
		},
		Events: map[string]ProtocolStreamEventSpec{
			"start": {{Action: "stream_start"}}, "message": {messageStart}, "message-snapshot": {messageSnapshot}, "part": {partStart},
			"finish-open":   {{Action: "stream_finish"}},
			"delta":         {{Action: "entity_append", Entity: "text", Key: field("/id"), Parent: field("/parent"), Payload: field("/text")}},
			"seal":          {{Action: "entity_snapshot", Entity: "text", Key: field("/id"), Parent: field("/parent"), Payload: field("/text"), SealPayload: true}},
			"close-part":    {{Action: "entity_finish", Entity: "text", Key: field("/id"), Parent: field("/parent"), Payload: field("/text"), Properties: field("/properties")}},
			"verify-part":   {partWithoutOrdinal},
			"close-message": {{ForEach: "/parts", Actions: []ProtocolStreamActionSpec{part}}, message},
			"usage":         {{Action: "assert", Assertions: []WireAssertion{{Path: "/total", Schema: &ValueSchema{Type: IntegerType}, Code: UpstreamContractViolation, Reason: "total must be an integer"}}}, {Action: "observe"}},
			"finish":        {{ForEach: "/messages", As: "message", IndexAs: "message-position", Actions: []ProtocolStreamActionSpec{{ForEach: "/parts", Actions: []ProtocolStreamActionSpec{terminalPart}}, terminalMessage}}, {Action: "stream_finish", RequireVerified: []string{"message"}}},
		},
	}, "/stream", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func consumeContainerFrames(t *testing.T, frames []string) (*packageStreamValidation, error) {
	t.Helper()
	state, err := newPackageStreamValidation(containerStreamContract(t, "reject"), Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range frames {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := state.consume(t.Context(), value); err != nil {
			return state, err
		}
	}
	return state, nil
}

func TestPackageStreamContainerSnapshots(t *testing.T) {
	start := []string{
		`{"type":"start","response":"r"}`,
		`{"type":"message","response":"r","id":"m","wireId":"wire-m","role":"assistant","status":"in_progress","properties":{"phase":"commentary"}}`,
		`{"type":"part","response":"r","id":"p","parent":"m","position":0,"text":"","properties":{"annotations":[]}}`,
		`{"type":"delta","response":"r","id":"p","parent":"m","text":"hello"}`,
	}
	seal := `{"type":"seal","response":"r","id":"p","parent":"m","text":"hello"}`
	closePart := `{"type":"close-part","response":"r","id":"p","parent":"m","text":"hello","properties":{"annotations":[]}}`
	verifyPart := strings.Replace(closePart, "close-part", "verify-part", 1)
	message := `"id":"m","wireId":"wire-m","role":"assistant","status":"completed","properties":{"phase":"final_answer"},"parts":[{"id":"p","parent":"m","position":0,"text":"hello","properties":{"annotations":[]}}]`
	closeMessage := `{"type":"close-message","response":"r",` + message + `}`
	terminal := `{"type":"finish","response":"r","messages":[{` + message + `}]}`
	for _, explicitDone := range []bool{false, true} {
		frames := append([]string{}, start...)
		frames = append(frames, seal, closePart)
		if explicitDone {
			frames = append(frames, closeMessage)
		}
		frames = append(frames, terminal)
		state, err := consumeContainerFrames(t, frames)
		if err != nil || state.finish() != nil || state.openItems != 0 {
			t.Fatal("valid child/container completion failed", explicitDone, err)
		}
	}
	// Normal text parts may close without a separate text seal.
	if _, err := consumeContainerFrames(t, append(append([]string{}, start...), closePart, verifyPart, terminal)); err != nil {
		t.Fatal("required an undeclared text.done barrier", err)
	}
	for _, fixture := range []struct {
		name   string
		frames []string
	}{
		{"delta-after-seal", append(append([]string{}, start...), seal, start[3])},
		{"seal-twice", append(append([]string{}, start...), seal, seal)},
		{"suffix-after-seal", append(append([]string{}, start...), seal, strings.Replace(closePart, "hello", "hello!", 1))},
		{"noncontiguous-child", append(append([]string{}, start[:2]...), strings.Replace(start[2], `"position":0`, `"position":1`, 1))},
		{"finish-before-child", append(append([]string{}, start...), closeMessage)},
		{"explicit-finish-twice", append(append([]string{}, start...), closePart, closeMessage, closeMessage)},
		{"terminal-omits-message", append(append([]string{}, start...), closePart, closeMessage, `{"type":"finish","response":"r","messages":[]}`)},
		{"terminal-text-changed", append(append([]string{}, start...), closePart, closeMessage, strings.Replace(terminal, "hello", "hello!", 1))},
		{"terminal-property-changed", append(append([]string{}, start...), closePart, closeMessage, strings.Replace(terminal, "final_answer", "commentary", 1))},
		{"terminal-child-property-changed", append(append([]string{}, start...), closePart, closeMessage, strings.Replace(terminal, `"annotations":[]`, `"annotations":[1]`, 1))},
		{"verify-missing-parent-without-ordinal", append(append([]string{}, start...), closePart, strings.Replace(verifyPart, `"parent":"m"`, `"parent":"missing"`, 1))},
		{"verify-changed-parent-without-ordinal", append(append([]string{}, start...), closePart, `{"type":"message","response":"r","id":"other","wireId":"wire-other","role":"assistant","properties":{}}`, strings.Replace(verifyPart, `"parent":"m"`, `"parent":"other"`, 1))},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if _, err := consumeContainerFrames(t, fixture.frames); err == nil {
				t.Fatal("accepted a changed or incomplete container")
			}
		})
	}
}

func TestPackageStreamContainerAutomaticCompletion(t *testing.T) {
	state, err := newPackageStreamValidation(containerStreamContract(t, "finish"), Target{Direction: DecodeEvent}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"start","response":"r"}`,
		`{"type":"message","response":"r","id":"m","wireId":"wire-m","role":"assistant","properties":{"phase":"initial"}}`,
		`{"type":"message-snapshot","response":"r","id":"m","properties":{"phase":"active"}}`,
		`{"type":"part","response":"r","id":"p","parent":"m","position":0,"text":"","properties":{}}`,
		`{"type":"delta","response":"r","id":"p","parent":"m","text":"hello"}`,
		`{"type":"finish-open","response":"r"}`,
	} {
		if err := state.consume(t.Context(), Value{raw: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.finish(); err != nil || state.openItems != 0 {
		t.Fatal("automatic completion did not close children before their parent", err)
	}
	for _, item := range state.entityOrder {
		if !item.isFinished {
			t.Fatal("terminal retained an open entity")
		}
	}
}
