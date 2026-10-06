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
