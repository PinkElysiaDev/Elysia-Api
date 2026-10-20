package protocol

import "context"

// WireEventValidator checks cross-frame wire requirements that are not part of
// the semantic event model (for example an explicit Chat assistant role).
// It runs on the installed codec's request-owned instance, after wire mappings.
type WireEventValidator interface {
	ValidateWireEvent(Value) error
}

// WireObjectEventValidator and TypedObjectEventModule share only the current
// frame's read-only field map. Implementations must not mutate or retain it.
type WireObjectEventValidator interface {
	ValidateWireObjectEvent(Value, Object) error
}

type TypedObjectEventModule interface {
	DecodeObjectEvents(context.Context, Value, Object, EvaluationContext) ([]Event, error)
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
	v.replay, err = NewEventReplay(Target{Codec: c.Codec(EncodeEvent), Protocol: c.identity, Direction: EncodeEvent, Scope: scope, Capabilities: c.Capabilities(EncodeEvent)}, c.limits)
	return v, err
}

func (v *WireStreamValidation) Consume(ctx context.Context, frame Value) error {
	var fields Object
	if _, ok := v.compiled.mappings[EncodeEvent].module.(WireObjectOutputValidator); ok && frame.IsObject() {
		var err error
		fields, err = (EvaluationContext{}).WithValueReader(frame).ReadObject(frame)
		if err != nil {
			return err
		}
	}
	if err := v.compiled.validateWireOutput(EncodeEvent, frame, fields); err != nil {
		return err
	}
	if v.decoder == nil {
		return nil
	}
	if validator, ok := v.decoder.(WireObjectEventValidator); ok && fields != nil {
		if err := validator.ValidateWireObjectEvent(frame, fields); err != nil {
			return v.compiled.runtimeError(EncodeEvent, err)
		}
	} else if validator, ok := v.decoder.(WireEventValidator); ok {
		if err := validator.ValidateWireEvent(frame); err != nil {
			return v.compiled.runtimeError(EncodeEvent, err)
		}
	}
	var batch []Event
	if decoder, ok := v.decoder.(TypedEventModule); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		var events []Event
		var err error
		if objectDecoder, ok := decoder.(TypedObjectEventModule); ok && fields != nil {
			events, err = objectDecoder.DecodeObjectEvents(ctx, frame, fields, v.options)
		} else {
			events, err = decoder.DecodeEvents(ctx, frame, v.options)
		}
		if err != nil {
			return err
		}
		// Preserve Convert's EncodeSemantic budget and JSON normalization.
		// Decoder state may retain nested pointers, so provenance stamping and
		// normalization operate on owned containers, not the decoder's state.
		limits := semanticLimits(DefaultLimits())
		if err := checkSemanticValue(events, limits); err != nil {
			return err
		}
		batch = normalizeMappingSemantic(events, limits)
	} else {
		events, err := v.decoder.Convert(ctx, DecodeEvent, frame, v.options)
		if err != nil {
			return err
		}
		if err = events.Decode(&batch); err != nil {
			return err
		}
	}
	for _, event := range batch {
		event.SchemaVersion, event.Source = SemanticSchemaVersion, v.compiled.identity
		if err := v.compiled.stampEventProvenance(&event, DecodeEvent, v.options.Scope); err != nil {
			return err
		}
		if event.Type == NativeEvent {
			event.Native = &Native{Source: Provenance{Protocol: v.compiled.identity, Direction: DecodeEvent, Scope: v.options.Scope}, Value: frame}
		}
		if _, err := v.replay.Consume(event); err != nil {
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
