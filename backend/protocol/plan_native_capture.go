package protocol

import (
	"fmt"
	"strconv"
)

type nativeLocation struct {
	path   string
	isWire bool
}

// Capture state exists only for wire programs which request Native or provenance.
// Locations follow raw reads, bindings and source-array iterations. Constructed
// or reordered containers have no location; equal contents cannot prove origin.
type nativeCaptureState struct {
	source           Provenance
	item             nativeLocation
	input            nativeLocation
	hasInputScope    bool
	bindings         []nativeLocation
	isNativeDisabled bool
}

func (compiled *Compiled) newNativeCapture(direction Direction, options EvaluationContext) *nativeCaptureState {
	plan := compiled.mappings[direction].outputPlan
	if !plan.hasSourceCapture && (!compiled.native.Preserve || !plan.hasCapture) {
		return nil
	}
	return &nativeCaptureState{source: Provenance{Protocol: compiled.identity, Direction: direction, Scope: options.Scope}, isNativeDisabled: !compiled.native.Preserve}
}

func nativeReadLocation(expression *compiledExpression, capture *nativeCaptureState) nativeLocation {
	if expression.op != "read" {
		return nativeLocation{}
	}
	location := nativeLocation{isWire: true}
	switch expression.from {
	case "input":
		if capture.hasInputScope {
			location = capture.input
		}
	case "root":
	case "item":
		location = capture.item
	case "binding":
		location = capture.bindings[expression.bindingSlot]
	default:
		return nativeLocation{}
	}
	if location.isWire {
		for _, key := range expression.pointer {
			location.path += "/" + escapePointer(key)
		}
	}
	return location
}

func (capture *nativeCaptureState) forItem(source *compiledExpression, index int) *nativeCaptureState {
	child := *capture
	child.item = nativeReadLocation(source, capture)
	if child.item.isWire {
		child.item.path += "/" + strconv.Itoa(index)
	}
	return &child
}

// A recur consumer executes the enclosing walk body, which is not a child in
// its compiled graph. Keep that body's capture requirements in the decision.
func (plan *compiledOutputPlan) needsItemCapture(capture *nativeCaptureState, walkBody *compiledOutputPlan) bool {
	if capture == nil {
		return false
	}
	if plan.hasSourceCapture || plan.hasCapture && !capture.isNativeDisabled {
		return true
	}
	return plan.hasRecursion && (walkBody == nil || walkBody.hasSourceCapture || walkBody.hasCapture && !capture.isNativeDisabled)
}

func (plan *compiledOutputPlan) captureNative(state evaluation) (plannedOutput, error) {
	if state.capture == nil || state.capture.isNativeDisabled {
		return plannedOutput{}, nil
	}
	selected, source, err := plan.captureWireValue(state)
	if err != nil {
		return plannedOutput{}, err
	}
	selected = selected.normalizeForContainer(state.cache)
	output := JSONObject(map[string]JSONOutput{"source": source, "value": selected.output})
	output.node.origin = capturedNativeOrigin
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (plan *compiledOutputPlan) captureSource(state evaluation) (plannedOutput, error) {
	_, source, err := plan.captureWireValue(state)
	if err != nil {
		return plannedOutput{}, err
	}
	return plannedOutput{output: source, bytes: normalizedOutputBytes(source)}, nil
}

func (plan *compiledOutputPlan) captureWireValue(state evaluation) (plannedOutput, JSONOutput, error) {
	selected, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, JSONOutput{}, err
	}
	location := nativeReadLocation(plan.source.expression, state.capture)
	if !location.isWire || selected.output.node != nil || selected.output.IsZero() {
		return plannedOutput{}, JSONOutput{}, fmt.Errorf("native capture requires a present value at a proven original wire location")
	}
	origin := state.capture.source
	origin.Path = location.path
	source, err := readCoreProvenance(origin, nil, nil)
	if err != nil {
		return plannedOutput{}, JSONOutput{}, err
	}
	source.node.origin = capturedProvenanceOrigin
	return selected, source, nil
}
