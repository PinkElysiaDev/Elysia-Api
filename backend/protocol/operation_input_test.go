package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

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
	if err := compiled.CheckOperationInput(t.Context(), name, definition.Samples[0].Input.Bytes()); err != nil {
		t.Fatal("operation snapshot mutated", err)
	}
	if err := compiled.CheckOperationInput(t.Context(), name, []byte(`{}`)); err == nil {
		t.Fatal("missing required field accepted")
	}
	if err := compiled.CheckOperationInput(t.Context(), "missing", []byte(`{}`)); err == nil {
		t.Fatal("unknown operation accepted")
	}
	operation.Input.Required = []string{"missing"}
	operation.Input.Properties["missing"] = ValueSchema{Type: StringType}
	definition.Operations[name] = operation
	if report := Verify(t.Context(), compileTestDefinition(t, definition)); report.Passed {
		t.Fatal("invalid operation bypassed verification")
	}
}

func TestOperationInputRequestBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bodyBytes     int
		requestLimit  int64
		authoredLimit int
		wantLimit     int
	}{
		{name: "default accepts over 8 MiB", bodyBytes: 9 << 20},
		{name: "configured exact boundary", bodyBytes: 1024, requestLimit: 1024},
		{name: "configured one byte over", bodyBytes: 1025, requestLimit: 1024, wantLimit: 1024},
		{name: "authored exact boundary", bodyBytes: 2048, requestLimit: 4096, authoredLimit: 2048},
		{name: "authored one byte over", bodyBytes: 2049, requestLimit: 4096, authoredLimit: 2048, wantLimit: 2048},
		{name: "configured tighter than authored", bodyBytes: 1025, requestLimit: 1024, authoredLimit: 2048, wantLimit: 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := textVerificationDefinition(t, "operation-budget")
			var name string
			for key, operation := range definition.Operations {
				name = key
				operation.Input = &ValueSchema{Type: ObjectType, Required: []string{"engine"}, Properties: map[string]ValueSchema{"engine": {Type: StringType}}, AllowUnknown: true}
				definition.Operations[key] = operation
			}
			if tc.authoredLimit > 0 {
				limits := DefaultLimits()
				limits.BufferBytes = tc.authoredLimit
				definition.Limits = &limits
			}
			compiled := compileTestDefinition(t, definition)
			ctx := t.Context()
			if tc.requestLimit > 0 {
				var err error
				ctx, err = WithRequestBodyLimit(ctx, tc.requestLimit)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Unknown descendants still count toward the complete wire budget.
			body := []byte(`{"engine":"m","payload":"` + strings.Repeat("a", tc.bodyBytes-len(`{"engine":"m","payload":""}`)) + `"}`)
			err := compiled.CheckOperationInput(ctx, name, body)
			value, parseErr := ParseValue(body)
			if parseErr != nil || fmt.Sprint(compiled.CheckOperationValueInput(ctx, name, value)) != fmt.Sprint(err) {
				t.Fatalf("immutable operation validation changed budget/schema behavior: %v", parseErr)
			}
			if tc.wantLimit == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var resource *ResourceLimitError
				var conversion *ConversionError
				if !errors.As(err, &resource) || resource.Metric != "bytes" || resource.Limit != tc.wantLimit || resource.Observed != len(body) {
					t.Fatalf("wrong request budget: %v", err)
				}
				if !errors.As(err, &conversion) || conversion.Issues[0].Code != LimitExceeded || conversion.Issues[0].Direction != DecodeRequest {
					t.Fatalf("resource diagnostic lost: %v", err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := compiled.CheckOperationInput(canceled, name, body); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if err := compiled.CheckOperationValueInput(canceled, name, value); !errors.Is(err, context.Canceled) {
				t.Fatalf("immutable validation lost cancellation: %v", err)
			}
		})
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
