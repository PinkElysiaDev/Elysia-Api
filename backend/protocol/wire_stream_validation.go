package protocol

import "context"

// WireEventValidator checks cross-frame wire requirements that are not part of
// the semantic event model (for example an explicit Chat assistant role).
// It runs on the installed codec's request-owned instance, after wire mappings.
type WireEventValidator interface {
	ValidateWireEvent(Value) error
}

// WireStreamValidation checks the bytes after after-mappings, wire rules and
// carrier insertion. It uses the installed target codec, not a user-authored
// decode mapping that could hide a malformed wire frame.
type WireStreamValidation struct {
	compiled *Compiled
	decoder  Module
	replay   *EventReplay
	options  EvaluationContext
}

func (c *Compiled) NewWireStreamValidation(scope Scope) (*WireStreamValidation, error) {
	v := &WireStreamValidation{compiled: c, options: EvaluationContext{Scope: scope, identity: c.identity}}
	if !knownConversionCodec(c.Codec(EncodeEvent)) {
		return v, nil
	}
	factory, ok := c.mappings[EncodeEvent].module.(StreamModule)
	if !ok {
		return v, nil
	}
	var err error
	v.decoder, err = factory.NewStream(DecodeEvent, c.limits)
	if err != nil {
		return nil, err
	}
	v.replay, err = NewEventReplay(Target{Protocol: c.identity, Direction: EncodeEvent, Scope: scope, Capabilities: c.Capabilities(EncodeEvent)}, c.limits)
	return v, err
}

func (v *WireStreamValidation) Consume(ctx context.Context, frame Value) error {
	if err := v.compiled.ValidateWireOutput(EncodeEvent, frame); err != nil {
		return err
	}
	if v.decoder == nil {
		return nil
	}
	if validator, ok := v.decoder.(WireEventValidator); ok {
		if err := validator.ValidateWireEvent(frame); err != nil {
			return v.compiled.runtimeError(EncodeEvent, err)
		}
	}
	events, err := v.decoder.Convert(ctx, DecodeEvent, frame, v.options)
	if err != nil {
		return err
	}
	var batch []Event
	if err = events.Decode(&batch); err != nil {
		return err
	}
	for _, event := range batch {
		event.SchemaVersion, event.Source = SemanticSchemaVersion, v.compiled.identity
		if err := v.compiled.stampEventProvenance(&event, DecodeEvent, v.options.Scope); err != nil {
			return err
		}
		if event.Type == NativeEvent {
			event.Native = &Native{Source: Provenance{Protocol: v.compiled.identity, Direction: DecodeEvent, Scope: v.options.Scope}, Value: frame}
		}
		if _, err = v.replay.Consume(event); err != nil {
			return err
		}
	}
	return nil
}

func (v *WireStreamValidation) Finish() error {
	if v.replay == nil {
		return nil
	}
	return v.replay.Finish()
}
