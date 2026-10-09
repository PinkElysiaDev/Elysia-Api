package builtin

import (
	"encoding/json"
	p "github.com/elysia-api/backend/protocol"
	"os"
	"testing"
)

func TestRepairResponsesMessageCompletionOracles(t *testing.T) {
	for _, version := range []string{"dev28", "dev29"} {
		t.Run(version, func(t *testing.T) { repairResponsesMessageCompletionOracles(t, version) })
	}
}

func repairResponsesMessageCompletionOracles(t *testing.T, version string) {
	raw, err := os.ReadFile("testdata/responses-" + version + "-message-samples.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []p.Sample
	if err = json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	definition := shippedProjectionProtocol(t, Responses).Definition()
	definition.ID = "existing-custom-responses"
	for i, sample := range definition.Samples {
		for _, old := range samples {
			if old.ID == sample.ID {
				definition.Samples[i] = old
			}
		}
	}
	repaired, changed := RepairOutputFixtures(definition)
	if !changed {
		t.Fatal("old lifecycle evidence was not repaired")
	}
	if _, changed := RepairOutputFixtures(repaired); changed {
		t.Fatal("repair is not idempotent")
	}
	compiler, _ := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	data, _ := json.Marshal(repaired)
	compiled, issues := compiler.Compile(data)
	if compiled == nil {
		t.Fatal(issues)
	}
	if report := p.Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	for i, before := range definition.Samples {
		if before.ID != "stream-text-decode" && before.ID != "stream-native-decode" {
			sameJSON(t, repaired.Samples[i].Expected.Bytes(), string(before.Expected.Bytes()))
		}
	}
	for _, mapping := range []p.Mapping{{Module: Responses, After: &p.Expression{Op: "read"}}, {Transform: &p.Expression{Op: "read"}}} {
		definition.Directions[p.DecodeEvent] = mapping
		if _, changed := RepairOutputFixtures(definition); changed {
			t.Fatal("guessed custom decoder semantics")
		}
	}
	changedSample := samples[0]
	changedSample.Expected = testValue(t, `[]`)
	if _, changed := repairResponsesMessageOracle(changedSample); changed {
		t.Fatal("rewrote user expectation")
	}
}

func TestRepairLegacyChatToolIdentityFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/chat-82406b9.json")
	if err != nil {
		t.Fatal(err)
	}
	var d p.Definition
	if err = json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	fixed, changed := RepairOutputFixtures(d)
	if !changed {
		t.Fatal("legacy generated oracle not repaired")
	}
	if _, changed = RepairOutputFixtures(fixed); changed {
		t.Fatal("repair not idempotent")
	}
	for i, before := range d.Samples {
		if before.ID != "stream-tool-encode" && before.Expected != fixed.Samples[i].Expected {
			t.Fatal("unrelated fixture changed", before.ID)
		}
	}
	for _, mapping := range []p.Mapping{{Module: Chat, After: &p.Expression{Op: "read"}}, {Transform: &p.Expression{Op: "read"}}} {
		d.Directions[p.EncodeEvent] = mapping
		if _, changed = RepairOutputFixtures(d); changed {
			t.Fatal("rewrote custom mapping expectation")
		}
	}
}

func TestRepairFixturesKeepsNativeReplayAssertions(t *testing.T) {
	for _, module := range []string{Chat, Responses} {
		for _, input := range []string{
			`[{"native":{"value":{}}}]`,
			`[{"item":{"native":{"value":{}}}}]`,
			`[{"response":{"content":[{"children":[{"native":{"value":{}}}]}]}}]`,
		} {
			expected := testValue(t, `[{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"f"}}]}}]},{"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{}"}}]}}]},{"type":"response.content_part.done","part":{"type":"output_text","text":"OK"}}]`)
			d := p.Definition{Directions: map[p.Direction]p.Mapping{p.EncodeEvent: {Module: module}}, Samples: []p.Sample{{Direction: p.EncodeEvent, Sequence: true, Input: testValue(t, input), Expected: expected}}}
			fixed, changed := RepairOutputFixtures(d)
			if changed || fixed.Samples[0].Expected != expected {
				t.Fatal("native replay assertion rewritten", module, input)
			}
		}
	}
}

func TestRepairOutputFixtureOnlyKnownEncoderExpectation(t *testing.T) {
	raw := testValue(t, `{"object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]},{"type":"function_call","arguments":"{\"type\":\"output_text\"}"}]}`)
	d := p.Definition{Directions: map[p.Direction]p.Mapping{p.EncodeResponse: {Module: Responses}}, Samples: []p.Sample{{Direction: p.EncodeResponse, Expected: raw}}}
	fixed, changed := RepairOutputFixtures(d)
	if !changed || fixed.Samples[0].Expected == raw {
		t.Fatal("missing correction")
	}
	if d.Samples[0].Expected != raw {
		t.Fatal("original revised in place")
	}
	f, _ := fixed.Samples[0].Expected.ReadObject()
	var output []p.Value
	_ = f["output"].Decode(&output)
	tool, _ := output[1].ReadObject()
	if tool["arguments"] != p.StringValue(`{"type":"output_text"}`) {
		t.Fatal("changed tool payload")
	}
	if _, changed := RepairOutputFixtures(fixed); changed {
		t.Fatal("repair not idempotent")
	}
	d.Directions[p.EncodeResponse] = p.Mapping{Module: Responses, After: &p.Expression{Op: "read"}}
	if _, changed := RepairOutputFixtures(d); changed {
		t.Fatal("rewrote user after mapping expectation")
	}
	d.Directions[p.EncodeResponse] = p.Mapping{Transform: &p.Expression{Op: "read"}}
	if _, changed := RepairOutputFixtures(d); changed {
		t.Fatal("guessed custom encoder semantics")
	}
}

func TestRepairOutputFixtureUpdatesGeneratedLifecycleStatus(t *testing.T) {
	d := p.Definition{Directions: map[p.Direction]p.Mapping{p.EncodeEvent: {Module: Responses}}, Samples: []p.Sample{{Direction: p.EncodeEvent, Expected: testValue(t, `[{"type":"response.output_item.added","item":{"type":"function_call","id":"t"}},{"type":"response.output_item.done","item":{"type":"function_call","id":"t"}}]`)}}}
	fixed, changed := RepairOutputFixtures(d)
	if !changed {
		t.Fatal("missing lifecycle correction")
	}
	var frames []p.Value
	_ = fixed.Samples[0].Expected.Decode(&frames)
	for i, name := range []string{"in_progress", "completed"} {
		f, _ := frames[i].ReadObject()
		item, _ := f["item"].ReadObject()
		if item["status"] != p.StringValue(name) {
			t.Fatal(f)
		}
	}
}
