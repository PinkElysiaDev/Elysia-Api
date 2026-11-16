package protocol

import (
	"context"
	"errors"
	"fmt"
)

// WireStreamValidation checks the bytes after after-mappings, wire rules and
// carrier insertion. Its contract is independent of a user-authored decode
// mapping that could hide a malformed wire frame.
type WireStreamValidation struct {
	compiled        *Compiled
	direction       Direction
	packageStream   *packageStreamValidation
	options         EvaluationContext
	deliveryAborted bool
	deliveryRanges  *deliveryRangeStream
}

// NewWireStreamValidation checks final event bytes against their output contract.
func (c *Compiled) NewWireStreamValidation(scope Scope) (*WireStreamValidation, error) {
	return c.newWireStreamValidation(EncodeEvent, scope)
}

// NewWireInputStreamValidation checks source frames independently of the decode
// mapping, so a mapping cannot hide an invalid sequence or missing terminal.
// It requires the independent Spec stream contract.
func (c *Compiled) NewWireInputStreamValidation(scope Scope) (*WireStreamValidation, error) {
	if c.wireContracts[DecodeEvent].packageStream == nil {
		return nil, fmt.Errorf("source wire validation requires an independent package stream contract")
	}
	return c.newWireStreamValidation(DecodeEvent, scope)
}

func (c *Compiled) newWireStreamValidation(direction Direction, scope Scope) (*WireStreamValidation, error) {
	v := &WireStreamValidation{compiled: c, direction: direction, options: EvaluationContext{Scope: scope, identity: c.identity}}
	contract := c.selectFinalWireContract(direction)
	if contract.packageStream != nil {
		var err error
		v.packageStream, err = newPackageStreamValidation(contract.packageStream, c.target(direction, v.options), c.limits)
		return v, err
	}
	return v, nil
}

func (v *WireStreamValidation) Consume(ctx context.Context, frame Value) error {
	if v.direction == EncodeEvent && len(v.compiled.mappings[EncodeEvent].deliveryRanges) != 0 {
		return v.ConsumeTransformed(ctx, frame, frame, v.options)
	}
	var view *inputView
	if v.packageStream != nil {
		view = newInputView(frame)
	}
	return v.consume(ctx, frame, view)
}

// DecodeFrame validates and decodes one source frame using shared immutable
// reads. Wire lifecycle and mapping state remain independent of each other.
func (v *WireStreamValidation) DecodeFrame(ctx context.Context, frame Value, options EvaluationContext) (*EventFrame, error) {
	if v.direction != DecodeEvent {
		return nil, fmt.Errorf("provider frame decoding requires a provider source validator")
	}
	return v.decodeFrame(ctx, frame, options)
}

func (v *WireStreamValidation) decodeFrame(ctx context.Context, frame Value, options EvaluationContext) (*EventFrame, error) {
	if !isEventDecoder(v.direction) || v.packageStream == nil {
		return nil, fmt.Errorf("frame decoding requires source wire stream validation")
	}
	if options.Scope != v.options.Scope {
		return nil, v.compiled.runtimeError(v.direction, streamIssue(ResourceScopeMismatch, "/state", "frame scope does not match source wire stream validation"))
	}
	options, err := options.withSourceFrameView(frame)
	if err != nil {
		return nil, v.compiled.runtimeError(v.direction, err)
	}
	view := options.inputView
	if err := v.consume(ctx, frame, view); err != nil {
		return nil, err
	}
	return v.compiled.decodeFrame(ctx, v.direction, frame, options)
}

func (v *WireStreamValidation) consume(ctx context.Context, frame Value, view *inputView) (err error) {
	defer func() {
		if err != nil && v.direction == DecodeEvent {
			err = v.compiled.observeRejectedFrame(ctx, frame, err)
		}
	}()
	if v.deliveryAborted {
		return v.compiled.runtimeError(v.direction, operationMetadataError("/delivery", "aborted output cannot accept another frame"))
	}
	if v.packageStream != nil {
		v.packageStream.hasPendingDelivery = false
	}
	contract := v.compiled.selectFinalWireContract(v.direction)
	if view != nil {
		err = v.compiled.validateWireInputView(ctx, v.direction, view, v.compiled.wireLimits(ctx, v.direction), contract)
	} else {
		err = v.compiled.validateSelectedWireContract(ctx, v.direction, frame, nil, v.compiled.wireLimits(ctx, v.direction), contract)
	}
	if err != nil {
		return v.rejectPackageFrame(err)
	}
	if v.packageStream != nil {
		err := v.packageStream.consumeInputView(ctx, view)
		if err != nil {
			return v.annotatePackageFailure(v.rejectPackageFrame(err))
		}
		if isEventDecoder(v.direction) {
			state := v.packageStream
			view.sourceReceipt = &sourceFrameReceipt{compiled: v.compiled, contract: state.contract, direction: v.direction, scope: v.options.Scope, frame: frame, limits: v.compiled.wireLimits(ctx, v.direction), class: state.frameClass,
				responseID: state.frameResponseID, hadResponse: state.frameHadResponse, hasSequence: state.frameHasSequence, sequence: state.frameSequence, frameNumber: state.frameNumber}
		}
		return nil
	}
	return nil
}

func (v *WireStreamValidation) annotatePackageFailure(err error) error {
	var failure *ConversionError
	if errors.As(err, &failure) {
		for _, issue := range failure.Issues {
			if issue.Protocol.DefinitionID == "" || issue.Direction == "" {
				return v.compiled.runtimeError(v.direction, err)
			}
		}
		// Already attributed boundaries may intentionally wrap a resource cause.
		// Preserve that outer classification instead of reclassifying the cause.
		return err
	}
	return v.compiled.runtimeError(v.direction, err)
}

func (v *WireStreamValidation) rejectPackageFrame(err error) error {
	if v.packageStream != nil {
		v.packageStream.hasRejectedFrame = true
	}
	return err
}

func (v *WireStreamValidation) Finish() error {
	if v.deliveryAborted {
		return operationMetadataError("/delivery", "aborted output cannot finish successfully")
	}
	if v.packageStream != nil {
		err := v.packageStream.finish()
		if err != nil {
			v.packageStream.hasPendingDelivery = false
		}
		return err
	}
	return nil
}
