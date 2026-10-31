package protocol

import (
	"fmt"
	"slices"
)

type compiledWireStateUpdate struct {
	slot    int
	program *compiledOutputPlan
}

func (compiler *packageStreamCompiler) compileCheckAction(spec ProtocolStreamActionSpec, path string) (compiledPackageStreamEvent, error) {
	event := compiledPackageStreamEvent{}
	if spec.Kind != "" || spec.ResponseID != "" || spec.ItemID != "" || spec.ItemIDValue != nil || spec.Text != "" || spec.Arguments != "" || spec.ArgumentsValue != nil || spec.Tool != nil || spec.Error != "" || spec.Usage != nil || spec.Implicit || spec.SparseIdentity || spec.AllowBeforeStart || hasStreamItemSelectors(spec) {
		return event, fmt.Errorf("%s: validation actions cannot declare lifecycle payloads", path)
	}
	if spec.Action == "state_update" {
		if len(spec.Updates) == 0 || len(spec.Assertions) != 0 || compiler.contract.state == nil {
			return event, fmt.Errorf("%s: state_update requires declared slots and nonempty updates without assertions", path)
		}
		for _, name := range sortedKeys(spec.Updates) {
			at := path + "/updates/" + escapePointer(name)
			binding, exists := compiler.scope.stateSlots[name]
			if !exists {
				return event, fmt.Errorf("%s: update requires a declared state slot", at)
			}
			program, err := compiler.expressions.compilePlan(spec.Updates[name], at, compiler.scope, 1)
			if err != nil {
				return event, err
			}
			if !isAssignable(program.expression.result, binding.result) && !(program.expression.result == NullType && binding.schema.Nullable) {
				return event, fmt.Errorf("%s: update result must match its state slot schema", at)
			}
			event.updates = append(event.updates, compiledWireStateUpdate{slot: binding.slot, program: program})
			compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, program.bindingSlots)
		}
		event.isStateUpdate = true
		return event, nil
	}
	if spec.Updates != nil {
		return event, fmt.Errorf("%s/updates: updates require a state_update action", path)
	}
	if len(spec.Assertions) == 0 {
		return event, fmt.Errorf("%s/assertions: assert requires a nonempty assertion list", path)
	}
	var err error
	event.assertions, err = compileWireAssertionsInScope(spec.Assertions, path+"/assertions", compiler.scope, compiler.expressions, 1)
	if err != nil {
		return event, err
	}
	event.isAssertion = true
	compiler.contract.bindingSlots = max(compiler.contract.bindingSlots, assertionBindingSlots(event.assertions))
	return event, nil
}

func assertionBindingSlots(assertions []compiledWireAssertion) int {
	slots := 0
	for _, assertion := range assertions {
		for _, plan := range []*compiledOutputPlan{assertion.when, assertion.check, assertion.reasonPlan} {
			if plan != nil {
				slots = max(slots, plan.bindingSlots)
			}
		}
		slots = max(slots, assertionBindingSlots(assertion.each))
	}
	return slots
}

func (frame *streamActionFrame) stageSlots(updates []compiledWireStateUpdate, input evaluation) error {
	if cap(frame.slotUpdates) < len(updates) {
		frame.slotUpdates = make([]Value, len(updates))
	} else {
		frame.slotUpdates = frame.slotUpdates[:len(updates)]
	}
	defer clear(frame.slotUpdates)
	bytes := frame.slotBytes
	for index, update := range updates {
		slot := frame.state.contract.state.slots[update.slot]
		value, err := evaluatePackageStateSlot(slot, update.program, frame.slots[update.slot], input)
		if err != nil {
			return err
		}
		frame.slotUpdates[index] = value
		bytes += len(value.raw) - len(frame.slots[update.slot].raw)
	}
	if err := frame.state.checkRetainedStateWithSlots(frame.state.identityBytes, frame.state.snapshotBytes, bytes, "/state"); err != nil {
		return err
	}
	if err := input.ctx.Err(); err != nil {
		return err
	}
	if !frame.hasStagedSlots {
		frame.slots = slices.Clone(frame.slots)
	}
	for index, update := range updates {
		frame.slots[update.slot] = frame.slotUpdates[index]
	}
	frame.slotBytes, frame.hasStagedSlots = bytes, true
	frame.state.pendingSlotBytes = bytes
	frame.state.hasPendingSlots = true
	return nil
}
