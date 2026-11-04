package protocol

import (
	"github.com/bytedance/sonic"
	"reflect"
	"slices"
	"unicode/utf8"
)

// originalNormalizeDocument reproduces the intervening JSON encode/decode boundary
// without copying unchanged payloads. The caller owns all mutable containers.
func (cache *semanticCache) originalNormalizeDocument(value reflect.Value, plan *typedPlan) {
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
			cache.originalNormalizeDocument(value.Elem(), plan.element)
		}
	case reflect.Struct:
		for _, field := range plan.fields {
			child := value.Field(field.index)
			if field.omitEmpty && isEmptyJSONField(child) || field.omitZero && child.IsZero() {
				child.SetZero()
				continue
			}
			cache.originalNormalizeDocument(child, field.plan)
		}
	case reflect.Slice:
		for index := range value.Len() {
			cache.originalNormalizeDocument(value.Index(index), plan.element)
		}
	case reflect.Map:
		originalNormalizeSemanticMapKeys(value)
		if object, ok := value.Interface().(Object); ok {
			cache.normalizeObject(object)
			return
		}
		iter := value.MapRange()
		for iter.Next() {
			child := reflect.New(value.Type().Elem()).Elem()
			child.Set(iter.Value())
			cache.originalNormalizeDocument(child, plan.element)
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

func originalNormalizeSemanticMapKeys(value reflect.Value) {
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
