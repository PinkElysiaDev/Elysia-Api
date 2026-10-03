package protocol

import "context"

// TypedModule avoids serializing semantic documents between trusted Go
// adapters and the runtime. It implements the same conversion as Module.
// Definitions with schemas, post-mappings or initial events use Execute so no
// author-declared transformation or constraint can be bypassed.
type TypedModule interface {
	Module
	DecodeRequest(context.Context, Value, EvaluationContext) (*Request, error)
	EncodeRequest(context.Context, *Request, EvaluationContext) (Value, error)
	DecodeResponse(context.Context, Value, EvaluationContext) (*Response, error)
	EncodeResponse(context.Context, *Response, EvaluationContext) (Value, error)
}

// TypedEventModule shares the request-owned stream instance with Module.
type TypedEventModule interface {
	Module
	DecodeEvents(context.Context, Value, EvaluationContext) ([]Event, error)
}

func (compiled *Compiled) hasTypedMapping(direction Direction) bool {
	mapping := compiled.mappings[direction]
	return mapping.module != nil && mapping.after == nil && mapping.initial == nil && mapping.input == nil && mapping.output == nil
}

func checkTypedLimits(value any, limits Limits) error {
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
	if compiled.hasTypedMapping(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := checkValueLimits(input, compiled.limits); err != nil {
				return nil, err
			}
			options.identity = compiled.identity
			output, err := decode(module, options)
			if err != nil {
				return nil, err
			}
			if err := checkTypedLimits(output, compiled.limits); err != nil {
				return nil, err
			}
			return output, nil
		}
	}
	output, issues := compiled.Execute(ctx, direction, input, options)
	if err := IssuesError(issues); err != nil {
		return nil, err
	}
	var result T
	if err := decodeContract(output.Bytes(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func encodeTyped[T any](ctx context.Context, compiled *Compiled, direction Direction, input *T, options EvaluationContext, encode func(TypedModule, EvaluationContext) (Value, error)) (Value, error) {
	if compiled.hasTypedMapping(direction) {
		if module, ok := compiled.mappings[direction].module.(TypedModule); ok {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if err := checkTypedLimits(input, compiled.limits); err != nil {
				return Value{}, err
			}
			options.identity = compiled.identity
			output, err := encode(module, options)
			if err != nil {
				return Value{}, err
			}
			return output, checkValueLimits(output, compiled.limits)
		}
	}
	value, err := EncodeValue(input)
	if err != nil {
		return Value{}, err
	}
	output, issues := compiled.Execute(ctx, direction, value, options)
	return output, IssuesError(issues)
}

func (compiled *Compiled) decodeEventValues(ctx context.Context, direction Direction, frame Value, options EvaluationContext) ([]Event, error) {
	if compiled.hasTypedMapping(direction) {
		instance, err := options.State.resolve(compiled, direction, compiled.mappings[direction].module)
		if err != nil {
			return nil, err
		}
		if module, ok := instance.(TypedEventModule); ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := checkValueLimits(frame, compiled.limits); err != nil {
				return nil, err
			}
			options.identity = compiled.identity
			events, err := module.DecodeEvents(ctx, frame, options)
			if err != nil {
				return nil, err
			}
			return events, checkTypedLimits(events, compiled.limits)
		}
	}
	output, issues := compiled.Execute(ctx, direction, frame, options)
	if err := IssuesError(issues); err != nil {
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
