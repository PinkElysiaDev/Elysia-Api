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
	requestNormalized     bool
	requestPartition      *requestPartition
	resolvedModelControls *modelControlExecution
	auxiliary             *sourceFrameReceipt
	omittedAuxiliary      *omittedEventReceipt
	control               *controlEventReceipt
	omittedControl        *omittedEventReceipt
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
	if rule.Action == "core_controls" {
		return phase == ConversionRequest || phase == ConversionResponse
	}
	if phase == ConversionResponse || phase == ConversionEvent {
		switch rule.Action {
		case "chat_response_model", "responses_turn_state", "anthropic_usage_envelope", "response_shape", "response_envelope", "response_metadata", "error_metadata", "usage_projection", "signatures":
			return true
		}
		return false
	}
	if phase != ConversionRequest {
		return false
	}
	switch rule.Action {
	case "system_instruction_hoist", "chat_history", "assistant_history", "stream_options", "request_projection", "responses_include", "responses_context", "responses_storage", "gemini_safety_settings", "reasoning_controls", "model_controls", "response_metadata", "signatures", "tool_result_text", "tool_result_object":
		return true
	}
	return false
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
	if phase == ConversionEvent {
		if err := restoreProtectedEvent(document.(*Event), state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl, true); err != nil {
			return err
		}
	}
	state.document, state.isStrict = document, strict
	state.requestNormalized, state.requestPartition = false, nil
	return nil
}

func (state *conversionState) apply(ctx context.Context, c *CompiledConversion, phase ConversionPhase, rule ConversionRule, route ConversionContext, sink *DiagnosticSink) (string, error) {
	isNodeRule := rule.Action == "signatures" || rule.Action == "tool_result_text" || rule.Action == "tool_result_object"
	if !isNodeRule && !state.matches(rule.Match, route) {
		return "", nil
	}
	isStrict := isNodeRule || rule.Action == "responses_turn_state" || (phase == ConversionRequest && rule.Action != "response_metadata")
	if err := state.load(phase, isStrict); err != nil {
		return "", err
	}
	if state.cache == nil {
		state.cache = &semanticCache{limits: state.limits}
	}
	if rule.partitionRequest {
		state.prepareRequestPartition(phase, rule.Action)
	} else {
		state.requestPartition = nil
	}
	var target string
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
	} else if rule.Action == "core_controls" {
		shouldEncode, err = c.applyCoreControls(ctx, state.document, route, rule, sink)
	} else if isNodeRule {
		err = c.applyNodeObject(ctx, phase, state.document, route, sink, rule, state.cache)
	} else if rule.Action == "response_metadata" {
		err = c.metadataObject(phase, state.document, route, rule, sink)
	} else if rule.Action == "model_controls" {
		state.resolvedModelControls, err = c.applyModelControls(ctx, state.document.(*Request), route, rule, sink)
	} else if phase == ConversionRequest {
		target, err = c.applyRequestObject(ctx, state.document.(*Request), route, rule, sink)
	} else {
		shouldEncode, err = c.applyResponseObject(phase, state.document, route, rule, sink)
	}
	if err != nil {
		return "", err
	}
	if !shouldEncode {
		return target, nil
	}
	// Match the old pre-serialization check before removing omitted fields.
	if !state.checkRequestPartition() {
		if err := state.cache.check(state.document, state.limits); err != nil {
			return "", err
		}
	}
	if state.requestPartition != nil {
		state.normalizeRequestPartition()
	} else {
		state.cache.normalizeDocument(reflect.ValueOf(state.document), typedLimitPlans[reflect.TypeOf(state.document)])
	}
	state.requestNormalized = phase == ConversionRequest
	if phase == ConversionEvent {
		if err := restoreProtectedEvent(state.document.(*Event), state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl, true); err != nil {
			return "", err
		}
	}
	state.needsEncoding, state.isStrict = true, true
	state.wire = Value{} // The owned document now contains the complete current value.
	return target, nil
}

func (state *conversionState) matches(match ConversionMatch, route ConversionContext) bool {
	if !state.needsEncoding {
		// A validation-only decode may have discarded unknown fields. Until a
		// typed action replaces the wire, presence still observes that wire.
		return match.matches(route, state.wire)
	}
	return match.matchesSemantic(route, state.document)
}

func (c *CompiledConversion) applyResponseObject(phase ConversionPhase, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	switch rule.Action {
	case "chat_response_model":
		return true, c.repairChatResponseModel(document.(*Response), route, rule, sink)
	case "responses_turn_state":
		return c.projectTurnStateObject(document.(*Event), rule, sink)
	case "anthropic_usage_envelope":
		return c.envelopeUsageObject(phase, document, route, rule, sink)
	case "response_shape", "response_envelope":
		return c.responseObject(rule.Action, phase, document, route, rule, sink)
	case "error_metadata":
		return c.errorMetadataObject(phase, document, route, rule, sink)
	case "usage_projection":
		return c.projectUsageObject(phase, document, route, rule, sink)
	}
	panic("unregistered typed response action")
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
