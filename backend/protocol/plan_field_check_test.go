package protocol

import (
	"errors"
	"testing"
)

func TestObjectFieldCheckKeepsValuesAndLocatesUnknowns(t *testing.T) {
	for _, policy := range []string{"member", "leaf"} {
		diagnostic := ObjectFieldDiagnostic{Code: UnsupportedCapability, Stage: "wire", PathPrefix: "/error", Reason: "field has no mapping"}
		expression := Expression{Op: "check_fields", Source: expressionPointer(readExpression("input", "")), Keys: []string{"message"}, Prefixes: []string{"wire:"}, Policy: policy, OnUnknown: &diagnostic}
		compiler := expressionCompiler{limits: DefaultLimits()}
		plan, err := compiler.compilePlan(expression, "/guard", expressionScope{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		input := parseTestValue(t, ` { "message":"value", "wire:test":{"count":9007199254740993,"data":null} } `)
		output, err := plan.evaluate(numericTestState(t, input))
		if err != nil || output != input {
			t.Fatal("field check changed its input storage or spelling", output, err)
		}
		input = parseTestValue(t, `{"z":"private","a/b":{"b":[],"a~":"not-a-path"}}`)
		_, err = plan.evaluate(numericTestState(t, input))
		var failure *unknownObjectFieldError
		if !errors.As(err, &failure) {
			t.Fatal("unknown member was accepted", err)
		}
		path := "/error/a~1b"
		if policy == "leaf" {
			path += "/a~0"
		}
		issue := failure.issue(Identity{}, EncodeResponse)
		if issue.Code != UnsupportedCapability || issue.Path != path || issue.Stage != "wire" || issue.Reason != diagnostic.Reason {
			t.Fatal("diagnostic was not derived from actual sorted member names", issue)
		}
	}
}

func TestObjectFieldCheckRejectsUnboundedAllowPrefix(t *testing.T) {
	diagnostic := ObjectFieldDiagnostic{Code: UnsupportedCapability, Stage: "wire", Reason: "field has no mapping"}
	compiler := expressionCompiler{limits: DefaultLimits()}
	_, err := compiler.compilePlan(Expression{Op: "check_fields", Source: expressionPointer(readExpression("input", "")), Keys: []string{}, Prefixes: []string{""}, Policy: "member", OnUnknown: &diagnostic}, "/guard", expressionScope{}, 1)
	if err == nil {
		t.Fatal("empty prefix bypassed the closed member set")
	}
}
