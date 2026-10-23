package protocol

import (
	"strings"
	"testing"
)

const (
	nativeComparisonNodeCount   = 1800
	nativeComparisonPayloadSize = 2 << 20
)

type nativeComparisonImplementation struct {
	name    string
	compare func(Value, Value) bool
}

func TestNativeComparisonBorrowedFragments(t *testing.T) {
	root := parseTestValue(t, `{"items":[{"text":"small","number":9007199254740993}]}`)
	reader := newNativeComparisonReader(root)
	fields, err := root.readObjectWithin(reader)
	if err != nil {
		t.Fatal(err)
	}
	items, err := fields["items"].readArrayWithin(reader)
	if err != nil {
		t.Fatal(err)
	}
	item, err := items[0].readObjectWithin(reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []Value{fields["items"], items[0], item["text"], item["number"]} {
		if !sharesValueStorage(root, value) {
			t.Fatal("comparison copied a temporary fragment")
		}
	}
	if item["number"].raw != "9007199254740993" {
		t.Fatal("comparison changed numeric spelling")
	}
	item["text"] = StringValue("changed")
	items[0] = Value{}
	delete(fields, "items")
	if root.raw != `{"items":[{"text":"small","number":9007199254740993}]}` {
		t.Fatal("comparison containers shared mutable ownership")
	}
}

func TestNativeComparisonReaderIndexesBorrowedPayload(t *testing.T) {
	root := parseTestValue(t, `{"nested":{"payload":"`+strings.Repeat("x", 2*requestViewMinBytes)+`","small":"value"}}`)
	reader := newNativeComparisonReader(root)
	fields, err := root.readObjectWithin(reader)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := fields["nested"].readObjectWithin(reader)
	if err != nil {
		t.Fatal(err)
	}
	if reader.index == nil || len(reader.index.ends) == 0 {
		t.Fatal("large comparison payload was not indexed")
	}
	for _, value := range []Value{fields["nested"], nested["payload"], nested["small"]} {
		if !sharesValueStorage(root, value) {
			t.Fatal("indexed comparison copied a temporary fragment")
		}
	}
}

func TestNativeComparisonWorkloadParity(t *testing.T) {
	for _, shape := range []string{"many-small", "large-string"} {
		for _, shouldChangeModel := range []bool{false, true} {
			left, right := nativeComparisonInputs(t, shape, shouldChangeModel)
			for _, implementation := range nativeComparisonImplementations() {
				if got := implementation.compare(left, right); got == shouldChangeModel {
					t.Fatalf("%s/%s model change %t: comparison = %t", shape, implementation.name, shouldChangeModel, got)
				}
			}
		}
	}
}

func BenchmarkNativeValueComparison(b *testing.B) {
	for _, shape := range []string{"many-small", "large-string"} {
		for _, branch := range []struct {
			name              string
			shouldChangeModel bool
		}{{"reordered", false}, {"model-change", true}} {
			left, right := nativeComparisonInputs(b, shape, branch.shouldChangeModel)
			for _, implementation := range nativeComparisonImplementations() {
				b.Run(shape+"/"+branch.name+"/"+implementation.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if got := implementation.compare(left, right); got == branch.shouldChangeModel {
							b.Fatal("comparison result changed")
						}
					}
				})
			}
		}
	}
}

func nativeComparisonInputs(t testing.TB, shape string, shouldChangeModel bool) (Value, Value) {
	t.Helper()
	count, width := 1, nativeComparisonPayloadSize
	if shape == "many-small" {
		count, width = nativeComparisonNodeCount, nativeComparisonPayloadSize/nativeComparisonNodeCount
	}
	block := `{"type":"text","text":"` + strings.Repeat("x", width) + `"}`
	content := "[" + strings.Repeat(block+",", count-1) + block + "]"
	history := `[{"role":"user","content":` + content + `}]`
	model := "original"
	if shouldChangeModel {
		model = "updated"
	}
	left, err := ParseValue([]byte(`{"model":"original","messages":` + history + `,"max_tokens":2048}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := ParseValue([]byte(`{"max_tokens":2048,"messages":` + history + `,"model":"` + model + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	return left, right
}

func nativeComparisonImplementations() []nativeComparisonImplementation {
	return []nativeComparisonImplementation{
		{"decoded-baseline", equalNativeValuesDecodedBaseline},
		{"request-reader", equalNativeValuesRequestReader},
		{"borrow-all-index", equalNativeValues},
		{"borrow-all-no-index", equalNativeValuesWithoutIndex},
	}
}

// Keep the pre-optimization comparator from 251bc5d for allocation attribution.
func equalNativeValuesDecodedBaseline(left, right Value) bool {
	if left.raw == right.raw {
		return true
	}
	if left.IsObject() && right.IsObject() {
		before, _ := left.readObjectWithin(requestValueReader(left))
		after, _ := right.readObjectWithin(requestValueReader(right))
		if len(before) != len(after) {
			return false
		}
		for key, value := range before {
			other, exists := after[key]
			if !exists {
				return false
			}
			if len(value.raw) <= requestViewMinBytes && len(other.raw) <= requestViewMinBytes && !equalValues(value, other) {
				return false
			}
		}
	}
	return equalValues(left, right)
}

// This is the local comparator before its request-reader ownership correction.
func equalNativeValuesRequestReader(left, right Value) bool {
	if left.raw == right.raw {
		return true
	}
	if left.IsZero() || right.IsZero() {
		return false
	}
	return equalNativeValuesWithin(left, right, requestValueReader(left), requestValueReader(right))
}

func equalNativeValuesWithoutIndex(left, right Value) bool {
	if left.raw == right.raw {
		return true
	}
	if left.IsZero() || right.IsZero() {
		return false
	}
	reader := &valueReader{}
	return equalNativeRoots(left, right, reader, reader)
}
