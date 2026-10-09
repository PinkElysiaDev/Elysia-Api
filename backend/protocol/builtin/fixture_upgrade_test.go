package builtin

import (
	p "github.com/elysia-api/backend/protocol"
	"testing"
)

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
