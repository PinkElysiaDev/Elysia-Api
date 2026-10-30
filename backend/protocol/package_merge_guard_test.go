package protocol

import (
	"strings"
	"testing"
)

func mergeAbsentCondition(path string) Expression {
	return Expression{Op: "not", Source: &Expression{Op: "exists", Source: expressionPointer(readExpression("input", path))}}
}

func guardedMergeTestPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := deepMergeTestPackage(t)
	schema := document.Spec.Documents[document.Mapping.Directions[EncodeRequest].Document].Schema
	schema.Properties["model"] = ValueSchema{Type: AnyType}
	schema.Required = []string{"prompt"}
	document.Mapping.Directions[EncodeRequest].Effects[0].When = &Expression{Op: "all", Items: []Expression{
		{Op: "exists", Source: expressionPointer(readExpression("input", "/model"))}, mergeAbsentCondition("/parameters/extension/model"),
	}}
	return document
}

func TestGuardedDeepMergePreservesWholeOwnerOnlyWhenApplicable(t *testing.T) {
	compiled := compilePackageForTest(t, guardedMergeTestPackage(t))
	for _, test := range []struct{ model, extension, want string }{
		{`{"a":1}`, `{}`, `{"a":1}`},
		{`null`, `{}`, `null`},
		{`{"a":1}`, `{"model":{"b":2}}`, `{"a":1,"b":2}`},
		{"", `{"model":"supplied"}`, `"supplied"`},
		{"", `{}`, ""},
	} {
		request := &Request{SchemaVersion: ProtocolCoreVersion, Model: Value{raw: test.model}, Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}, Parameters: Object{"extension": Value{raw: test.extension}}}
		wire, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
		if err != nil {
			t.Fatal("guarded merge changed input acceptance", test, err)
		}
		fields, _ := wire.ReadObject()
		if fields["model"].raw != test.want {
			t.Fatal("conditional emit became an unconditional copy claim", test, wire)
		}
	}
	for _, operand := range []Expression{
		objectExpression(map[string]Expression{"other": literalExpression(1)}),
		{Op: "project", Source: expressionPointer(readExpression("input", "/parameters/extension")), Keys: []string{"model"}, Policy: "exclude"},
		{Op: "if", When: &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/parameters/extension"))}, Then: expressionPointer(readExpression("input", "/parameters/extension")), Otherwise: expressionPointer(objectExpression(nil))},
	} {
		document := guardedMergeTestPackage(t)
		document.Mapping.Directions[EncodeRequest].Program.Items[1] = operand
		compilePackageForTest(t, document)
	}
}

func TestGuardedDeepMergeRejectsUnprovedAbsence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"wrong-owner-path", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Effects[0].When.Items[1] = mergeAbsentCondition("/parameters/other/model")
		}},
		{"not-present-allows-null", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Effects[0].When.Items[1].Source.Op = "present"
		}},
		{"disjunction", func(document *ProtocolPackage) { document.Mapping.Directions[EncodeRequest].Effects[0].When.Op = "any" }},
		{"false-guard", func(document *ProtocolPackage) {
			condition := document.Mapping.Directions[EncodeRequest].Effects[0].When
			condition.Items = append(condition.Items, literalExpression(false))
		}},
		{"contradictory-source", func(document *ProtocolPackage) {
			condition := document.Mapping.Directions[EncodeRequest].Effects[0].When
			condition.Items = append(condition.Items, mergeAbsentCondition("/model"))
		}},
		{"missing-source-guard", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Effects[0].When.Items = document.Mapping.Directions[EncodeRequest].Effects[0].When.Items[1:]
		}},
		{"another-owner-writer", func(document *ProtocolPackage) {
			mapping := document.Mapping.Directions[EncodeRequest]
			mapping.Program.Items = append(mapping.Program.Items, objectExpression(map[string]Expression{"model": objectExpression(map[string]Expression{"other": literalExpression(1)})}))
			document.Mapping.Directions[EncodeRequest] = mapping
		}},
		{"one-branch-writes-owner", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Program.Items[1] = Expression{Op: "if", When: expressionPointer(literalExpression(true)), Then: expressionPointer(readExpression("input", "/parameters/extension")), Otherwise: expressionPointer(objectExpression(map[string]Expression{"model": literalExpression("other")}))}
		}},
		{"required-read", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Program.Items[1].Required = true
		}},
		{"omitted-operand", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Program.Items[1] = Expression{Op: "omit"}
		}},
		{"binding-not-in-proof", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Program.Items[1] = Expression{Op: "let", Name: "owner", Source: expressionPointer(readExpression("input", "/parameters/extension")), Body: expressionPointer(bindingTestRead("owner", ""))}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := guardedMergeTestPackage(t)
			test.change(&document)
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
			if err := IssuesError(issues); compiled != nil || err == nil || !strings.Contains(err.Error(), "provable read") {
				t.Fatal("unproved absence compiled", compiled, issues)
			}
		})
	}
}

func TestGuardedDeepMergeParentAndBranchProof(t *testing.T) {
	base := objectExpression(map[string]Expression{"parent": objectExpression(map[string]Expression{"leaf": readExpression("input", "/source")})})
	merge := Expression{Op: "merge_deep", Items: []Expression{base, readExpression("input", "/extension")}}
	condition := Expression{Op: "all", Items: []Expression{{Op: "exists", Source: expressionPointer(readExpression("input", "/source"))}, mergeAbsentCondition("/extension/parent/leaf")}}
	input, output := []string{"source"}, []string{"parent", "leaf"}
	program := compileTestOutputExpression(t, merge)
	if provesGuardedDeepMergeEmission(program, output, input, compileTestOutputExpression(t, condition), DefaultLimits().Nodes) {
		t.Fatal("child absence proved exclusive ownership of its parent")
	}
	condition.Items[1] = mergeAbsentCondition("/extension/parent")
	guard := compileTestOutputExpression(t, condition)
	if !provesGuardedDeepMergeEmission(program, output, input, guard, DefaultLimits().Nodes) ||
		provesGuardedDeepMergeEmission(program, output, input, guard, 1) || provesGuardedDeepMergeEmission(program, nil, input, guard, DefaultLimits().Nodes) {
		t.Fatal("fixed-parent proof ignored its work or path boundary")
	}
	branch := Expression{Op: "if", When: &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/extension"))}, Then: &merge, Otherwise: &base}
	if !provesGuardedDeepMergeEmission(compileTestOutputExpression(t, branch), output, input, guard, DefaultLimits().Nodes) {
		t.Fatal("an independent plain-copy branch was rejected")
	}
	branch.Otherwise = expressionPointer(objectExpression(nil))
	if provesGuardedDeepMergeEmission(compileTestOutputExpression(t, branch), output, input, guard, DefaultLimits().Nodes) {
		t.Fatal("program branch condition was used to excuse a missing copy")
	}
}
