package protocol

import (
	"reflect"
	"strings"
	"testing"
)

// Pointer comparisons inspect ownership only; no unsafe conversion or mutable
// access to Value storage is used by the implementation or these assertions.
func sharesValueStorage(root, child Value) bool {
	start, fragment := reflect.ValueOf(root.raw).Pointer(), reflect.ValueOf(child.raw).Pointer()
	return fragment >= start && fragment < start+uintptr(len(root.raw))
}

func TestRequestViewsSharePayloadAndDetachSmallFields(t *testing.T) {
	root, _ := ParseValue([]byte(`{"small":"retained","payload":{"data":"` + strings.Repeat("A", 2<<20) + `"},"items":[{"data":"` + strings.Repeat("B", 2<<20) + `"}]}`))
	options := (EvaluationContext{}).WithValueReader(root)
	fields, err := options.ReadObject(root)
	if err != nil {
		t.Fatal(err)
	}
	if !sharesValueStorage(root, fields["payload"]) || sharesValueStorage(root, fields["small"]) {
		t.Fatal("request view ownership is incorrect")
	}
	items, _ := options.ReadArray(fields["items"])
	if !sharesValueStorage(root, items[0]) {
		t.Fatal("array snapshots copied the large payload")
	}
	independent, _ := root.ReadObject()
	if sharesValueStorage(root, independent["payload"]) {
		t.Fatal("public reader borrowed storage")
	}
	fields["small"] = StringValue("changed")
	if independent["small"].IsString("changed") {
		t.Fatal("mutable containers are shared")
	}
	copy := root.Bytes()
	copy[0] = '['
	if !root.IsObject() {
		t.Fatal("Bytes exposed mutable storage")
	}
}

func TestValueImmutableStringAndLength(t *testing.T) {
	raw := []byte(`{"n":9007199254740993,"s":"escaped\\text"}`)
	value, err := ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	wire := value.JSONString()
	raw[0] = '['
	copy := value.Bytes()
	copy[0] = '['
	if wire != `{"n":9007199254740993,"s":"escaped\\text"}` || value.Len() != len(wire) || !value.IsObject() {
		t.Fatal("immutable wire exposed caller-owned bytes or changed number spelling")
	}
	if got := testing.AllocsPerRun(100, func() {
		if value.Len() != len(value.JSONString()) {
			t.Fatal("inconsistent wire length")
		}
	}); got != 0 {
		t.Fatalf("read-only wire access allocated: %g", got)
	}
}

func TestRequestResourcesDoNotRetainBody(t *testing.T) {
	root, _ := ParseValue([]byte(`{"signature":"` + strings.Repeat("A", 1<<20) + `","body":"` + strings.Repeat("B", 1<<20) + `"}`))
	fields, _ := root.readObjectWithin(requestValueReader(root))
	request := &Request{Content: []Node{{Resources: []Resource{{ID: fields["signature"]}}}}}
	if !sharesValueStorage(root, request.Content[0].Resources[0].ID) {
		t.Fatal("fixture did not borrow signature")
	}
	detachRequestResources(request)
	if sharesValueStorage(root, request.Content[0].Resources[0].ID) || request.Content[0].Resources[0].ID != fields["signature"] {
		t.Fatal("retained signature must have independent, identical bytes")
	}
}
