package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// wireSchemaPlan validates the restricted ValueSchema without decoding payloads
// into an interface tree. The existing validator supplies failure diagnostics
// only; it can never turn a planned rejection into successful validation.
type wireSchemaPlan struct {
	kind         JSONType
	nullable     bool
	allowUnknown bool
	required     []string
	properties   map[string]*wireSchemaPlan
	items        *wireSchemaPlan
	enum         []Value
	diagnostic   *runtimeValueSchema
}

func compileWireSchemaPlan(schema *ValueSchema, limits Limits) (*wireSchemaPlan, error) {
	diagnostic, err := compileValueSchema(schema, limits)
	if err != nil || schema == nil {
		return nil, err
	}
	plan := buildWireSchemaPlan(schema)
	plan.diagnostic = diagnostic
	return plan, nil
}

func buildWireSchemaPlan(schema *ValueSchema) *wireSchemaPlan {
	if schema == nil {
		return nil
	}
	plan := &wireSchemaPlan{kind: schema.Type, nullable: schema.Nullable, allowUnknown: schema.AllowUnknown, required: slices.Clone(schema.Required), enum: slices.Clone(schema.Enum)}
	if len(schema.Properties) > 0 {
		plan.properties = make(map[string]*wireSchemaPlan, len(schema.Properties))
		for name, child := range schema.Properties {
			plan.properties[name] = buildWireSchemaPlan(&child)
		}
	}
	plan.items = buildWireSchemaPlan(schema.Items)
	return plan
}

func (plan *wireSchemaPlan) validate(ctx context.Context, value Value, path string, limits Limits) error {
	return plan.validateWithin(ctx, value, path, limits, nil)
}

func (plan *wireSchemaPlan) validateWithin(ctx context.Context, value Value, path string, limits Limits, view *inputView) error {
	if plan == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.IsZero() {
		return fmt.Errorf("%s is missing", path)
	}
	if err := view.checkResources(ctx, value, limits, "wire"); err != nil {
		return err
	}
	// No selected value escapes validation. Borrow small fragments as well as
	// large payloads, retaining the existing structural index for nested values.
	var reader *valueReader
	if view != nil {
		reader = &view.borrowed
	} else {
		reader = newNativeComparisonReader(value)
	}
	var objects *planObjectReader
	if view != nil {
		objects = view.objectReader()
	}
	return plan.validateShapeWithin(ctx, value, path, limits, reader, objects)
}

func (plan *wireSchemaPlan) validateShape(ctx context.Context, value Value, path string, limits Limits, reader *valueReader) error {
	return plan.validateShapeWithin(ctx, value, path, limits, reader, nil)
}

func (plan *wireSchemaPlan) validateShapeWithin(ctx context.Context, value Value, path string, limits Limits, reader *valueReader, objects *planObjectReader) error {
	isValid, err := plan.accepts(ctx, value, reader, objects)
	if err != nil {
		return err
	}
	if isValid {
		return ctx.Err()
	}
	if err := plan.diagnostic.validate(value, path, limits); err != nil {
		return err
	}
	return locateMappingError(path, fmt.Errorf("wire schema plan rejected a value without a matching schema diagnostic"))
}

func (plan *wireSchemaPlan) accepts(ctx context.Context, value Value, reader *valueReader, objects *planObjectReader) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	raw := strings.TrimSpace(value.raw)
	kind := wireSchemaValueType(raw)
	if kind == NumberType {
		if _, exceeded := schemaNumberLimitPath(json.Number(raw)); exceeded {
			return false, nil
		}
	}
	if plan != nil {
		if plan.nullable && kind == NullType {
			return true, nil
		}
		if plan.kind == IntegerType && kind == NumberType {
			number, ok := exactNumber(raw)
			if !ok || !number.IsInt() {
				return false, nil
			}
		} else if plan.kind != AnyType && plan.kind != kind {
			return false, nil
		}
	}
	switch kind {
	case ObjectType:
		fields, err := objects.readFields(value, reader)
		if err != nil {
			return false, err
		}
		if plan != nil {
			for _, key := range plan.required {
				if _, exists := fields[key]; !exists {
					return false, nil
				}
			}
		}
		for key, child := range fields {
			var childPlan *wireSchemaPlan
			if plan != nil {
				childPlan = plan.properties[key]
				if plan.kind == ObjectType && childPlan == nil && !plan.allowUnknown {
					return false, nil
				}
			}
			if isValid, err := childPlan.accepts(ctx, child, reader, objects); !isValid || err != nil {
				return isValid, err
			}
		}
	case ArrayType:
		var itemPlan *wireSchemaPlan
		if plan != nil {
			itemPlan = plan.items
		}
		for offset := skipJSONSpace(raw, 1); raw[offset] != ']'; {
			end := reader.valueEnd(raw, offset)
			item := Value{raw: raw[offset:end]}
			if isValid, err := itemPlan.accepts(ctx, item, reader, objects); !isValid || err != nil {
				return isValid, err
			}
			offset = skipJSONSpace(raw, end)
			if raw[offset] == ',' {
				offset = skipJSONSpace(raw, offset+1)
			}
		}
	}
	if plan != nil && len(plan.enum) > 0 {
		for _, allowed := range plan.enum {
			if equalNativeValuesWithin(value, allowed, reader, newNativeComparisonReader(allowed)) {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}

// Defer rational-number work until an integer or enum constraint needs it.
func wireSchemaValueType(raw string) JSONType {
	switch raw[0] {
	case '{':
		return ObjectType
	case '[':
		return ArrayType
	case '"':
		return StringType
	case 't', 'f':
		return BooleanType
	case 'n':
		return NullType
	default:
		return NumberType
	}
}
