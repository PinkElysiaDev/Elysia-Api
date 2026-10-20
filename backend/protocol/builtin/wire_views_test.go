package builtin

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestWireViewsSharePayloadAndOwnContainers(t *testing.T) {
	root := testValue(t, `{"small":"id","large":[{"data":"`+strings.Repeat("A", 1<<20)+`"}]}`)
	fields, err := readWireObject(root, "")
	if err != nil {
		t.Fatal(err)
	}
	items, err := readWireArray(fields["large"], "/large")
	if err != nil || len(items) != 1 {
		t.Fatalf("wire array: %v", err)
	}
	start := reflect.ValueOf(root.JSONString()).Pointer()
	for _, value := range []p.Value{fields["large"], items[0]} {
		pointer := reflect.ValueOf(value.JSONString()).Pointer()
		if pointer < start || pointer >= start+uintptr(root.Len()) {
			t.Fatal("wire validation copied a large immutable fragment")
		}
	}
	other, _ := readWireObject(root, "")
	fields["small"] = p.StringValue("changed")
	if !other["small"].IsString("id") {
		t.Fatal("wire validators shared mutable map storage")
	}
	for _, value := range []p.Value{{}, testValue(t, `null`), testValue(t, `{}`)} {
		if _, err := readWireArray(value, "/array"); err == nil {
			t.Fatal("invalid required array accepted")
		}
	}
}

func TestWireObjectValidationParityAndIsolation(t *testing.T) {
	adapter := module{name: Chat}
	for _, raw := range []string{
		chatContractFrame("r", "m", 1, `{"role":"assistant","content":"text"}`, `null`),
		chatContractFrame("r", "m", 1, `{"role":"user"}`, `null`),
		`{"object":"wrong","choices":[]}`,
		`{"error":{"message":"failure"}}`,
	} {
		frame := testValue(t, raw)
		fields, _ := frame.ReadObject()
		before := copyFields(fields)
		want := adapter.ValidateWireOutput(p.EncodeEvent, frame)
		got := adapter.ValidateWireObjectOutput(p.EncodeEvent, frame, fields)
		if fmt.Sprint(got) != fmt.Sprint(want) || !reflect.DeepEqual(before, fields) {
			t.Fatalf("parsed wire validation differs or mutates fields: %v / %v", got, want)
		}
	}
}
