package protocol

import (
	"fmt"
)

// ProtocolContinuationMapping declares gateway carrier positions, independently
// of provider state. Cryptography and authenticated recovery remain host-owned.
type ProtocolContinuationMapping struct {
	Extract        *Expression                 `json:"extract,omitempty"`
	EncodeResponse *ProtocolContinuationOutput `json:"encode_response,omitempty"`
	EncodeEvent    *ProtocolContinuationOutput `json:"encode_event,omitempty"`
}

// ProtocolContinuationOutput renders a carrier at a declared boundary. Emitted
// observes actual final token values without access to the planned token list.
type ProtocolContinuationOutput struct {
	When    *Expression `json:"when,omitempty"`
	Program Expression  `json:"program"`
	Emitted Expression  `json:"emitted"`
}

type compiledContinuation struct {
	extract         *compiledOutputPlan
	response, event *compiledContinuationOutput
}

type compiledContinuationOutput struct {
	when, program, emitted *compiledOutputPlan
}

func (compiled *Compiled) compileContinuation(declaration *ProtocolContinuationMapping, compiler *expressionCompiler) error {
	if declaration == nil {
		return nil
	}
	entry := &compiledContinuation{}
	if declaration.Extract != nil {
		if !compiled.Supports(DecodeRequest) {
			return fmt.Errorf("/mapping/continuation/extract: requires request decoding")
		}
		plan, err := compiler.compilePlan(*declaration.Extract, "/mapping/continuation/extract", expressionScope{wireCapture: true}, 1)
		if err != nil {
			return err
		}
		if !isAssignable(plan.expression.result, ArrayType) {
			return fmt.Errorf("/mapping/continuation/extract: requires a selection array")
		}
		entry.extract = plan
	}
	for _, item := range []struct {
		direction Direction
		name      string
		value     *ProtocolContinuationOutput
		target    **compiledContinuationOutput
	}{
		{EncodeResponse, "encode_response", declaration.EncodeResponse, &entry.response}, {EncodeEvent, "encode_event", declaration.EncodeEvent, &entry.event},
	} {
		if item.value == nil {
			continue
		}
		at := "/mapping/continuation/" + item.name
		if !compiled.Supports(item.direction) || item.direction == EncodeEvent && item.value.When == nil {
			return fmt.Errorf("%s: requires its encoding direction and an explicit event boundary", at)
		}
		scope := expressionScope{canReadContinuation: true, canReadOutputStream: item.direction == EncodeEvent}
		plan, err := compileContinuationOutput(item.value, compiler, at, scope, item.direction)
		if err != nil {
			return err
		}
		*item.target = plan
	}
	compiled.packagePlan.continuation = entry
	return nil
}

func compileContinuationOutput(value *ProtocolContinuationOutput, compiler *expressionCompiler, path string, scope expressionScope, direction Direction) (*compiledContinuationOutput, error) {
	plan := &compiledContinuationOutput{}
	var err error
	plan.when, err = compileWirePredicateInScope(value.When, path+"/when", scope, compiler, 1)
	if err != nil {
		return nil, err
	}
	plan.program, err = compiler.compilePlan(value.Program, path+"/program", scope, 1)
	if err != nil {
		return nil, err
	}
	kind := ObjectType
	if direction == EncodeEvent {
		kind = ArrayType
	}
	if !isAssignable(plan.program.expression.result, kind) {
		return nil, fmt.Errorf("%s/program: unexpected carrier output type", path)
	}
	plan.emitted, err = compiler.compilePlan(value.Emitted, path+"/emitted", expressionScope{isWireContract: true}, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.emitted.expression.result, ArrayType) {
		return nil, fmt.Errorf("%s/emitted: requires token array", path)
	}
	return plan, nil
}

func (compiled *Compiled) continuationOutput(direction Direction) *compiledContinuationOutput {
	if compiled.packagePlan == nil || compiled.packagePlan.continuation == nil {
		return nil
	}
	switch direction {
	case EncodeResponse:
		return compiled.packagePlan.continuation.response
	case EncodeEvent:
		return compiled.packagePlan.continuation.event
	}
	return nil
}

// HasContinuationCarrier reports whether this pinned target can render gateway
// carriers in the selected client output direction.
func (compiled *Compiled) HasContinuationCarrier(direction Direction) bool {
	return compiled.continuationOutput(direction) != nil
}
