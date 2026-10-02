package protocol

import (
	"os"
	"testing"
)

func loadJobDefinition(t *testing.T, name string) (*Compiler, Definition, *Compiled) {
	t.Helper()
	compiler, err := NewCompiler(DefaultLimits(), nil, []string{"tasks.async"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(body)
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	return compiler, compiled.Definition(), compiled
}

func TestJobMappingsIndependentFieldsAndVerification(t *testing.T) {
	_, _, alpha := loadJobDefinition(t, "job-alpha")
	_, _, beta := loadJobDefinition(t, "job-beta")
	for _, compiled := range []*Compiled{alpha, beta} {
		report := Verify(t.Context(), compiled)
		if !report.Passed {
			t.Fatalf("%s: %+v", compiled.Identity().DefinitionID, report.Issues)
		}
	}
	report := VerifyCombination(t.Context(), alpha, beta)
	if !report.Passed {
		t.Fatalf("pair: %+v", report.Issues)
	}
	var wire = StringValue("invalid")
	if _, err := alpha.DecodeTaskUpdate(t.Context(), "submit", "status", wire); err == nil {
		t.Fatal("invalid provider shape accepted")
	}
}

func TestJobVerificationRejectsMissingStateAndLostIdentity(t *testing.T) {
	compiler, definition, _ := loadJobDefinition(t, "job-alpha")
	for _, mode := range []string{"coverage", "identity"} {
		t.Run(mode, func(t *testing.T) {
			copy := definition
			if mode == "coverage" {
				copy.TaskSamples = nil
			} else {
				operation := copy.Operations["submit"]
				operation.Task.Encode.Transform.Fields["ticket"] = Expression{Op: "literal", Value: StringValue("wrong")}
			}
			raw, err := EncodeValue(copy)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(raw.Bytes())
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			if Verify(t.Context(), compiled).Passed {
				t.Fatal("invalid flow passed activation verification")
			}
		})
	}
}
