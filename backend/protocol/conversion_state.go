package protocol

import (
	"context"
	"reflect"
)

// conversionState owns the decoded document for a contiguous run of built-in
// actions. Authored JSON operations synchronize it before observing any value.
// It is never retained by the compiled policy or shared with another call.
type conversionState struct {
	wire                  Value
	document              any
	limits                Limits
	isStrict              bool
	needsEncoding         bool
	cache                 *semanticCache
	resolvedModelControls *modelControlExecution
	auxiliary             *sourceFrameReceipt
	omittedAuxiliary      *omittedEventReceipt
	control               *controlEventReceipt
	omittedControl        *omittedEventReceipt
	rangeAssociations     map[string]string
}

func (state *conversionState) validateContext(phase ConversionPhase) error {
	if phase != ConversionIngress && phase != ConversionRequest {
		return nil
	}
	if err := state.load(phase, false); err != nil {
		return err
	}
	return validateRequestContext(state.document.(*Request))
}

func (state *conversionState) accepts(phase ConversionPhase, rule ConversionRule) bool {
	if rule.Action == "auxiliary_event" || rule.Action == "control_event" {
		return phase == ConversionEvent
	}
	if rule.Action == "core_controls" || rule.Action == "continuation_recovery" {
		return phase == ConversionRequest || phase == ConversionResponse || phase == ConversionEvent
	}
	return phase == ConversionRequest && (rule.Action == "model_controls" || rule.Action == "request_projection")
}

func (state *conversionState) load(phase ConversionPhase, strict bool) error {
	if state.document != nil && (!strict || state.isStrict) {
		return nil
	}
	var document any
	var err error
	if strict {
		// The wire is already validated JSON. Direct writers retain the root's
		// borrowing threshold and recover the contract decoder's exact errors.
		output := JSONLeaf(state.wire)
		switch phase {
		case ConversionIngress, ConversionRequest:
			document, err = decodeCoreRequestOutput(output)
		case ConversionResponse:
			document, err = decodeCoreResponseOutput(output)
		case ConversionEvent:
			document, err = decodeCoreEventOutput(output)
		default:
			panic("strict conversion requires a semantic phase")
		}
	} else {
		switch phase {
		case ConversionIngress, ConversionRequest:
			document = &Request{}
		case ConversionResponse:
			document = &Response{}
		case ConversionEvent:
			document = &Event{}
		}
		err = state.wire.Decode(document)
	}
	if err != nil {
		return err
	}
	restoreRangeAssociations(document, state.rangeAssociations)
	state.rangeAssociations = nil
	if phase == ConversionEvent {
		if err := restoreProtectedEvent(document.(*Event), state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl, true); err != nil {
			return err
		}
	}
	state.document, state.isStrict = document, strict
	return nil
}

func (state *conversionState) apply(ctx context.Context, c *CompiledConversion, phase ConversionPhase, rule ConversionRule, route ConversionContext, sink *DiagnosticSink) error {
	isNodeRule := rule.Action == "provider_signature"
	if !isNodeRule && !state.matches(rule.Match, route) {
		return nil
	}
	isStrict := isNodeRule || rule.Action == "core_controls" || phase == ConversionRequest
	if err := state.load(phase, isStrict); err != nil {
		return err
	}
	if state.cache == nil {
		state.cache = &semanticCache{limits: state.limits}
	}
	var err error
	shouldEncode := true
	if rule.Action == "control_event" {
		event := state.document.(*Event)
		shouldEncode, err = c.applyControlEvent(event, route, rule, sink)
		state.control, state.omittedControl = event.controlReceipt, event.omittedControl
	} else if rule.Action == "auxiliary_event" {
		event := state.document.(*Event)
		shouldEncode, err = c.applyAuxiliaryEvent(event, route, rule, sink)
		state.auxiliary, state.omittedAuxiliary = event.auxiliary, event.omittedAuxiliary
	} else if rule.Action == "continuation_recovery" {
		err = c.applyContinuationRecovery(ctx, state.document, route, rule, sink)
	} else if rule.Action == "core_controls" {
		shouldEncode, err = c.applyCoreControls(ctx, state.document, route, rule, sink)
		if event, ok := state.document.(*Event); ok {
			state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl = event.auxiliary, event.omittedAuxiliary, event.controlReceipt, event.omittedControl
		}
	} else if isNodeRule {
		err = c.applyNodeObject(ctx, phase, state.document, route, sink, rule, state.cache)

	} else if rule.Action == "model_controls" {
		state.resolvedModelControls, err = c.applyModelControls(ctx, state.document.(*Request), route, rule, sink)
	} else {
		err = c.requestProjection(ctx, state.document.(*Request), route, rule, sink)
	}
	if err != nil {
		return err
	}
	if !shouldEncode {
		return nil
	}
	if err := state.cache.check(state.document, state.limits); err != nil {
		return err
	}
	state.cache.normalizeDocument(reflect.ValueOf(state.document), typedLimitPlans[reflect.TypeOf(state.document)])
	if phase == ConversionEvent {
		if err := restoreProtectedEvent(state.document.(*Event), state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl, true); err != nil {
			return err
		}
	}
	state.needsEncoding, state.isStrict = true, true
	state.wire = Value{} // The owned document now contains the complete current value.
	return nil
}

func (state *conversionState) matches(match ConversionMatch, route ConversionContext) bool {
	if !state.needsEncoding {
		// A validation-only decode may have discarded unknown fields. Until a
		// typed action replaces the wire, presence still observes that wire.
		return match.matches(route, state.wire)
	}
	return match.matchesSemantic(route, state.document)
}

func (state *conversionState) flush() (Value, error) {
	if !state.needsEncoding {
		return state.wire, nil
	}
	value, err := encodeSemanticWithin(state.document, state.limits)
	if err != nil {
		return Value{}, err
	}
	state.wire, state.needsEncoding = value, false
	return value, nil
}
