package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestValueContainerEncodingMatchesJSON(t *testing.T) {
	raws := []string{`null`, `0`, `1.00`, `1e+02`, `9007199254740993`, `""`, `"<>&"`, `"\u0061\/"`, "\"\u2028\u2029\"", ` { "a" : [ 1 , 2 ], "a": 3 } `, "\"\xff\""}
	items := []Value{}
	for _, raw := range raws {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, value)
	}
	for _, input := range []any{Object(nil), []Value(nil), Object{}, []Value{}, items, Object{"z": items[4], "a": items[5], "\xff": items[7], "<": items[9]}, Object{"missing": {}}, []Value{{}}} {
		expected, expectedErr := json.Marshal(input)
		actual, actualErr := EncodeValue(input)
		if !reflect.DeepEqual(expectedErr, actualErr) || string(expected) != string(actual.Bytes()) {
			t.Fatalf("%T mismatch: expected=%q error=%v actual=%q error=%v", input, expected, expectedErr, actual.Bytes(), actualErr)
		}
	}
}

func BenchmarkValueImageContainer(b *testing.B) {
	input := Object{"data": StringValue(strings.Repeat("A", 2<<20)), "mime": StringValue("image/png")}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := EncodeValue(input); err != nil {
			b.Fatal(err)
		}
	}
}
