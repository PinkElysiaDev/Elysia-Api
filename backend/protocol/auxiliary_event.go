package protocol

import (
	"context"
	"fmt"
	"strconv"
)

// A receipt belongs to one successful source-wire observation. Neither mapping
// JSON nor semantic serialization can manufacture its authority.
type sourceFrameReceipt struct {
	compiled    *Compiled
	contract    *compiledPackageStreamContract
	direction   Direction
	scope       Scope
	frame       Value
	class       wireFrameClass
	responseID  string
	hadResponse bool
	sequence    int64
	hasSequence bool
	frameNumber uint64
}

type omittedEventReceipt struct {
	source      Identity
	target      Identity
	scope       Scope
	responseID  Value
	sequence    Value
	encoderHash string
	decoderHash string
}

func (compiled *Compiled) validateSourceFrame(ctx context.Context, direction Direction, frame Value, options EvaluationContext) (EvaluationContext, error) {
	if compiled.packagePlan == nil || compiled.wireContracts[direction].packageStream == nil {
		return options, nil
	}
	if view := options.inputView; view.owns(frame) && view.sourceReceipt != nil {
		receipt := view.sourceReceipt
		if receipt.compiled == compiled && receipt.contract == compiled.wireContracts[direction].packageStream && receipt.direction == direction && receipt.scope == options.Scope {
			return options, nil
		}
	}
	if options.State == nil {
		return options, fmt.Errorf("frame decoding requires one EvaluationState per source stream")
	}
	key := sourceStreamKey{moduleStateKey: moduleStateKey{compiled: compiled, direction: direction}, scope: options.Scope}
	validator := options.State.sourceStreams[key]
	if validator == nil {
		if len(options.State.sourceStreams) >= compiled.limits.StateItems {
			return options, streamIssue(LimitExceeded, "/state", "source stream count exceeds the item limit")
		}
		var err error
		validator, err = compiled.newWireStreamValidation(direction, options.Scope)
		if err != nil {
			return options, err
		}
		if options.State.sourceStreams == nil {
			options.State.sourceStreams = make(map[sourceStreamKey]*WireStreamValidation)
		}
		options.State.sourceStreams[key] = validator
		if budget := options.State.retention.budget; budget != nil {
			if err := validator.bindStreamBudget(budget); err != nil {
				return options, err
			}
		}
	}
	options.inputView = newInputView(frame)
	if err := validator.consume(ctx, frame, options.inputView); err != nil {
		return options, err
	}
	return options, nil
}

func (receipt *sourceFrameReceipt) attach(event *Event) error {
	if !receipt.hadResponse || receipt.responseID == "" {
		return streamIssue(InvalidAssociation, "/responseId", "auxiliary events require an earlier response start")
	}
	event.auxiliary = receipt
	return restoreAuxiliaryEvent(event, receipt, nil, false)
}

func hasAuxiliaryPayload(event Event) bool {
	return event.Control != nil || event.ResponsesTurnState != nil || len(event.Metadata) != 0 || !event.SessionID.IsZero() || !event.ItemID.IsZero() || !event.ParentID.IsZero() || event.Collection != "" || !event.CallID.IsZero() || event.Index != nil || !event.Delta.IsZero() || event.Item != nil || event.Response != nil || event.Request != nil || event.Usage != nil || event.Media != nil || !event.Error.IsZero() || event.Unmapped != nil
}

func restoreAuxiliaryEvent(event *Event, receipt *sourceFrameReceipt, omitted *omittedEventReceipt, normalized bool) error {
	if receipt == nil && omitted == nil {
		return nil
	}
	if hasAuxiliaryPayload(*event) {
		return streamIssue(UnsupportedNative, "/native", "auxiliary events cannot carry generation data or unmapped fields")
	}
	if omitted != nil {
		if err := restoreOmittedEvent(event, omitted); err != nil {
			return err
		}
		event.auxiliary, event.omittedAuxiliary = nil, omitted
		return nil
	}
	if event.Type != NativeEvent || event.Source != receipt.compiled.identity || event.Native == nil || !event.ResponseID.IsString(receipt.responseID) {
		return streamIssue(UnsupportedNative, "/native", "auxiliary source identity or native ownership changed")
	}
	if receipt.hasSequence {
		if !equalValues(event.Sequence, Value{raw: strconv.FormatInt(receipt.sequence, 10)}) {
			return streamIssue(InvalidAssociation, "/sequence", "auxiliary sequence differs from its source frame")
		}
	} else if !event.Sequence.IsZero() {
		return streamIssue(InvalidAssociation, "/sequence", "auxiliary event invented a source sequence")
	}
	if err := restoreReceiptNative(&event.Native, receipt, normalized); err != nil {
		return err
	}
	event.auxiliary, event.omittedAuxiliary = receipt, nil
	return nil
}

func isOmittedAuxiliaryEvent(event Event, target Target) bool {
	return matchesOmissionTarget(event.omittedAuxiliary, target) && restoreAuxiliaryEvent(&event, nil, event.omittedAuxiliary, false) == nil
}

func matchesOmissionTarget(proof *omittedEventReceipt, target Target) bool {
	return proof != nil && proof.target == target.Protocol && proof.encoderHash == target.eventEncoderHash && proof.decoderHash == target.eventDecoderHash && proof.scope == target.Scope && target.Direction == EncodeEvent
}

func restoreOmittedEvent(event *Event, proof *omittedEventReceipt) error {
	if hasAuxiliaryPayload(*event) || event.Type != MetadataUpdated || event.Native != nil || event.Source != proof.source || !equalValues(event.ResponseID, proof.responseID) || !equalValues(event.Sequence, proof.sequence) {
		return streamIssue(UnsupportedNative, "/native", "an omitted event cannot be repurposed")
	}
	return nil
}

func restoreReceiptNative(native **Native, receipt *sourceFrameReceipt, canNormalize bool) error {
	value := *native
	if value.RequestContext != nil || value.requestWorkspace != nil || value.Source != (Provenance{Protocol: receipt.compiled.identity, Direction: receipt.direction, Scope: receipt.scope}) {
		return streamIssue(UnsupportedNative, "/native", "validated frame ownership changed")
	}
	if value.Value != receipt.frame {
		if !canNormalize || compactValueString(value.Value.raw) != compactValueString(receipt.frame.raw) {
			return streamIssue(UnsupportedNative, "/native", "validated native frame changed")
		}
		copy := *value
		copy.Value = receipt.frame
		*native = &copy
	}
	return nil
}
