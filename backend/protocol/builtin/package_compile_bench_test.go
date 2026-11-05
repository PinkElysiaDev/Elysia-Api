package builtin

import (
	"os"
	"path/filepath"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// BenchmarkPackageCompile includes both independent documents and their complete
// executable contract. File I/O and compiler setup stay outside the measurement.
func BenchmarkPackageCompile(b *testing.B) {
	for _, name := range []string{"openai-chat", "openai-responses", "anthropic", "google-generate-content"} {
		b.Run(name, func(b *testing.B) {
			read := func(part string) []byte {
				b.Helper()
				raw, err := os.ReadFile(filepath.Join("packages", name, part+".json"))
				if err != nil {
					b.Fatal(err)
				}
				return raw
			}
			spec, mapping := read("spec"), read("mapping")
			compiler, err := p.NewCompiler(p.DefaultLimits(), nil, nil)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				compiled, issues := compiler.CompileSources(spec, mapping)
				if err := p.IssuesError(issues); err != nil {
					b.Fatal(err)
				}
				if compiled.CoreVersion() != p.ProtocolCoreVersion {
					b.Fatal("package did not compile the current core")
				}
			}
		})
	}
}
