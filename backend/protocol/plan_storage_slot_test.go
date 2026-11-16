package protocol

import "testing"

func TestPlanStorageSlotsKeepIndependentTables(t *testing.T) {
	const bindingValue, localValue, initialCount, initialPosition = 100, 10, 30, 20
	document := indexedEventPackage(t)
	mapping := document.Mapping.Directions[DecodeEvent]
	for name, initial := range map[string]int{"count": initialCount, "nextItem": initialPosition} {
		slot := mapping.State[name]
		slot.Initial = coreInteger(int64(initial))
		mapping.State[name] = slot
	}
	persistentPosition := mapping.Program.Body.Fields["index"]
	key := literalExpression("same-key")
	localRead := Expression{Op: "recall", Name: "local", Policy: "reject", Source: &key}
	mapping.Program.Body.Fields["index"] = Expression{Op: "integer", Operation: "add", Items: []Expression{
		bindingTestRead("outer", ""), readExpression("state", "/count"), localRead, persistentPosition,
	}}
	body := mapping.Program
	remember := Expression{Op: "remember", Name: "local", Source: &key, Body: expressionPointer(literalExpression(localValue))}
	local := Expression{Op: "with_index", Name: "local", KeyMode: "raw_json", Policy: "last", Body: &Expression{
		Op: "let", Name: "stored", Source: &remember, Body: &body,
	}}
	mapping.Program = Expression{Op: "let", Name: "outer", Source: expressionPointer(literalExpression(bindingValue)), Body: &local}
	document.Mapping.Directions[DecodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	stateKey := executionStateKey{compiled: compiled, direction: DecodeEvent}
	for _, fixture := range []struct {
		wire  string
		index int
	}{
		{`{"kind":"item.started","response_id":"r","item_id":"a","seq":0}`, bindingValue + localValue + initialCount + initialPosition},
		{`{"kind":"item.delta","response_id":"r","item_id":"a","seq":1,"text":"x"}`, bindingValue + localValue + initialCount + initialPosition + 1},
		{`{"kind":"item.started","response_id":false,"item_id":"b","seq":2}`, -1},
		{`{"kind":"item.started","response_id":"r","item_id":"b","seq":2}`, bindingValue + localValue + initialCount + initialPosition + 3},
	} {
		previous := options.State.packageStates[stateKey]
		events, err := compiled.DecodeEvents(t.Context(), parseTestValue(t, fixture.wire), options)
		if fixture.index < 0 {
			if err == nil || options.State.packageStates[stateKey] != previous {
				t.Fatal("failed mixed-table evaluation committed state", err)
			}
			continue
		}
		if err != nil || len(events) != 1 || events[0].Index == nil || *events[0].Index != fixture.index {
			t.Fatal("binding, state, local index and persistent index slots crossed tables", events, err)
		}
	}
}
