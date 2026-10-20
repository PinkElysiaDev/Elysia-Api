package protocol

import (
	"context"
	"fmt"
	"strings"
)

// JSONOutput is an immutable, closed JSON construction plan. Containers snapshot
// their input maps/slices; leaves are already validated immutable Values. Nested
// containers do not encode intermediate documents or expose placeholder Values.
// The zero value means absent, not null. Runtime callers retain their normal
// semantic, mapping and final-wire budgets around Encode.
type JSONOutput struct {
	value Value
	node  *jsonOutputNode
}

type jsonOutputNode struct {
	object bool
	fields []jsonOutputField
	items  []JSONOutput
}

type jsonOutputField struct {
	key   string
	value JSONOutput
}

// JSONLeaf shares immutable storage. A direct root leaf retains its exact wire
// spelling; enclosing containers apply the same compaction/escaping as EncodeValue.
func JSONLeaf(value Value) JSONOutput { return JSONOutput{value: value} }

// JSONObject snapshots fields in the same sorted-key order as EncodeValue.
// Absent entries are invalid; adapters that omit optional fields remove them first.
func JSONObject(fields map[string]JSONOutput) JSONOutput {
	if fields == nil {
		return JSONLeaf(Value{raw: "null"})
	}
	node := &jsonOutputNode{object: true, fields: make([]jsonOutputField, 0, len(fields))}
	for _, key := range sortedKeys(fields) {
		node.fields = append(node.fields, jsonOutputField{key, fields[key]})
	}
	return JSONOutput{node: node}
}

// JSONArray snapshots items. A nil slice is null; a non-nil empty slice is [].
func JSONArray(items []JSONOutput) JSONOutput {
	if items == nil {
		return JSONLeaf(Value{raw: "null"})
	}
	return JSONOutput{node: &jsonOutputNode{items: append([]JSONOutput{}, items...)}}
}

func (output JSONOutput) IsZero() bool { return output.node == nil && output.value.IsZero() }

// Encode resolves raw leaf normalization once, computes the exact output size,
// then writes one owned result. No payload-sized intermediate container strings
// are built. Plans contain no public mutable nodes and cannot form cycles.
func (output JSONOutput) Encode(ctx context.Context) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if output.node == nil {
		if output.value.IsZero() {
			return Value{}, fmt.Errorf("absent JSON value requires omission")
		}
		return output.value, nil
	}
	var storage [64]string
	parts := jsonOutputParts{parts: storage[:0]}
	if err := parts.collect(ctx, output); err != nil {
		return Value{}, err
	}
	var builder strings.Builder
	builder.Grow(parts.bytes)
	for _, part := range parts.parts {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		builder.WriteString(part)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{raw: builder.String()}, nil
}

type jsonOutputParts struct {
	parts []string
	bytes int
}

func (parts *jsonOutputParts) add(raw string) error {
	if len(raw) > int(^uint(0)>>1)-parts.bytes {
		return fmt.Errorf("JSON output exceeds integer capacity")
	}
	parts.bytes += len(raw)
	parts.parts = append(parts.parts, raw)
	return nil
}

func (parts *jsonOutputParts) collect(ctx context.Context, output JSONOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if output.node == nil {
		if output.value.IsZero() {
			return fmt.Errorf("absent JSON value requires omission")
		}
		return parts.add(compactValueString(output.value.raw))
	}
	node := output.node
	open, close := "[", "]"
	if node.object {
		open, close = "{", "}"
	}
	if err := parts.add(open); err != nil {
		return err
	}
	count := len(node.items)
	if node.object {
		count = len(node.fields)
	}
	for index := range count {
		if index > 0 {
			if err := parts.add(","); err != nil {
				return err
			}
		}
		var child JSONOutput
		if node.object {
			field := node.fields[index]
			if jsonStringBytes(field.key) == len(field.key)+2 {
				if err := parts.add(`"`); err != nil {
					return err
				}
				if err := parts.add(field.key); err != nil {
					return err
				}
				if err := parts.add(`":`); err != nil {
					return err
				}
			} else {
				if err := parts.add(StringValue(field.key).raw + ":"); err != nil {
					return err
				}
			}
			child = field.value
		} else {
			child = node.items[index]
		}
		if err := parts.collect(ctx, child); err != nil {
			return err
		}
	}
	return parts.add(close)
}
