package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestPackageStreamContainerRetentionBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.BufferBytes = 256
	state, err := newPackageStreamValidation(containerStreamContract(t), Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true}}, limits)
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
	if !errors.As(err, &limit) || limit.Metric != "state_bytes" || state.snapshotBytes != before || len(state.itemSnapshots) != 1 {
		t.Fatal("properties escaped the shared retention budget or committed on failure", err, state.snapshotBytes, len(state.itemSnapshots))
	}
}

func containerStreamContract(t *testing.T) *compiledPackageStreamContract {
	t.Helper()
	properties := readExpression("input", "/properties")
	count := Expression{Op: "length", Source: expressionPointer(readExpression("input", "/parts"))}
	message := ProtocolStreamActionSpec{Action: "item_finish", Kind: MessageNode, WireID: "/wireId", Role: "/role", Status: "/status", PropertiesPath: "/properties", Properties: &properties, ChildrenPath: "/parts", ChildrenCount: &count}
	part := ProtocolStreamActionSpec{Action: "item_verify", Kind: TextNode, Text: "/text", ParentID: "/parent", Ordinal: "/position", PropertiesPath: "/properties", Properties: &properties}
	partWithoutOrdinal := part
	partWithoutOrdinal.Ordinal = ""
	terminalPart := part
	terminalPart.ParentIDValue = expressionPointer(Expression{Op: "read", From: "binding", Name: "message", Path: "/id"})
	terminalPart.OrdinalValue = expressionPointer(Expression{Op: "index"})
	terminalMessage := message
	terminalMessage.VerifyCompleted = true
	contract, err := compilePackageStreamContract(&ProtocolStreamSpec{
		Discriminator: "/type", ResponseID: "/response", ItemID: "/id", ItemKinds: []NodeKind{MessageNode, TextNode},
		Events: map[string]ProtocolStreamEventSpec{
			"start":         {{Action: "response_start"}},
			"message":       {{Action: "item_start", Kind: MessageNode, WireID: "/wireId", Role: "/role", Status: "/status", PropertiesPath: "/properties", Properties: &properties}},
			"part":          {{Action: "item_start", Kind: TextNode, ParentID: "/parent", Ordinal: "/position", Text: "/text", PropertiesPath: "/properties", Properties: &properties}},
			"delta":         {{Action: "text_delta", ParentID: "/parent", Text: "/text"}},
			"seal":          {{Action: "text_snapshot", ParentID: "/parent", Text: "/text", SealText: true}},
			"close-part":    {{Action: "item_finish", ParentID: "/parent", Text: "/text", PropertiesPath: "/properties", Properties: &properties}},
			"verify-part":   {partWithoutOrdinal},
			"close-message": {{ForEach: "/parts", Actions: []ProtocolStreamActionSpec{part}}, message},
			"usage":         {{Action: "usage", Usage: &ProtocolStreamUsageSpec{Total: "/total"}}},
			"finish":        {{ForEach: "/messages", As: "message", IndexAs: "message-position", Actions: []ProtocolStreamActionSpec{{ForEach: "/parts", Actions: []ProtocolStreamActionSpec{terminalPart}}, terminalMessage}}, {Action: "response_finish", RequireItems: []NodeKind{MessageNode}}},
		},
	}, "/stream", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func consumeContainerFrames(t *testing.T, frames []string) (*packageStreamValidation, error) {
	t.Helper()
	state, err := newPackageStreamValidation(containerStreamContract(t), Target{Direction: DecodeEvent, Capabilities: CapabilitySet{TextCapability: true, UsageCapability: true}}, DefaultLimits())
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
