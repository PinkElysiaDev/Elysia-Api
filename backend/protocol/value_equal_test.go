package protocol

import (
	"strings"
	"testing"
)

func TestValueEqualityPreservesJSONSemantics(t *testing.T) {
	for _, fixture := range []struct {
		left, right string
		isEqual     bool
	}{
		{`"user"`, `"assistant"`, false},
		{`"\u0075ser"`, ` "user" `, true},
		{`"\ud800"`, `"�"`, true},
		{"\"\xff\xfe\"", `"��"`, true},
		{`"false"`, `false`, false},
		{`null`, "\n null ", true},
		{`true`, ` false `, false},
		{`false`, ` false `, true},
		{`9007199254740993`, `9007199254740993.0`, true},
		{`9007199254740993`, `9007199254740992`, false},
		{`1e9999`, `10e9998`, false},
		{`{"k":0,"\u006b":2,"a":1}`, `{"a":1.0,"k":2}`, true},
		{`[1,"x",null]`, `[1.0,"\u0078",null]`, true},
		{`[1,2]`, `[2,1]`, false},
	} {
		left, err := ParseValue([]byte(fixture.left))
		if err != nil {
			t.Fatal(err)
		}
		right, err := ParseValue([]byte(fixture.right))
		if err != nil {
			t.Fatal(err)
		}
		if equalValues(left, right) != fixture.isEqual || equalValues(right, left) != fixture.isEqual {
			t.Fatalf("%s and %s: want equal=%t", fixture.left, fixture.right, fixture.isEqual)
		}
		if equalValues(left, Value{}) || equalValues(Value{}, left) {
			t.Fatal("present value equals absence")
		}
	}
	if !equalValues(Value{}, Value{}) {
		t.Fatal("absence is not equal to itself")
	}
}

func FuzzValueEquality(f *testing.F) {
	for _, pair := range [][2]string{{`"x"`, `"\u0078"`}, {`"\ud800"`, `"�"`}, {`9007199254740993`, `9007199254740993.0`}, {`{"x":1,"x":2}`, `{"x":2}`}, {`null`, ` false `}} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, leftRaw, rightRaw string) {
		left, leftErr := ParseValue([]byte(leftRaw))
		right, rightErr := ParseValue([]byte(rightRaw))
		if leftErr != nil || rightErr != nil {
			t.Skip()
		}
		// The retained semantic decoder is an independent oracle for scalar
		// fast paths, including replacement of malformed Unicode sequences.
		var first, second any
		want := left.raw == right.raw || left.Decode(&first) == nil && right.Decode(&second) == nil && equalJSON(first, second)
		if equalValues(left, right) != want || equalValues(right, left) != want {
			t.Fatal("comparison differs from decoded JSON semantics", leftRaw, rightRaw)
		}
	})
}

func BenchmarkValueEquality(b *testing.B) {
	for _, fixture := range []struct {
		name, left, right string
	}{
		{"different-strings", `"user"`, `"assistant"`},
		{"escaped-string", `"\u0075ser"`, `"user"`},
		{"different-types", `"false"`, `false`},
		{"number", `9007199254740993`, `9007199254740993.0`},
		{"object", `{"k":0,"\u006b":2,"a":1}`, `{"a":1.0,"k":2}`},
		{"large-text", `"` + strings.Repeat("x", 64<<10) + `a"`, `"` + strings.Repeat("x", 64<<10) + `b"`},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			left, err := ParseValue([]byte(fixture.left))
			if err != nil {
				b.Fatal(err)
			}
			right, err := ParseValue([]byte(fixture.right))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				equalValues(left, right)
			}
		})
	}
}
