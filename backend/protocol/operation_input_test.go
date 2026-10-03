package protocol

import "testing"

func TestOperationSchemaIsVerifiedAndImmutable(t *testing.T) {
	definition := textVerificationDefinition(t, "operation-input")
	var name string
	for key := range definition.Operations {
		name = key
		break
	}
	operation := definition.Operations[name]
	operation.Input = &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"engine": {Type: StringType}}, Required: []string{"engine"}, AllowUnknown: true}
	definition.Operations[name] = operation
	compiled := compileTestDefinition(t, definition)
	if report := Verify(t.Context(), compiled); !report.Passed {
		t.Fatal(report.Issues)
	}
	copy := compiled.Operations()[name]
	copy.Input.Required[0] = "missing"
	if err := compiled.CheckOperationInput(compiled.Operations()[name], definition.Samples[0].Input.Bytes()); err != nil {
		t.Fatal("operation snapshot mutated", err)
	}
	if err := compiled.CheckOperationInput(compiled.Operations()[name], []byte(`{}`)); err == nil {
		t.Fatal("missing required field accepted")
	}
	operation.Input.Required = []string{"missing"}
	operation.Input.Properties["missing"] = ValueSchema{Type: StringType}
	definition.Operations[name] = operation
	if report := Verify(t.Context(), compileTestDefinition(t, definition)); report.Passed {
		t.Fatal("invalid operation bypassed verification")
	}
}

func TestOperationFixtureRejectsUnknownReference(t *testing.T) {
	definition := textVerificationDefinition(t, "operation-reference")
	definition.Samples[0].Operation = "missing"
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := fixtureValue(t, definition)
	if compiled, issues := compiler.Compile(value.Bytes()); compiled != nil || len(issues) == 0 {
		t.Fatal("unknown operation fixture accepted")
	}
}

func TestCombinationChecksTargetOperationInput(t *testing.T) {
	source := compileTestDefinition(t, textVerificationDefinition(t, "optional-budget"))
	definition := textVerificationDefinition(t, "required-budget")
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		mapping := definition.Directions[direction]
		mapping.Transform.Fields["parameters"] = Expression{Op: "read", Path: "/parameters"}
		definition.Directions[direction] = mapping
	}
	for name, operation := range definition.Operations {
		operation.Input = &ValueSchema{Type: ObjectType, AllowUnknown: true, Required: []string{"parameters"}, Properties: map[string]ValueSchema{"parameters": {Type: ObjectType, Required: []string{"budget"}, Properties: map[string]ValueSchema{"budget": {Type: IntegerType}}}}}
		definition.Operations[name] = operation
	}
	for index, sample := range definition.Samples {
		if sample.Direction != DecodeRequest && sample.Direction != EncodeRequest {
			continue
		}
		for _, value := range []*Value{&sample.Input, &sample.Expected} {
			fields, err := value.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			fields["parameters"] = fixtureValue(t, map[string]int{"budget": 10})
			*value = fixtureValue(t, fields)
		}
		definition.Samples[index] = sample
	}
	target := compileTestDefinition(t, definition)
	for _, compiled := range []*Compiled{source, target} {
		if report := Verify(t.Context(), compiled); !report.Passed {
			t.Fatal("each independent contract should pass", report.Issues)
		}
	}
	report := VerifyCombination(t.Context(), source, target)
	if report.Passed || len(report.Issues) == 0 {
		t.Fatal("pair verification ignored the target's required wire budget")
	}
	if report.Issues[0].Code != InvalidInput {
		t.Fatalf("missing budget should have a located input diagnostic: %+v", report.Issues)
	}
}
