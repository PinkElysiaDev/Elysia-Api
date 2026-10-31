package protocol

import (
	"fmt"
	"slices"
)

// ProtocolControlDisposition permits omission of explicitly named opaque
// control kinds when a target has no equivalent state channel.
type ProtocolControlDisposition struct {
	Kinds       []string `json:"kinds"`
	Disposition string   `json:"disposition"`
	Path        string   `json:"path"`
	Reason      string   `json:"reason"`
}

type controlConversionOptions struct {
	CoreVersion int                        `json:"coreVersion"`
	Target      Identity                   `json:"target"`
	EncoderHash string                     `json:"encoderHash"`
	DecoderHash string                     `json:"decoderHash"`
	Control     ProtocolControlDisposition `json:"control"`
}

func (compiled *Compiled) compileControlDefault(declaration ProtocolConversionDefault) (ConversionRule, error) {
	if declaration.Phase != ConversionEvent || declaration.Steps != nil || declaration.Auxiliary != nil || declaration.Collection || declaration.Match.NodeKind != "" {
		return ConversionRule{}, fmt.Errorf("control disposition requires the event phase without steps, auxiliary disposition or collection")
	}
	encoder, hasEncoder := compiled.mappings[EncodeEvent]
	decoder, hasDecoder := compiled.mappings[DecodeEvent]
	if !hasEncoder || !hasDecoder {
		return ConversionRule{}, fmt.Errorf("control disposition requires paired event directions")
	}
	value, err := EncodeValue(controlConversionOptions{CoreVersion: compiled.CoreVersion(), Target: compiled.identity, EncoderHash: encoder.definitionHash, DecoderHash: decoder.definitionHash, Control: *declaration.Control})
	if err != nil {
		return ConversionRule{}, err
	}
	rule := ConversionRule{ID: declaration.ID, Order: declaration.Order, Enabled: declaration.Enabled, Phase: declaration.Phase, Match: declaration.Match, Action: "control_event", Value: value}
	_, err = compileControlEventOption(rule)
	return rule, err
}

func compileControlEventOption(rule ConversionRule) (conversionOption, error) {
	if rule.Phase != ConversionEvent || rule.Expression != nil || rule.Path != "" || rule.Match.NodeKind != "" {
		return conversionOption{}, fmt.Errorf("control_event requires a complete event declaration")
	}
	var options controlConversionOptions
	if err := decodeContract(rule.Value.Bytes(), &options); err != nil {
		return conversionOption{}, err
	}
	if options.CoreVersion != ProtocolCoreVersion || options.Target.DefinitionID == "" || options.EncoderHash == "" || options.DecoderHash == "" || options.Control.Disposition != "omit" || options.Control.Reason == "" || len(options.Control.Kinds) == 0 {
		return conversionOption{}, fmt.Errorf("control_event requires a pinned target, declared kinds and an omission reason")
	}
	seen := make(map[string]bool, len(options.Control.Kinds))
	for _, kind := range options.Control.Kinds {
		if !definitionIdentifier.MatchString(kind) || seen[kind] {
			return conversionOption{}, fmt.Errorf("control_event requires unique nonempty kind identifiers")
		}
		seen[kind] = true
	}
	if _, err := parsePointer(options.Control.Path); err != nil {
		return conversionOption{}, err
	}
	return conversionOption{raw: rule.Value, value: options}, nil
}

func (conversion *CompiledConversion) applyControlEvent(event *Event, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	proof := event.controlReceipt
	if proof == nil {
		return false, nil
	}
	if err := restoreControlEvent(event, proof, nil, false); err != nil {
		return false, err
	}
	options, err := readConversionOption[controlConversionOptions](conversion, rule)
	if err != nil {
		return false, err
	}
	if options.Target != route.Target || proof.source.Protocol != route.Source || proof.source.Direction != DecodeEvent || event.SchemaVersion != options.CoreVersion {
		return false, streamIssue(UnsupportedNative, "/control", "control disposition does not match the pinned route")
	}
	if CanPreserveNative(event.Control.Source, Target{Protocol: route.Target, Direction: EncodeEvent}) || !slices.Contains(options.Control.Kinds, event.Control.Kind) {
		return false, nil
	}
	if err := conversion.controlDiagnostic(rule, route, ProtocolControlDiagnostic{Kind: "degrade", Path: options.Control.Path, Attribution: "source"}, options.Control.Reason, sink); err != nil {
		return false, err
	}
	event.omittedControl = omitProtectedEvent(event, route, options.EncoderHash, options.DecoderHash)
	return true, nil
}
