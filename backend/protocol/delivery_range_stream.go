package protocol

import "context"

// ConsumeTransformed coordinates final wire, reference and carrier validation
// for one actual output frame. Before belongs to the host's pre-wire-rule
// output; accepted observations remain staged until CommitDelivery succeeds.
func (validation *WireStreamValidation) ConsumeTransformed(ctx context.Context, before, after Value, options EvaluationContext) error {
	if validation.direction != EncodeEvent {
		return operationMetadataError("/delivery", "transformed output requires an event output validator")
	}
	if options.Scope != validation.options.Scope {
		return operationMetadataError("/delivery", "transformed output scope does not match its validator")
	}
	if validation.packageStream == nil {
		return validation.consume(ctx, after, nil)
	}
	view := newInputView(after)
	if err := validation.consume(ctx, after, view); err != nil {
		validation.AbortDelivery()
		return err
	}
	if err := validation.consumeDeliveryRanges(ctx, before, view); err != nil {
		validation.AbortDelivery()
		return validation.compiled.mappingError(EncodeEvent, err)
	}
	if binding := options.continuation; binding != nil {
		if err := binding.check(validation.compiled, EncodeEvent, options.Scope); err != nil {
			validation.AbortDelivery()
			return err
		}
		if err := binding.observe(ctx, view); err != nil {
			validation.AbortDelivery()
			return err
		}
	}
	return nil
}

func (validation *WireStreamValidation) consumeDeliveryRanges(ctx context.Context, before Value, after *inputView) error {
	declarations := validation.compiled.mappings[EncodeEvent].deliveryRanges
	if len(declarations) == 0 {
		return nil
	}
	prior := after
	if before.raw != after.value.raw {
		prior = newInputView(before)
	}
	limits := validation.compiled.limits
	beforeState, afterState := deliveryEvaluation(ctx, prior, limits), deliveryEvaluation(ctx, after, limits)
	return consumeRangeDeclarations(&validation.deliveryRanges, declarations, beforeState, afterState, prior == after, limits)
}

func consumeRangeDeclarations(owner **deliveryRangeStream, declarations []compiledDeliveryRange, beforeState, afterState evaluation, unchanged bool, limits Limits) error {
	for channel, declaration := range declarations {
		beforeTexts, err := declaration.readTexts(beforeState)
		if err != nil {
			return err
		}
		beforeRanges, err := declaration.readRanges(beforeState)
		if err != nil {
			return err
		}
		afterTexts, afterRanges := beforeTexts, beforeRanges
		if !unchanged {
			afterTexts, err = declaration.readTexts(afterState)
			if err != nil {
				return err
			}
			afterRanges, err = declaration.readRanges(afterState)
			if err != nil {
				return err
			}
		}
		if len(beforeTexts)+len(afterTexts)+len(afterRanges) == 0 {
			continue
		}
		if *owner == nil {
			*owner = &deliveryRangeStream{}
		}
		stream := *owner
		afterByKey := make(map[string]deliveryTextObservation, len(afterTexts))
		for _, text := range afterTexts {
			afterByKey[text.key] = text
		}
		measure := func(text string) (int64, error) { return declaration.measureText(afterState, text) }
		for _, original := range beforeTexts {
			text, exists := afterByKey[original.key]
			if !exists {
				// Removing an append still advances the source position. A later
				// reference cannot accidentally attach to the next delivered text.
				text = original
				text.text = ""
			}
			delete(afterByKey, original.key)
			pending := stream.edit(deliveryRangeKey{channel, original.key})
			count := len(pending.positions.next.spans)
			bytes, err := pending.observe(original, text, measure)
			if err != nil {
				return err
			}
			stream.items += len(pending.positions.next.spans) - count
			stream.stagedBytes += bytes
		}
		for _, text := range afterByKey {
			original := text
			original.text = ""
			pending := stream.edit(deliveryRangeKey{channel, text.key})
			count := len(pending.positions.next.spans)
			bytes, err := pending.observe(original, text, measure)
			if err != nil {
				return err
			}
			stream.items += len(pending.positions.next.spans) - count
			stream.stagedBytes += bytes
		}
		if stream.items > limits.StateItems {
			return resourceFailure("delivery", "state_items", limits.StateItems, stream.items)
		}
		if bytes := stream.bytes + stream.stagedBytes; bytes > limits.BufferBytes {
			return resourceFailure("delivery", "state_bytes", limits.BufferBytes, bytes)
		}
		references := make(map[[2]string]deliveryRangeObservation, len(beforeRanges))
		for _, reference := range beforeRanges {
			references[[2]string{reference.key, reference.id}] = reference
		}
		for _, reference := range afterRanges {
			original, exists := references[[2]string{reference.key, reference.id}]
			key := deliveryRangeKey{channel, reference.key}
			pending, previous := stream.pending[key], stream.texts[key]
			if !exists || pending == nil && previous == nil {
				return deliveryRangeIssue(InvalidAssociation, reference.path, "reference has no proven original text association")
			}
			var start, end int64
			if pending != nil {
				start, end, err = pending.positions.project(original.start, original.end)
			} else {
				start, end, err = previous.positions.project(original.start, original.end)
			}
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
