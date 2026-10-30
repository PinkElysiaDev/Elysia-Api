package protocol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func loadTaskPackage(t *testing.T, name string) (*Compiler, ProtocolPackage, *Compiled) {
	t.Helper()
	read := func(part string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "tasks", name, part+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	compiler, err := NewCompiler(DefaultLimits(), nil, []string{"tasks.async"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.CompileSources(read("spec"), read("mapping"))
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	document, _ := compiled.Package()
	return compiler, document, compiled
}

func TestTaskPackagesPreserveExistingFlows(t *testing.T) {
	var packages []*Compiled
	for _, name := range []string{"job-alpha", "job-beta"} {
		t.Run(name, func(t *testing.T) {
			_, document, compiled := loadTaskPackage(t, name)
			_, _, legacy := loadJobDefinition(t, name)
			for _, sample := range document.Mapping.TaskSamples {
				want, wantErr := executeTaskSample(t.Context(), legacy, sample)
				got, gotErr := executeTaskSample(t.Context(), compiled, sample)
				if wantErr != nil || gotErr != nil || !equalValues(want, got) {
					t.Fatal("task mapping changed the existing fixture", sample.ID, wantErr, gotErr, want.JSONString(), got.JSONString())
				}
			}
			if report := Verify(t.Context(), compiled); !report.Passed {
				t.Fatal("task evidence was not verified", report.Issues)
			}
			operation, _ := compiled.Operation("submit")
			if operation.Task.Decode.Transform != nil || operation.Task.Encode.Transform != nil || operation.Task.Control.Transform != nil {
				t.Fatal("wire task flow contains executable legacy mappings")
			}
			packages = append(packages, compiled)
		})
	}
	if len(packages) == 2 {
		if report := VerifyCombination(t.Context(), packages[0], packages[1]); !report.Passed {
			t.Fatal("task pair lost receipt or result conversion", report.Issues)
		}
	}
}

func TestTaskPackageRequiresFlowAndEvidence(t *testing.T) {
	compiler, original, _ := loadTaskPackage(t, "job-alpha")
	for _, fixture := range []struct {
		name   string
		change func(*ProtocolPackage)
	}{
		{"missing-program", func(p *ProtocolPackage) { p.Mapping.Operations["submit"].Task.Decode = Expression{} }},
		{"missing-link", func(p *ProtocolPackage) { p.Spec.Operations["submit"].Task.Status = "missing" }},
		{"credential-header", func(p *ProtocolPackage) { p.Spec.Operations["submit"].Task.IdempotencyHeader = "Authorization" }},
		{"unlinked-control", func(p *ProtocolPackage) {
			p.Spec.Operations["orphan"] = p.Spec.Operations["status"]
			p.Mapping.Operations["orphan"] = ProtocolOperationMapping{}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var document ProtocolPackage
			if err := fixtureValue(t, original).Decode(&document); err != nil {
				t.Fatal(err)
			}
			fixture.change(&document)
			if _, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); len(issues) == 0 {
				t.Fatal("incomplete task declaration compiled")
			}
		})
	}
	original.Mapping.TaskSamples = nil
	compiled, issues := compiler.CompilePackage(fixtureValue(t, original).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if Verify(t.Context(), compiled).Passed {
		t.Fatal("task flow passed without state/receipt evidence")
	}
}

func TestTaskPackageChecksActualWireAndCancellation(t *testing.T) {
	compiler, document, _ := loadTaskPackage(t, "job-alpha")
	contract := document.Spec.Documents["task-status"]
	contract.Assertions = []WireAssertion{{Path: "/valid", Schema: ValueSchema{Type: BooleanType, Enum: []Value{Value{raw: "true"}}}, Code: UpstreamContractViolation, Reason: "invalid task reply"}}
	document.Spec.Documents["task-status"] = contract
	compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	_, err := compiled.DecodeTaskUpdate(t.Context(), "submit", "status", parseTestValue(t, `{"valid":false}`))
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Path != "/valid" {
		t.Fatal("task mapping hid invalid wire", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.ConvertTask(ctx, "submit", "control", "status", parseTestValue(t, `{"id":"local"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("task mapping ignored cancellation", err)
	}
}
