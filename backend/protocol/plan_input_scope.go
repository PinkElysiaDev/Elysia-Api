package protocol

import "fmt"

func (plan *compiledOutputPlan) buildInputScope(state evaluation) (plannedOutput, error) {
	selected, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if selected.output.IsZero() {
		return plannedOutput{}, fmt.Errorf("with_input requires a present value")
	}
	state.input, state.coreRequest = selected.output.value, nil
	var reader *planObjectReader
	if selected.output.node == nil && plan.body.rootObjectReads >= objectReuseReads && selected.output.value.IsObject() {
		reader = &planObjectReader{root: spanIdentity(selected.output.value.raw), parent: state.objectReader}
		state.objectReader = reader
	}
	state.inputReader = func(pointer []string) (JSONOutput, error) {
		return readOutputPointerWithObjectReader(selected.output, pointer, state.valueReader, state.objectReader)
	}
	if state.capture != nil {
		capture := *state.capture
		capture.input, capture.hasInputScope = nativeReadLocation(plan.source.expression, state.capture), true
		state.capture = &capture
	}
	output, err := plan.body.build(state)
	reader.releaseItem(err)
	return output, err
}
