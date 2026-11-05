package protocol

import (
	"crypto/sha256"
	"strconv"
	"strings"
)

type controlEventReceipt struct {
	kind            string
	payload         Value
	source          Provenance
	responseID      Value
	sequence        Value
	frameNumber     int64
	frameDigest     [sha256.Size]byte
	encoderHash     string
	decoderHash     string
	canReplayNative bool
}

func (receipt *sourceFrameReceipt) attachControl(event *Event, view *inputView) error {
	control := event.Control
	if event.Type != ControlUpdated || control == nil || control.Kind == "" || control.FrameNumber != 0 || control.Source.Protocol != receipt.compiled.identity || control.Source.Direction != receipt.direction || control.Source.Scope != receipt.scope {
		return streamIssue(UnsupportedNative, "/control", "state frames require an isolated source-bound control value")
	}
	pointer, err := parsePointer(control.Source.Path)
	if err != nil {
		return err
	}
	payload, err := readValuePointerWithin(receipt.frame, pointer, &view.owned)
	if err != nil {
		return err
	}
	if payload.IsZero() || control.Payload.IsZero() || payload != control.Payload && compactValueString(payload.raw) != compactValueString(control.Payload.raw) {
		return streamIssue(UnsupportedNative, "/control/payload", "control payload differs from its captured source location")
	}
	if !event.Sequence.IsZero() && (!receipt.hasSequence || !equalValues(event.Sequence, Value{raw: strconv.FormatInt(receipt.sequence, 10)})) {
		return streamIssue(InvalidAssociation, "/sequence", "control sequence differs from its source frame")
	}
	// State may wait behind another item. Retain only its selected payload and
	// detached identity scalars, never the backing frame or its read caches.
	control.Payload, control.FrameNumber = Value{raw: strings.Clone(payload.raw)}, int64(receipt.frameNumber)
	control.Kind, control.Source.Path = strings.Clone(control.Kind), strings.Clone(control.Source.Path)
	event.ResponseID, event.Sequence = Value{raw: strings.Clone(event.ResponseID.raw)}, Value{raw: strings.Clone(event.Sequence.raw)}
	event.controlReceipt = &controlEventReceipt{kind: control.Kind, payload: control.Payload, source: control.Source, responseID: event.ResponseID, sequence: event.Sequence,
		frameNumber: control.FrameNumber, frameDigest: hashValueChunks(receipt.frame.raw), encoderHash: receipt.compiled.mappings[eventEncoder(receipt.direction)].definitionHash,
		decoderHash: receipt.compiled.mappings[receipt.direction].definitionHash, canReplayNative: receipt.compiled.native.Preserve}
	return restoreControlEvent(event, event.controlReceipt, nil, false)
}

func restoreControlEvent(event *Event, receipt *controlEventReceipt, omitted *omittedEventReceipt, canNormalize bool) error {
	if receipt == nil && omitted == nil {
		return nil
	}
	if omitted != nil {
		if err := restoreOmittedEvent(event, omitted); err != nil {
			return err
		}
		event.controlReceipt, event.omittedControl = nil, omitted
		return nil
	}
	control := event.Control
	withoutControl := *event
	withoutControl.Control = nil
	if hasAuxiliaryPayload(withoutControl) || event.Type != ControlUpdated || control == nil || event.Source != receipt.source.Protocol || control.Kind != receipt.kind || control.Source != receipt.source || control.FrameNumber != receipt.frameNumber || !equalValues(event.ResponseID, receipt.responseID) || !equalValues(event.Sequence, receipt.sequence) {
		return streamIssue(UnsupportedNative, "/control", "source control identity or isolated payload changed")
	}
	if control.Payload != receipt.payload {
		if !canNormalize || compactValueString(control.Payload.raw) != compactValueString(receipt.payload.raw) {
			return streamIssue(UnsupportedNative, "/control/payload", "source control payload changed")
		}
		copy := *control
		copy.Payload = receipt.payload
		event.Control = &copy
	}
	if event.Native != nil {
		if err := receipt.checkNative(event.Native); err != nil {
			return err
		}
	}
	event.controlReceipt, event.omittedControl = receipt, nil
	return nil
}

func (receipt *controlEventReceipt) checkNative(native *Native) error {
	source := receipt.source
	source.Path = ""
	if native.RequestContext != nil || native.requestWorkspace != nil || native.Source != source || hashValueChunks(native.Value.raw) != receipt.frameDigest {
		return streamIssue(UnsupportedNative, "/native", "control native value differs from its authenticated source frame")
	}
	return nil
}

func restoreProtectedEvent(event *Event, auxiliary *sourceFrameReceipt, omittedAuxiliary *omittedEventReceipt, control *controlEventReceipt, omittedControl *omittedEventReceipt, canNormalize bool) error {
	if err := restoreAuxiliaryEvent(event, auxiliary, omittedAuxiliary, canNormalize); err != nil {
		return err
	}
	return restoreControlEvent(event, control, omittedControl, canNormalize)
}

func isOmittedControlEvent(event Event, target Target) bool {
	return matchesOmissionTarget(event.omittedControl, target) && restoreControlEvent(&event, nil, event.omittedControl, false) == nil
}
