package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureValue(t *testing.T, value any) Value {
	t.Helper()
	encoded, err := EncodeValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func textVerificationDefinition(t *testing.T, id string) Definition {
	t.Helper()
	definition := testDefinition(id, "engine", "history", "functions", "answer")
	definition.Capabilities = CapabilitySet{TextCapability: true}
	delete(definition.Directions, DecodeEvent)
	delete(definition.Directions, EncodeEvent)
	request := Request{SchemaVersion: 1, Model: StringValue("m"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}}
	response := Response{SchemaVersion: 1, ID: StringValue("r"), Status: StringValue("completed"), Content: []Node{{Kind: MessageNode, Role: StringValue("assistant"), Children: []Node{{Kind: TextNode, Payload: StringValue("answer")}}}}}
	wireRequest, _ := ParseValue([]byte(`{"engine":"m","history":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}]}]}`))
	wireResponse, _ := ParseValue([]byte(`{"request":"r","answer":[{"kind":"message","speaker":"assistant","parts":[{"kind":"text","payload":"answer"}]}]}`))
	canonicalRequest, canonicalResponse := fixtureValue(t, request), fixtureValue(t, response)
	definition.Samples = []Sample{
		{ID: "request.decode", Direction: DecodeRequest, Input: wireRequest, Expected: canonicalRequest},
		{ID: "request.encode", Direction: EncodeRequest, Input: canonicalRequest, Expected: wireRequest},
		{ID: "response.decode", Direction: DecodeResponse, Input: wireResponse, Expected: canonicalResponse},
		{ID: "response.encode", Direction: EncodeResponse, Input: canonicalResponse, Expected: wireResponse},
	}
	return definition
}

func TestVerifyTextOnlyProtocolAndActivationBinding(t *testing.T) {
	definition := textVerificationDefinition(t, "text-only")
	compiled := compileTestDefinition(t, definition)
	report := Verify(t.Context(), compiled)
	if !report.Passed {
		t.Fatalf("valid text-only definition: %+v", report.Issues)
	}
	if err := IssuesError(CanActivate(compiled, report)); err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 4 || len(report.Covered) != 1 || report.Covered[0] != TextCapability || report.Kind != OfflineVerification {
		t.Fatalf("wrong evidence: %+v", report)
	}
	definition.Version = "changed"
	changed := compileTestDefinition(t, definition)
	if IssuesError(CanActivate(changed, report)) == nil {
		t.Fatal("old report authorized a different definition")
	}
	online := report
	online.Kind = UpstreamVerification
	if IssuesError(CanActivate(compiled, online)) == nil {
		t.Fatal("online report bypassed offline prerequisite")
	}
	report.CompilerVersion = "older"
	if IssuesError(CanActivate(compiled, report)) == nil {
		t.Fatal("stale compiler evidence authorized activation")
	}
}

func TestVerifyRejectsDeclaredToolsWithoutLifecycle(t *testing.T) {
	definition := textVerificationDefinition(t, "false-tools")
	definition.Capabilities[FunctionToolsCapability] = true
	for index := range definition.Samples {
		definition.Samples[index].Capabilities = []Capability{FunctionToolsCapability}
	}
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed || !containsVerificationCode(report, IncompleteCoverage) {
		t.Fatalf("unproven declaration passed: %+v", report)
	}
}

func TestVerifyFindsLossEvenWhenAuthorExpectedIt(t *testing.T) {
	definition := textVerificationDefinition(t, "lossy")
	encode := definition.Directions[EncodeRequest]
	encode.Transform.Fields["history"] = literalExpression([]any{})
	definition.Directions[EncodeRequest] = encode
	definition.Samples[1].Expected = fixtureValue(t, map[string]any{"engine": "m", "history": []any{}})
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed || !containsVerificationCode(report, VerificationMismatch) {
		t.Fatalf("self-consistent deletion passed: %+v", report)
	}
	hasRoundTripIssue := false
	for _, issue := range report.Issues {
		hasRoundTripIssue = hasRoundTripIssue || strings.HasPrefix(issue.Path, "/roundtrip")
	}
	if !hasRoundTripIssue {
		t.Fatalf("no semantic roundtrip evidence: %+v", report.Issues)
	}
}

func TestVerifyNegativeSampleCannotReplacePositiveCoverage(t *testing.T) {
	definition := textVerificationDefinition(t, "negative-only")
	definition.Samples = []Sample{{ID: "invalid", Direction: DecodeRequest, Input: fixtureValue(t, map[string]any{"history": false}), ExpectedIssue: InvalidInput}}
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed || !report.Checks[0].Passed || !containsVerificationCode(report, IncompleteCoverage) {
		t.Fatalf("negative sample authorized capability: %+v", report)
	}
}

func addEventVerification(t *testing.T, definition *Definition) {
	definition.Capabilities[UsageCapability] = true
	for direction, mapping := range definition.Directions {
		mapping.Capabilities = CapabilitySet{TextCapability: true}
		definition.Directions[direction] = mapping
	}
	decode := objectExpression(map[string]Expression{
		"type":   readExpression("input", "/event"),
		"itemId": readExpression("input", "/item"),
		"item":   readExpression("input", "/node"),
		"delta":  readExpression("input", "/delta"),
		"usage":  readExpression("input", "/usage"),
	})
	encode := objectExpression(map[string]Expression{
		"event": readExpression("input", "/type"),
		"item":  readExpression("input", "/itemId"),
		"node":  readExpression("input", "/item"),
		"delta": readExpression("input", "/delta"),
		"usage": readExpression("input", "/usage"),
	})
	definition.Directions[DecodeEvent] = Mapping{Transform: &decode}
	definition.Directions[EncodeEvent] = Mapping{Transform: &encode}
	events := []Event{
		{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("i"), Item: &Node{Kind: TextNode}},
		{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("i"), Delta: StringValue("hello")},
		{SchemaVersion: 1, Type: ResponseFinished},
		{SchemaVersion: 1, Type: UsageUpdated, Usage: &Usage{Input: &Counter{Count: 100, Origin: ObservedCount}, CacheRead: &Counter{Count: 40, Origin: ObservedCount}, Output: &Counter{Count: 0, Origin: ObservedCount}}},
	}
	wire, _ := ParseValue([]byte(`[{"event":"item.started","item":"i","node":{"kind":"text"}},{"event":"item.delta","item":"i","delta":"hello"},{"event":"response.finished"},{"event":"usage.updated","usage":{"input":{"count":100,"origin":"observed"},"cacheRead":{"count":40,"origin":"observed"},"output":{"count":0,"origin":"observed"}}}]`))
	definition.Samples = append(definition.Samples,
		Sample{ID: "stream.decode", Direction: DecodeEvent, Sequence: true, Input: wire, Expected: fixtureValue(t, events)},
		Sample{ID: "stream.encode", Direction: EncodeEvent, Sequence: true, Input: fixtureValue(t, events), Expected: wire},
	)
}

func TestVerifyEventSequenceAndUsageTail(t *testing.T) {
	definition := textVerificationDefinition(t, "event-sequence")
	addEventVerification(t, &definition)
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if !report.Passed {
		t.Fatalf("valid event sequence: %+v", report.Issues)
	}
	var frames []Value
	if err := definition.Samples[4].Input.Decode(&frames); err != nil {
		t.Fatal(err)
	}
	frames = append(frames[:2], frames[3:]...)
	definition.Samples[4].Input = fixtureValue(t, frames)
	report = Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed || !containsVerificationCode(report, UpstreamContractViolation) {
		t.Fatalf("missing terminal accepted: %+v", report.Issues)
	}
}

func TestVerifySamplesAndDirectionCapabilityHashes(t *testing.T) {
	definition := textVerificationDefinition(t, "hashes")
	first := compileTestDefinition(t, definition)
	definition.Samples[0].ID = "renamed"
	second := compileTestDefinition(t, definition)
	if first.SamplesHash() == second.SamplesHash() || first.Hash() == second.Hash() {
		t.Fatal("sample change did not invalidate report binding")
	}
	mapping := definition.Directions[EncodeRequest]
	mapping.Capabilities = CapabilitySet{}
	definition.Directions[EncodeRequest] = mapping
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"capabilities":{}`) {
		t.Fatal("explicit empty directional capability set was omitted")
	}
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed || !containsVerificationCode(report, UnsupportedCapability) {
		t.Fatalf("direction capability override ignored: %+v", report.Issues)
	}
}

func containsVerificationCode(report VerificationReport, code IssueCode) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestVerifyCombinationOfIndependentDefinitions(t *testing.T) {
	firstDefinition := textVerificationDefinition(t, "independent-a")
	addEventVerification(t, &firstDefinition)
	secondDefinition := textVerificationDefinition(t, "independent-b")
	addEventVerification(t, &secondDefinition)
	raw, err := json.Marshal(secondDefinition)
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.NewReplacer("engine", "deployment", "history", "turns", "answer", "reply", "speaker", "actor", "parts", "chunks").Replace(string(raw))
	if err := json.Unmarshal([]byte(renamed), &secondDefinition); err != nil {
		t.Fatal(err)
	}
	first, second := compileTestDefinition(t, firstDefinition), compileTestDefinition(t, secondDefinition)
	for _, compiled := range []*Compiled{first, second} {
		if report := Verify(t.Context(), compiled); !report.Passed {
			t.Fatalf("individual definition failed: %+v", report.Issues)
		}
	}
	for _, pair := range [][2]*Compiled{{first, second}, {second, first}} {
		report := VerifyCombination(t.Context(), pair[0], pair[1])
		if !report.Passed || len(report.Checks) < 3 || report.SourceHash != pair[0].Hash() || report.TargetHash != pair[1].Hash() {
			t.Fatalf("independent composition: %+v", report)
		}
	}
}

func addToolLifecycle(t *testing.T, definition *Definition) {
	definition.Capabilities[FunctionToolsCapability] = true
	for _, direction := range []Direction{DecodeResponse, EncodeResponse} {
		mapping := definition.Directions[direction]
		mapping.Capabilities = CapabilitySet{TextCapability: true}
		definition.Directions[direction] = mapping
	}
	arguments := fixtureValue(t, map[string]any{"query": "data"})
	schema := fixtureValue(t, map[string]any{"type": "object"})
	request := Request{SchemaVersion: 1, Model: StringValue("m"), Tools: []Tool{{Kind: FunctionTool, Name: StringValue("lookup"), InputSchema: schema}}, Content: []Node{
		{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}},
		{Kind: ToolCallNode, Name: StringValue("lookup"), CallID: StringValue("c"), Input: &ToolInput{Kind: JSONInput, Value: arguments}},
		{Kind: ToolResultNode, CallID: StringValue("c"), Payload: StringValue("found")},
	}}
	wire, _ := ParseValue([]byte(`{"engine":"m","history":[{"kind":"message","speaker":"user","parts":[{"kind":"text","payload":"hello"}]},{"kind":"tool_call","operation":"lookup","call":"c","input":{"kind":"json","value":{"query":"data"}}},{"kind":"tool_result","call":"c","result":"found"}],"functions":[{"operation":"lookup","schema":{"type":"object"}}]}`))
	definition.Samples = append(definition.Samples,
		Sample{ID: "tools.decode", Direction: DecodeRequest, Input: wire, Expected: fixtureValue(t, request)},
		Sample{ID: "tools.encode", Direction: EncodeRequest, Input: fixtureValue(t, request), Expected: wire},
	)
}

func TestVerifyFullToolHistoryAndUnsupportedComposition(t *testing.T) {
	definition := textVerificationDefinition(t, "tool-lifecycle")
	addToolLifecycle(t, &definition)
	compiled := compileTestDefinition(t, definition)
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatalf("valid lifecycle rejected: %+v", report.Issues)
	}
	textOnly := compileTestDefinition(t, textVerificationDefinition(t, "text-target"))
	report := VerifyCombination(t.Context(), compiled, textOnly)
	if report.Passed {
		t.Fatal("tools were silently removed for text-only upstream")
	}
	hasUnsupported := false
	for _, issue := range report.Issues {
		hasUnsupported = hasUnsupported || issue.Code == UnsupportedCapability
	}
	if !hasUnsupported {
		t.Fatalf("missing unsupported-capability evidence: %+v", report.Issues)
	}
}

func TestVerifyNativeExtensionProbe(t *testing.T) {
	definition := textVerificationDefinition(t, "native-proof")
	definition.Native.Preserve = true
	definition.Capabilities[NativeExtensionsCapability] = true
	report := Verify(t.Context(), compileTestDefinition(t, definition))
	if !report.Passed {
		t.Fatalf("native replay failed probe: %+v", report.Issues)
	}
	mapping := definition.Directions[DecodeRequest]
	mapping.Input = &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"engine": {Type: StringType}, "history": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}}, "functions": {Type: ArrayType, Items: &ValueSchema{Type: AnyType}}}}
	definition.Directions[DecodeRequest] = mapping
	report = Verify(t.Context(), compileTestDefinition(t, definition))
	if report.Passed {
		t.Fatal("native extension support passed while rejecting extensions")
	}
}
