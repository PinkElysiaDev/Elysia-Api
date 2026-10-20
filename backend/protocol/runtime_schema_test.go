package protocol

import (
	"errors"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRuntimeSchemaContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		schema         ValueSchema
		valid, invalid []string
	}{
		{"integer", ValueSchema{Type: IntegerType}, []string{`9007199254740993123`, `1.0`, `1e3`, `-0`}, []string{`1.5`, `null`, `"1"`}},
		{"number", ValueSchema{Type: NumberType}, []string{`1.25`, `1e-4096`, `1e4096`}, []string{`true`, `"1.25"`}},
		{"exact-enum", ValueSchema{Type: NumberType, Enum: []Value{{raw: `9007199254740993123`}, {raw: `9007199254740993123.0`}}}, []string{`9007199254740993123e0`}, []string{`9007199254740993122`, `9007199254740993124`}},
		{"nullable-enum", ValueSchema{Type: StringType, Nullable: true, Enum: []Value{StringValue("yes")}}, []string{`null`, `"yes"`}, []string{`"no"`, `false`}},
		{"any", ValueSchema{Type: AnyType}, []string{`null`, `false`, `0`, `""`, `[]`, `{}`}, nil},
		{"closed", ValueSchema{Type: ObjectType, Required: []string{"id"}, Properties: map[string]ValueSchema{"id": {Type: IntegerType}, "n": {Type: StringType, Nullable: true}}}, []string{`{"id":0}`, `{"id":1,"n":null}`, `{"id":"wrong","\u0069d":1}`}, []string{`{}`, `{"id":null}`, `{"id":1,"extra":true}`, `{"id":1,"\u0069d":"wrong"}`}},
		{"open", ValueSchema{Type: ObjectType, AllowUnknown: true}, []string{`{"a":[1,null,{"x":false}]}`}, []string{`[]`}},
		{"empty-object", ValueSchema{Type: ObjectType}, []string{`{}`}, []string{`{"x":1}`}},
		{"array", ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: IntegerType}}, []string{`[]`, `[1,2.0]`}, []string{`[1,null]`, `[false]`}},
		{"array-any", ValueSchema{Type: ArrayType}, []string{`[null,{},[],false]`}, []string{`{}`}},
		{"object-enum", ValueSchema{Type: ObjectType, AllowUnknown: true, Enum: []Value{{raw: `{"a":1,"b":[2]}`}}}, []string{`{"b":[2.0],"a":1e0}`}, []string{`{"a":1,"b":[3]}`}},
		{"unicode", ValueSchema{Type: ObjectType, Required: []string{"��"}, Properties: map[string]ValueSchema{"��": {Type: StringType, Enum: []Value{StringValue("�")}}}}, []string{"{\"\xff\xfe\":\"\xff\"}", `{"��":"\ud800"}`}, []string{`{"��":"x"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validator, err := compileValueSchema(&tc.schema, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range tc.valid {
				value, err := ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				if err := validator.validate(value, "/input", DefaultLimits()); err != nil {
					t.Errorf("rejected %s: %v", raw, err)
				}
				if value.raw != raw {
					t.Fatal("mutated wire value")
				}
			}
			for _, raw := range tc.invalid {
				value, err := ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				err = validator.validate(value, "/input", DefaultLimits())
				var validation *jsonschema.ValidationError
				if !errors.As(err, &validation) {
					t.Errorf("expected library rejection for %s: %v", raw, err)
				}
			}
			if err := validator.validate(Value{}, "/input", DefaultLimits()); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("absent value: %v", err)
			}
		})
	}
}

func TestRuntimeSchemaNumericAndResourceBounds(t *testing.T) {
	t.Parallel()
	limits := DefaultLimits()
	validator, err := compileValueSchema(&ValueSchema{Type: AnyType}, limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`1e4097`, `1e-4097`, `1e9999999999999999999999999`, strings.Repeat("1", 4097), `{"a/b~":[1e999999999]}`} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.validate(value, "/input", limits); err == nil || !strings.Contains(err.Error(), "numeric conversion limit") {
			t.Fatalf("unbounded number %s: %v", raw, err)
		}
		var absent *runtimeValueSchema
		if err := absent.validate(value, "/input", limits); err != nil {
			t.Fatalf("schema-free path changed: %v", err)
		}
	}
	if _, err := compileValueSchema(&ValueSchema{Type: NumberType, Enum: []Value{{raw: `1e999999999`}}}, limits); err == nil {
		t.Fatal("unbounded enum compiled")
	}
	for _, tc := range []struct {
		name, raw string
		limits    Limits
	}{
		{"depth", `{"unknown":[[[]]]}`, func() Limits { l := limits; l.Depth = 2; return l }()},
		{"nodes", `[1,2,3]`, func() Limits { l := limits; l.Nodes = 3; return l }()},
		{"bytes", `"large"`, func() Limits { l := limits; l.BufferBytes = 3; return l }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, _ := ParseValue([]byte(tc.raw))
			if err := validator.validate(value, "/input", tc.limits); err == nil {
				t.Fatal("resource limit bypassed")
			}
		})
	}
}

func TestRuntimeSchemaErrorLocations(t *testing.T) {
	t.Parallel()
	schema := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"a/b~": {Type: ArrayType, Items: &ValueSchema{Type: IntegerType}}}}
	validator, err := compileValueSchema(schema, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	err = validator.validate(Value{raw: `{"a/b~":["wrong"]}`}, "/input", DefaultLimits())
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) || !strings.Contains(err.Error(), "/a~1b~0/0") {
		t.Fatalf("lost instance pointer: %v", err)
	}
	if got := mappingErrorPath("/fallback", err); got != "/input/a~1b~0/0" {
		t.Fatalf("diagnostic path: %s", got)
	}
}

func TestRuntimeSchemaCompiledIsolationAndConcurrency(t *testing.T) {
	definition := testDefinition("schema-library", "model", "history", "tools", "answer")
	op := definition.Operations["generate"]
	op.Input = &ValueSchema{Type: ObjectType, Required: []string{"id"}, Properties: map[string]ValueSchema{"id": {Type: IntegerType}}}
	definition.Operations["generate"] = op
	compiled := compileTestDefinition(t, definition)
	if len(compiled.operationSchemas) != 1 {
		t.Fatal("operation schema not precompiled")
	}
	operation := compiled.Operations()["generate"]
	if err := compiled.CheckOperationInput(operation, []byte(`{"id":9007199254740993123}`)); err != nil {
		t.Fatal(err)
	}
	operation.Input.Properties["id"] = ValueSchema{Type: StringType}
	if err := compiled.CheckOperationInput(operation, []byte(`{"id":"customized"}`)); err != nil {
		t.Fatalf("customized schema ignored: %v", err)
	}
	if err := compiled.CheckOperationInput(operation, []byte(`{"id":1}`)); err == nil {
		t.Fatal("stale validator reused for customized metadata")
	}
	for range 8 {
		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			for range 20 {
				original := compiled.Operations()["generate"]
				if err := compiled.CheckOperationInput(original, []byte(`{"id":1}`)); err != nil {
					t.Fatal(err)
				}
				if err := compiled.CheckOperationInput(original, []byte(`{"id":"wrong"}`)); err == nil {
					t.Fatal("compiled schema mutated")
				}
			}
		})
	}
}

func TestRuntimeSchemaMappingValidation(t *testing.T) {
	definition := testDefinition("schema-mapping", "model", "history", "tools", "answer")
	definition.Directions[EncodeRequest] = Mapping{
		Input:     &ValueSchema{Type: ObjectType, Required: []string{"value"}, Properties: map[string]ValueSchema{"value": {Type: AnyType}}},
		Output:    &ValueSchema{Type: IntegerType},
		Transform: expressionPointer(readExpression("input", "/value")),
	}
	compiled := compileTestDefinition(t, definition)
	mapping := compiled.mappings[EncodeRequest]
	if mapping.input == nil || mapping.output == nil {
		t.Fatal("mapping schemas were not precompiled")
	}
	for _, tc := range []struct{ raw, want, errorPath string }{
		{`{"value":9007199254740993123}`, `9007199254740993123`, ""},
		{`{}`, "", "/input"},
		{`{"value":false}`, "", "/output"},
	} {
		value, _ := ParseValue([]byte(tc.raw))
		output, err := compiled.executeMapping(t.Context(), EncodeRequest, value, EvaluationContext{})
		if tc.errorPath == "" {
			if err != nil || output.raw != tc.want {
				t.Fatalf("output %q: %v", output.raw, err)
			}
			continue
		}
		var conversion *ConversionError
		var validation *jsonschema.ValidationError
		if !errors.As(err, &conversion) || !errors.As(err, &validation) || conversion.Issues[0].Path != tc.errorPath {
			t.Fatalf("lost validation cause/location: %v", err)
		}
	}
}
