package protocol

import (
	"context"
	"fmt"
)

// decodePlannedCore keeps the mapping's JSON shape and resource boundaries until
// the completed plan is written into the core. Measuring the decoded struct
// would lose unknown fields and explicit empty values before they are checked.
func decodePlannedCore[T any](ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext) (*T, error) {
	output, err := evaluateCoreOutput(ctx, compiled, direction, input, options)
	if err != nil {
		return nil, err
	}
	// Contract-decoding errors retain their original unwrapped boundary.
	var reader *valueReader
	if compiled.packagePlan != nil {
		reader = capturedCoreReader(output)
	}
	var target *T
	switch any(target).(type) {
	case *Request:
		result, err := decodeCoreRequestOutputWithin(output, reader)
		return any(result).(*T), err
	case *Response:
		result, err := decodeCoreResponseOutputWithin(output, reader)
		return any(result).(*T), err
	default:
		panic("core output plan requires a request or response destination")
	}
}

func evaluateCoreOutput(ctx context.Context, compiled *Compiled, direction Direction, input Value, options EvaluationContext) (JSONOutput, error) {
	fail := func(err error) (JSONOutput, error) { return JSONOutput{}, compiled.mappingError(direction, err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	inputLimits, outputLimits, workLimits := compiled.mappingLimits(ctx, direction)
	mapping := compiled.mappings[direction]
	if err := checkResourceValue(input, inputLimits, "mapping_input"); err != nil {
		return fail(err)
	}
	if err := mapping.input.validate(input, "/input", inputLimits); err != nil {
		return fail(err)
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	state := evaluation{ctx: ctx, input: input, root: input, context: contextValue, valueReader: options.valueReader, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	if compiled.packagePlan != nil {
		state.capture = compiled.newNativeCapture(direction, options)
		if state.valueReader == nil {
			state.valueReader = requestValueReader(input)
		}
		if err := compiled.preparePackageState(direction, state, inputLimits); err != nil {
			return fail(err)
		}
	}
	output, err := mapping.outputPlan.build(state)
	if err != nil {
		return fail(err)
	}
	if output.output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkPlannedOutputLimits(output, outputLimits, "mapping_output"); err != nil {
		return fail(err)
	}
	if compiled.packagePlan != nil {
		if err := compiled.finishPackageCoreOutput(ctx, direction, output.output, state.valueReader); err != nil {
			return fail(err)
		}
	}
	return output.output, nil
}

func checkPlannedOutputLimits(output plannedOutput, limits Limits, domain string) error {
	if output.bytes > limits.BufferBytes {
		return resourceFailure(domain, "bytes", limits.BufferBytes, output.bytes)
	}
	meter := valueTokenMeter{limits: limits, domain: domain}
	return meter.visitOutput(output.output)
}

func (meter *valueTokenMeter) visitOutput(output JSONOutput) error {
	if output.node == nil {
		return meter.scan(output.value.raw)
	}
	if err := meter.consume(1); err != nil {
		return err
	}
	if output.node.object {
		for _, field := range output.node.fields {
			if err := meter.consume(0); err != nil {
				return err
			}
			if err := meter.visitOutput(field.value); err != nil {
				return err
			}
		}
	} else {
		for _, item := range output.node.items {
			if err := meter.visitOutput(item); err != nil {
				return err
			}
		}
	}
	return meter.consume(-1)
}
