package protocol

import (
	"strings"
	"testing"
)

func compilePackageForTest(t *testing.T, document ProtocolPackage) *Compiled {
	t.Helper()
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompilePackage(value.Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestCompilePackageExecutesIndependentContracts(t *testing.T) {
	document := textProtocolPackage(t)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		contract := document.Spec.WireContracts[direction]
		contract.Assertions = []WireAssertion{{Path: "/prompt", Schema: ValueSchema{Type: StringType, Enum: []Value{StringValue("hello <world>")}}, Code: ConversionRejected, Reason: "synthetic prompt contract"}}
		document.Spec.WireContracts[direction] = contract
	}
	compiled := compilePackageForTest(t, document)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse} {
		if compiled.Codec(direction) != "" || compiled.mappings[direction].outputPlan == nil {
			t.Fatal("package did not compile a declarative output plan", direction)
		}
	}
	for _, tc := range []struct {
		decode, encode Direction
		wire           string
	}{
		{DecodeRequest, EncodeRequest, `{"model":"m","prompt":"hello <world>"}`},
		{DecodeResponse, EncodeResponse, `{"reply":"answer"}`},
	} {
		input, _ := ParseValue([]byte(tc.wire))
		core, issues := compiled.Execute(t.Context(), tc.decode, input, EvaluationContext{})
		if err := IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		wire, issues := compiled.Execute(t.Context(), tc.encode, core, EvaluationContext{})
		if err := IssuesError(issues); err != nil || !equalValues(input, wire) {
			t.Fatal("package round trip changed text", err, wire.JSONString())
		}
		if tc.decode == DecodeRequest {
			badWire, _ := ParseValue([]byte(`{"model":"m","prompt":"private-prompt"}`))
			_, issues := compiled.Execute(t.Context(), DecodeRequest, badWire, EvaluationContext{})
			if len(issues) != 1 || issues[0].Path != "/prompt" || issues[0].Reason != "synthetic prompt contract" {
				t.Fatal("decode bypassed its independent wire assertion", issues)
			}
			badCore, err := ApplyMutations(core, []Mutation{{Op: ReplaceValue, Path: "/content/0/children/0/payload", Value: StringValue("private-prompt")}}, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			_, issues = compiled.Execute(t.Context(), EncodeRequest, badCore, EvaluationContext{})
			if len(issues) != 1 || issues[0].Path != "/prompt" || issues[0].Reason != "synthetic prompt contract" {
				t.Fatal("encode bypassed its independent wire assertion", issues)
			}
			legacy, err := ApplyMutations(core, []Mutation{{Op: ReplaceValue, Path: "/schemaVersion", Value: literalExpression(1).Value}}, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			_, issues = compiled.Execute(t.Context(), EncodeRequest, legacy, EvaluationContext{})
			if len(issues) != 1 || issues[0].Path != "/schemaVersion" {
				t.Fatal("legacy core was accepted as core2", issues)
			}
		}
	}
}

func TestCompilePackageRejectsUnprovedEffects(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*ProtocolPackage)
	}{
		{"changed-value", "provable read", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeRequest].Program.Fields["model"] = literalExpression("changed")
		}},
		{"different-branches", "provable read", func(p *ProtocolPackage) {
			condition := Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("other")}}
			p.Mapping.Directions[DecodeRequest].Program.Fields["model"] = Expression{Op: "if", When: &condition, Then: expressionPointer(readExpression("input", "/model")), Otherwise: expressionPointer(readExpression("input", "/prompt"))}
		}},
		{"array-index-shift", "provable read", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[DecodeRequest]
			content := entry.Program.Fields["content"]
			content.Items = append([]Expression{{Op: "omit"}}, content.Items...)
			entry.Program.Fields["content"] = content
			entry.Effects[1].Path = "/content/1/children/0/payload"
			p.Mapping.Directions[DecodeRequest] = entry
		}},
		{"aggregate", "layout", func(p *ProtocolPackage) { p.Mapping.Directions[EncodeRequest].Effects[1].Kind = "aggregate" }},
		{"default", "not executable", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeRequest].Effects[1].Kind = "default"
			p.Mapping.Directions[EncodeRequest].Effects[1].Reason = "synthetic default"
		}},
		{"drop", "not executable", func(p *ProtocolPackage) {
			p.Mapping.Directions[EncodeRequest].Effects[1].Kind = "drop"
			p.Mapping.Directions[EncodeRequest].Effects[1].Reason = "synthetic drop"
		}},
		{"event-runtime", "core event runtime", func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeEvent] = p.Mapping.Directions[DecodeRequest]
			p.Spec.WireContracts[DecodeEvent] = p.Spec.WireContracts[DecodeRequest]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			tc.change(&document)
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			value, _ := EncodeValue(document)
			compiled, issues := compiler.CompilePackage(value.Bytes())
			if err := IssuesError(issues); compiled != nil || err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatal("unsupported effect compiled", compiled, issues)
			}
		})
	}
}

func TestPackageEffectsObserveInputPresence(t *testing.T) {
	document := textProtocolPackage(t)
	condition := Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("blocked")}}
	entry := document.Mapping.Directions[DecodeRequest]
	entry.Effects = append([]ProtocolSemanticEffect{{ID: "model-guard", Kind: "reject", Path: "/model", Target: "/model", When: &condition, Reason: "synthetic model rejection"}}, entry.Effects...)
	document.Mapping.Directions[DecodeRequest] = entry
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		contract := document.Spec.WireContracts[direction]
		contract.Schema.Required = []string{"model"}
		contract.Schema.Properties["prompt"] = ValueSchema{Type: StringType, Nullable: true}
		document.Spec.WireContracts[direction] = contract
	}
	compiled := compilePackageForTest(t, document)
	for _, tc := range []struct{ input, rule string }{
		{`{"model":"blocked","prompt":"private-value"}`, "model-guard"},
		{`{"model":"m"}`, "prompt"},
	} {
		input, _ := ParseValue([]byte(tc.input))
		_, issues := compiled.Execute(t.Context(), DecodeRequest, input, EvaluationContext{})
		if len(issues) != 1 || issues[0].Code != ConversionRejected || issues[0].RuleID != tc.rule || strings.Contains(issues[0].Reason, "private-value") {
			t.Fatal("effect was ignored or exposed private data", issues)
		}
	}
	input, _ := ParseValue([]byte(`{"model":"m","prompt":null}`))
	core, issues := compiled.Execute(t.Context(), DecodeRequest, input, EvaluationContext{})
	if err := IssuesError(issues); err != nil {
		t.Fatal("explicit null became absent", err)
	}
	output, issues := compiled.Execute(t.Context(), EncodeRequest, core, EvaluationContext{})
	if err := IssuesError(issues); err != nil || !equalValues(input, output) {
		t.Fatal("explicit null did not round trip", err, output.JSONString())
	}
}

func TestPackageIdentityIncludesReferencesAndWireContracts(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Expressions = map[string]Expression{"model": readExpression("input", "/model")}
	document.Mapping.Directions[EncodeRequest].Program.Fields["model"] = Expression{Op: "ref", Ref: "model"}
	before := compilePackageForTest(t, document)
	reference := document.Mapping.Expressions["model"]
	reference.Required = true
	document.Mapping.Expressions["model"] = reference
	after := compilePackageForTest(t, document)
	if before.Hash() == after.Hash() || before.mappings[EncodeRequest].definitionHash == after.mappings[EncodeRequest].definitionHash {
		t.Fatal("referenced program changes did not invalidate identity")
	}
	contract := document.Spec.WireContracts[EncodeRequest]
	contract.Assertions = []WireAssertion{{Path: "/model", Schema: ValueSchema{Type: StringType}, Code: InvalidInput, Reason: "synthetic wire assertion"}}
	document.Spec.WireContracts[EncodeRequest] = contract
	withAssertion := compilePackageForTest(t, document)
	if after.Hash() == withAssertion.Hash() || after.mappings[EncodeRequest].definitionHash == withAssertion.mappings[EncodeRequest].definitionHash {
		t.Fatal("wire contract changes did not invalidate identity")
	}
	copy, ok := before.Package()
	if !ok {
		t.Fatal("missing package document")
	}
	delete(copy.Mapping.Directions, EncodeRequest)
	copy, ok = before.Package()
	if !ok || len(copy.Mapping.Directions) != 4 {
		t.Fatal("caller mutated the compiled package")
	}
}
