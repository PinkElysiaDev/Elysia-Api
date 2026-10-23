package protocol

import (
	"errors"
	"strings"
	"testing"
)

func testNativeTarget() (Native, Target) {
	identity := Identity{Family: "responses", WireVersion: "v1", DefinitionID: "source-copy", Revision: "r1"}
	native := Native{Source: Provenance{Protocol: identity, Direction: DecodeRequest, Path: "/tools/0"}}
	target := Target{Protocol: identity, Direction: EncodeRequest}
	target.Protocol.DefinitionID = "different-id"
	target.Protocol.Revision = "r2"
	return native, target
}

func TestNativePreservationAndEdits(t *testing.T) {
	native, target := testNativeTarget()
	native.Value = parseTestValue(t, `{"name":"old","tools":[1,2],"vendor":{"number":900719925474099312345,"empty":null,"flag":false},"a/b":{"~":1}}`)
	untouched, issues := PreserveNative(native, target, nil, DefaultLimits())
	if len(issues) != 0 || untouched != native.Value {
		t.Fatalf("compatible untouched value changed: %+v", issues)
	}
	mutations := []Mutation{
		{Op: SetValue, Path: "/name", Value: StringValue("new")},
		{Op: DeleteValue, Path: "/tools"},
		{Op: ReplaceValue, Path: "/a~1b/~0", Value: parseTestValue(t, `false`)},
		{Op: SetValue, Path: "/new", Value: parseTestValue(t, `null`)},
	}
	modified, issues := PreserveNative(native, target, mutations, DefaultLimits())
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	object, err := modified.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	if object["name"] != StringValue("new") || !object["new"].IsNull() {
		t.Fatalf("edits lost: %s", modified.Bytes())
	}
	if _, hasTools := object["tools"]; hasTools {
		t.Fatal("deleted tools restored")
	}
	if !strings.Contains(string(modified.Bytes()), `900719925474099312345`) {
		t.Fatal("extension precision lost")
	}
	if !strings.Contains(string(native.Value.Bytes()), `"name":"old"`) {
		t.Fatal("source mutated")
	}
	array, err := ApplyMutations(parseTestValue(t, `["a","b","c"]`), []Mutation{{Op: DeleteValue, Path: "/1"}}, DefaultLimits())
	if err != nil || string(array.Bytes()) != `["a","c"]` {
		t.Fatalf("array order: %s %v", array.Bytes(), err)
	}
}

func TestNativeIdentityAndResourceIsolation(t *testing.T) {
	native, target := testNativeTarget()
	native.Value = parseTestValue(t, `{"type":"custom","name":"patch"}`)
	for _, mismatch := range []string{"family", "version", "direction", "account"} {
		t.Run(mismatch, func(t *testing.T) {
			source, destination := native, target
			switch mismatch {
			case "family":
				destination.Protocol.Family = "anthropic"
			case "version":
				destination.Protocol.WireVersion = "v2"
			case "direction":
				destination.Direction = EncodeResponse
			case "account":
				source.Source.Scope.Account = "original-account"
			}
			value, issues := PreserveNative(source, destination, nil, DefaultLimits())
			if !value.IsZero() || len(issues) != 1 || issues[0].Path != "/tools/0" {
				t.Fatalf("foreign content leaked: %s %+v", value.Bytes(), issues)
			}
			var failure *ConversionError
			if !errors.As(IssuesError(issues), &failure) {
				t.Fatal("structured diagnostic missing")
			}
		})
	}
}

func TestMutationTransactionAndLimits(t *testing.T) {
	original := parseTestValue(t, `{"name":"old","items":[0]}`)
	for _, mutation := range []Mutation{
		{Op: DeleteValue, Path: "/missing"}, {Op: ReplaceValue, Path: "/missing", Value: StringValue("x")},
		{Op: SetValue, Path: "/parent/missing", Value: StringValue("x")}, {Op: SetValue, Path: "/items/01", Value: StringValue("x")},
		{Op: SetValue, Path: "/bad~2", Value: StringValue("x")}, {Op: SetValue, Path: "/name"},
		{Op: DeleteValue, Path: "/name", Value: StringValue("x")}, {Op: DeleteValue},
	} {
		value, err := ApplyMutations(original, []Mutation{{Op: SetValue, Path: "/name", Value: StringValue("new")}, mutation}, DefaultLimits())
		if err == nil || !value.IsZero() || !strings.Contains(string(original.Bytes()), "old") {
			t.Fatalf("non-atomic invalid mutation: %+v %v", mutation, err)
		}
	}
	limits := DefaultLimits()
	limits.Depth = 2
	if _, err := ApplyMutations(parseTestValue(t, `{"a":{"b":{"c":1}}}`), nil, limits); err == nil {
		t.Fatal("deep native JSON accepted")
	}
	limits = DefaultLimits()
	limits.Nodes = 3
	if _, err := ApplyMutations(original, nil, limits); err == nil {
		t.Fatal("node limit ignored")
	}
	limits = DefaultLimits()
	limits.BufferBytes = 4
	if _, err := ApplyMutations(original, nil, limits); err == nil {
		t.Fatal("buffer limit ignored")
	}
}

func TestNativeValueComparisonMatchesDecodedValues(t *testing.T) {
	payload := strings.Repeat("A", 128<<10)
	for _, pair := range [][2]string{
		{`null`, ` null `}, {`null`, `false`}, {`[]`, `{}`}, {`true`, `false`},
		{`"a"`, `"\u0061"`}, {`"\ud800"`, `"�"`}, {"\"\xff\"", `"�"`},
		{`1`, `1.0`}, {`-0`, `0e0`}, {`9007199254740993`, `9007199254740993.0`},
		{`{"a":1,"a":2}`, `{"a":2}`}, {`{"a":1,"\u0061":2}`, `{"a":2}`},
		{`{"a":1e9999,"a":1}`, `{"a":1}`}, {`{"a":null}`, `{}`},
		{`{"a":[1,2],"b":false}`, `{"b":false,"a":[1.0,2e0]}`},
		{`[1,2]`, `[2,1]`},
		{`{"payload":"` + payload + `","n":1}`, `{"n":1.0,"payload":"` + payload + `"}`},
		{`{"payload":"` + payload + `","n":1}`, `{"n":2,"payload":"` + payload + `"}`},
	} {
		left, right := parseTestValue(t, pair[0]), parseTestValue(t, pair[1])
		if got, want := equalNativeValues(left, right), equalValues(left, right); got != want {
			t.Fatalf("native comparison differs for %.100q / %.100q: got %t, want %t", pair[0], pair[1], got, want)
		}
	}
	if !equalNativeValues(Value{}, Value{}) || equalNativeValues(Value{}, parseTestValue(t, `null`)) {
		t.Fatal("absent and null comparison changed")
	}
}

func TestNativeValueComparisonRetainsNestedNumericBounds(t *testing.T) {
	for _, number := range []string{strings.Repeat("9", maxNumericConversionDigits), strings.Repeat("9", maxNumericConversionDigits+1), `1e4096`, `1e4097`, `1e-4097`} {
		identical := parseTestValue(t, `{"nested":[`+number+`]}`)
		if !equalNativeValues(identical, identical) {
			t.Fatal("identical root no longer preserves its native spelling")
		}
		left := parseTestValue(t, `{"nested":[`+number+`],"n":0}`)
		right := parseTestValue(t, `{"n":0.0,"nested":[`+number+`]}`)
		if got, want := equalNativeValues(left, right), equalValues(left, right); got != want {
			t.Fatalf("nested number bound differs for %.20q: got %t, want %t", number, got, want)
		}
	}
}

func FuzzNativeValueComparison(f *testing.F) {
	for _, pair := range [][2]string{
		{`{"a":1,"a":2}`, `{"a":2.0}`},
		{`{"a":[1e4097],"b":0}`, `{"b":0.0,"a":[1e4097]}`},
		{`["\ud800",null]`, `["�",null]`},
		{"{\"\xff\":1}", `{"�":1}`},
	} {
		f.Add(pair[0], pair[1])
	}
	f.Fuzz(func(t *testing.T, before, after string) {
		if len(before) > fuzzInputLimit || len(after) > fuzzInputLimit {
			return
		}
		left, leftErr := ParseValue([]byte(before))
		right, rightErr := ParseValue([]byte(after))
		if leftErr != nil || rightErr != nil || checkValueLimits(left, DefaultLimits()) != nil || checkValueLimits(right, DefaultLimits()) != nil {
			return
		}
		if got, want := equalNativeValues(left, right), equalValues(left, right); got != want {
			t.Fatalf("native comparison differs: %q / %q: %t != %t", before, after, got, want)
		}
	})
}
