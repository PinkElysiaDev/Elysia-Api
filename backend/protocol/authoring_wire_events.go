package protocol

import "context"

func (service *Service) previewWireEvents(ctx context.Context, conversion *CompiledConversion, route ConversionContext, input Value, sequence bool, sink *DiagnosticSink) (Value, error) {
	if !sequence {
		return Value{}, streamIssue(InvalidInput, "/sequence", "final wire event preview requires sequence: true")
	}
	target, exists := service.View().Pin(route.Target.DefinitionID)
	if !exists || target.Identity() != route.Target {
		return Value{}, operationMetadataError("/context", "preview target revision changed")
	}
	validation, err := target.NewWireStreamValidation(route.Scope)
	if err != nil {
		return Value{}, err
	}
	defer validation.Release()
	var frames []Value
	if err := input.Decode(&frames); err != nil {
		return Value{}, streamIssue(InvalidInput, "/input", "wire event preview requires a frame array")
	}
	options := EvaluationContext{Scope: route.Scope, operation: route.operation}
	for index, before := range frames {
		after, err := conversion.ApplyValue(ctx, ConversionWire, before, route, sink)
		if err != nil {
			return Value{}, err
		}
		if err := validation.ConsumeTransformed(ctx, before, after, options); err != nil {
			return Value{}, err
		}
		if target.packagePlan != nil {
			if err := validation.CommitDelivery(); err != nil {
				return Value{}, err
			}
		}
		frames[index] = after
	}
	if err := validation.Finish(); err != nil {
		return Value{}, err
	}
	return EncodeValue(frames)
}
