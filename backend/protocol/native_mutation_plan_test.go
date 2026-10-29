package protocol

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestMutationPlanPreservesBytesAndFailures(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		mutations []Mutation
	}{
		{"identity", ` { "z": 1, "a": 2 } `, nil},
		{"same-value-sort", `{"z":1,"a":2}`, []Mutation{{Op: SetValue, Path: "/z", Value: Value{raw: "1"}}}},
		{"nested-sort", `{"z":[{"b":2,"a":1}],"a":0}`, []Mutation{{Op: SetValue, Path: "/z/0/a", Value: Value{raw: "1"}}}},
		{"duplicate-keys", `{"a":1,"\u0061":2,"b":null}`, []Mutation{{Op: SetValue, Path: "/b", Value: Value{raw: "false"}}}},
		{"escaped-pointer", `{"a/b":{"~key":null}}`, []Mutation{{Op: SetValue, Path: "/a~1b/~0key", Value: Value{raw: "0"}}}},
		{"new-null", `{"a":1}`, []Mutation{{Op: SetValue, Path: "/b", Value: Value{raw: "null"}}}},
		{"delete-null", `{"a":null}`, []Mutation{{Op: DeleteValue, Path: "/a"}}},
		{"delete-array", `[{"z":1,"a":2},null,1e+03]`, []Mutation{{Op: DeleteValue, Path: "/1"}}},
		{"nested-array", `{"items":[[0,1],[2,3]]}`, []Mutation{{Op: ReplaceValue, Path: "/items/1/0", Value: Value{raw: ` { "a" : "<>&\u2028" } `}}}},
		{"root-spelling", `null`, []Mutation{{Op: SetValue, Value: Value{raw: ` [ 1e+03, "\u0061" ] `}}}},
		{"nested-spelling", `{"number":900719925474099312345,"html":"<>&\u2028"}`, []Mutation{{Op: SetValue, Path: "/new", Value: Value{raw: ` { "z" : 1e+03, "a" : "\u0061" } `}}}},
		{"missing-parent", `{}`, []Mutation{{Op: SetValue, Path: "/parent/child", Value: Value{raw: "null"}}}},
		{"missing-delete", `{}`, []Mutation{{Op: DeleteValue, Path: "/missing"}}},
		{"missing-replace", `{}`, []Mutation{{Op: ReplaceValue, Path: "/missing", Value: Value{raw: "null"}}}},
		{"null-parent", `{"parent":null}`, []Mutation{{Op: SetValue, Path: "/parent/child", Value: Value{raw: "1"}}}},
		{"array-zero-prefix", `[0]`, []Mutation{{Op: SetValue, Path: "/00", Value: Value{raw: "1"}}}},
		{"array-append", `[0]`, []Mutation{{Op: SetValue, Path: "/1", Value: Value{raw: "1"}}}},
		{"root-delete", `{}`, []Mutation{{Op: DeleteValue}}},
		{"transaction-failure", `{"name":"old"}`, []Mutation{{Op: SetValue, Path: "/name", Value: StringValue("new")}, {Op: DeleteValue, Path: "/missing"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compareMutationPlan(t, parseTestValue(t, test.raw), test.mutations, DefaultLimits())
		})
	}
}

func TestMutationPlanPreservesBudgetOrder(t *testing.T) {
	input := parseTestValue(t, `{"z":{"a":1},"items":[0,{"raw":"<"}]}`)
	mutations := []Mutation{{Op: SetValue, Path: "/z/a", Value: Value{raw: ` {"html":"<<<<<<<<","values":[1,2,3]} `}}, {Op: SetValue, Path: "/new", Value: Value{raw: "true"}}}
	for _, metric := range []string{"bytes", "depth", "nodes", "mutations"} {
		for _, limit := range []int{1, 2, 8, 32, 64, 128} {
			limits := DefaultLimits()
			switch metric {
			case "bytes":
				limits.BufferBytes = limit
			case "depth":
				limits.Depth = limit
			case "nodes":
				limits.Nodes = limit
			case "mutations":
				limits.Mutations = limit
			}
			compareMutationPlan(t, input, mutations, limits)
		}
	}
	limits := DefaultLimits()
	limits.Depth = 2
	compareMutationPlan(t, Value{raw: `{}`}, []Mutation{{Op: SetValue, Path: "/missing/deep/field", Value: Value{raw: "null"}}}, limits)
	limits = DefaultLimits()
	limits.BufferBytes = 16
	compareMutationPlan(t, Value{raw: `{}`}, []Mutation{{Op: SetValue, Path: "/missing/field", Value: StringValue(strings.Repeat("x", 32))}}, limits)
}

func TestMutationPlanBorrowsOnlyUntilRootEncoding(t *testing.T) {
	input := parseTestValue(t, `{"body":"`+strings.Repeat("p", 2<<20)+`","settings":{"temperature":1}}`)
	mutation := Mutation{Op: SetValue, Path: "/settings/temperature", Value: Value{raw: "2"}}
	plan, err := planValueMutation(input, []string{"settings", "temperature"}, mutation, &valueReader{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := readOutputPointer(plan, []string{"body"}, &valueReader{})
	if err != nil || !sharesValueStorage(input, body.value) {
		t.Fatal("mutation plan copied an untouched payload", err)
	}
	output, err := ApplyMutations(input, []Mutation{mutation}, DefaultLimits())
	if err != nil || sharesValueStorage(input, output) {
		t.Fatal("final mutation output did not own its storage", err)
	}
	compareMutationPlan(t, input, []Mutation{mutation}, DefaultLimits())
}

func compareMutationPlan(t *testing.T, original Value, mutations []Mutation, limits Limits) {
	t.Helper()
	expected, expectedErr := legacyApplyMutations(original, mutations, limits)
	actual, actualErr := ApplyMutations(original, mutations, limits)
	if actual != expected {
		t.Fatalf("mutation bytes changed: got %.200q, want %.200q", actual.raw, expected.raw)
	}
	for before, after := expectedErr, actualErr; before != nil || after != nil; before, after = errors.Unwrap(before), errors.Unwrap(after) {
		if reflect.TypeOf(before) != reflect.TypeOf(after) || fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatalf("mutation error chain changed: %T %v / %T %v", after, after, before, before)
		}
	}
}

// Frozen pre-plan implementation: only tests retain the recursive JSON boundary.
func legacyApplyMutations(original Value, mutations []Mutation, limits Limits) (Value, error) {
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
		result, err = legacyMutateValue(result, path, mutation)
		if err != nil {
			return Value{}, fmt.Errorf("mutation %d %s: %w", index, mutation.Path, err)
		}
		if err := checkValueLimits(result, limits); err != nil {
			return Value{}, fmt.Errorf("mutation %d: %w", index, err)
		}
	}
	return result, nil
}

func legacyMutateValue(current Value, path []string, mutation Mutation) (Value, error) {
	if len(path) == 0 {
		if mutation.Op == DeleteValue {
			return Value{}, fmt.Errorf("cannot delete the document root")
		}
		return mutation.Value, nil
	}
	trimmed := strings.TrimSpace(current.raw)
	if strings.HasPrefix(trimmed, "{") {
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
			object[key], err = legacyMutateValue(child, path[1:], mutation)
			if err != nil {
				return Value{}, err
			}
		}
		return EncodeValue(object)
	}
	if strings.HasPrefix(trimmed, "[") {
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
			array[index], err = legacyMutateValue(array[index], path[1:], mutation)
			if err != nil {
				return Value{}, err
			}
		}
		return EncodeValue(array)
	}
	return Value{}, fmt.Errorf("path traverses a scalar")
}
