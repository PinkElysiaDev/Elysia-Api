package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func arrayTrackerItems(t *testing.T, raw string) []JSONOutput {
	t.Helper()
	value, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	items, err := plannedArrayItems(JSONLeaf(value))
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestStreamArrayDigestUsesExactJSONSemantics(t *testing.T) {
	for _, fixture := range []struct {
		name, first, snapshot string
		isEqual               bool
	}{
		{"object-and-string", `[{"n":9007199254740993,"s":"u"}]`, `[{"s":"\u0075","n":9007199254740993.0}]`, true},
		{"duplicate-key", `[{"u":1,"\u0075":2}]`, `[{"u":2.00}]`, true},
		{"zeros-and-scale", `[0,-0.0e9999999999999999,1.00e+000]`, `[0e-9999999999999999,0,100e-2]`, true},
		{"large-exponent", `[1e999999999999999999999999999999]`, `[10e999999999999999999999999999998]`, true},
		{"negative-exponent", `[1000e-1000000000]`, `[1e-999999997]`, true},
		{"different-large-integer", `[9007199254740993]`, `[9007199254740992]`, false},
		{"member-boundaries", `[["a","bc"]]`, `[["ab","c"]]`, false},
		{"scalar-types", `["1"]`, `[1]`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			original, _, err := (streamArrayTracker{}).update(t.Context(), arrayTrackerItems(t, fixture.first), false)
			if err != nil {
				t.Fatal(err)
			}
			current, offset, err := original.update(t.Context(), arrayTrackerItems(t, fixture.snapshot), true)
			if fixture.isEqual {
				if err != nil || current != original || offset != original.count {
					t.Fatal("equivalent JSON changed the delivered prefix", current, offset, err)
				}
			} else if err == nil || current != original {
				t.Fatal("changed JSON was accepted or mutated the tracker", current, err)
			}
		})
	}
}

func TestPackageArrayTrackerTransactionsAndRetention(t *testing.T) {
	delta := Expression{Op: "track_array", Name: "probabilities", Source: expressionPointer(readExpression("input", "/item_id")), Body: expressionPointer(readExpression("input", "/value")), Policy: "delta"}
	snapshot := delta
	snapshot.Policy = "snapshot"
	selected := Expression{Op: "if", When: expressionPointer(readExpression("input", "/snapshot")), Then: &snapshot, Otherwise: &delta}
	compiled := streamPrimitivePackage(t, Expression{Op: "stringify_json", Source: &selected})
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	apply := func(value string, isSnapshot, isInvalid bool) (string, error) {
		fields := map[string]any{"value": json.RawMessage(value), "snapshot": isSnapshot}
		if isInvalid {
			fields["kind"] = "not-an-event"
		}
		return decodePrimitiveText(t, compiled, options, fields)
	}
	if output, err := apply(`[1,{"s":"u"}]`, false, false); err != nil || output != `[1,{"s":"u"}]` {
		t.Fatal(output, err)
	}
	retainedBytes := options.State.packageStateBytes
	for _, fixture := range []struct {
		value      string
		isSnapshot bool
		isInvalid  bool
	}{
		{`[9]`, false, true},
		{`[1]`, true, false},
		{`[2,{"s":"u"}]`, true, false},
		{`null`, false, false},
	} {
		if _, err := apply(fixture.value, fixture.isSnapshot, fixture.isInvalid); err == nil {
			t.Fatal("invalid update was accepted", fixture)
		}
	}
	if output, err := apply(`[1.0,{"s":"\u0075"},false]`, true, false); err != nil || output != `[false]` {
		t.Fatal("failed event advanced the prefix", output, err)
	}
	if output, err := apply(`[1,{"s":"u"},false]`, true, false); err != nil || output != `[]` {
		t.Fatal("snapshot repeated delivered members", output, err)
	}
	const largeMemberBytes = 256 << 10
	if _, err := apply(`["`+strings.Repeat("x", largeMemberBytes)+`"]`, false, false); err != nil {
		t.Fatal(err)
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}]
	tracker := retained.primitives.arrays[streamTrackKey{channel: "probabilities", item: "i"}]
	if tracker.count != 4 || options.State.packageStateBytes != retainedBytes || len(retained.primitives.arrays) != 1 {
		t.Fatal("array tracking retained payload or changed failed transactions", tracker.count, options.State.packageStateBytes, retainedBytes)
	}
}
