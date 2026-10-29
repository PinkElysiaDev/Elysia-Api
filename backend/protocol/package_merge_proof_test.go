package protocol

import (
	"errors"
	"strings"
	"testing"
)

func deepMergeTestPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := textProtocolPackage(t)
	document.Spec.WireContracts[EncodeRequest].Schema.AllowUnknown = true
	mapping := document.Mapping.Directions[EncodeRequest]
	mapping.Program = Expression{Op: "merge_deep", Items: []Expression{mapping.Program, readExpression("input", "/parameters/extension")}}
	document.Mapping.Directions[EncodeRequest] = mapping
	return document
}

func TestDeepMergeEmissionRequiresUnconditionalNonObjectCopy(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		change     func(*ProtocolPackage)
	}{
		{"unknown-leaf", "provable read", func(document *ProtocolPackage) {
			document.Spec.WireContracts[EncodeRequest].Schema.Properties["model"] = ValueSchema{Type: AnyType}
		}},
		{"object-leaf", "provable read", func(document *ProtocolPackage) {
			document.Spec.WireContracts[EncodeRequest].Schema.Properties["model"] = ValueSchema{Type: ObjectType, Nullable: true, AllowUnknown: true}
		}},
		{"different-source", "provable read", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Program.Items[0].Fields["model"] = readExpression("input", "/other")
		}},
		{"optional-source-can-be-filled", "provable read", func(document *ProtocolPackage) {
			document.Mapping.Directions[EncodeRequest].Effects[0].When = &Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/model"))}
		}},
		{"decode-has-no-output-wire-schema", "provable read", func(document *ProtocolPackage) {
			mapping := document.Mapping.Directions[DecodeRequest]
			mapping.Program = Expression{Op: "merge_deep", Items: []Expression{mapping.Program, objectExpression(nil)}}
			document.Mapping.Directions[DecodeRequest] = mapping
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := deepMergeTestPackage(t)
			test.change(&document)
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
			if err := IssuesError(issues); compiled != nil || err == nil || !strings.Contains(err.Error(), test.kind) {
				t.Fatal("unproved deep merge emit compiled", compiled, issues)
			}
		})
	}
}

func TestDeepMergeEmissionChecksFinalSchemaAndPreservesNull(t *testing.T) {
	document := deepMergeTestPackage(t)
	document.Spec.WireContracts[EncodeRequest].Schema.Properties["model"] = ValueSchema{Type: StringType, Nullable: true}
	compiled := compilePackageForTest(t, document)
	request := &Request{SchemaVersion: ProtocolCoreVersion, Model: StringValue("m"), Content: []Node{{Kind: MessageNode, Role: StringValue("user"), Children: []Node{{Kind: TextNode, Payload: StringValue("hello")}}}}, Parameters: Object{"extension": Value{raw: `{"metadata":{"n":9007199254740993}}`}}}
	for _, model := range []Value{StringValue("m"), Value{raw: "null"}} {
		request.Model = model
		wire, err := compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
		if err != nil {
			t.Fatal("declared non-object copy rejected", err)
		}
		fields, _ := wire.ReadObject()
		if fields["model"] != model || fields["metadata"].raw != `{"n":9007199254740993}` {
			t.Fatal("deep merge changed the copied leaf", wire)
		}
		request.Parameters["extension"] = fixtureValue(t, Object{"model": model})
		_, err = compiled.EncodeRequestValue(t.Context(), request, EvaluationContext{})
		var conflict *ObjectMergeConflictError
		if !errors.As(err, &conflict) || conflict.Path != "/model" {
			t.Fatal("nullable scalar was overwritten", err)
		}
		request.Parameters["extension"] = Value{raw: `{"metadata":{"n":9007199254740993}}`}
	}
	request.Model = Value{raw: `{}`}
	_, issues := compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
	if err := IssuesError(issues); err == nil || issues[0].Path != "/model" {
		t.Fatal("the final schema did not reject object-valued emit input", issues)
	}
	request.Model = Value{}
	request.Parameters["extension"] = Value{raw: `{"model":"invented"}`}
	_, issues = compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), EvaluationContext{})
	if len(issues) != 1 || issues[0].RuleID != "model" || issues[0].Code != ConversionRejected {
		t.Fatal("another operand supplied an absent unconditional emit input", issues)
	}
}
