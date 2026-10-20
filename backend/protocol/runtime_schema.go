package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// runtimeValueSchema is private, immutable after compilation, and owned by one
// compiled revision. The library owns validation; ValueSchema remains our
// deliberately restricted authoring language, not an arbitrary JSON Schema.
type runtimeValueSchema struct {
	schema *jsonschema.Schema
}

func compileValueSchema(schema *ValueSchema, limits Limits) (*runtimeValueSchema, error) {
	if schema == nil {
		return nil, nil
	}
	if err := validateSchema(schema, "/schema", 1, limits); err != nil {
		return nil, err
	}
	document, err := runtimeSchemaDocument(schema, limits)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const location = "urn:elysia:runtime-schema"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, err
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, err
	}
	return &runtimeValueSchema{schema: compiled}, nil
}

func runtimeSchemaDocument(schema *ValueSchema, limits Limits) (map[string]any, error) {
	document := map[string]any{}
	if schema.Type != AnyType {
		document["type"] = string(schema.Type)
	}
	if len(schema.Enum) > 0 {
		values := make([]any, 0, len(schema.Enum))
		for _, value := range schema.Enum {
			decoded, err := decodeSchemaValue(value, "/schema/enum", limits)
			if err != nil {
				return nil, err
			}
			// Our authoring subset permits repeated enum values, whereas the
			// standard metaschema requires a unique array. Deduplicate exactly.
			duplicate := false
			for _, previous := range values {
				if equalJSON(previous, decoded) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				values = append(values, decoded)
			}
		}
		document["enum"] = values
	}
	if schema.Type == ObjectType {
		document["additionalProperties"] = schema.AllowUnknown
		properties := map[string]any{}
		for _, key := range sortedKeys(schema.Properties) {
			child := schema.Properties[key]
			property, err := runtimeSchemaDocument(&child, limits)
			if err != nil {
				return nil, err
			}
			properties[key] = property
		}
		document["properties"] = properties
		if len(schema.Required) > 0 {
			required := make([]any, len(schema.Required))
			for i, key := range schema.Required {
				required[i] = key
			}
			document["required"] = required
		}
	}
	if schema.Type == ArrayType && schema.Items != nil {
		items, err := runtimeSchemaDocument(schema.Items, limits)
		if err != nil {
			return nil, err
		}
		document["items"] = items
	}
	// Nullable bypasses *all* constraints in the gateway subset, including enum.
	if schema.Nullable {
		return map[string]any{"anyOf": []any{map[string]any{"type": "null"}, document}}, nil
	}
	return document, nil
}

func (schema *runtimeValueSchema) validate(value Value, path string, limits Limits) error {
	if schema == nil {
		return nil
	}
	if value.IsZero() {
		return fmt.Errorf("%s is missing", path)
	}
	decoded, err := decodeSchemaValue(value, path, limits)
	if err != nil {
		return err
	}
	if err := schema.schema.Validate(decoded); err != nil {
		location := path
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			for len(validation.Causes) > 0 {
				validation = validation.Causes[0]
			}
			for _, key := range validation.InstanceLocation {
				location += "/" + escapePointer(key)
			}
		}
		return locateMappingError(location, fmt.Errorf("%s: %w", path, err))
	}
	return nil
}

// Standalone compilation is used by compile-time literal checks. Requests reuse
// the validators stored in the compiled revision.
func checkValueSchema(value Value, schema *ValueSchema, path string, depth int, limits Limits) error {
	if schema == nil {
		return nil
	}
	if depth > limits.Depth {
		return fmt.Errorf("%s exceeds value depth limit", path)
	}
	compiled, err := compileValueSchema(schema, limits)
	if err != nil {
		return err
	}
	return compiled.validate(value, path, limits)
}

var schemaJSON = sonic.Config{UseNumber: true}.Froze()

func decodeSchemaValue(value Value, path string, limits Limits) (any, error) {
	// Bound the entire value before materializing an interface tree, including
	// unconstrained/unknown descendants that the old walker could skip.
	if err := checkValueLimits(value, limits); err != nil {
		return nil, err
	}
	var decoded any
	raw := value.raw
	// Value accepts the standard decoder's invalid-UTF-8 replacement behavior.
	// Normalize only the temporary validation view, never the stored wire bytes.
	if !utf8.ValidString(raw) {
		raw = string([]rune(raw))
	}
	if err := schemaJSON.UnmarshalFromString(raw, &decoded); err != nil {
		return nil, err
	}
	if location, exceeded := schemaNumberLimitPath(decoded); exceeded {
		for _, key := range location {
			path += "/" + escapePointer(key)
		}
		return nil, fmt.Errorf("%s exceeds schema numeric conversion limit", path)
	}
	return decoded, nil
}

// The library uses big.Rat for integer/enum comparisons. Check decimal work
// before invoking it: a short exponent must not trigger unbounded allocation.
// This does not constrain native Value storage or schema-free mapping paths.
func schemaNumberLimitPath(value any) ([]string, bool) {
	switch value := value.(type) {
	case json.Number:
		text := string(value)
		bounded := len(text) <= maxNumericConversionDigits
		if index := strings.IndexAny(text, "eE"); index >= 0 {
			exponent, err := strconv.Atoi(text[index+1:])
			bounded = bounded && err == nil && exponent >= -maxNumericConversionDigits && exponent <= maxNumericConversionDigits
		}
		if !bounded {
			return nil, true
		}
	case map[string]any:
		for key, child := range value {
			if location, exceeded := schemaNumberLimitPath(child); exceeded {
				return append([]string{key}, location...), true
			}
		}
	case []any:
		for i, child := range value {
			if location, exceeded := schemaNumberLimitPath(child); exceeded {
				return append([]string{strconv.Itoa(i)}, location...), true
			}
		}
	}
	return nil, false
}
