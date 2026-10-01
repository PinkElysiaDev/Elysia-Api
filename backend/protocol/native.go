package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	Protocol     Identity
	Direction    Direction
	Scope        Scope
	Capabilities CapabilitySet
}

// CanPreserveNative requires an explicit family and wire-version match. A
// user-selected protocol ID or a vendor's type string is never sufficient.
func CanPreserveNative(source Provenance, target Target) bool {
	if source.Protocol.Family == "" || source.Protocol.WireVersion == "" || source.Protocol.Family != target.Protocol.Family || source.Protocol.WireVersion != target.Protocol.WireVersion {
		return false
	}
	return (source.Direction == DecodeRequest && target.Direction == EncodeRequest) ||
		(source.Direction == DecodeResponse && target.Direction == EncodeResponse) ||
		(source.Direction == DecodeEvent && target.Direction == EncodeEvent)
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
	issue := ConversionIssue{Severity: SeverityError, Protocol: target.Protocol, Direction: target.Direction,
		Stage: "encode", Path: native.Source.Path, Suggestion: "Declare an equivalent semantic mapping or use a compatible target."}
	if !CanPreserveNative(native.Source, target) {
		issue.Code, issue.Reason = UnsupportedNative, "native source family, wire version or direction is incompatible"
		return Value{}, []ConversionIssue{issue}
	}
	if !CheckScope(native.Source.Scope, target.Scope) {
		issue.Code, issue.Reason = ResourceScopeMismatch, "native resource scope differs from the selected target"
		return Value{}, []ConversionIssue{issue}
	}
	value, err := ApplyMutations(native.Value, mutations, limits)
	if err != nil {
		issue.Code, issue.Reason = InvalidMutation, err.Error()
		return Value{}, []ConversionIssue{issue}
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
		return Value{}, fmt.Errorf("mutation count exceeds limit %d", limits.Mutations)
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
			return Value{}, fmt.Errorf("mutation %d exceeds depth limit", index)
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
		return fmt.Errorf("native node exceeds buffer limit")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(value.raw))
	decoder.UseNumber()
	depth, nodes := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		nodes++
		if nodes > limits.Nodes {
			return fmt.Errorf("native node exceeds node count limit")
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter == '{' || delimiter == '[' {
				depth++
			} else {
				depth--
			}
			if depth > limits.Depth {
				return fmt.Errorf("native node exceeds depth limit")
			}
		}
	}
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
	if len(path) == 0 {
		if mutation.Op == DeleteValue {
			return Value{}, fmt.Errorf("cannot delete the document root")
		}
		return mutation.Value, nil
	}
	trimmed := strings.TrimSpace(current.raw)
	if strings.HasPrefix(trimmed, "{") {
		return mutateObject(current, path, mutation)
	}
	if strings.HasPrefix(trimmed, "[") {
		return mutateArray(current, path, mutation)
	}
	return Value{}, fmt.Errorf("path traverses a scalar")
}

func mutateObject(current Value, path []string, mutation Mutation) (Value, error) {
	object, err := current.ReadObject()
	if err != nil {
		return Value{}, err
	}
	key := path[0]
	child, hasChild := object[key]
	if !hasChild && (len(path) > 1 || mutation.Op != SetValue) {
		return Value{}, fmt.Errorf("target does not exist")
	}
	if len(path) == 1 && mutation.Op == DeleteValue {
		delete(object, key)
	} else {
		object[key], err = mutateValue(child, path[1:], mutation)
		if err != nil {
			return Value{}, err
		}
	}
	return EncodeValue(object)
}

func mutateArray(current Value, path []string, mutation Mutation) (Value, error) {
	var array []Value
	if err := current.Decode(&array); err != nil {
		return Value{}, err
	}
	index, err := strconv.Atoi(path[0])
	if err != nil || index < 0 || index >= len(array) || strconv.Itoa(index) != path[0] {
		return Value{}, fmt.Errorf("array index is invalid or outside the array")
	}
	if len(path) == 1 && mutation.Op == DeleteValue {
		array = append(array[:index], array[index+1:]...)
	} else {
		array[index], err = mutateValue(array[index], path[1:], mutation)
		if err != nil {
			return Value{}, err
		}
	}
	return EncodeValue(array)
}
