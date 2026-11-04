package protocol

import (
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"
)

// normalizeMappingSemantic reproduces a semantic JSON boundary on owned mutable
// containers while preserving immutable payloads that need no normalization.
func normalizeMappingSemantic[T any](input T, limits Limits) T {
	value := reflect.ValueOf(input)
	plan := typedLimitPlans[value.Type()]
	owned := cloneSemantic(value, plan)
	cache := semanticCache{limits: limits}
	cache.normalizeDocument(owned, plan)
	return owned.Interface().(T)
}

// normalizeDocument reproduces the intervening JSON encode/decode boundary
// without copying unchanged payloads. The caller owns all mutable containers.
func (cache *semanticCache) normalizeDocument(value reflect.Value, plan *typedPlan) {
	if plan.isJSON {
		original := value.Interface().(Value)
		if !original.IsZero() {
			value.Set(reflect.ValueOf(cache.normalize(original)))
		}
		return
	}
	switch plan.kind {
	case reflect.Pointer:
		if !value.IsNil() {
			cache.normalizeDocument(value.Elem(), plan.element)
		}
	case reflect.Struct:
		for _, field := range plan.fields {
			child := value.Field(field.index)
			if field.omitEmpty && isEmptyJSONField(child) || field.omitZero && child.IsZero() {
				child.SetZero()
				continue
			}
			cache.normalizeDocument(child, field.plan)
		}
	case reflect.Slice:
		for index := range value.Len() {
			cache.normalizeDocument(value.Index(index), plan.element)
		}
	case reflect.Map:
		normalizeSemanticMapKeys(value)
		if object, ok := value.Interface().(Object); ok {
			cache.normalizeObject(object)
			return
		}
		iter := value.MapRange()
		for iter.Next() {
			child := reflect.New(value.Type().Elem()).Elem()
			child.Set(iter.Value())
			cache.normalizeDocument(child, plan.element)
			value.SetMapIndex(iter.Key(), child)
		}
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			raw, _ := sonic.ConfigStd.Marshal(value.String())
			var normalized string
			_ = sonic.ConfigStd.Unmarshal(raw, &normalized)
			value.SetString(normalized)
		}
	}
}

func (cache *semanticCache) normalizeObject(object Object) {
	for key, value := range object {
		if value.IsZero() {
			continue
		}
		if normalized := cache.normalize(value); normalized != value {
			object[key] = normalized
		}
	}
}

func normalizeSemanticMapKeys(value reflect.Value) {
	hasInvalidKey := false
	iter := value.MapRange()
	for iter.Next() {
		if !utf8.ValidString(iter.Key().String()) {
			hasInvalidKey = true
			break
		}
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
		wire, _ := sonic.ConfigStd.Marshal(key)
		var name string
		_ = sonic.ConfigStd.Unmarshal(wire, &name)
		normalized.SetMapIndex(reflect.ValueOf(name), value.MapIndex(reflect.ValueOf(key)))
	}
	value.Set(normalized)
}

// cloneSemantic owns mutable Core containers while sharing immutable Values.
func cloneSemantic(value reflect.Value, _ *typedPlan) reflect.Value {
	return cloneCoreSemantic(value.Interface())
}

// Value is already valid JSON. Only whitespace outside strings and the HTML /
// JavaScript separators and invalid UTF-8 escaped by the codec can change here.
func needsSemanticCompaction(raw string) bool {
	for offset := 0; offset < len(raw); offset++ {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n':
			return true
		case '"':
			end := jsonStringEnd(raw, offset)
			text := raw[offset:end]
			if strings.IndexByte(text, '<') >= 0 || strings.IndexByte(text, '>') >= 0 || strings.IndexByte(text, '&') >= 0 || strings.Contains(text, "\u2028") || strings.Contains(text, "\u2029") || !utf8.ValidString(text) {
				return true
			}
			offset = end - 1
		}
	}
	return false
}
