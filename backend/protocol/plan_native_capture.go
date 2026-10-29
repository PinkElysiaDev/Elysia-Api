package protocol

import (
	"fmt"
	"strconv"
)

type nativeLocation struct {
	path   string
	isWire bool
}

// Capture state exists only for wire programs which actually request Native.
// Locations follow raw reads, bindings and source-array iterations. Constructed
// or reordered containers have no location; equal contents cannot prove origin.
type nativeCaptureState struct {
	source   Provenance
	item     nativeLocation
	bindings []nativeLocation
}

func (compiled *Compiled) newNativeCapture(direction Direction, options EvaluationContext) *nativeCaptureState {
	plan := compiled.mappings[direction].outputPlan
	if !compiled.native.Preserve || !plan.hasCapture {
		return nil
	}
	return &nativeCaptureState{source: Provenance{Protocol: compiled.identity, Direction: direction, Scope: options.Scope}}
}

func nativeReadLocation(expression *compiledExpression, capture *nativeCaptureState) nativeLocation {
	if expression.op != "read" {
		return nativeLocation{}
	}
	location := nativeLocation{isWire: true}
	switch expression.from {
	case "input", "root":
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

func (plan *compiledOutputPlan) captureNative(state evaluation) (plannedOutput, error) {
	if state.capture == nil {
		return plannedOutput{}, nil
	}
	selected, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	location := nativeReadLocation(plan.source.expression, state.capture)
	if !location.isWire || selected.output.node != nil || selected.output.IsZero() {
		return plannedOutput{}, fmt.Errorf("native capture requires a present value at a proven original wire location")
	}
	selected = selected.normalizeForContainer()
	origin := state.capture.source
	origin.Path = location.path
	source, err := readCoreProvenance(origin, nil)
	if err != nil {
		return plannedOutput{}, err
	}
	source.node.origin = capturedProvenanceOrigin
	output := JSONObject(map[string]JSONOutput{"source": source, "value": selected.output})
	output.node.origin = capturedNativeOrigin
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}
