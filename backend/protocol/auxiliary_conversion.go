package protocol

import (
	"context"
	"fmt"
	"strings"
)

func (conversion *CompiledConversion) convertProtectedEvent(ctx context.Context, event Event, route ConversionContext, sink *DiagnosticSink) (Event, error) {
	if err := restoreProtectedEvent(&event, event.auxiliary, event.omittedAuxiliary, event.controlReceipt, event.omittedControl, false); err != nil {
		return Event{}, err
	}
	if conversion == nil {
		return event, nil
	}
	limits := conversion.phaseLimits(ctx, ConversionEvent)
	if err := checkSemanticValue(event, limits); err != nil {
		return Event{}, err
	}
	// Source frames keep opaque immutable values. Only their small headers
	// need ownership; normalizing the native body would destroy byte replay.
	if event.Native != nil {
		native := *event.Native
		event.Native = &native
	}
	if event.Control != nil {
		control := *event.Control
		event.Control = &control
	}
	state := conversionState{document: &event, limits: limits, isStrict: true, needsEncoding: true, cache: &semanticCache{limits: limits}, auxiliary: event.auxiliary, omittedAuxiliary: event.omittedAuxiliary, control: event.controlReceipt, omittedControl: event.omittedControl}
	if _, err := conversion.applyConversionState(ctx, ConversionEvent, Value{}, route, sink, &state, false); err != nil {
		return Event{}, err
	}
	if err := state.load(ConversionEvent, true); err != nil {
		return Event{}, err
	}
	result := *state.document.(*Event)
	if err := restoreProtectedEvent(&result, state.auxiliary, state.omittedAuxiliary, state.control, state.omittedControl, false); err != nil {
		return Event{}, err
	}
	return result, nil
}

// ProtocolAuxiliaryDisposition declares the target's treatment of a source
// frame that its independent wire contract proved to contain no generation.
type ProtocolAuxiliaryDisposition struct {
	Disposition string `json:"disposition"`
	Path        string `json:"path"`
	Reason      string `json:"reason"`
}

type auxiliaryConversionOptions struct {
	CoreVersion    int                          `json:"coreVersion"`
	Target         Identity                     `json:"target"`
	EncoderHash    string                       `json:"encoderHash"`
	DecoderHash    string                       `json:"decoderHash"`
	PreserveNative bool                         `json:"preserveNative"`
	Disposition    ProtocolAuxiliaryDisposition `json:"auxiliary"`
}

func (compiled *Compiled) compileAuxiliaryDefault(declaration ProtocolConversionDefault) (ConversionRule, error) {
	if declaration.Phase != ConversionEvent || declaration.Steps != nil || declaration.Collection || declaration.Match.NodeKind != "" {
		return ConversionRule{}, fmt.Errorf("auxiliary disposition requires the event phase, without control steps or collection")
	}
	encoder, hasEncoder := compiled.mappings[EncodeEvent]
	decoder, hasDecoder := compiled.mappings[DecodeEvent]
	if !hasEncoder || !hasDecoder {
		return ConversionRule{}, fmt.Errorf("auxiliary disposition requires paired event directions")
	}
	options := auxiliaryConversionOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, EncoderHash: encoder.definitionHash, DecoderHash: decoder.definitionHash, PreserveNative: compiled.native.Preserve, Disposition: *declaration.Auxiliary}
	value, err := EncodeValue(options)
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "auxiliary_event", Value: value}
	_, err = compileAuxiliaryOption(rule)
	return rule, err
}

func compileAuxiliaryOption(rule ConversionRule) (conversionOption, error) {
	if rule.Phase != ConversionEvent || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" {
		return conversionOption{}, fmt.Errorf("auxiliary_event requires a complete event declaration")
	}
	var options auxiliaryConversionOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || options.EncoderHash == "" || options.DecoderHash == "" || options.Disposition.Disposition != "omit" || options.Disposition.Reason == "" {
		return conversionOption{}, fmt.Errorf("auxiliary_event requires a pinned target and an explicit omission reason")
	}
	if _, err := parsePointer(options.Disposition.Path); err != nil {
		return conversionOption{}, err
	}
	return conversionOption{raw: rule.Value, value: options}, nil
}

func (conversion *CompiledConversion) applyAuxiliaryEvent(event *Event, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	proof := event.auxiliary
	if proof == nil {
		return false, nil
	}
	if err := restoreAuxiliaryEvent(event, proof, nil, false); err != nil {
		return false, err
	}
	options, err := readConversionOption[auxiliaryConversionOptions](conversion, rule)
	if err != nil {
		return false, err
	}
	if options.Target != route.Target || proof.compiled.identity != route.Source || proof.direction != DecodeEvent || event.SchemaVersion != options.CoreVersion {
		return false, streamIssue(UnsupportedNative, "/native", "auxiliary disposition does not match the pinned route")
	}
	target := Target{Protocol: route.Target, Direction: EncodeEvent, Scope: route.Scope}
	if CanPreserveNative(event.Native.Source, target) {
		if options.PreserveNative && proof.compiled.native.Preserve && proof.compiled.mappings[EncodeEvent].definitionHash == options.EncoderHash && proof.compiled.mappings[DecodeEvent].definitionHash == options.DecoderHash && CheckScope(proof.scope, route.Scope) {
			return false, nil
		}
		return false, streamIssue(UnsupportedNative, options.Disposition.Path, "auxiliary replay requires equivalent mappings, native policy and scope")
	}
	if err := conversion.controlDiagnostic(rule, route, ProtocolControlDiagnostic{Kind: "degrade", Path: options.Disposition.Path, Attribution: "source"}, options.Disposition.Reason, sink); err != nil {
		return false, err
	}
	event.omittedAuxiliary = omitProtectedEvent(event, route, options.EncoderHash, options.DecoderHash)
	return true, nil
}

// An omission keeps detached identity scalars, never the source frame or its
// compiled plan. It is only called after a disposition has authorized loss.
func omitProtectedEvent(event *Event, route ConversionContext, encoderHash, decoderHash string) *omittedEventReceipt {
	event.ResponseID = Value{raw: strings.Clone(event.ResponseID.raw)}
	event.Sequence = Value{raw: strings.Clone(event.Sequence.raw)}
	proof := &omittedEventReceipt{source: event.Source, target: route.Target, scope: route.Scope, responseID: event.ResponseID, sequence: event.Sequence, encoderHash: encoderHash, decoderHash: decoderHash}
	event.Control, event.controlReceipt, event.auxiliary, event.Native, event.Type = nil, nil, nil, nil, MetadataUpdated
	return proof
}
