package protocol

import "fmt"

// WithOutputStream binds the actual target wire validator for read-only runtime
// facts. Identity, direction and scope must match this mapping. Required position
// reads reject an absent binding; optional item lookups remain missing. Carrying
// this context through another mapping does not grant access.
func (options EvaluationContext) WithOutputStream(validation *WireStreamValidation) EvaluationContext {
	options.outputStream = validation
	return options
}

// CommitDelivery records successful delivery of the last accepted output batch.
// The single owner calls it only after all writes and their Flush succeed, or
// after explicitly simulating successful delivery during offline verification.
// It does not check cancellation: a successful Flush has already happened.
func (validation *WireStreamValidation) CommitDelivery() error {
	if validation == nil || validation.direction != EncodeEvent || validation.packageStream == nil {
		return fmt.Errorf("delivery commit requires an output package stream")
	}
	state := validation.packageStream
	if !state.hasPendingDelivery {
		return fmt.Errorf("delivery commit requires a successfully accepted output frame")
	}
	state.delivered = state.accepted
	state.deliveredFrameNumber = state.frameNumber
	state.hasPendingDelivery = false
	return nil
}

// AbortDelivery closes this output after an uncertain Write or Flush. Previously
// committed positions remain facts; the staged batch can never be committed or
// resumed. The host keeps the original transport error as the failure cause.
func (validation *WireStreamValidation) AbortDelivery() {
	validation.deliveryAborted = true
	if state := validation.packageStream; state != nil {
		state.hasPendingDelivery = false
		state.hasRejectedFrame = true
	}
}

func (runtime evaluationRuntime) readOutputStream(field string) (plannedOutput, error) {
	if runtime.outputStream == nil {
		return plannedOutput{}, fmt.Errorf("output stream metadata is not bound to this mapping and scope")
	}
	if runtime.outputStream.hasRejectedFrame {
		return plannedOutput{}, operationMetadataError("/delivery", "rejected output cannot resume from a delivery position")
	}
	position := runtime.outputStream.delivered
	switch field {
	case "responseId":
		if !position.hasStarted {
			return plannedOutput{}, nil
		}
		return plannedLeaf(StringValue(position.responseID)), nil
	case "sequence":
		if !position.hasSequence {
			return plannedOutput{}, nil
		}
		return plannedLeaf(coreInteger(position.sequence)), nil
	case "started":
		return plannedLeaf(coreBoolean(position.hasStarted)), nil
	case "terminal":
		return plannedLeaf(coreBoolean(position.isTerminal)), nil
	}
	panic("uncompiled output stream field")
}
