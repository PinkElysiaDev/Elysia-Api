package protocol

import (
	"context"
	"fmt"
)

func encodePlannedEventFrames(ctx context.Context, compiled *Compiled, direction Direction, event *Event, options EvaluationContext) ([]Value, error) {
	if err := compiled.checkSemanticEventRanges(ctx, direction, event, options); err != nil {
		return nil, err
	}
	state, limits, err := preparePlannedCoreEncoding(ctx, compiled, direction, event, options)
	if err != nil {
		return nil, err
	}
	fail := func(err error) ([]Value, error) { return nil, compiled.mappingError(direction, err) }
	output, err := compiled.mappings[direction].outputPlan.build(state)
	if err != nil {
		return fail(err)
	}
	if err := options.packageTransaction.resolveDispositions(); err != nil {
		return fail(err)
	}
	frames, err := compiled.encodePackageFrameOutput(ctx, direction, output, limits)
	if err != nil {
		return fail(err)
	}
	if event.Native != nil && compiled.native.Preserve && CanPreserveNative(event.Native.Source, compiled.target(direction, options)) {
		if len(frames) != 1 {
			return fail(fmt.Errorf("native event reconciliation requires one frame; use compound frame replay for unchanged native batches"))
		}
		if state.budget.hasReadOutputStream {
			return fail(streamIssue(UnsupportedNative, "/native", "dynamic output position requires explicit native field ownership"))
		}
		frames[0], err = compiled.preserveMappedNative(ctx, direction, event.Native, frames[0], options)
		if err != nil {
			return fail(err)
		}
	}
	if err := options.packageTransaction.stage(state, true); err != nil {
		return fail(err)
	}
	return frames, nil
}

func (compiled *Compiled) encodePackageFrameOutput(ctx context.Context, direction Direction, output plannedOutput, limits Limits) ([]Value, error) {
	if err := checkPlannedOutputLimits(output, limits, "mapping_output"); err != nil {
		return nil, err
	}
	items, err := plannedArrayItems(output.output)
	if err != nil {
		return nil, err
	}
	frames := make([]Value, len(items))
	for index, item := range items {
		value, err := item.Encode(ctx)
		if err != nil {
			return nil, err
		}
		// Preserve array-member normalization without materializing its wrapper.
		value.raw = compactValueString(value.raw)
		if err := compiled.validateWireContract(ctx, direction, value, nil, limits); err != nil {
			return nil, err
		}
		frames[index] = value
	}
	return frames, nil
}
