package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedProtocolExamplesVerify(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*", "spec.json"))
	if err != nil || len(paths) == 0 {
		t.Fatal("published protocol examples missing", err)
	}
	compiler, err := NewCompiler(DefaultLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mapping, err := os.ReadFile(filepath.Join(filepath.Dir(path), "mapping.json"))
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.CompileSources(raw, mapping)
			if compiled == nil {
				t.Fatal(issues)
			}
			if report := Verify(t.Context(), compiled); !report.Passed {
				t.Fatal(report.Issues)
			}
		})
	}
}
