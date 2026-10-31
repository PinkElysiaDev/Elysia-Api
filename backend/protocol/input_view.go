package protocol

import "context"

// inputView belongs to one immutable input invocation. Validation borrows small
// selections; mapping detaches them before they can become retained core values.
// Only readers and successful root checks are shared, never stream state.
type inputView struct {
	value        Value
	owned        valueReader
	borrowed     valueReader
	objects      planObjectReader
	rootLimits   Limits
	hasRootCheck bool
	validator    *specWireValidator
	direction    Direction
	specLimits   Limits
}

func newInputView(value Value) *inputView {
	view := &inputView{value: value, owned: *requestValueReader(value)}
	view.borrowed = view.owned
	view.borrowed.minimumSharedBytes = 0
	view.objects.root = spanIdentity(value.raw)
	return view
}

func (view *inputView) owns(value Value) bool {
	return view != nil && spanIdentity(view.value.raw) == spanIdentity(value.raw)
}

func (view *inputView) objectReader() *planObjectReader {
	if !view.value.IsObject() {
		return nil
	}
	return &view.objects
}

func (options EvaluationContext) withInputView(value Value) EvaluationContext {
	if !options.inputView.owns(value) {
		options.inputView = newInputView(value)
	}
	return options
}

func (view *inputView) checkResources(ctx context.Context, value Value, limits Limits, domain string) error {
	if view == nil {
		return checkResourceValue(value, limits, domain)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	isRoot := view.owns(value)
	if isRoot && view.hasRootCheck && view.rootLimits == limits {
		return nil
	}
	if err := checkResourceValue(value, limits, domain); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if isRoot {
		view.rootLimits, view.hasRootCheck = limits, true
	}
	return nil
}

func (compiled *Compiled) validateWireInputView(ctx context.Context, direction Direction, view *inputView, limits Limits, contract compiledWireContract) error {
	validator := contract.contextValidator.(*specWireValidator)
	return compiled.runtimeError(direction, validator.validateInputView(ctx, direction, view, limits))
}
