package protocol_test

import (
	"crypto/sha256"
	"testing"

	p "github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestCompilationCacheEmbeddedHashesMatchBuiltinPackages(t *testing.T) {
	packages, err := builtin.Packages()
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) == 0 {
		t.Fatal("builtin package inventory is empty")
	}
	hashes := p.EmbeddedPresetHashesForTest()
	seen := make(map[string]bool)
	for _, value := range packages {
		var header struct {
			Spec struct {
				ID string `json:"id"`
			} `json:"spec"`
		}
		if err := value.Decode(&header); err != nil {
			t.Fatal(err)
		}
		id := header.Spec.ID
		if id == "" || seen[id] {
			t.Fatalf("builtin package has an empty or duplicate ID %q", id)
		}
		seen[id] = true
		t.Run(id, func(t *testing.T) {
			expected, exists := hashes[id]
			if !exists {
				t.Fatalf("builtin package %q is missing from the cache's embedded inventory", id)
			}
			raw := value.Bytes()
			if actual := sha256.Sum256(raw); actual != expected {
				t.Fatalf("builtin package bytes differ from the cache's embedded hash: got %x, want %x", actual, expected)
			}
			spaced := sha256.Sum256(append([]byte(" "), raw...))
			for presetID, hash := range hashes {
				if spaced == hash {
					t.Fatalf("space-prefixed builtin package matched embedded preset %q", presetID)
				}
			}
		})
	}
	for id := range hashes {
		if !seen[id] {
			t.Errorf("cache embeds preset %q that builtin.Packages does not ship", id)
		}
	}
}
