package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func runtimeSchemaFixtures() []struct {
	name   string
	schema *ValueSchema
	raw    string
} {
	object := &ValueSchema{Type: ObjectType, Required: []string{"id"}, Properties: map[string]ValueSchema{"id": {Type: IntegerType}, "name": {Type: StringType}}}
	wide := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{}}
	var fields []string
	for i := range 64 {
		key := fmt.Sprintf("k%d", i)
		wide.Properties[key] = ValueSchema{Type: IntegerType}
		fields = append(fields, fmt.Sprintf(`"%s":%d`, key, i))
	}
	return []struct {
		name   string
		schema *ValueSchema
		raw    string
	}{
		{"object", object, `{"id":9007199254740993123,"name":"sample"}`},
		{"wide", wide, "{" + strings.Join(fields, ",") + "}"},
		{"array", &ValueSchema{Type: ArrayType, Items: object}, "[" + strings.Repeat(`{"id":123,"name":"sample"},`, 127) + `{"id":124}]`},
		{"opaque-payload", &ValueSchema{Type: ObjectType, AllowUnknown: true, Properties: map[string]ValueSchema{"id": {Type: IntegerType}}}, `{"id":1,"payload":"` + strings.Repeat("A", 1<<20) + `"}`},
		{"enum", &ValueSchema{Type: NumberType, Enum: []Value{{raw: `9007199254740993123`}, {raw: `1.25`}}}, `9007199254740993123.0`},
		{"invalid", object, `{"id":"wrong","name":false,"extra":0}`},
	}
}

func BenchmarkRuntimeSchema(b *testing.B) {
	for _, tc := range runtimeSchemaFixtures() {
		b.Run(tc.name, func(b *testing.B) {
			value, err := ParseValue([]byte(tc.raw))
			if err != nil {
				b.Fatal(err)
			}
			limits := DefaultLimits()
			validator, err := compileValueSchema(tc.schema, limits)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				err = validator.validate(value, "/input", limits)
				if (err != nil) != (tc.name == "invalid") {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRuntimeSchemaCompile(b *testing.B) {
	for _, tc := range runtimeSchemaFixtures() {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := compileValueSchema(tc.schema, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
