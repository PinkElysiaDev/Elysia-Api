package server

import (
	"slices"
	"testing"
)

func TestCLISourceManualCatalogSurvivesRefresh(t *testing.T) {
	s := newOpsTestServer(t)
	if result := opsExecute(t, &createSourceTool{server: s}, `{"name":"manual-audit","baseUrl":"https://example.test","manualModels":["one","two"]}`); !result.OK {
		t.Fatal(result.Summary)
	}
	for _, expected := range [][]string{{"one", "two"}, {"two", "three"}, {}} {
		switch len(expected) {
		case 0:
			if result := opsExecute(t, &updateSourceTool{server: s}, `{"source":"manual-audit","manualModels":[]}`); !result.OK {
				t.Fatal(result.Summary)
			}
		default:
			if expected[0] == "two" {
				if result := opsExecute(t, &updateSourceTool{server: s}, `{"source":"manual-audit","manualModels":["two","three"]}`); !result.OK {
					t.Fatal(result.Summary)
				}
			}
		}
		if _, err := s.refreshSourceByID(t.Context(), "manual-audit"); err != nil {
			t.Fatal(err)
		}
		sources, err := s.store.ListSources(t.Context())
		if err != nil || len(sources) != 1 {
			t.Fatal(sources, err)
		}
		models, err := s.store.ListModels(t.Context())
		if err != nil || len(models) != len(expected) || len(sources[0].ManualModels) != len(expected) {
			t.Fatal("authoritative list and models disagree after refresh", err)
		}
		for _, model := range append(models, sources[0].ManualModels...) {
			if !slices.Contains(expected, model.ID) {
				t.Fatalf("unexpected model after refresh: %s", model.ID)
			}
		}
	}
}
