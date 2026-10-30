package protocol

import "testing"

func historyOwnerTestProgram(t *testing.T) (Expression, compiledSemanticEffect) {
	t.Helper()
	document := historyPrototype(t, "anthropic")
	compiled := compilePackageForTest(t, document)
	branch := document.Mapping.Directions[DecodeRequest].Program
	program := objectExpression(map[string]Expression{
		"reasoning": {Op: "if", When: branch.When, Then: expressionPointer(branch.Then.Fields["reasoning"])},
		"parameters": {Op: "if", When: branch.When, Then: expressionPointer(branch.Then.Fields["parameters"]),
			Otherwise: expressionPointer(branch.Otherwise.Fields["parameters"])},
	})
	for _, effect := range compiled.packagePlan.effects[DecodeRequest] {
		if effect.valueMapping != nil {
			return program, effect
		}
	}
	t.Fatal("prototype has no history owner")
	return Expression{}, compiledSemanticEffect{}
}

func TestHistoryLocalOwnerProof(t *testing.T) {
	for _, test := range []struct {
		name     string
		isProven bool
		change   func(Expression) Expression
	}{
		{"fixed owners", true, func(program Expression) Expression { return program }},
		{"retained projection", true, func(program Expression) Expression {
			return Expression{Op: "project", Source: &program, Policy: "include", Keys: []string{"reasoning", "parameters"}}
		}},
		{"empty parent omission", true, func(program Expression) Expression {
			return historyOwnerOmitEmpty("owner", program)
		}},
		{"lexical source scope", true, func(program Expression) Expression {
			return Expression{Op: "let", Name: "owner", Source: &program, Body: expressionPointer(Expression{
				Op: "let", Name: "alias", Source: expressionPointer(bindingTestRead("owner", "")), Body: expressionPointer(Expression{
					Op: "let", Name: "owner", Source: expressionPointer(objectExpression(nil)), Body: expressionPointer(bindingTestRead("alias", "")),
				}),
			})}
		}},
		{"unique merge owner", true, func(program Expression) Expression {
			return Expression{Op: "merge", Policy: "reject", Items: []Expression{objectExpression(map[string]Expression{"unrelated": literalExpression(true)}), program}}
		}},
		{"unique deep merge owner", true, func(program Expression) Expression {
			return Expression{Op: "merge_deep", Items: []Expression{objectExpression(map[string]Expression{"unrelated": literalExpression(true)}), program}}
		}},
		{"deep merge ancestor writer", false, func(program Expression) Expression {
			other := objectExpression(map[string]Expression{"reasoning": objectExpression(map[string]Expression{"summary": literalExpression("brief")})})
			return Expression{Op: "merge_deep", Items: []Expression{program, other}}
		}},
		{"deep merge conditional writer", false, func(program Expression) Expression {
			other := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/other"))}),
				Then: expressionPointer(objectExpression(nil)), Otherwise: expressionPointer(objectExpression(map[string]Expression{"reasoning": objectExpression(map[string]Expression{"summary": literalExpression("brief")})}))}
			return Expression{Op: "merge_deep", Items: []Expression{program, other}}
		}},
		{"partial comparison", false, func(program Expression) Expression {
			program.Fields["reasoning"].When.Items[0].Items[0] = readExpression("input", "/context_management/edits/0/keep")
			return program
		}},
		{"missing variant", false, func(program Expression) Expression {
			program.Fields["reasoning"].When.Items = program.Fields["reasoning"].When.Items[:1]
			return program
		}},
		{"duplicate variant", false, func(program Expression) Expression {
			program.Fields["reasoning"].When.Items[1] = program.Fields["reasoning"].When.Items[0]
			return program
		}},
		{"filtered unmatched value", false, func(program Expression) Expression {
			program.Fields["parameters"].Otherwise.Fields["wire:claude"] = objectExpression(map[string]Expression{
				"context_management": {Op: "project", Policy: "exclude", Keys: []string{"future"}, Source: expressionPointer(readExpression("input", "/context_management"))},
			})
			return program
		}},
		{"wrong unmatched source", false, func(program Expression) Expression {
			program.Fields["parameters"].Otherwise.Fields["wire:claude"] = objectExpression(map[string]Expression{
				"context_management": readExpression("input", "/other"),
			})
			return program
		}},
		{"removed owner", false, func(program Expression) Expression {
			return Expression{Op: "project", Source: &program, Policy: "exclude", Keys: []string{"reasoning"}}
		}},
		{"duplicate writer", false, func(program Expression) Expression {
			return Expression{Op: "merge", Policy: "reject", Items: []Expression{program, program}}
		}},
		{"ancestor collision", false, func(program Expression) Expression {
			other := objectExpression(map[string]Expression{"reasoning": objectExpression(map[string]Expression{"summary": literalExpression("brief")})})
			return Expression{Op: "merge", Policy: "reject", Items: []Expression{program, other}}
		}},
		{"overwrite merge", false, func(program Expression) Expression {
			return Expression{Op: "merge", Policy: "last", Items: []Expression{objectExpression(nil), program}}
		}},
		{"unknown branch loses owner", false, func(program Expression) Expression {
			owner := program.Fields["reasoning"]
			program.Fields["reasoning"] = Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/other"))}), Then: &owner}
			return program
		}},
		{"shadowed owner", false, func(program Expression) Expression {
			return Expression{Op: "let", Name: "owner", Source: &program, Body: expressionPointer(Expression{
				Op: "let", Name: "owner", Source: expressionPointer(objectExpression(nil)), Body: expressionPointer(bindingTestRead("owner", "")),
			})}
		}},
		{"different parent binding", false, func(program Expression) Expression {
			wrapped := historyOwnerOmitEmpty("owner", program)
			wrapped.Body.Otherwise = expressionPointer(bindingTestRead("other", ""))
			return Expression{Op: "let", Name: "other", Source: expressionPointer(program), Body: &wrapped}
		}},
		{"empty wrapper at leaf", false, func(program Expression) Expression {
			program.Fields["reasoning"].Then.Fields["historyRetention"] = historyOwnerOmitEmpty("value", literalExpression("all"))
			return program
		}},
		{"iteration owner", false, func(program Expression) Expression {
			program.Fields["reasoning"] = arrayMap(readExpression("input", "/items"), program.Fields["reasoning"])
			return program
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			program, effect := historyOwnerTestProgram(t)
			compiled := compileTestOutputExpression(t, test.change(program))
			if isProven := provesHistoryValueMapping(compiled, DecodeRequest, effect, DefaultLimits().Nodes); isProven != test.isProven {
				t.Fatalf("proven = %v; want %v", isProven, test.isProven)
			}
		})
	}
}

func historyOwnerOmitEmpty(name string, source Expression) Expression {
	read := bindingTestRead(name, "")
	body := Expression{Op: "if", When: expressionPointer(Expression{Op: "equal", Items: []Expression{read, objectExpression(nil)}}),
		Then: expressionPointer(Expression{Op: "omit"}), Otherwise: &read}
	return Expression{Op: "let", Name: name, Source: &source, Body: &body}
}

func TestHistoryLocalOwnerReconstruction(t *testing.T) {
	_, effect := historyOwnerTestProgram(t)
	condition := Expression{Op: "exists", Source: expressionPointer(readExpression("input", historyRetentionPath))}
	effect.when = &compiledOutputPlan{expression: compileTestOutputExpression(t, condition)}
	for _, test := range []struct {
		name     string
		mapped   Value
		fallback Expression
		isProven bool
	}{
		{"canonical variant", effect.valueMapping.Variants[0], readExpression("input", effect.valueMapping.Unmatched), true},
		{"other declared variant", effect.valueMapping.Variants[1], readExpression("input", effect.valueMapping.Unmatched), true},
		{"undeclared variant", StringValue("all"), readExpression("input", effect.valueMapping.Unmatched), false},
		{"wrong fallback", effect.valueMapping.Variants[0], readExpression("input", "/other"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := objectExpression(map[string]Expression{"context_management": {
				Op: "if", When: &condition, Then: &Expression{Op: "literal", Value: test.mapped}, Otherwise: &test.fallback,
			}})
			compiled := compileTestOutputExpression(t, program)
			if isProven := provesHistoryValueMapping(compiled, EncodeRequest, effect, DefaultLimits().Nodes); isProven != test.isProven {
				t.Fatalf("proven = %v; want %v", isProven, test.isProven)
			}
		})
	}
}

func TestHistoryOwnerProofBoundsAliasExpansion(t *testing.T) {
	program, effect := historyOwnerTestProgram(t)
	for range 16 {
		source := program
		read := bindingTestRead("owner", "")
		branch := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/other"))}), Then: &read, Otherwise: &read}
		program = Expression{Op: "let", Name: "owner", Source: &source, Body: &branch}
	}
	compiled := compileTestOutputExpression(t, program)
	proof := historyOwnerProof{direction: DecodeRequest, effect: effect, steps: 100}
	if proof.field(compiled, nil, effect.corePointer, historyOwnerGoal{value: effect.valueMapping.Value}, true, true) || proof.steps != 0 {
		t.Fatal("aliased branches exceeded the proof budget", proof.steps)
	}
}
