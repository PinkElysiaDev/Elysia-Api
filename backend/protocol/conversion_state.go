package protocol

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// conversionState owns the decoded document for a contiguous run of built-in
// actions. Authored JSON operations synchronize it before observing any value.
// It is never retained by the compiled policy or shared with another call.
type conversionState struct {
	wire          Value
	document      any
	limits        Limits
	isStrict      bool
	needsEncoding bool
	cache         *semanticCache
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
	if rule.Match.Present != nil || rule.Match.Path != "" {
		return false
	}
	if phase == ConversionResponse || phase == ConversionEvent {
		switch rule.Action {
		case "chat_response_model", "responses_turn_state", "anthropic_usage_envelope", "response_shape", "response_envelope", "response_metadata", "error_metadata", "usage_projection", "signatures":
			return true
		}
		return false
	}
	if phase != ConversionRequest {
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
	isStrict := isNodeRule || rule.Action == "responses_turn_state" || (phase == ConversionRequest && rule.Action != "response_metadata")
	if err := state.load(phase, isStrict); err != nil {
		return "", err
	}
	if state.cache == nil {
		state.cache = &semanticCache{limits: state.limits}
	}
	var target string
	var err error
	shouldEncode := true
	if isNodeRule {
		err = c.applyNodeObject(ctx, phase, state.document, route, sink, rule, state.cache)
	} else if rule.Action == "response_metadata" {
		err = c.metadataObject(phase, state.document, route, rule, sink)
	} else if phase == ConversionRequest {
		target, err = c.applyRequestObject(state.document.(*Request), route, rule, sink)
	} else {
		shouldEncode, err = c.applyResponseObject(phase, state.document, route, rule, sink)
	}
	if err != nil {
		return "", err
	}
	if !shouldEncode {
		return target, nil
	}
	// Match the old pre-serialization check before removing omitted fields.
	if err := state.cache.check(state.document, state.limits); err != nil {
		return "", err
	}
	state.normalize(reflect.ValueOf(state.document), typedLimitPlans[reflect.TypeOf(state.document)])
	state.needsEncoding, state.isStrict = true, true
	state.wire = Value{} // The owned document now contains the complete current value.
	return target, nil
}

func (c *CompiledConversion) applyResponseObject(phase ConversionPhase, document any, route ConversionContext, rule ConversionRule, sink *DiagnosticSink) (bool, error) {
	switch rule.Action {
	case "chat_response_model":
		return true, c.repairChatResponseModel(document.(*Response), route, rule, sink)
	case "responses_turn_state":
		return c.projectTurnStateObject(document.(*Event), rule, sink)
	case "anthropic_usage_envelope":
		return c.envelopeUsageObject(phase, document, route, rule, sink)
	case "response_shape", "response_envelope":
		return c.responseObject(rule.Action, phase, document, route, rule, sink)
	case "error_metadata":
		return c.errorMetadataObject(phase, document, route, rule, sink)
	case "usage_projection":
		return c.projectUsageObject(phase, document, route, rule, sink)
	}
	panic("unregistered typed response action")
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
		value.Set(reflect.ValueOf(state.cache.normalize(original)))
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
		normalizeSemanticMapKeys(value)
		iter := value.MapRange()
		for iter.Next() {
			child := reflect.New(value.Type().Elem()).Elem()
			child.Set(iter.Value())
			state.normalize(child, plan.element)
			value.SetMapIndex(iter.Key(), child)
		}
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			raw, _ := json.Marshal(value.String())
			var normalized string
			_ = json.Unmarshal(raw, &normalized)
			value.SetString(normalized)
		}
	}
}

func normalizeSemanticMapKeys(value reflect.Value) {
	hasInvalidKey := false
	iter := value.MapRange()
	for iter.Next() {
		hasInvalidKey = hasInvalidKey || !utf8.ValidString(iter.Key().String())
	}
	if !hasInvalidKey {
		return
	}
	keys := make([]string, 0, value.Len())
	iter = value.MapRange()
	for iter.Next() {
		keys = append(keys, iter.Key().String())
	}
	slices.Sort(keys)
	normalized := reflect.MakeMapWithSize(value.Type(), value.Len())
	for _, key := range keys {
		wire, _ := json.Marshal(key)
		var name string
		_ = json.Unmarshal(wire, &name)
		normalized.SetMapIndex(reflect.ValueOf(name), value.MapIndex(reflect.ValueOf(key)))
	}
	value.Set(normalized)
}

// cloneSemantic owns all mutable containers, including nested Native pointers.
// Unlike a JSON round trip, copying an immutable Value never copies its bytes.
func cloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {
	if plan.isJSON {
		return value
	}
	result := reflect.New(value.Type()).Elem()
	switch plan.kind {
	case reflect.Pointer:
		if !value.IsNil() {
			result.Set(reflect.New(value.Type().Elem()))
			result.Elem().Set(cloneSemantic(value.Elem(), plan.element))
		}
	case reflect.Struct:
		result.Set(value)
		for _, field := range plan.fields {
			result.Field(field.index).Set(cloneSemantic(value.Field(field.index), field.plan))
		}
	case reflect.Slice:
		if !value.IsNil() {
			result.Set(reflect.MakeSlice(value.Type(), value.Len(), value.Len()))
			for index := range value.Len() {
				result.Index(index).Set(cloneSemantic(value.Index(index), plan.element))
			}
		}
	case reflect.Map:
		if !value.IsNil() {
			result.Set(reflect.MakeMapWithSize(value.Type(), value.Len()))
			iter := value.MapRange()
			for iter.Next() {
				result.SetMapIndex(iter.Key(), cloneSemantic(iter.Value(), plan.element))
			}
		}
	default:
		result.Set(value)
	}
	return result
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
