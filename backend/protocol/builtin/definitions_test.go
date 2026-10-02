package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestShippedDefinitionsVerify(t *testing.T) {
	compiler, err := p.NewCompiler(p.DefaultLimits(), Modules(), nil)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := Definitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != len(modules) {
		t.Fatal("missing built-in definition")
	}
	for _, definition := range definitions {
		compiled, issues := compiler.Compile(definition.Bytes())
		if compiled == nil {
			t.Fatal(issues)
		}
		t.Run(compiled.Identity().DefinitionID, func(t *testing.T) {
			report := p.Verify(t.Context(), compiled)
			if !report.Passed {
				for _, issue := range report.Issues {
					t.Error(issue.Path, issue.Reason, issue.Evidence)
				}
			}
		})
	}
}
