package builtin

import (
	"os"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func liveAuditFixture(t *testing.T, name string) p.Value {
	t.Helper()
	body, err := os.ReadFile("testdata/audit_20261013.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := p.ParseValue(body)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := value.ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	if fixtures[name].IsZero() {
		t.Fatalf("missing audit fixture %q", name)
	}
	return fixtures[name]
}
