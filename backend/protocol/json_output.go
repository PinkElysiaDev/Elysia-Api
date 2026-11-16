package protocol

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const jsonOutputPartCapacity = 64

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
	object           bool
	origin           coreOriginTag
	hasCanonicalKeys bool
	hasSortedFields  bool
	encodedBytes     int
	fields           []jsonOutputField
	items            []JSONOutput
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
	node := &jsonOutputNode{object: true, hasCanonicalKeys: true, hasSortedFields: true, fields: make([]jsonOutputField, 0, len(fields))}
	for _, key := range sortedKeys(fields) {
		node.hasCanonicalKeys = node.hasCanonicalKeys && utf8.ValidString(key)
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
	return output.encodeWithin(ctx, nil)
}

func (output JSONOutput) encodeWithin(ctx context.Context, cache *semanticCache) (Value, error) {
	if output.node == nil {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if output.value.IsZero() {
			return Value{}, fmt.Errorf("absent JSON value requires omission")
		}
		return output.value, nil
	}
	parts := jsonOutputParts{cache: cache}
	if err := parts.prepare(ctx, output); err != nil {
		return Value{}, err
	}
	return parts.encode(ctx)
}

// equalJSONOutputBytes compares complete encoded roots. Byte equality does not
// establish provenance or authorize native replay.
func equalJSONOutputBytes(ctx context.Context, left, right JSONOutput) (bool, error) {
	var leftParts, rightParts jsonOutputParts
	if err := leftParts.prepare(ctx, left); err != nil {
		return false, err
	}
	if err := rightParts.prepare(ctx, right); err != nil {
		return false, err
	}
	return leftParts.equalBytes(ctx, &rightParts)
}

type jsonOutputParts struct {
	parts    [jsonOutputPartCapacity]string
	overflow []string
	count    int
	bytes    int
	cache    *semanticCache
}

// prepare borrows exact root spelling; only leaves embedded in containers are
// compacted. Parts are local to the operation that encodes or compares them.
func (parts *jsonOutputParts) prepare(ctx context.Context, output JSONOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if output.node == nil {
		if output.value.IsZero() {
			return fmt.Errorf("absent JSON value requires omission")
		}
		return parts.add(output.value.raw)
	}
	return parts.collect(ctx, output)
}

func (parts *jsonOutputParts) encode(ctx context.Context) (Value, error) {
	var builder strings.Builder
	builder.Grow(parts.bytes)
	for index := range parts.count {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		builder.WriteString(parts.readPart(index))
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{raw: builder.String()}, nil
}

func (parts *jsonOutputParts) equalBytes(ctx context.Context, other *jsonOutputParts) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if parts.bytes != other.bytes {
		return false, nil
	}
	leftIndex, rightIndex := 0, 0
	leftOffset, rightOffset := 0, 0
	for leftIndex < parts.count && rightIndex < other.count {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		leftPart, rightPart := parts.readPart(leftIndex), other.readPart(rightIndex)
		left, right := leftPart[leftOffset:], rightPart[rightOffset:]
		length := min(len(left), len(right))
		if left[:length] != right[:length] {
			return false, ctx.Err()
		}
		leftOffset += length
		rightOffset += length
		if leftOffset == len(leftPart) {
			leftIndex++
			leftOffset = 0
		}
		if rightOffset == len(rightPart) {
			rightIndex++
			rightOffset = 0
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (parts *jsonOutputParts) add(raw string) error {
	if len(raw) > int(^uint(0)>>1)-parts.bytes {
		return fmt.Errorf("JSON output exceeds integer capacity")
	}
	parts.bytes += len(raw)
	if parts.count < len(parts.parts) {
		parts.parts[parts.count] = raw
	} else {
		if parts.count == len(parts.parts) {
			parts.overflow = make([]string, 0, jsonOutputPartCapacity)
		}
		parts.overflow = append(parts.overflow, raw)
	}
	parts.count++
	return nil
}

func (parts *jsonOutputParts) readPart(index int) string {
	if index < len(parts.parts) {
		return parts.parts[index]
	}
	return parts.overflow[index-len(parts.parts)]
}

func (parts *jsonOutputParts) collect(ctx context.Context, output JSONOutput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if output.node == nil {
		if output.value.IsZero() {
			return fmt.Errorf("absent JSON value requires omission")
		}
		return parts.add(parts.cache.normalize(output.value).raw)
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
