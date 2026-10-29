package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWireContractSpecOrderedAssertions(t *testing.T) {
	spec := WireContractSpec{
		Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true},
		Assertions: []WireAssertion{
			{Path: "/payload/text", When: expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression("text")}}), Schema: ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: "text payload must be a string"},
			{Path: "/usage", Schema: ValueSchema{Type: IntegerType}, Code: InvalidInput, Reason: "usage must be an integer"},
			{Path: "", Schema: ValueSchema{Type: ObjectType, AllowUnknown: true, Properties: map[string]ValueSchema{"id": {Type: StringType}}, Required: []string{"id"}}, Code: UnsupportedCapability, Reason: "response id is required"},
		},
	}
	contract, err := compileWireContractSpec(spec, "/spec/wireContracts/encode_response", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw, path, reason string
		code                    IssueCode
	}{
		{"valid", `{"id":"r","kind":"text","payload":{"text":"private value"},"usage":9007199254740993}`, "", "", ""},
		{"condition-false", `{"id":"r","kind":"other","usage":0}`, "", "", ""},
		{"missing", `{"kind":"text","payload":{},"usage":"invalid"}`, "/payload/text", "text payload must be a string", ConversionRejected},
		{"explicit-null", `{"kind":"text","payload":{"text":null},"usage":0}`, "/payload/text", "text payload must be a string", ConversionRejected},
		{"parent-type", `{"kind":"text","payload":"private value","usage":0}`, "/payload/text", "text payload must be a string", ConversionRejected},
		{"second-assertion", `{"kind":"other","usage":"private value"}`, "/usage", "usage must be an integer", InvalidInput},
		{"whole-document", `{"kind":"other","usage":1}`, "", "response id is required", UnsupportedCapability},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := ParseValue([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			err = contract.validator.ValidateWireOutput(EncodeResponse, value)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *ConversionError
			if !errors.As(err, &failure) || len(failure.Issues) != 1 {
				t.Fatal("missing first assertion failure", err)
			}
			issue := failure.Issues[0]
			if issue.Code != tc.code || issue.Path != tc.path || issue.Reason != tc.reason || issue.Severity != SeverityError || strings.Contains(err.Error(), "private value") || errors.Unwrap(err) == nil {
				t.Fatal("wire failure lost its location, static message or cause", err)
			}
		})
	}
}

func TestWireContractSpecRejectsPostMappingChanges(t *testing.T) {
	compiled := compileTestDefinition(t, testDefinition("declared-contract", "model", "history", "tools", "answer"))
	contract, err := compileWireContractSpec(WireContractSpec{
		Schema:     &ValueSchema{Type: ObjectType, AllowUnknown: true},
		Assertions: []WireAssertion{{Path: "/request", Schema: ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: "request identity must be a string"}},
	}, "/spec/wireContracts/encode_response", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	compiled.wireContracts[EncodeResponse] = contract
	semantic := fixtureValue(t, Response{SchemaVersion: 1, ID: StringValue("r"), Content: []Node{}})
	wire, issues := compiled.Execute(t.Context(), EncodeResponse, semantic, EvaluationContext{})
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateWireOutput(EncodeResponse, wire); err != nil {
		t.Fatal(err)
	}
	modified, err := ApplyMutations(wire, []Mutation{{Op: SetValue, Path: "/request", Value: fixtureValue(t, 42)}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	err = compiled.ValidateWireOutput(EncodeResponse, modified)
	var failure *ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 {
		t.Fatal("post-mapping modification bypassed the wire contract", err)
	}
	issue := failure.Issues[0]
	if issue.Protocol != compiled.Identity() || issue.Direction != EncodeResponse || issue.Code != ConversionRejected || issue.Path != "/request" {
		t.Fatal("final wire diagnostic lost protocol attribution", issue)
	}
}

func TestWireContractSpecRejectsInvalidDeclarations(t *testing.T) {
	for name, change := range map[string]func(*WireContractSpec){
		"missing-schema":       func(spec *WireContractSpec) { spec.Schema = nil },
		"invalid-path":         func(spec *WireContractSpec) { spec.Assertions[0].Path = "not-a-pointer" },
		"invented-scope-error": func(spec *WireContractSpec) { spec.Assertions[0].Code = ResourceScopeMismatch },
		"missing-reason":       func(spec *WireContractSpec) { spec.Assertions[0].Reason = "" },
		"nonboolean-condition": func(spec *WireContractSpec) { spec.Assertions[0].When = expressionPointer(literalExpression("true")) },
		"invalid-schema": func(spec *WireContractSpec) {
			spec.Assertions[0].Schema = ValueSchema{Type: StringType, AllowUnknown: true}
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec := WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}, Assertions: []WireAssertion{{Path: "/id", Schema: ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "id must be a string"}}}
			change(&spec)
			if _, err := compileWireContractSpec(spec, "/spec/wireContracts/decode_request", DefaultLimits()); err == nil {
				t.Fatal("invalid wire declaration was accepted")
			}
		})
	}
}

func TestWireContractSpecUsesRequestContextAndBudget(t *testing.T) {
	// Small stand-in limits exercise request expansion without a large fixture.
	limits := DefaultLimits()
	limits.BufferBytes = 16
	contract, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: StringType}}, "/spec/wireContracts/encode_request", limits)
	if err != nil {
		t.Fatal(err)
	}
	compiled := &Compiled{limits: limits, wireContracts: map[Direction]compiledWireContract{EncodeRequest: contract}}
	value := StringValue(strings.Repeat("x", 32))
	ctx, err := WithRequestBodyLimit(t.Context(), int64(value.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateWireOutputContext(ctx, EncodeRequest, value); err != nil {
		t.Fatal("declarative contract ignored the expanded request budget", err)
	}
	tighter, err := WithRequestBodyLimit(t.Context(), int64(value.Len()-1))
	if err != nil {
		t.Fatal(err)
	}
	var resource *ResourceLimitError
	if err := compiled.ValidateWireOutputContext(tighter, EncodeRequest, value); !errors.As(err, &resource) || resource.Limit != value.Len()-1 {
		t.Fatal("declarative contract ignored the tighter request budget", err)
	}
	compiled.explicitLimits = true
	if err := compiled.ValidateWireOutputContext(ctx, EncodeRequest, value); !errors.As(err, &resource) || resource.Limit != limits.BufferBytes {
		t.Fatal("request budget bypassed the authored contract limit", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := compiled.ValidateWireOutputContext(cancelled, EncodeRequest, value); !errors.Is(err, context.Canceled) {
		t.Fatal("declarative contract lost request cancellation", err)
	}
}

func TestWireContractSpecChecksSourceArrayLocations(t *testing.T) {
	rootSchema := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{
		"id": {Type: StringType}, "messages": {Type: ArrayType},
	}, Required: []string{"id", "messages"}}
	contentCheck := WireAssertion{
		Path: "/content", Schema: ValueSchema{Type: ArrayType}, Code: InvalidInput, Reason: "content must be an array",
		Each: []WireAssertion{
			{Path: "/response_id", Schema: ValueSchema{Type: StringType}, Code: InvalidAssociation, Reason: "content belongs to another response",
				Check: expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", ""), readExpression("root", "/id")}})},
			{Path: "/text", Schema: ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "text must be a string"},
		},
	}
	spec := WireContractSpec{Schema: rootSchema, Assertions: []WireAssertion{
		{Path: "/messages", Schema: ValueSchema{Type: ArrayType}, Code: InvalidInput, Reason: "messages must be an array", Each: []WireAssertion{contentCheck}},
	}}
	contract, err := compileWireContractSpec(spec, "/spec/wireContracts/decode_response", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		raw, path string
		code      IssueCode
	}{
		{`{"id":"r","messages":[{"content":[]},{"content":[{"response_id":"r","text":"ok"}]}]}`, "", ""},
		{`{"id":"r","messages":[{"content":[]},{"content":[{"response_id":"private","text":false}]}]}`, "/messages/1/content/0/response_id", InvalidAssociation},
		{`{"id":"r","messages":[{"content":[]},{"content":[{"response_id":"r","text":false}]}]}`, "/messages/1/content/0/text", InvalidInput},
		{`{"id":"r","messages":[{"content":null}]}`, "/messages/0/content", InvalidInput},
	} {
		value, err := ParseValue([]byte(test.raw))
		if err != nil {
			t.Fatal(err)
		}
		err = contract.validator.ValidateWireOutput(DecodeResponse, value)
		if test.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var failure *ConversionError
		if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != test.code || failure.Issues[0].Path != test.path || strings.Contains(err.Error(), "private") {
			t.Fatal("nested assertion lost source position, order or privacy", err)
		}
	}
	// The same immutable plan is reusable; failure cannot retain per-document state.
	if err := contract.validator.ValidateWireOutput(DecodeResponse, Value{raw: `{"id":"next","messages":[{"content":[{"response_id":"next","text":"ok"}]}]}`}); err != nil {
		t.Fatal(err)
	}
}

func TestWireContractSpecRejectsUnboundedOrUntypedChecks(t *testing.T) {
	base := WireAssertion{Path: "", Schema: ValueSchema{Type: ArrayType}, Code: InvalidInput, Reason: "expected an array"}
	for _, test := range []struct {
		name   string
		change func(*WireAssertion)
	}{
		{"nonboolean-check", func(check *WireAssertion) { check.Check = expressionPointer(literalExpression("yes")) }},
		{"nullable-each", func(check *WireAssertion) { check.Schema.Nullable = true; check.Each = []WireAssertion{base} }},
		{"nonarray-each", func(check *WireAssertion) { check.Schema.Type = ObjectType; check.Each = []WireAssertion{base} }},
		{"deep-each", func(check *WireAssertion) {
			for range DefaultLimits().Depth {
				child := *check
				check.Each = []WireAssertion{child}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			check := base
			test.change(&check)
			_, err := compileWireContractSpec(WireContractSpec{Schema: &ValueSchema{Type: ArrayType}, Assertions: []WireAssertion{check}}, "/wire", DefaultLimits())
			if err == nil {
				t.Fatal("invalid predicate or iteration accepted")
			}
		})
	}
}

func TestWireContractSpecBoundsRepeatedItemWork(t *testing.T) {
	limits := DefaultLimits()
	limits.Nodes = 32
	check := WireAssertion{Path: "", Schema: ValueSchema{Type: IntegerType}, Code: InvalidInput, Reason: "integer required", Check: expressionPointer(literalExpression(true))}
	contract, err := compileWireContractSpec(WireContractSpec{
		Schema:     &ValueSchema{Type: ArrayType},
		Assertions: []WireAssertion{{Path: "", Schema: ValueSchema{Type: ArrayType}, Code: InvalidInput, Reason: "array required", Each: []WireAssertion{check, check}}},
	}, "/wire", limits)
	if err != nil {
		t.Fatal(err)
	}
	var limit *ResourceLimitError
	err = contract.validator.ValidateWireOutput(DecodeRequest, Value{raw: `[1,2,3,4,5,6,7,8,9,10]`})
	if !errors.As(err, &limit) || limit.Domain != "evaluation" || limit.Metric != "steps" {
		t.Fatal("iteration escaped the shared work budget", err)
	}
}
