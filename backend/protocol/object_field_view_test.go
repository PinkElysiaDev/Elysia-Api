package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestScopedFieldTablesPreserveDecodedKeys(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"a":null,"b":false,"c":0,"d":"","\u0061":9007199254740993}`,
		`{"a":0,"b":1,"c":2,"d":3,"e":4,"\u0061":1e+30,"e":null}`,
		"{\"bad\xff\":0,\"bad�\":9007199254740993,\"b\":false,\"c\":[],\"d\":{},\"e\":null}",
		`{"\u0061":1,"a":2,"b":3,"c":4,"d":5,"e":6,"f":7,"g":8,"h":9,"a":10}`,
		`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"\u0061":9007199254740993}`,
	} {
		t.Run(raw, func(t *testing.T) {
			input := parseTestValue(t, raw)
			var expected Object
			if err := input.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			reader := newPlanObjectReader(input, objectReuseReads)
			for range 2 {
				for _, key := range append(sortedKeys(expected), "missing") {
					value, err := reader.readOwnedMember(input, key)
					if err != nil || value != expected[key] {
						t.Fatal("decoded field changed", key, value, expected[key], err)
					}
				}
			}
			public, err := input.readObject()
			if err != nil {
				t.Fatal(err)
			}
			public["a"] = StringValue("changed")
			value, err := reader.readOwnedMember(input, "a")
			if err != nil || value != expected["a"] {
				t.Fatal("public field mutation changed private table", value, err)
			}
		})
	}
}

func TestScopedFieldTablesDetachSmallResults(t *testing.T) {
	for _, count := range []int{2, 4, 5, 8, 9, 16} {
		fields := Object{"id": StringValue("small"), "payload": StringValue(strings.Repeat("x", requestViewMinBytes*2))}
		for index := 2; index < count; index++ {
			fields[fmt.Sprintf("field%d", index)] = StringValue("other")
		}
		input, err := EncodeValue(fields)
		if err != nil {
			t.Fatal(err)
		}
		reader := newPlanObjectReader(input, objectReuseReads)
		selected, err := readValuePointerWithObjectReader(input, []string{"id"}, requestValueReader(input), reader)
		if err != nil || selected != fields["id"] || sharesValueStorage(input, selected) {
			t.Fatal("small selected value retained its input", count, err)
		}
		reader.releaseItem(nil)
		if reader.fields != nil || reader.children != nil || selected != fields["id"] {
			t.Fatal("releasing field owner changed escaped value", count)
		}
	}
}

func TestFieldTablePromotionKeepsSchemaDiagnostics(t *testing.T) {
	plan, err := compileWireSchemaPlan(&ValueSchema{Type: ObjectType, AllowUnknown: true}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"a":1e999999999,"b":0,"c":0,"d":0,"\u0061":0}`,
		`{"a":1e999999999,"b":0,"c":0,"d":0,"e":0,"\u0061":0}`,
		`{"a":0,"b":0,"c":0,"d":0,"\u0061":1e999999999}`,
		`{"a":0,"b":0,"c":0,"d":0,"e":0,"\u0061":1e999999999}`,
		`{"a":1e999999999,"b":0,"c":0,"d":0,"e":0,"f":0,"g":0,"h":0,"i":0,"\u0061":0}`,
		`{"a":0,"b":0,"c":0,"d":0,"e":0,"f":0,"g":0,"h":0,"i":0,"\u0061":1e999999999}`,
	} {
		input := parseTestValue(t, raw)
		want := plan.diagnostic.validate(input, "/wire", DefaultLimits())
		got := plan.validateWithin(t.Context(), input, "/wire", DefaultLimits(), newInputView(input))
		if (got == nil) != (want == nil) || mappingErrorPath("/wire", got) != mappingErrorPath("/wire", want) {
			t.Fatal("duplicate folding changed schema result", raw, got, want)
		}
	}
}

func BenchmarkObjectFieldViews(b *testing.B) {
	for _, count := range []int{0, 1, 2, 4, 5, 8, 9, 16, 64} {
		fields := Object{}
		for index := range count {
			fields[fmt.Sprintf("field%d", index)] = StringValue("small")
		}
		input, err := EncodeValue(fields)
		if err != nil {
			b.Fatal(err)
		}
		plan, err := compileWireSchemaPlan(&ValueSchema{Type: ObjectType, AllowUnknown: true}, DefaultLimits())
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("public/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				selected, err := input.readObject()
				if err != nil || len(selected) != count {
					b.Fatal("public field selection changed", err)
				}
			}
		})
		b.Run(fmt.Sprintf("schema/%d", count), func(b *testing.B) {
			ctx, limits := b.Context(), DefaultLimits()
			b.ReportAllocs()
			for b.Loop() {
				if err := plan.validateWithin(ctx, input, "/wire", limits, newInputView(input)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
