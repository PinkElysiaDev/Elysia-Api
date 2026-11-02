package protocol

import "testing"

func TestPackagePositionIntervals(t *testing.T) {
	key := expressionPointer(readExpression("input", "/item_id"))
	appendPosition := Expression{Op: "track_position", Name: "text", Source: key, Body: expressionPointer(readExpression("input", "/length"))}
	project := Expression{Op: "project_range", Name: "text", Source: key, Body: expressionPointer(readExpression("input", "/range"))}
	program := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/length"))}), Then: &appendPosition, Otherwise: &project}
	compiled := streamPrimitivePackage(t, Expression{Op: "stringify_json", Source: &program})
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, fixture := range []struct {
		fields map[string]any
		want   string
	}{
		{map[string]any{"length": 0}, "0"},
		{map[string]any{"range": map[string]int{"start": 0, "end": 0}}, `{"end":0,"start":0}`},
		{map[string]any{"length": 2}, "2"},
		{map[string]any{"length": 1}, "3"},
		{map[string]any{"item_id": "other", "length": 2}, "5"},
		{map[string]any{"length": 2}, "7"},
		{map[string]any{"item_id": "other", "range": map[string]int{"start": 0, "end": 2}}, `{"end":5,"start":3}`},
		{map[string]any{"range": map[string]int{"start": 3, "end": 5}}, `{"end":7,"start":5}`},
	} {
		if output, err := decodePrimitiveText(t, compiled, options, fixture.fields); err != nil || output != fixture.want {
			t.Fatal(fixture.fields, output, err)
		}
	}
	for _, fields := range []map[string]any{
		{"length": 100, "kind": "invalid"},
		{"length": -1},
		{"range": map[string]int{"start": 2, "end": 4}},
		{"range": map[string]int{"start": 0, "end": 6}},
		{"range": map[string]int{"start": 4, "end": 2}},
		{"item_id": "unknown", "range": map[string]int{"start": 0, "end": 1}},
	} {
		if _, err := decodePrimitiveText(t, compiled, options, fields); err == nil {
			t.Fatal("invalid update or range was accepted", fields)
		}
	}
	retainedBytes := options.State.packageStateBytes
	for range 1024 {
		if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"length": 1}); err != nil {
			t.Fatal(err)
		}
	}
	state := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}].primitives
	tracker := state.positions[streamTrackKey{channel: "text", item: "i"}]
	if len(tracker.spans) != 2 || tracker.length != 1029 || state.positionEnds["text"] != 1031 || state.positionItems != 3 || options.State.packageStateBytes != retainedBytes {
		t.Fatal("failed update advanced state or contiguous appends grew retained state", tracker, state.positionEnds, state.positionItems)
	}
	// The budget covers identities and actual intervals, not frame count.
	compiled.limits.StateItems = len(state.trackedItems) + state.positionItems
	if _, err := decodePrimitiveText(t, compiled, options, map[string]any{"item_id": "other", "length": 1}); err == nil {
		t.Fatal("interleaving exceeded the interval budget")
	}
	if state.positionEnds["text"] != 1031 || len(state.positions[streamTrackKey{channel: "text", item: "other"}].spans) != 1 {
		t.Fatal("budget failure committed interval state")
	}
}
