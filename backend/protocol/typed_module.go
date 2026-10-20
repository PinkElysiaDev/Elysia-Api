package protocol

import (
	"context"
	"fmt"
	"reflect"
)

// TypedModule avoids serializing semantic documents between trusted Go
// adapters and the runtime. It implements the same conversion as Module.
// Request wire schemas still run at their original boundary. Semantic schemas,
// post-mappings and initial events retain the generic execution path.
type TypedModule interface {
	Module
	DecodeRequest(context.Context, Value, EvaluationContext) (*Request, error)
	EncodeRequest(context.Context, *Request, EvaluationContext) (Value, error)
	DecodeResponse(context.Context, Value, EvaluationContext) (*Response, error)
	EncodeResponse(context.Context, *Response, EvaluationContext) (Value, error)
}

// TypedEventModule shares the request-owned stream instance with Module.
// DecodeEvents exposes the same events as Convert; callers retain resource
// checks and reproduce a JSON boundary's normalization where required.
type TypedEventModule interface {
	Module
	DecodeEvents(context.Context, Value, EvaluationContext) ([]Event, error)
}

// TypedEventEncoder retains the module's wire result (including frame batches)
// while accepting an owned, JSON-normalized semantic event.
type TypedEventEncoder interface {
	Module
	EncodeEventValue(context.Context, Event, EvaluationContext) (Value, error)
}

func (compiled *Compiled) encodeEventValue(ctx context.Context, direction Direction, event Event, options EvaluationContext) (Value, error) {
	if !compiled.hasTypedMapping(direction) {
		input, err := EncodeSemantic(ctx, direction, event)
		if err != nil {
			return Value{}, err
		}
		return compiled.executeMapping(ctx, direction, input, options)
	}
	// The original semantic boundary precedes executeMapping's cancellation check.
	budget := semanticLimits(DefaultLimits())
	if err := checkSemanticValue(event, budget); err != nil {
		return Value{}, err
	}
	input := normalizeMappingSemantic(&event, budget)
	fail := func(err error) (Value, error) { return Value{}, compiled.mappingError(direction, err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	inputLimits, outputLimits, _ := compiled.mappingLimits(ctx, direction)
	if err := checkMappingSemantic(input, inputLimits, "mapping_input"); err != nil {
		return fail(err)
	}
	if _, err := EncodeValue(options.Values); err != nil {
		return fail(err)
	}
	options = options.forDefinition(compiled)
	options.wireLimits = compiled.nativeLimits(ctx, direction)
	instance, err := options.State.resolve(compiled, direction, compiled.mappings[direction].module)
	if err != nil {
		return fail(err)
	}
	encoder, ok := instance.(TypedEventEncoder)
	if !ok {
		value, err := EncodeSemantic(ctx, direction, event)
		if err != nil {
			return Value{}, err
		}
		return compiled.executeMapping(ctx, direction, value, options)
	}
	output, err := encoder.EncodeEventValue(ctx, *input, options)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkResourceValue(output, outputLimits, "mapping_output"); err != nil {
		return fail(err)
	}
	return output, nil
}

func (compiled *Compiled) hasTypedMapping(direction Direction) bool {
	mapping := compiled.mappings[direction]
	return mapping.module != nil && mapping.after == nil && mapping.initial == nil && mapping.input == nil && mapping.output == nil
}

// Only wire-side request schemas are eligible; event and response selection is
// intentionally unchanged. Modules still own the same typed/JSON contract.
func (compiled *Compiled) hasTypedRequestSchema(direction Direction) bool {
	mapping := compiled.mappings[direction]
	if mapping.module == nil || mapping.after != nil || mapping.initial != nil {
		return false
	}
	if _, isPureRequestModule := mapping.module.(RequestBaselineModule); !isPureRequestModule {
		return false
	}
	if direction == DecodeRequest {
		return mapping.input != nil && mapping.output == nil
	}
	return direction == EncodeRequest && mapping.input == nil && mapping.output != nil
}

// normalizeMappingSemantic reproduces a semantic JSON boundary on owned mutable
// containers while preserving immutable payloads that need no normalization.
func normalizeMappingSemantic[T any](input T, limits Limits) T {
	value := reflect.ValueOf(input)
	plan := typedLimitPlans[value.Type()]
	owned := cloneSemantic(value, plan)
	state := conversionState{cache: &semanticCache{limits: limits}}
	state.normalize(owned, plan)
	return owned.Interface().(T)
}

func checkMappingSemantic(value any, limits Limits, domain string) error {
	if fitsTypedLimits(value, limits) {
		return nil
	}
	encoded, err := EncodeValue(value)
	if err != nil {
		return err
	}
	return checkResourceValue(encoded, limits, domain)
}

func checkTypedLimits(value any, limits Limits) error {
	if fitsTypedLimits(value, limits) {
		return nil
	}
	encoded, isValue := value.(Value)
	if !isValue {
		var err error
		encoded, err = EncodeValue(value)
		if err != nil {
			return err
		}
	}
	return checkValueLimits(encoded, limits)
}

// decodeTyped retains the exact resource checks of Execute. Trusted typed
// outputs need no strict JSON reparse; capability/provenance checks follow in
// the caller exactly as they do for declarative mappings.
func decodeTyped[T any](ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext, decode func(TypedModule, EvaluationContext) (*T, error)) (*T, error) {
	inputLimits, outputLimits, _ := compiled.mappingLimits(ctx, direction)
	if compiled.hasTypedMapping(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := checkResourceValue(input, inputLimits, "wire_input"); err != nil {
				return nil, err
			}
			options = options.forDefinition(compiled)
			options.wireLimits = compiled.nativeLimits(ctx, direction)
			output, err := decode(module, options)
			if err != nil {
				return nil, err
			}
			if err := checkSemanticValue(output, outputLimits); err != nil {
				return nil, err
			}
			return output, nil
		}
	}
	if compiled.hasTypedRequestSchema(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			fail := func(err error) (*T, error) { return nil, compiled.mappingError(direction, err) }
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if err := checkResourceValue(input, inputLimits, "mapping_input"); err != nil {
				return fail(err)
			}
			if err := compiled.mappings[direction].input.validate(input, "/input", inputLimits); err != nil {
				return fail(err)
			}
			if _, err := EncodeValue(options.Values); err != nil {
				return fail(err)
			}
			options = options.forDefinition(compiled)
			options.wireLimits = compiled.nativeLimits(ctx, direction)
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			output, err := decode(module, options)
			if err != nil {
				return fail(err)
			}
			// Module.Convert first calls EncodeSemantic with the call-wide budget.
			semanticBudget := semanticLimits(requestLimits(ctx, DefaultLimits(), false))
			if err := checkSemanticValue(output, semanticBudget); err != nil {
				return fail(err)
			}
			output = normalizeMappingSemantic(output, semanticBudget)
			if err := checkMappingSemantic(output, outputLimits, "mapping_output"); err != nil {
				return fail(err)
			}
			return output, nil
		}
	}

	output, err := compiled.executeMapping(ctx, direction, input, options)
	if err != nil {
		return nil, err
	}
	var result T
	if err := decodeContract(output.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func encodeTyped[T any](ctx context.Context, compiled *Compiled, direction Direction, input *T, options EvaluationContext, encode func(TypedModule, *T, EvaluationContext) (Value, error)) (Value, error) {
	inputLimits, outputLimits, _ := compiled.mappingLimits(ctx, direction)
	if compiled.hasTypedMapping(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if err := checkSemanticValue(input, inputLimits); err != nil {
				return Value{}, err
			}
			options = options.forDefinition(compiled)
			options.wireLimits = compiled.nativeLimits(ctx, direction)
			output, err := encode(module, input, options)
			if err != nil {
				return Value{}, err
			}
			return output, checkResourceValue(output, outputLimits, "wire_output")
		}
	}
	if compiled.hasTypedRequestSchema(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			return encodeRequestMapping(ctx, compiled, input, options, module, encode)
		}
	}

	value, err := EncodeSemantic(ctx, direction, input)
	if err != nil {
		return Value{}, err
	}
	return compiled.executeMapping(ctx, direction, value, options)
}

func (compiled *Compiled) decodeEventValues(ctx context.Context, direction Direction, frame Value, options EvaluationContext) ([]Event, error) {
	inputLimits, outputLimits, _ := compiled.mappingLimits(ctx, direction)
	if compiled.hasTypedMapping(direction) {
		instance, err := options.State.resolve(compiled, direction, compiled.mappings[direction].module)
		if err != nil {
			return nil, err
		}
		if module, ok := instance.(TypedEventModule); ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := checkResourceValue(frame, inputLimits, "wire_input"); err != nil {
				return nil, err
			}
			options = options.forDefinition(compiled)
			options.wireLimits = compiled.nativeLimits(ctx, direction)
			events, err := module.DecodeEvents(ctx, frame, options)
			if err != nil {
				return nil, err
			}
			return events, checkSemanticValue(events, outputLimits)
		}
	}
	output, err := compiled.executeMapping(ctx, direction, frame, options)
	if err != nil {
		return nil, err
	}
	items, err := eventValues(output)
	if err != nil {
		return nil, err
	}
	events := make([]Event, len(items))
	for index, item := range items {
		if err := decodeContract(item.Bytes(), &events[index]); err != nil {
			return nil, err
		}
	}
	return events, nil
}

// encodeRequestMapping retains the former semantic JSON mapping boundary for
// native baseline rendering, including evaluate-stage errors and normalization.
func encodeRequestMapping[T any](ctx context.Context, compiled *Compiled, input *T, options EvaluationContext, module TypedModule, encode func(TypedModule, *T, EvaluationContext) (Value, error)) (Value, error) {
	direction := EncodeRequest
	inputLimits, outputLimits, _ := compiled.mappingLimits(ctx, direction)
	// This originally ran before executeMapping, including its context check.
	semanticBudget := semanticLimits(requestLimits(ctx, DefaultLimits(), false))
	if err := checkSemanticValue(input, semanticBudget); err != nil {
		return Value{}, err
	}
	input = normalizeMappingSemantic(input, semanticBudget)
	fail := func(err error) (Value, error) { return Value{}, compiled.mappingError(direction, err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := checkMappingSemantic(input, inputLimits, "mapping_input"); err != nil {
		return fail(err)
	}
	if _, err := EncodeValue(options.Values); err != nil {
		return fail(err)
	}
	options = options.forDefinition(compiled)
	options.wireLimits = compiled.nativeLimits(ctx, direction)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	output, err := encode(module, input, options)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkResourceValue(output, outputLimits, "mapping_output"); err != nil {
		return fail(err)
	}
	if err := compiled.mappings[direction].output.validate(output, "/output", outputLimits); err != nil {
		return fail(err)
	}
	return output, nil
}
