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

// normalizeDocument preserves the semantic JSON boundary on owned containers.
func (cache *semanticCache) normalizeDocument(value reflect.Value, _ *typedPlan) {
	normalizeCoreSemantic(value, cache)
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

func normalizeSemanticString(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	raw, _ := sonic.ConfigStd.Marshal(value)
	var normalized string
	_ = sonic.ConfigStd.Unmarshal(raw, &normalized)
	return normalized
}

// Preserve the encoder's sorted-key collision order when invalid UTF-8 keys
// normalize to the same decoded spelling. Values keep their existing ownership.
func normalizeSemanticKeys[M ~map[string]V, V any](value M) M {
	hasInvalidKey := false
	for key := range value {
		if !utf8.ValidString(key) {
			hasInvalidKey = true
			break
		}
	}
	if !hasInvalidKey {
		return value
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	normalized := make(M, len(value))
	for _, key := range keys {
		normalized[normalizeSemanticString(key)] = value[key]
	}
	return normalized
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
