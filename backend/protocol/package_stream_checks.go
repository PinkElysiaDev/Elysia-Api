package protocol

import "fmt"

func (compiler *packageStreamCompiler) compileCheckAction(spec ProtocolStreamActionSpec, path string, depth int) (compiledPackageStreamEvent, error) {
	event := compiledPackageStreamEvent{}
	if spec.Kind != "" || spec.ResponseID != "" || spec.ItemID != "" || spec.ItemIDValue != nil || spec.Text != "" || spec.Arguments != "" || spec.Tool != nil || spec.Error != "" || spec.Usage != nil || spec.Implicit || spec.SparseIdentity || spec.AllowBeforeStart || hasStreamItemSelectors(spec) {
		return event, fmt.Errorf("%s: validation actions cannot declare lifecycle payloads", path)
	}
	if spec.Action == "state_update" {
		if depth != 1 || len(spec.Assertions) != 0 || compiler.contract.state == nil {
			return event, fmt.Errorf("%s: state_update requires declared slots and a root action without assertions", path)
		}
		event.isStateUpdate = true
		return event, nil
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

func (frame *streamActionFrame) stageSlots(input evaluation) error {
	if frame.hasStagedSlots {
		return frame.state.issue("/state", "stream state can update only once per frame", nil)
	}
	values, bytes, err := evaluatePackageStateSlots(frame.state.contract.state, frame.slots, input)
	if err != nil {
		return err
	}
	if err := frame.state.checkRetainedStateWithSlots(frame.state.identityBytes, frame.state.snapshotBytes, bytes, "/state"); err != nil {
		return err
	}
	if err := input.ctx.Err(); err != nil {
		return err
	}
	frame.slots, frame.slotBytes, frame.hasStagedSlots = values, bytes, true
	frame.state.pendingSlotBytes = bytes
	frame.state.hasPendingSlots = true
	return nil
}
