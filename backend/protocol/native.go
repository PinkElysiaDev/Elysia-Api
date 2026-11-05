package protocol

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Limits bounds declarative traversal and buffering. Callers configure limits
// at the engine boundary; defaults live here, never in individual adapters.
type Limits struct {
	Depth       int `json:"depth"`
	Nodes       int `json:"nodes"`
	Mutations   int `json:"mutations"`
	StateItems  int `json:"stateItems"`
	BufferBytes int `json:"bufferBytes"`
}

// DefaultLimits returns an independent configuration value.
func DefaultLimits() Limits {
	return Limits{Depth: 64, Nodes: 100000, Mutations: 1024, StateItems: 4096, BufferBytes: 8 << 20}
}

// Validate rejects incomplete or nonpositive resource limits.
func (limits Limits) Validate() error {
	if limits.Depth <= 0 || limits.Nodes <= 0 || limits.Mutations <= 0 || limits.StateItems <= 0 || limits.BufferBytes <= 0 {
		return fmt.Errorf("all protocol resource limits must be positive")
	}
	maxInt := int(^uint(0) >> 1)
	if limits.BufferBytes > maxInt/semanticByteMultiplier || limits.Nodes > maxInt/semanticNodeMultiplier || limits.Depth > maxInt/semanticDepthMultiplier {
		return fmt.Errorf("protocol limits exceed safe budget arithmetic")
	}
	return nil
}

// MutationOp specifies an explicit update to a native snapshot.
type MutationOp string

const (
	SetValue     MutationOp = "set"
	DeleteValue  MutationOp = "delete"
	ReplaceValue MutationOp = "replace"
)

// Mutation uses an RFC 6901 pointer. Set creates a final object key; replace
// and delete require an existing target. Parents are never implicitly created.
type Mutation struct {
	Op    MutationOp `json:"op"`
	Path  string     `json:"path"`
	Value Value      `json:"value,omitzero"`
}

// Target contains the pinned wire and resource identity used for conversion.
type Target struct {
	Codec            string // installed direction module; never inferred from a family label
	Protocol         Identity
	Direction        Direction
	Scope            Scope
	Capabilities     CapabilitySet
	coreVersion      int
	eventEncoderHash string
	eventDecoderHash string
}

// CanPreserveNative requires an explicit family and wire-version match. A
// user-selected protocol ID or a vendor's type string is never sufficient.
func CanPreserveNative(source Provenance, target Target) bool {
	if source.Protocol.Family == "" || source.Protocol.WireVersion == "" || source.Protocol.Family != target.Protocol.Family || source.Protocol.WireVersion != target.Protocol.WireVersion {
		return false
	}
	return (source.Direction == DecodeRequest && target.Direction == EncodeRequest) ||
		(source.Direction == DecodeResponse && target.Direction == EncodeResponse) ||
		(source.Direction == DecodeEvent && target.Direction == EncodeEvent) ||
		(source.Direction == DecodeClientEvent && target.Direction == EncodeUpstreamEvent)
}

// CheckScope requires every restriction present in the source to match.
func CheckScope(source, target Scope) bool {
	return (source.Provider == "" || source.Provider == target.Provider) &&
		(source.Account == "" || source.Account == target.Account) &&
		(source.Model == "" || source.Model == target.Model) &&
		(source.Session == "" || source.Session == target.Session)
}

// PreserveNative replays compatible native JSON with explicit semantic edits.
// Cross-wire encoders must construct mapped output instead of calling this API.
func PreserveNative(native Native, target Target, mutations []Mutation, limits Limits) (Value, []ConversionIssue) {
	value, err := ReplayNative(native, target, mutations, limits)
	if err == nil {
		return value, nil
	}
	return Value{}, err.(*ConversionError).Issues
}

// ReplayNative preserves resource error causes for runtime callers. Authoring
// callers can retain the diagnostic-list API through PreserveNative.
func ReplayNative(native Native, target Target, mutations []Mutation, limits Limits) (Value, error) {
	issue := ConversionIssue{Severity: SeverityError, Protocol: target.Protocol, Direction: target.Direction,
		Stage: "encode", Path: native.Source.Path, Suggestion: "Declare an equivalent semantic mapping or use a compatible target."}
	if !CanPreserveNative(native.Source, target) {
		issue.Code, issue.Reason = UnsupportedNative, "native source family, wire version or direction is incompatible"
		return Value{}, &ConversionError{Issues: []ConversionIssue{issue}}
	}
	if !CheckScope(native.Source.Scope, target.Scope) {
		issue.Code, issue.Reason = ResourceScopeMismatch, "native resource scope differs from the selected target"
		return Value{}, &ConversionError{Issues: []ConversionIssue{issue}}
	}
	value, err := ApplyMutations(native.Value, mutations, limits)
	if err != nil {
		annotated := resourceIssue(err, target.Protocol, target.Direction, "preserve")
		if annotated != err {
			return Value{}, annotated
		}
		issue.Code, issue.Reason = InvalidMutation, err.Error()
		return Value{}, &ConversionError{cause: err, Issues: []ConversionIssue{issue}}
	}
	return value, nil
}

// ApplyMutations applies an ordered patch transaction without changing the
// source. Failure returns no partially modified value.
func ApplyMutations(original Value, mutations []Mutation, limits Limits) (Value, error) {
	if err := limits.Validate(); err != nil {
		return Value{}, err
	}
	if original.IsZero() {
		return Value{}, fmt.Errorf("cannot edit an absent native node")
	}
	if len(mutations) > limits.Mutations {
		return Value{}, resourceFailure("mutation", "operations", limits.Mutations, len(mutations))
	}
	if err := checkValueLimits(original, limits); err != nil {
		return Value{}, err
	}
	result := original
	for index, mutation := range mutations {
		path, err := parsePointer(mutation.Path)
		if err != nil {
			return Value{}, fmt.Errorf("mutation %d: %w", index, err)
		}
		if len(path) > limits.Depth {
			return Value{}, resourceFailure("mutation", "depth", limits.Depth, len(path))
		}
		if mutation.Op != SetValue && mutation.Op != DeleteValue && mutation.Op != ReplaceValue {
			return Value{}, fmt.Errorf("mutation %d has unknown operation %q", index, mutation.Op)
		}
		if (mutation.Op == DeleteValue) != mutation.Value.IsZero() {
			return Value{}, fmt.Errorf("mutation %d requires a value only for set/replace", index)
		}
		if !mutation.Value.IsZero() {
			if err := checkValueLimits(mutation.Value, limits); err != nil {
				return Value{}, fmt.Errorf("mutation %d: %w", index, err)
			}
		}
		result, err = mutateValue(result, path, mutation)
		if err != nil {
			return Value{}, fmt.Errorf("mutation %d %s: %w", index, mutation.Path, err)
		}
		if err := checkValueLimits(result, limits); err != nil {
			return Value{}, fmt.Errorf("mutation %d: %w", index, err)
		}
	}
	return result, nil
}

func checkValueLimits(value Value, limits Limits) error {
	if len(value.raw) > limits.BufferBytes {
		return resourceFailure("wire", "bytes", limits.BufferBytes, len(value.raw))
	}
	meter := valueTokenMeter{limits: limits, domain: "wire"}
	return meter.scan(value.raw)
}

type valueTokenMeter struct {
	limits Limits
	domain string
	depth  int
	nodes  int
}

func (meter *valueTokenMeter) scan(raw string) error {
	// Value is already valid JSON. Count the same tokens as Decoder.Token
	// without allocating decoded keys/scalars or reparsing nested snapshots.
	for offset := 0; offset < len(raw); offset++ {
		token := raw[offset]
		depthChange := 0
		switch token {
		case ' ', '\t', '\r', '\n', ',', ':':
			continue
		case '{', '[':
			depthChange = 1
		case '}', ']':
			depthChange = -1
		case '"':
			offset = jsonStringEnd(raw, offset) - 1
		default:
			offset = jsonValueEnd(raw, offset) - 1
		}
		if err := meter.consume(depthChange); err != nil {
			return err
		}
	}
	return nil
}

func (meter *valueTokenMeter) consume(depthChange int) error {
	meter.depth += depthChange
	if depthChange > 0 && meter.depth > meter.limits.Depth {
		return resourceFailure(meter.domain, "depth", meter.limits.Depth, meter.depth)
	}
	meter.nodes++
	if meter.nodes > meter.limits.Nodes {
		return resourceFailure(meter.domain, "nodes", meter.limits.Nodes, meter.nodes)
	}
	return nil
}

func parsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("expected JSON pointer")
	}
	parts := strings.Split(pointer[1:], "/")
	for index, part := range parts {
		var token strings.Builder
		for offset := 0; offset < len(part); offset++ {
			if part[offset] != '~' {
				token.WriteByte(part[offset])
				continue
			}
			offset++
			if offset >= len(part) || (part[offset] != '0' && part[offset] != '1') {
				return nil, fmt.Errorf("invalid JSON pointer escape")
			}
			if part[offset] == '0' {
				token.WriteByte('~')
			} else {
				token.WriteByte('/')
			}
		}
		parts[index] = token.String()
	}
	return parts, nil
}

func mutateValue(current Value, path []string, mutation Mutation) (Value, error) {
	// Temporary fragments remain borrowed until the root owns its encoded bytes.
	// Each mutation still materializes before its original output-budget check.
	output, err := planValueMutation(current, path, mutation, &valueReader{})
	if err != nil {
		return Value{}, err
	}
	return output.Encode(context.Background())
}

func planValueMutation(current Value, path []string, mutation Mutation, reader *valueReader) (JSONOutput, error) {
	if len(path) == 0 {
		if mutation.Op == DeleteValue {
			return JSONOutput{}, fmt.Errorf("cannot delete the document root")
		}
		return JSONLeaf(mutation.Value), nil
	}
	if current.IsObject() {
		return planObjectMutation(current, path, mutation, reader)
	}
	if current.IsArray() {
		return planArrayMutation(current, path, mutation, reader)
	}
	return JSONOutput{}, fmt.Errorf("path traverses a scalar")
}

func planObjectMutation(current Value, path []string, mutation Mutation, reader *valueReader) (JSONOutput, error) {
	object, err := current.readObjectWithin(reader)
	if err != nil {
		return JSONOutput{}, err
	}
	key := path[0]
	child, hasChild := object[key]
	if !hasChild && (len(path) > 1 || mutation.Op != SetValue) {
		return JSONOutput{}, fmt.Errorf("target does not exist")
	}
	isDelete := len(path) == 1 && mutation.Op == DeleteValue
	var replacement JSONOutput
	if isDelete {
		delete(object, key)
	} else {
		replacement, err = planValueMutation(child, path[1:], mutation, reader)
		if err != nil {
			return JSONOutput{}, err
		}
	}
	fields := make(map[string]JSONOutput, len(object))
	for key, value := range object {
		fields[key] = JSONLeaf(value)
	}
	if !isDelete {
		fields[key] = replacement
	}
	return JSONObject(fields), nil
}

func planArrayMutation(current Value, path []string, mutation Mutation, reader *valueReader) (JSONOutput, error) {
	array, err := current.readArrayWithin(reader)
	if err != nil {
		return JSONOutput{}, err
	}
	index, err := strconv.Atoi(path[0])
	if err != nil || index < 0 || index >= len(array) || strconv.Itoa(index) != path[0] {
		return JSONOutput{}, fmt.Errorf("array index is invalid or outside the array")
	}
	isDelete := len(path) == 1 && mutation.Op == DeleteValue
	var replacement JSONOutput
	if isDelete {
		array = append(array[:index], array[index+1:]...)
	} else {
		replacement, err = planValueMutation(array[index], path[1:], mutation, reader)
		if err != nil {
			return JSONOutput{}, err
		}
	}
	items := make([]JSONOutput, len(array))
	for index, value := range array {
		items[index] = JSONLeaf(value)
	}
	if !isDelete {
		items[index] = replacement
	}
	return JSONArray(items), nil
}
