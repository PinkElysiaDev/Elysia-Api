package protocol

import (
	"errors"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestWireSchemaPlanMatchesRuntimeSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema ValueSchema
		values []string
	}{
		{"integer", ValueSchema{Type: IntegerType}, []string{`9007199254740993123`, `1.0`, `1e3`, `-0`, `1.5`, `null`, `"1"`}},
		{"number-enum", ValueSchema{Type: NumberType, Enum: []Value{{raw: `9007199254740993123`}, {raw: `9007199254740993123.0`}}}, []string{`9007199254740993123e0`, `9007199254740993122`, `true`}},
		{"nullable-enum", ValueSchema{Type: StringType, Nullable: true, Enum: []Value{StringValue("yes")}}, []string{`null`, `"y\u0065s"`, `"no"`, `false`}},
		{"closed-object", ValueSchema{Type: ObjectType, Required: []string{"id"}, Properties: map[string]ValueSchema{"id": {Type: IntegerType}}}, []string{`{}`, `{"id":1}`, `{"id":1,"extra":true}`, `{"id":"wrong","\u0069d":1}`, `{"id":1,"\u0069d":"wrong"}`}},
		{"array", ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: IntegerType}}, []string{`[]`, `[1,2.0]`, `[1,null]`, `{}`}},
		{"any", ValueSchema{Type: AnyType}, []string{`null`, `true`, `""`, `[]`, `{}`, `1e4096`, `1e-4096`, `{"unknown":[1e4097]}`, `{"x":1e999999999,"\u0078":0}`, `{"x":0,"\u0078":1e999999999}`}},
		{"open-object", ValueSchema{Type: ObjectType, AllowUnknown: true}, []string{`{"nested":[1,false,null]}`, `{"a/b~":[1e999999999]}`, `[]`}},
		{"container-enum", ValueSchema{Type: ObjectType, AllowUnknown: true, Enum: []Value{{raw: `{"a":1,"b":[2]}`}}}, []string{`{"b":[2.0],"a":1e0}`, `{"a":1,"b":[3]}`}},
		{"unicode", ValueSchema{Type: ObjectType, Required: []string{"��"}, Properties: map[string]ValueSchema{"��": {Type: StringType, Enum: []Value{StringValue("�")}}}}, []string{"{\"\xff\xfe\":\"\xff\"}", `{"��":"\ud800"}`, `{"��":"x"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := compileWireSchemaPlan(&tc.schema, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := compileValueSchema(&tc.schema, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range tc.values {
				value, err := ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				want := oracle.validate(value, "/wire", DefaultLimits())
				got := plan.validate(t.Context(), value, "/wire", DefaultLimits())
				if (want == nil) != (got == nil) {
					t.Fatalf("schema result differs for %q: legacy=%v planned=%v", raw, want, got)
				}
				if got != nil {
					var before, after *jsonschema.ValidationError
					if errors.As(want, &before) != errors.As(got, &after) || mappingErrorPath("/wire", want) != mappingErrorPath("/wire", got) {
						t.Fatalf("schema diagnostic differs for %q: legacy=%v planned=%v", raw, want, got)
					}
				}
				if value.raw != raw {
					t.Fatal("validation modified the wire")
				}
			}
			if err := plan.validate(t.Context(), Value{}, "/wire", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatal("absent value was accepted", err)
			}
		})
	}
}

func TestWireSchemaPlanKeepsUnconstrainedValueBudgets(t *testing.T) {
	plan, err := compileWireSchemaPlan(&ValueSchema{Type: AnyType}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw string
		limit     func(*Limits)
	}{
		{"depth", `{"unknown":[[[]]]}`, func(limits *Limits) { limits.Depth = 2 }},
		{"nodes", `[1,2,3]`, func(limits *Limits) { limits.Nodes = 3 }},
		{"bytes", `"large"`, func(limits *Limits) { limits.BufferBytes = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := DefaultLimits()
			tc.limit(&limits)
			value, err := ParseValue([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			got := plan.validate(t.Context(), value, "/wire", limits)
			want := plan.diagnostic.validate(value, "/wire", limits)
			var before, after *ResourceLimitError
			if !errors.As(want, &before) || !errors.As(got, &after) || *before != *after {
				t.Fatalf("resource boundary changed: legacy=%v planned=%v", want, got)
			}
		})
	}
}

func BenchmarkWireSchemaLargeString(b *testing.B) {
	const payloadBytes = 2 << 20
	value, err := EncodeValue(Object{"payload": StringValue(strings.Repeat("x", payloadBytes))})
	if err != nil {
		b.Fatal(err)
	}
	schema := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"payload": {Type: StringType}}, Required: []string{"payload"}}
	plan, err := compileWireSchemaPlan(schema, DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	b.Run("runtime", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := plan.diagnostic.validate(value, "/wire", DefaultLimits()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("planned", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := plan.validate(b.Context(), value, "/wire", DefaultLimits()); err != nil {
				b.Fatal(err)
			}
		}
	})
}
