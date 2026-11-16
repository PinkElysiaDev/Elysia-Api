package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func BenchmarkFinalWireValidation(b *testing.B) {
	compiled := testCompiled(b, Chat)
	parse := func(delta, finish string) p.Value {
		value, err := p.ParseValue([]byte(chatContractFrame("r", "m", 1, delta, finish)))
		if err != nil {
			b.Fatal(err)
		}
		return value
	}
	start := parse(`{"role":"assistant"}`, `null`)
	delta := parse(`{"content":"hello"}`, `null`)
	end := parse(`{}`, `"stop"`)
	b.ReportAllocs()
	for b.Loop() {
		validation, err := compiled.NewWireStreamValidation(p.Scope{})
		if err != nil {
			b.Fatal(err)
		}
		if err := validation.Consume(b.Context(), start); err != nil {
			b.Fatal(err)
		}
		for range 256 {
			if err := validation.Consume(b.Context(), delta); err != nil {
				b.Fatal(err)
			}
		}
		if err := validation.Consume(b.Context(), end); err != nil {
			b.Fatal(err)
		}
		if err := validation.Finish(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestResponsesNullableHistoryRetainsAuthoredBoundaries(t *testing.T) {
	const body = `{"model":"m","input":[` + nullableReasoningItem + `]}`
	for _, mode := range []string{"custom", "reject-input", "replace-output"} {
		t.Run(mode, func(t *testing.T) {
			document, _ := testCompiled(t, Responses).Package()
			document.Spec.ID = "nullable-history-" + mode
			if mode == "reject-input" {
				name := document.Mapping.Directions[p.DecodeRequest].Document
				contract := document.Spec.Documents[name]
				contract.Assertions = append(contract.Assertions, p.WireAssertion{Path: "/input", Schema: &p.ValueSchema{Type: p.ArrayType, Items: &p.ValueSchema{Type: p.ObjectType, AllowUnknown: true, Properties: map[string]p.ValueSchema{"content": {Type: p.ArrayType}}}}, Code: p.InvalidInput, Reason: "custom history requires array content"})
				document.Spec.Documents[name] = contract
			}
			compiled, issues := compilePackageDocument(t, document)
			if err := p.IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}})
			request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
			if mode == "reject-input" {
				if err == nil {
					t.Fatal("nullable history bypassed the authored array-only source contract")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.ValidateWireOutput(p.EncodeRequest, wire); err != nil {
				t.Fatal("valid nullable history failed the final contract", err)
			}
			if mode == "replace-output" {
				wire, err = p.ApplyMutations(wire, []p.Mutation{{Op: p.ReplaceValue, Path: "/input/0/content", Value: testValue(t, `{}`)}}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				if err := compiled.ValidateWireOutput(p.EncodeRequest, wire); err == nil {
					t.Fatal("nullable compatibility hid an invalid authored final object")
				}
				return
			}
			sameJSON(t, wire.Bytes(), body)
		})
	}
}
