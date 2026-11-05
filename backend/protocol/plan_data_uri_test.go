package protocol

import (
	"errors"
	"testing"
)

func TestSplitDataURIPlanBoundary(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	expression := Expression{Op: "split_data_uri", Source: expressionPointer(readExpression("input", ""))}
	plan, err := compiler.compilePlan(expression, "/media", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := StringValue("data:x;base64,y")
	state := numericTestState(t, input)
	reads := 0
	state.inputReader = func([]string) (JSONOutput, error) {
		reads++
		return JSONLeaf(input), nil
	}
	got, err := plan.evaluate(state)
	if err != nil || reads != 1 || got.raw != `{"data":"y","kind":"base64","mime":"x"}` {
		t.Fatal("compiled split must read once and preserve its result", got, reads, err)
	}
	state = numericTestState(t, input)
	state.budget.limits.BufferBytes = got.Len() - 1
	_, err = plan.evaluate(state)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Domain != "evaluation" || limit.Metric != "bytes" || limit.Observed != got.Len() {
		t.Fatal("planned split bypassed the output budget", err)
	}
	expression.Source = expressionPointer(literalExpression(1))
	if _, err := compiler.compilePlan(expression, "/media", expressionScope{}, 1); err == nil {
		t.Fatal("a statically non-string source compiled")
	}
}

func TestSplitDataURI(t *testing.T) {
	for _, fixture := range []struct {
		name, raw, want string
	}{
		{"url", `"https://example.invalid/image"`, `{"kind":"other"}`},
		{"prefix-case", `"Data:text/plain;base64,YQ=="`, `{"kind":"other"}`},
		{"missing-comma", `"data:text/plain;base64"`, `{"kind":"data"}`},
		{"plain-data", `"data:text/plain,hello"`, `{"kind":"data"}`},
		{"suffix-case", `"data:text/plain;BASE64,YQ=="`, `{"kind":"data"}`},
		{"suffix-position", `"data:text/plain;base64;charset=utf-8,YQ=="`, `{"kind":"data"}`},
		{"base64", `"data:image/png;base64,YQ=="`, `{"data":"YQ==","kind":"base64","mime":"image/png"}`},
		{"metadata-parameters", `"data:text/plain;charset=utf-8;base64;base64,YQ==,tail"`, `{"data":"YQ==,tail","kind":"base64","mime":"text/plain;charset=utf-8;base64"}`},
		{"empty-fields", `"data:;base64,"`, `{"data":"","kind":"base64","mime":""}`},
		{"opaque-payload", `"data:text/plain;base64,%2B?not-base64"`, `{"data":"%2B?not-base64","kind":"base64","mime":"text/plain"}`},
		{"json-escaping", `"data:\u0074ext/plain;base64,YQ\u003d\u003d"`, `{"data":"YQ==","kind":"base64","mime":"text/plain"}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			output, err := splitDataURI(JSONLeaf(Value{raw: fixture.raw}))
			if err != nil {
				t.Fatal(err)
			}
			value, err := output.Encode(t.Context())
			if err != nil || value.JSONString() != fixture.want {
				t.Fatalf("got %s / %v, want %s", value.JSONString(), err, fixture.want)
			}
		})
	}
}

func TestSplitDataURIRejectsNonStrings(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		input JSONOutput
	}{
		{"missing", JSONOutput{}},
		{"null", JSONLeaf(Value{raw: "null"})},
		{"number", JSONLeaf(Value{raw: "42"})},
		{"boolean", JSONLeaf(Value{raw: "true"})},
		{"raw-object", JSONLeaf(Value{raw: "{}"})},
		{"raw-array", JSONLeaf(Value{raw: "[]"})},
		{"planned-object", JSONObject(map[string]JSONOutput{})},
		{"planned-array", JSONArray([]JSONOutput{})},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			output, err := splitDataURI(fixture.input)
			if err == nil || err.Error() != "expected a string" || !output.IsZero() {
				t.Fatalf("non-string input produced %v / %v", output, err)
			}
		})
	}
}
