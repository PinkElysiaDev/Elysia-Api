package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// BenchmarkVerifyPreset measures offline evidence, not source I/O or compilation.
func BenchmarkVerifyPreset(b *testing.B) {
	values, err := Packages()
	if err != nil {
		b.Fatal(err)
	}
	compiler, err := p.NewCompiler(p.DefaultLimits(), nil)
	if err != nil {
		b.Fatal(err)
	}
	for _, value := range values {
		compiled, issues := compiler.Compile(value.Bytes())
		if err := p.IssuesError(issues); err != nil {
			b.Fatal(err)
		}
		b.Run(compiled.Identity().DefinitionID, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if report := p.Verify(b.Context(), compiled); !report.Passed {
					b.Fatal(report.Issues)
				}
			}
		})
	}
}
