package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadObjectMembersMatchesOwnedObject(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"a":null,"b":false,"c":0,"extra":[{}]}`,
		`{"a":1,"\u0061":9007199254740993,"b":{"s":"\""},"c":[1,2]}`,
		` { "a" : -0, "b": 1.20e+40, "c":"\u0061" } `,
		"{\"\xff\":1,\"�\":2}",
		`{"a":"small","extra":"` + strings.Repeat("x", 2<<20) + `"}`,
	} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"a", "b", "c", "missing", "a", "�"}
		slots := make([]Value, len(names))
		for index := range slots {
			slots[index] = StringValue("stale")
		}
		if err := value.ReadObjectMembers(names, slots); err != nil {
			t.Fatal(err)
		}
		for index, name := range names {
			if slots[index] != fields[name] {
				t.Fatalf("%s differs", name)
			}
			if !slots[index].IsZero() && sharesValueStorage(value, slots[index]) {
				t.Fatal("selected field borrows input")
			}
		}
		before := append([]Value(nil), slots...)
		if err := value.ReadObjectMembers(names, slots[:1]); err == nil || !reflect.DeepEqual(before, slots) {
			t.Fatal("slot mismatch must fail before mutation")
		}
	}
	for _, raw := range []string{"", "null", "[]", "0", `"x"`} {
		var value Value
		if raw != "" {
			var err error
			value, err = ParseValue([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := value.ReadObjectMembers(nil, nil); err == nil {
			t.Fatal("accepted nonobject")
		}
	}
}
