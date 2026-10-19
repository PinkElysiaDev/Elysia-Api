package protocol

import (
	"context"
	"reflect"
	"strings"
)

// conversionState owns the decoded document for a contiguous run of built-in
// actions. Authored JSON operations synchronize it before observing any value.
// It is never retained by the compiled policy or shared with another call.
type conversionState struct {
	wire            Value
	document        any
	limits          Limits
	isStrict        bool
	needsEncoding   bool
	normalized      map[Value]Value
	normalizedBytes int
}

func (state *conversionState) validateContext(phase ConversionPhase) error {
	if phase != ConversionIngress && phase != ConversionRequest {
		return nil
	}
	if err := state.load(phase, false); err != nil {
		return err
	}
	return validateRequestContext(state.document.(*Request))
}

func (state *conversionState) accepts(phase ConversionPhase, rule ConversionRule) bool {
	if rule.Match.Present != nil || rule.Match.Path != "" || phase != ConversionRequest {
		return false
	}
	switch rule.Action {
	case "system_instruction_hoist", "chat_history", "assistant_history", "stream_options", "request_projection", "responses_include", "responses_context", "responses_storage", "gemini_safety_settings", "reasoning_controls", "response_metadata", "signatures", "tool_result_text", "tool_result_object":
		return true
	}
	return false
}

func (state *conversionState) load(phase ConversionPhase, strict bool) error {
	if state.document != nil && (!strict || state.isStrict) {
		return nil
	}
	var document any
	switch phase {
	case ConversionIngress, ConversionRequest:
		document = &Request{}
	case ConversionResponse:
		document = &Response{}
	case ConversionEvent:
		document = &Event{}
	}
	var err error
	if strict {
		err = decodeContract(state.wire.Bytes(), document)
	} else {
		err = state.wire.Decode(document)
	}
	if err != nil {
		return err
	}
	state.document, state.isStrict = document, strict
	return nil
}

func (state *conversionState) apply(ctx context.Context, c *CompiledConversion, phase ConversionPhase, rule ConversionRule, route ConversionContext, sink *DiagnosticSink) (string, error) {
	isNodeRule := rule.Action == "signatures" || rule.Action == "tool_result_text" || rule.Action == "tool_result_object"
	if !isNodeRule && !rule.Match.matches(route, Value{}) {
		return "", nil
	}
	if err := state.load(phase, rule.Action != "response_metadata"); err != nil {
		return "", err
	}
	var target string
	var err error
	if isNodeRule {
		err = c.applyNodeObject(ctx, phase, state.document, route, sink, rule)
	} else if rule.Action == "response_metadata" {
		err = c.metadataObject(phase, state.document, route, rule, sink)
	} else {
		target, err = c.applyRequestObject(state.document.(*Request), route, rule, sink)
	}
	if err != nil {
		return "", err
	}
	// Match the old pre-serialization check before removing omitted fields.
	if err := checkSemanticValue(state.document, state.limits); err != nil {
		return "", err
	}
	state.normalize(reflect.ValueOf(state.document), typedLimitPlans[reflect.TypeOf(state.document)])
	state.needsEncoding, state.isStrict = true, true
	return target, nil
}

func (state *conversionState) flush() (Value, error) {
	if !state.needsEncoding {
		return state.wire, nil
	}
	value, err := encodeSemanticWithin(state.document, state.limits)
	if err != nil {
		return Value{}, err
	}
	state.wire, state.needsEncoding = value, false
	return value, nil
}

// normalize reproduces the intervening JSON encode/decode boundary without
// copying unchanged payloads. Empty omitted containers disappear before the
// next rule; immutable JSON keeps number spelling and existing escape spelling.
func (state *conversionState) normalize(value reflect.Value, plan *typedPlan) {
	if plan.isJSON {
		original := value.Interface().(Value)
		if original.IsZero() {
			return
		}
		if result, ok := state.normalized[original]; ok {
			value.Set(reflect.ValueOf(result))
			return
		}
		result := original
		if needsSemanticCompaction(original.raw) {
			result, _ = EncodeValue(original)
		}
		retainedBytes := len(original.raw)
		if original != result {
			retainedBytes += len(result.raw)
		}
		if len(state.normalized) < state.limits.StateItems && retainedBytes <= state.limits.BufferBytes-state.normalizedBytes {
			if state.normalized == nil {
				state.normalized = make(map[Value]Value)
			}
			state.normalized[original] = result
			state.normalizedBytes += retainedBytes
		}
		value.Set(reflect.ValueOf(result))
		return
	}
	switch plan.kind {
	case reflect.Pointer:
		if !value.IsNil() {
			state.normalize(value.Elem(), plan.element)
		}
	case reflect.Struct:
		for _, field := range plan.fields {
			child := value.Field(field.index)
			if field.omitEmpty && isEmptyJSONField(child) || field.omitZero && child.IsZero() {
				child.SetZero()
				continue
			}
			state.normalize(child, field.plan)
		}
	case reflect.Slice:
		for index := range value.Len() {
			state.normalize(value.Index(index), plan.element)
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			child := reflect.New(value.Type().Elem()).Elem()
			child.Set(iter.Value())
			state.normalize(child, plan.element)
			value.SetMapIndex(iter.Key(), child)
		}
	}
}

// Value is already valid JSON. Only whitespace outside strings and the HTML /
// JavaScript separators escaped by encoding/json can change at this boundary.
func needsSemanticCompaction(raw string) bool {
	for offset := 0; offset < len(raw); offset++ {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n':
			return true
		case '"':
			end := jsonStringEnd(raw, offset)
			text := raw[offset:end]
			if strings.ContainsAny(text, "<>&") || strings.Contains(text, "\u2028") || strings.Contains(text, "\u2029") {
				return true
			}
			offset = end - 1
		}
	}
	return false
}
