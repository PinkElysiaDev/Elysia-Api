package builtin

import (
	"os"
	"path/filepath"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestPreviousDefinitionFingerprintsProtectEdits(t *testing.T) {
	for id := range priorDefinitionFingerprints {
		raw, err := os.ReadFile(filepath.Join("testdata", "previous", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		value, err := p.ParseValue(raw)
		if err != nil || !IsPreviousDefinition(id, value) {
			t.Fatalf("missing historical fingerprint: %s %v", id, err)
		}
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		fields["name"] = p.StringValue("user edited")
		edited, err := p.EncodeValue(fields)
		if err != nil {
			t.Fatal(err)
		}
		if IsPreviousDefinition(id, edited) {
			t.Fatal("ID must not authorize replacing edits")
		}
	}
}
