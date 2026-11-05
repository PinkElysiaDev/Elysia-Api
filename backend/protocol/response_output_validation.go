package protocol

import "context"

// ValidateResponseOutput checks the actual response after final wire edits.
// The caller supplies the immutable pre-edit output; configuration values cannot
// replace that host-owned comparison. Successful checks do not commit delivery.
func (compiled *Compiled) ValidateResponseOutput(ctx context.Context, before, after Value, options EvaluationContext) error {
	if compiled.packagePlan == nil {
		return compiled.ValidateWireOutputContext(ctx, EncodeResponse, after)
	}
	options = options.withInputView(after)
	view := options.inputView
	if err := compiled.validateWireInputView(ctx, EncodeResponse, view, compiled.limits, compiled.selectFinalWireContract(EncodeResponse)); err != nil {
		return err
	}
	if err := compiled.validateResponseRanges(ctx, before, view); err != nil {
		return compiled.mappingError(EncodeResponse, err)
	}
	return compiled.validateContinuationResponseView(ctx, view, options)
}

func (compiled *Compiled) validateResponseRanges(ctx context.Context, before Value, after *inputView) error {
	declarations := compiled.mappings[EncodeResponse].deliveryRanges
	if len(declarations) == 0 {
		return nil
	}
	prior := after
	if before.raw != after.value.raw {
		prior = newInputView(before)
	}
	beforeState := deliveryEvaluation(ctx, prior, compiled.limits)
	afterState := deliveryEvaluation(ctx, after, compiled.limits)
	return checkResponseRangeDeclarations(declarations, beforeState, afterState, prior == after)
}

func checkResponseRangeDeclarations(declarations []compiledDeliveryRange, beforeState, afterState evaluation, unchanged bool) error {
	for _, declaration := range declarations {
		afterRanges, err := declaration.readRanges(afterState)
		if err != nil {
			return err
		}
		if len(afterRanges) == 0 {
			continue
		}
		beforeTexts, err := declaration.readTexts(beforeState)
		if err != nil {
			return err
		}
		afterTexts, beforeRanges := beforeTexts, afterRanges
		if !unchanged {
			afterTexts, err = declaration.readTexts(afterState)
			if err != nil {
				return err
			}
			beforeRanges, err = declaration.readRanges(beforeState)
			if err != nil {
				return err
			}
		}
		texts := make(map[string]string, len(beforeTexts))
		for _, text := range beforeTexts {
			texts[text.key] = text.text
		}
		positions := make(map[string]*deliveryTextPositions, len(afterTexts))
		measure := func(text string) (int64, error) { return declaration.measureText(afterState, text) }
		for _, text := range afterTexts {
			original, exists := texts[text.key]
			if !exists {
				continue
			}
			position := &deliveryTextPositions{}
			if err := position.append(original, text.text, measure); err != nil {
				return err
			}
			positions[text.key] = position
		}
		references := make(map[[2]string]deliveryRangeObservation, len(beforeRanges))
		for _, reference := range beforeRanges {
			references[[2]string{reference.key, reference.id}] = reference
		}
		for _, reference := range afterRanges {
			original, exists := references[[2]string{reference.key, reference.id}]
			position := positions[reference.key]
			if !exists || position == nil {
				return deliveryRangeIssue(InvalidAssociation, reference.path, "reference has no proven original text association")
			}
			start, end, err := position.project(original.start, original.end)
			if err != nil {
				failure := err.(*declaredRejectionError)
				failure.diagnostic.Path = reference.path
				return failure
			}
			if start != reference.start || end != reference.end {
				return deliveryRangeIssue(ConversionRejected, reference.path, "reference indices do not match the proven delivery interval")
			}
		}
	}
	return nil
}
