package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestWireConstraintSharesChecksAndKeepsSourcePaths(t *testing.T) {
	item := &ValueSchema{Type: ObjectType, AllowUnknown: true}
	root := &ValueSchema{Type: ObjectType, AllowUnknown: true}
	compiler := newExpressionCompiler(DefaultLimits(), nil)
	compiler.constraints = newWireConstraintCompiler(map[string][]WireAssertion{
		"item": {{Path: "/name", Schema: &ValueSchema{Type: StringType}, Reason: "name is required"}},
	})
	declaration := []WireAssertion{{Path: "/items", Schema: &ValueSchema{Type: ArrayType, Items: item},
		Code: InvalidInput, Reason: "items are required", Each: []WireAssertion{{Ref: "item", Path: "/item"}}}}
	compile := func() []compiledWireAssertion {
		t.Helper()
		checks, err := compileWireAssertions(declaration, "/spec/documents/list/assertions", root, root, compiler, 1)
		if err != nil {
			t.Fatal(err)
		}
		return checks
	}
	first, second := compile(), compile()
	if &first[0].each[0].group[0] != &second[0].each[0].group[0] {
		t.Fatal("constraint instructions expanded at the call site")
	}
	schema, err := compileWireSchemaPlan(root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	validator := &specWireValidator{schema: schema, assertions: first, limits: DefaultLimits()}
	for direction, code := range map[Direction]IssueCode{DecodeRequest: InvalidInput, DecodeResponse: UpstreamContractViolation, EncodeEvent: ConversionRejected, DecodeClientEvent: InvalidInput, EncodeUpstreamEvent: ConversionRejected} {
		for _, fixture := range []struct{ body, path string }{
			{`{"items":[{"item":{"name":"first"}},{"item":{"name":"second"}}]}`, ""},
			{`{"items":[{"item":{"name":"first"}},{"item":{"name":2}}]}`, "/items/1/item/name"},
		} {
			err := validator.ValidateWireOutput(direction, parseTestValue(t, fixture.body))
			if fixture.path == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var failure *ConversionError
				if !errors.As(err, &failure) || len(failure.Issues) != 1 || failure.Issues[0].Code != code || failure.Issues[0].Path != fixture.path {
					t.Fatal("constraint lost its boundary or source location", err)
				}
			}
		}
	}
}

func TestWireConstraintReferencesBindDocumentIdentity(t *testing.T) {
	document := textProtocolPackage(t)
	document.Spec.Expressions = map[string]Expression{"gate": literalExpression(true)}
	document.Spec.Constraints = map[string][]WireAssertion{
		"outer": {{Ref: "inner"}},
		"inner": {{Path: "/guarded", Schema: &ValueSchema{Type: StringType}, Code: InvalidInput,
			Reason: "guarded field is required", When: &Expression{Op: "ref", Ref: "gate"}}},
	}
	contract := WireContractSpec{Schema: &ValueSchema{Type: ObjectType, AllowUnknown: true}, Assertions: []WireAssertion{{Ref: "outer"}}}
	document.Spec.Documents["provider-response"], document.Spec.Documents["checked-final"] = contract, contract
	entry := document.Mapping.Directions[EncodeResponse]
	entry.FinalDocument = "checked-final"
	document.Mapping.Directions[EncodeResponse] = entry
	before := compilePackageForTest(t, document)
	for _, change := range []func(){
		func() { document.Spec.Expressions["gate"] = literalExpression(false) },
		func() { document.Spec.Constraints["inner"][0].Path = "/changed" },
	} {
		change()
		after := compilePackageForTest(t, document)
		for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
			isChanged := before.mappings[direction].definitionHash != after.mappings[direction].definitionHash
			if isChanged != (direction == DecodeResponse || direction == EncodeResponse) {
				t.Fatal("document identity omitted a transitive dependency or changed an unrelated direction", direction)
			}
		}
		before = after
	}
	document.Spec.Constraints["unused"] = []WireAssertion{{Ref: "inner"}}
	if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), "unreachable constraint") {
		t.Fatal("unused declaration compiled", err)
	}
}

func TestWireConstraintRejectsInvalidCallsAndCachedDepth(t *testing.T) {
	limits := DefaultLimits()
	limits.Depth = 3
	compiler := newExpressionCompiler(limits, nil)
	compiler.constraints = newWireConstraintCompiler(map[string][]WireAssertion{
		"outer": {{Ref: "inner"}},
		"inner": {{Path: "/name", Schema: &ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "name required"}},
		"cycle": {{Ref: "cycle"}},
	})
	scope := expressionScope{input: &ValueSchema{Type: ObjectType, AllowUnknown: true}, isWireContract: true}
	if _, err := compileWireAssertionsInScope([]WireAssertion{{Ref: "outer"}}, "/checks", scope, compiler, 1); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		assertion WireAssertion
		depth     int
	}{
		{WireAssertion{Ref: "outer"}, 2},
		{WireAssertion{Ref: "cycle"}, 1},
		{WireAssertion{Ref: "missing"}, 1},
		{WireAssertion{Ref: "inner", Code: InvalidInput}, 1},
	} {
		if _, err := compileWireAssertionsInScope([]WireAssertion{fixture.assertion}, "/checks", scope, compiler, fixture.depth); err == nil {
			t.Fatal("invalid constraint call compiled", fixture)
		}
	}
}
