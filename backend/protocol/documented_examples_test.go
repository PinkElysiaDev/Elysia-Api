package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedProtocolExamplesVerify(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "docs", "examples", "*-v2.json"))
	if err != nil || len(paths) == 0 {
		t.Fatal("published protocol examples missing", err)
	}
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			compiled, issues := compiler.Compile(raw)
			if compiled == nil {
				t.Fatal(issues)
			}
			if report := Verify(t.Context(), compiled); !report.Passed {
				t.Fatal(report.Issues)
			}
		})
	}
}
